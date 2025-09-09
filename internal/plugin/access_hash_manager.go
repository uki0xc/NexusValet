package plugin

import (
	"context"
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

// AccessHashManager 管理用户的access_hash缓存
type AccessHashManager struct {
	api       *tg.Client
	userCache map[int64]*UserInfo
	mutex     sync.RWMutex
	// 缓存过期时间（24小时）
	cacheExpiry time.Duration
}

// NewAccessHashManager 创建新的AccessHashManager
func NewAccessHashManager(api *tg.Client) *AccessHashManager {
	return &AccessHashManager{
		api:         api,
		userCache:   make(map[int64]*UserInfo),
		cacheExpiry: 24 * time.Hour,
	}
}

// GetUserPeer 获取用户的InputPeerUser，自动处理access_hash
func (ahm *AccessHashManager) GetUserPeer(ctx context.Context, userID int64) (*tg.InputPeerUser, error) {
	// 先检查缓存
	if userInfo := ahm.getCachedUser(userID); userInfo != nil {
		logger.Debugf("从缓存获取用户%d的access_hash: %d", userID, userInfo.AccessHash)
		return &tg.InputPeerUser{
			UserID:     userInfo.ID,
			AccessHash: userInfo.AccessHash,
		}, nil
	}

	// 缓存中没有，尝试多种方法获取
	userInfo, err := ahm.fetchAndCacheUser(ctx, userID)
	if err != nil {
		logger.Warnf("直接获取用户%d失败: %v", userID, err)
		// 如果直接获取失败，返回错误而不是使用0
		return nil, fmt.Errorf("无法获取用户%d的access_hash: %v", userID, err)
	}

	return &tg.InputPeerUser{
		UserID:     userInfo.ID,
		AccessHash: userInfo.AccessHash,
	}, nil
}

// GetUserPeerWithFallback 获取用户Peer，包含回退策略
func (ahm *AccessHashManager) GetUserPeerWithFallback(ctx context.Context, userID int64, channelPeer tg.InputChannelClass) (*tg.InputPeerUser, error) {
	// 方法1：先尝试标准获取
	userPeer, err := ahm.GetUserPeer(ctx, userID)
	if err == nil {
		return userPeer, nil
	}

	// 方法2：尝试从群组参与者获取
	if channelPeer != nil {
		userPeer, err = ahm.GetUserPeerFromParticipant(ctx, channelPeer, userID)
		if err == nil {
			return userPeer, nil
		}
		logger.Warnf("从群组参与者获取用户%d失败: %v", userID, err)
	}

	// 方法3：尝试通过搜索群组成员获取
	if channelPeer != nil {
		userPeer, err = ahm.searchUserInChannel(ctx, channelPeer, userID)
		if err == nil {
			return userPeer, nil
		}
		logger.Warnf("通过搜索群组成员获取用户%d失败: %v", userID, err)
	}

	// 最后的回退：使用AccessHash=0
	logger.Warnf("所有方法都失败，对用户%d使用AccessHash=0作为最后回退", userID)
	return &tg.InputPeerUser{UserID: userID, AccessHash: 0}, nil
}

// searchUserInChannel 在频道中搜索用户
func (ahm *AccessHashManager) searchUserInChannel(ctx context.Context, channelPeer tg.InputChannelClass, userID int64) (*tg.InputPeerUser, error) {
	// 尝试获取频道的参与者列表
	participants, err := ahm.api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: channelPeer,
		Filter:  &tg.ChannelParticipantsRecent{},
		Offset:  0,
		Limit:   200, // 获取最近的200个成员
		Hash:    0,
	})
	if err != nil {
		return nil, fmt.Errorf("获取频道参与者失败: %v", err)
	}

	// 检查返回的类型
	var users []tg.UserClass
	switch p := participants.(type) {
	case *tg.ChannelsChannelParticipants:
		users = p.Users
	default:
		return nil, fmt.Errorf("不支持的参与者类型")
	}

	// 在用户列表中搜索目标用户
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			// 找到目标用户，缓存并返回
			ahm.cacheUser(user)
			logger.Infof("通过搜索频道成员找到用户%d的access_hash: %d", userID, user.AccessHash)
			return &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}, nil
		}
	}

	return nil, fmt.Errorf("在频道成员中未找到用户%d", userID)
}

// GetUserPeerFromMessage 从消息中获取用户信息
func (ahm *AccessHashManager) GetUserPeerFromMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, userID int64) (*tg.InputPeerUser, error) {
	// 使用InputUserFromMessage获取用户信息
	inputUser := &tg.InputUserFromMessage{
		Peer:   peer,
		MsgID:  msgID,
		UserID: userID,
	}

	// 通过UsersGetUsers获取完整的用户信息
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

	// 缓存用户信息
	ahm.cacheUser(user)
	logger.Infof("通过消息获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)

	return &tg.InputPeerUser{
		UserID:     user.ID,
		AccessHash: user.AccessHash,
	}, nil
}

// GetUserPeerFromParticipant 从群组参与者中获取用户信息
func (ahm *AccessHashManager) GetUserPeerFromParticipant(ctx context.Context, channelPeer tg.InputChannelClass, userID int64) (*tg.InputPeerUser, error) {
	// 尝试通过ChannelsGetParticipant获取用户信息
	participant, err := ahm.api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     channelPeer,
		Participant: &tg.InputPeerUser{UserID: userID, AccessHash: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("从群组参与者获取用户信息失败: %v", err)
	}

	// 查找对应的用户信息
	for _, u := range participant.Users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			// 缓存用户信息
			ahm.cacheUser(user)
			logger.Infof("从群组参与者获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)

			return &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}, nil
		}
	}

	return nil, fmt.Errorf("在群组参与者中未找到用户%d", userID)
}

// UpdateUserFromMessage 从消息中更新用户信息
func (ahm *AccessHashManager) UpdateUserFromMessage(message *tg.Message) {
	if message.FromID == nil {
		return
	}

	if peerUser, ok := message.FromID.(*tg.PeerUser); ok {
		// 如果消息中包含发送者信息，尝试提取
		// 注意：这里需要从消息的其他字段中获取用户信息
		// 在实际的消息处理中，通常会有完整的用户信息
		logger.Debugf("检测到来自用户%d的消息", peerUser.UserID)
	}
}

// CacheUsersFromUpdate 从更新中缓存用户信息
func (ahm *AccessHashManager) CacheUsersFromUpdate(users []tg.UserClass) {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()

	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			ahm.userCache[user.ID] = &UserInfo{
				ID:         user.ID,
				AccessHash: user.AccessHash,
				Username:   user.Username,
				FirstName:  user.FirstName,
				LastName:   user.LastName,
				UpdatedAt:  time.Now(),
			}
			logger.Debugf("从更新缓存用户%d的access_hash: %d", user.ID, user.AccessHash)
		}
	}
}

// getCachedUser 从缓存获取用户信息
func (ahm *AccessHashManager) getCachedUser(userID int64) *UserInfo {
	ahm.mutex.RLock()
	defer ahm.mutex.RUnlock()

	userInfo, exists := ahm.userCache[userID]
	if !exists {
		return nil
	}

	// 检查缓存是否过期
	if time.Since(userInfo.UpdatedAt) > ahm.cacheExpiry {
		logger.Debugf("用户%d的缓存已过期", userID)
		return nil
	}

	return userInfo
}

// fetchAndCacheUser 从API获取并缓存用户信息
func (ahm *AccessHashManager) fetchAndCacheUser(ctx context.Context, userID int64) (*UserInfo, error) {
	// 尝试通过UsersGetUsers获取用户信息
	users, err := ahm.api.UsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUser{UserID: userID, AccessHash: 0},
	})
	if err != nil {
		return nil, err
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("用户不存在")
	}

	user, ok := users[0].(*tg.User)
	if !ok {
		return nil, fmt.Errorf("用户信息类型错误")
	}

	// 缓存用户信息
	userInfo := ahm.cacheUser(user)
	logger.Infof("获取并缓存用户%d的access_hash: %d", userID, user.AccessHash)

	return userInfo, nil
}

// cacheUser 缓存用户信息
func (ahm *AccessHashManager) cacheUser(user *tg.User) *UserInfo {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()

	userInfo := &UserInfo{
		ID:         user.ID,
		AccessHash: user.AccessHash,
		Username:   user.Username,
		FirstName:  user.FirstName,
		LastName:   user.LastName,
		UpdatedAt:  time.Now(),
	}

	ahm.userCache[user.ID] = userInfo
	return userInfo
}

// GetCachedUserInfo 获取缓存的用户信息（用于显示）
func (ahm *AccessHashManager) GetCachedUserInfo(userID int64) *UserInfo {
	return ahm.getCachedUser(userID)
}

// ClearExpiredCache 清理过期的缓存
func (ahm *AccessHashManager) ClearExpiredCache() {
	ahm.mutex.Lock()
	defer ahm.mutex.Unlock()

	now := time.Now()
	for userID, userInfo := range ahm.userCache {
		if now.Sub(userInfo.UpdatedAt) > ahm.cacheExpiry {
			delete(ahm.userCache, userID)
			logger.Debugf("清理过期的用户%d缓存", userID)
		}
	}
}

// GetCacheStats 获取缓存统计信息
func (ahm *AccessHashManager) GetCacheStats() (total int, expired int) {
	ahm.mutex.RLock()
	defer ahm.mutex.RUnlock()

	now := time.Now()
	total = len(ahm.userCache)

	for _, userInfo := range ahm.userCache {
		if now.Sub(userInfo.UpdatedAt) > ahm.cacheExpiry {
			expired++
		}
	}

	return total, expired
}
