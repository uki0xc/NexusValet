package peers

import (
	"context"
	"database/sql"
	"fmt"
	"nexusvalet/pkg/logger"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

// UserInfo 存储用户信息和access_hash
type UserInfo struct {
	ID         int64
	AccessHash int64
	Username   string
	FirstName  string
	LastName   string
	UpdatedAt  time.Time
}

// AccessHashManager 管理用户与频道的 access_hash 解析与缓存
type AccessHashManager struct {
	api          *tg.Client
	db           *sql.DB
	userCache    map[int64]*UserInfo
	mutex        sync.RWMutex
	cacheExpiry  time.Duration
	failureCount map[int64]int
	failureMutex sync.RWMutex
	persistent   bool
}

// NewAccessHashManager 创建新的AccessHashManager
func NewAccessHashManager(api *tg.Client) *AccessHashManager {
	return &AccessHashManager{
		api:          api,
		userCache:    make(map[int64]*UserInfo),
		cacheExpiry:  12 * time.Hour,
		failureCount: make(map[int64]int),
		persistent:   false,
	}
}

// NewAccessHashManagerWithDB 创建带数据库持久化的AccessHashManager
func NewAccessHashManagerWithDB(api *tg.Client, db *sql.DB) *AccessHashManager {
	ahm := &AccessHashManager{
		api:          api,
		db:           db,
		userCache:    make(map[int64]*UserInfo),
		cacheExpiry:  12 * time.Hour,
		failureCount: make(map[int64]int),
		persistent:   true,
	}

	if err := ahm.initDatabase(); err != nil {
		logger.Errorf("Failed to initialize access_hash database: %v", err)
		ahm.persistent = false
	} else {
		if err := ahm.loadFromDatabase(); err != nil {
			logger.Errorf("Failed to load access_hash from database: %v", err)
		}
	}

	return ahm
}

// GetInputPeer 统一根据 peerID 返回可用的 tg.InputPeerClass。
func (ahm *AccessHashManager) GetInputPeer(ctx context.Context, peerID int64) (tg.InputPeerClass, error) {
	if peerID > 0 {
		userPeer, err := ahm.GetUserPeer(ctx, peerID)
		if err != nil {
			return nil, err
		}
		return userPeer, nil
	}
	if peerID > -1000000000000 {
		return &tg.InputPeerChat{ChatID: -peerID}, nil
	}
	channelID := -peerID - 1000000000000
	return ahm.getChannelPeer(ctx, channelID)
}

// GetUserPeer 获取用户的InputPeerUser
func (ahm *AccessHashManager) GetUserPeer(ctx context.Context, userID int64) (*tg.InputPeerUser, error) {
	if userInfo := ahm.getCachedUser(userID); userInfo != nil {
		logger.Debugf("从缓存获取用户%d的access_hash: %d", userID, userInfo.AccessHash)
		return &tg.InputPeerUser{UserID: userInfo.ID, AccessHash: userInfo.AccessHash}, nil
	}
	userInfo, err := ahm.fetchAndCacheUser(ctx, userID)
	if err != nil {
		logger.Warnf("直接获取用户%d失败: %v", userID, err)
		return nil, fmt.Errorf("无法获取用户%d的access_hash: %v", userID, err)
	}
	return &tg.InputPeerUser{UserID: userInfo.ID, AccessHash: userInfo.AccessHash}, nil
}

// GetUserPeerWithFallback 获取用户Peer，包含回退策略
func (ahm *AccessHashManager) GetUserPeerWithFallback(ctx context.Context, userID int64, channelPeer tg.InputChannelClass) (*tg.InputPeerUser, error) {
	if ahm.getFailureCount(userID) >= 3 {
		return nil, fmt.Errorf("用户%d的AccessHash获取失败次数过多，请重新建立连接", userID)
	}
	userPeer, err := ahm.GetUserPeer(ctx, userID)
	if err == nil {
		ahm.resetFailureCount(userID)
		return userPeer, nil
	}
	if channelPeer != nil {
		userPeer, err = ahm.GetUserPeerFromParticipant(ctx, channelPeer, userID)
		if err == nil {
			ahm.resetFailureCount(userID)
			return userPeer, nil
		}
		logger.Warnf("从群组参与者获取用户%d失败: %v", userID, err)
	}
	if channelPeer != nil {
		userPeer, err = ahm.searchUserInChannel(ctx, channelPeer, userID)
		if err == nil {
			ahm.resetFailureCount(userID)
			return userPeer, nil
		}
		logger.Warnf("通过搜索群组成员获取用户%d失败: %v", userID, err)
	}
	ahm.incrementFailureCount(userID)
	return nil, fmt.Errorf("无法获取用户%d的有效AccessHash，请重新建立与该用户的连接", userID)
}

func (ahm *AccessHashManager) searchUserInChannel(ctx context.Context, channelPeer tg.InputChannelClass, userID int64) (*tg.InputPeerUser, error) {
	participants, err := ahm.api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: channelPeer,
		Filter:  &tg.ChannelParticipantsRecent{},
		Offset:  0,
		Limit:   200,
		Hash:    0,
	})
	if err != nil {
		return nil, fmt.Errorf("获取频道参与者失败: %v", err)
	}
	var users []tg.UserClass
	switch p := participants.(type) {
	case *tg.ChannelsChannelParticipants:
		users = p.Users
	default:
		return nil, fmt.Errorf("不支持的参与者类型")
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			ahm.cacheUser(user)
			logger.Infof("通过搜索频道成员找到用户%d的access_hash: %d", userID, user.AccessHash)
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}
	return nil, fmt.Errorf("在频道成员中未找到用户%d", userID)
}

// GetUserPeerFromMessage 从消息中获取用户信息
func (ahm *AccessHashManager) GetUserPeerFromMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, userID int64) (*tg.InputPeerUser, error) {
	inputUser := &tg.InputUserFromMessage{Peer: peer, MsgID: msgID, UserID: userID}
	users, err := ahm.api.UsersGetUsers(ctx, []tg.InputUserClass{inputUser})
	if err != nil {
		return nil, fmt.Errorf("通过消息获取用户信息失败: %v", err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("未找到用户信息")
	}
	user, ok := users[0].(*tg.User)
	if !ok {
		return nil, fmt.Errorf("用户信息类型错误")
	}
	ahm.cacheUser(user)
	logger.Infof("通过消息获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
}

// GetUserPeerFromParticipant 从群组参与者中获取用户信息
func (ahm *AccessHashManager) GetUserPeerFromParticipant(ctx context.Context, channelPeer tg.InputChannelClass, userID int64) (*tg.InputPeerUser, error) {
	participant, err := ahm.api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     channelPeer,
		Participant: &tg.InputPeerUser{UserID: userID, AccessHash: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("从群组参与者获取用户信息失败: %v", err)
	}
	for _, u := range participant.Users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			ahm.cacheUser(user)
			logger.Infof("从群组参与者获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}
	return nil, fmt.Errorf("在群组参与者中未找到用户%d", userID)
}

func (ahm *AccessHashManager) UpdateUserFromMessage(message *tg.Message) {}

func (ahm *AccessHashManager) CacheUsersFromUpdate(users []tg.UserClass) {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			ahm.userCache[user.ID] = &UserInfo{ID: user.ID, AccessHash: user.AccessHash, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName, UpdatedAt: time.Now()}
			logger.Debugf("从更新缓存用户%d的access_hash: %d", user.ID, user.AccessHash)
		}
	}
}

func (ahm *AccessHashManager) getCachedUser(userID int64) *UserInfo {
	ahm.mutex.RLock()
	defer ahm.mutex.RUnlock()
	userInfo, exists := ahm.userCache[userID]
	if !exists {
		return nil
	}
	if time.Since(userInfo.UpdatedAt) > ahm.cacheExpiry {
		return nil
	}
	return userInfo
}

func (ahm *AccessHashManager) fetchAndCacheUser(ctx context.Context, userID int64) (*UserInfo, error) {
	users, err := ahm.api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: userID, AccessHash: 0}})
	if err == nil && len(users) > 0 {
		if user, ok := users[0].(*tg.User); ok {
			userInfo := ahm.cacheUser(user)
			logger.Infof("通过UsersGetUsers获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
			return userInfo, nil
		}
	}
	logger.Debugf("UsersGetUsers方法失败: %v", err)

	userInfo, err := ahm.fetchUserFromDialogs(ctx, userID)
	if err == nil {
		return userInfo, nil
	}
	logger.Debugf("从对话列表获取用户失败: %v", err)

	userInfo, err = ahm.fetchUserFromContacts(ctx, userID)
	if err == nil {
		return userInfo, nil
	}
	logger.Debugf("从联系人获取用户失败: %v", err)

	userInfo, err = ahm.fetchBotByUsername(ctx, userID)
	if err == nil {
		return userInfo, nil
	}
	logger.Debugf("通过用户名解析机器人失败: %v", err)

	return nil, fmt.Errorf("所有方法都无法获取用户%d的信息", userID)
}

func (ahm *AccessHashManager) fetchUserFromDialogs(ctx context.Context, userID int64) (*UserInfo, error) {
	dialogs, err := ahm.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetDate: 0, OffsetID: 0, OffsetPeer: &tg.InputPeerEmpty{}, Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("获取对话列表失败: %v", err)
	}
	var users []tg.UserClass
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		users = ds.Users
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		users = ds.Users
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			userInfo := ahm.cacheUser(user)
			logger.Infof("从对话列表获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
			return userInfo, nil
		}
	}
	return nil, fmt.Errorf("在对话列表中未找到用户%d", userID)
}

func (ahm *AccessHashManager) fetchUserFromContacts(ctx context.Context, userID int64) (*UserInfo, error) {
	contacts, err := ahm.api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("获取联系人列表失败: %v", err)
	}
	var users []tg.UserClass
	if contactsResult, ok := contacts.(*tg.ContactsContacts); ok {
		users = contactsResult.Users
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			userInfo := ahm.cacheUser(user)
			logger.Infof("从联系人获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
			return userInfo, nil
		}
	}
	return nil, fmt.Errorf("在联系人中未找到用户%d", userID)
}

func (ahm *AccessHashManager) fetchBotByUsername(ctx context.Context, userID int64) (*UserInfo, error) {
	searchResult, err := ahm.api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: fmt.Sprintf("%d", userID), Limit: 10})
	if err == nil {
		for _, u := range searchResult.Users {
			if user, ok := u.(*tg.User); ok && user.ID == userID {
				userInfo := ahm.cacheUser(user)
				logger.Infof("通过搜索获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)
				return userInfo, nil
			}
		}
	}
	return nil, fmt.Errorf("无法通过搜索找到用户%d", userID)
}

func (ahm *AccessHashManager) cacheUser(user *tg.User) *UserInfo {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()
	userInfo := &UserInfo{ID: user.ID, AccessHash: user.AccessHash, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName, UpdatedAt: time.Now()}
	ahm.userCache[user.ID] = userInfo
	if ahm.persistent {
		if err := ahm.saveToDatabase(userInfo); err != nil {
			logger.Errorf("Failed to save user %d to database: %v", user.ID, err)
		}
	}
	return userInfo
}

func (ahm *AccessHashManager) GetCachedUserInfo(userID int64) *UserInfo {
	return ahm.getCachedUser(userID)
}

func (ahm *AccessHashManager) ClearExpiredCache() {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()
	now := time.Now()
	for userID, userInfo := range ahm.userCache {
		if now.Sub(userInfo.UpdatedAt) > ahm.cacheExpiry {
			delete(ahm.userCache, userID)
		}
	}
}

func (ahm *AccessHashManager) GetCacheStats() (total int, expired int) {
	ahm.mutex.RLock()
	defer ahm.mutex.RUnlock()
	now := time.Now()
	for _, userInfo := range ahm.userCache {
		total++
		if now.Sub(userInfo.UpdatedAt) > ahm.cacheExpiry {
			expired++
		}
	}
	return
}

func (ahm *AccessHashManager) getFailureCount(userID int64) int {
	ahm.failureMutex.RLock()
	defer ahm.failureMutex.RUnlock()
	return ahm.failureCount[userID]
}
func (ahm *AccessHashManager) incrementFailureCount(userID int64) {
	ahm.failureMutex.Lock()
	defer ahm.failureMutex.Unlock()
	ahm.failureCount[userID]++
}
func (ahm *AccessHashManager) resetFailureCount(userID int64) {
	ahm.failureMutex.Lock()
	defer ahm.failureMutex.Unlock()
	delete(ahm.failureCount, userID)
}

// FailureCount 返回特定用户最近的失败次数（对外公开，用于观测与告警）。
func (ahm *AccessHashManager) FailureCount(userID int64) int { return ahm.getFailureCount(userID) }

func (ahm *AccessHashManager) ClearUserCache(userID int64) {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()
	delete(ahm.userCache, userID)
	ahm.resetFailureCount(userID)
	if ahm.persistent && ahm.db != nil {
		_, err := ahm.db.Exec("DELETE FROM access_hash_cache WHERE user_id = ?", userID)
		if err != nil {
			logger.Errorf("Failed to delete user %d from database: %v", userID, err)
		}
	}
}

func (ahm *AccessHashManager) initDatabase() error {
	if ahm.db == nil {
		return fmt.Errorf("database not available")
	}
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS access_hash_cache (
		user_id INTEGER PRIMARY KEY,
		access_hash INTEGER NOT NULL,
		username TEXT,
		first_name TEXT,
		last_name TEXT,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	_, err := ahm.db.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create access_hash_cache table: %w", err)
	}
	return nil
}

func (ahm *AccessHashManager) loadFromDatabase() error {
	if ahm.db == nil {
		return fmt.Errorf("database not available")
	}
	rows, err := ahm.db.Query(`
		SELECT user_id, access_hash, username, first_name, last_name, updated_at
		FROM access_hash_cache
	`)
	if err != nil {
		return fmt.Errorf("failed to query access_hash_cache: %w", err)
	}
	defer rows.Close()
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()
	for rows.Next() {
		var userInfo UserInfo
		var updatedAtStr string
		if err := rows.Scan(&userInfo.ID, &userInfo.AccessHash, &userInfo.Username, &userInfo.FirstName, &userInfo.LastName, &updatedAtStr); err != nil {
			continue
		}
		if t, err := time.Parse("2006-01-02 15:04:05", updatedAtStr); err == nil {
			userInfo.UpdatedAt = t
		} else {
			userInfo.UpdatedAt = time.Now()
		}
		if time.Since(userInfo.UpdatedAt) <= ahm.cacheExpiry {
			ahm.userCache[userInfo.ID] = &userInfo
		}
	}
	return nil
}

func (ahm *AccessHashManager) saveToDatabase(userInfo *UserInfo) error {
	if !ahm.persistent || ahm.db == nil {
		return nil
	}
	_, err := ahm.db.Exec(`
		INSERT OR REPLACE INTO access_hash_cache 
		(user_id, access_hash, username, first_name, last_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userInfo.ID, userInfo.AccessHash, userInfo.Username, userInfo.FirstName, userInfo.LastName, userInfo.UpdatedAt.Format("2006-01-02 15:04:05"))
	return err
}

func (ahm *AccessHashManager) CleanExpiredFromDatabase() error {
	if !ahm.persistent || ahm.db == nil {
		return nil
	}
	expiredTime := time.Now().Add(-ahm.cacheExpiry)
	_, err := ahm.db.Exec(`
		DELETE FROM access_hash_cache WHERE updated_at < ?
	`, expiredTime.Format("2006-01-02 15:04:05"))
	return err
}

// 频道解析
func (ahm *AccessHashManager) getChannelPeer(ctx context.Context, channelID int64) (tg.InputPeerClass, error) {
	channels, err := ahm.api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: channelID, AccessHash: 0}})
	if err == nil {
		if chats, ok := channels.(*tg.MessagesChats); ok {
			for _, c := range chats.Chats {
				if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
					return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
				}
			}
		}
	}
	dialogs, derr := ahm.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetDate: 0, OffsetID: 0, OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100})
	if derr == nil {
		if peer := ahm.searchChannelInDialogs(dialogs, channelID); peer != nil {
			return peer, nil
		}
	}
	if derr == nil {
		dialogs2, err2 := ahm.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetDate: 0, OffsetID: 0, OffsetPeer: &tg.InputPeerEmpty{}, Limit: 500})
		if err2 == nil {
			if peer := ahm.searchChannelInDialogs(dialogs2, channelID); peer != nil {
				return peer, nil
			}
		}
	}
	return nil, fmt.Errorf("channel not found: %d (tried multiple resolution methods, original errors: channels=%v, dialogs=%v)", channelID, err, derr)
}

func (ahm *AccessHashManager) searchChannelInDialogs(dialogs tg.MessagesDialogsClass, channelID int64) tg.InputPeerClass {
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			}
		}
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			}
		}
	}
	return nil
}
