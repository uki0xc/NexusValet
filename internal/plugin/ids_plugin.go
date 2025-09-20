package plugin

import (
	"context"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// DC编号到国家的映射
var dcCountryMapping = map[int]string{
	1: "Miami, USA",
	2: "Amsterdam, NLD",
	3: "Miami, USA",
	4: "Amsterdam, NLD",
	5: "Singapore",
	// 可以在这里添加更多的映射
}

// IdsPlugin ID查询插件
type IdsPlugin struct {
	*BasePlugin
	telegramAPI       *TelegramAPI
	accessHashManager *AccessHashManager
}

// NewIdsPlugin 创建ID查询插件
func NewIdsPlugin() *IdsPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "ids",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "查询用户ID信息，包括等级、DC位置等",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	return &IdsPlugin{
		BasePlugin:        NewBasePlugin(info),
		telegramAPI:       &TelegramAPI{},
		accessHashManager: nil, // 将在SetTelegramClient中初始化
	}
}

// SetTelegramClient 设置Telegram客户端
func (ip *IdsPlugin) SetTelegramClient(client *tg.Client) {
	ip.telegramAPI.client = client
	ip.accessHashManager = NewAccessHashManager(client)
}

// estimateLevel 根据用户ID估算等级
func estimateLevel(id int64) string {
	if id > 7000000000 {
		return "🆕 新手村村民 (刚入坑)"
	} else if id >= 6000000000 {
		return "⚔️ 青铜战士 (初出茅庐)"
	} else if id >= 5000000000 {
		return "🛡️ 白银骑士 (小有名气)"
	} else if id >= 4000000000 {
		return "🏆 黄金勇者 (声名鹊起)"
	} else if id >= 3000000000 {
		return "💎 钻石大师 (威名远扬)"
	} else if id >= 2075484114 {
		return "👑 王者传说 (名震天下)"
	} else if id >= 1000000000 {
		return "🌟 传奇英雄 (威震八方)"
	} else if id >= 500000000 {
		return "🔥 史诗霸主 (独霸一方)"
	} else if id >= 100000000 {
		return "⚡ 神话至尊 (威震寰宇)"
	} else if id >= 50000000 {
		return "🌌 创世之神 (开天辟地)"
	} else {
		return "👑 终极BOSS (无敌存在)"
	}
}

// resolveUser 解析用户，支持多种输入方式
func (ip *IdsPlugin) resolveUser(ctx *command.CommandContext) (*tg.User, error) {
	// 如果回复了消息，获取被回复消息的发送者
	if ctx.Message.Message.ReplyTo != nil {
		logger.Debugf("检测到回复消息，ReplyTo类型: %T", ctx.Message.Message.ReplyTo)
		if replyTo, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
			logger.Debugf("获取回复消息ID: %d", replyTo.ReplyToMsgID)
			// 获取被回复的消息
			replyMsg, err := ip.getReplyMessage(ctx, replyTo.ReplyToMsgID)
			if err != nil {
				logger.Errorf("获取回复消息失败: %v", err)
				return nil, fmt.Errorf("获取回复消息失败: %v", err)
			}

			logger.Debugf("成功获取回复消息，FromID类型: %T", replyMsg.FromID)
			logger.Debugf("回复消息详情: ID=%d, Out=%v, PeerID=%T", replyMsg.ID, replyMsg.Out, replyMsg.PeerID)

			// 首先尝试从消息中直接获取用户信息
			if user := ip.extractUserFromMessage(replyMsg); user != nil {
				logger.Debugf("从消息中直接获取到用户信息: %d", user.ID)
				return user, nil
			}

			// 如果无法从消息中直接获取，尝试通过用户ID获取
			if replyMsg.FromID != nil {
				switch fromID := replyMsg.FromID.(type) {
				case *tg.PeerUser:
					logger.Debugf("回复消息来自用户ID: %d", fromID.UserID)
					return ip.getUserByID(ctx.Context, fromID.UserID)
				case *tg.PeerChannel:
					return nil, fmt.Errorf("不支持查询频道信息，只能查询用户")
				}
			}

			// 如果回复的消息没有FromID，尝试从PeerID推断
			if replyMsg.PeerID != nil {
				switch peerID := replyMsg.PeerID.(type) {
				case *tg.PeerUser:
					logger.Debugf("从PeerID获取用户ID: %d", peerID.UserID)
					return ip.getUserByID(ctx.Context, peerID.UserID)
				case *tg.PeerChannel:
					return nil, fmt.Errorf("不支持查询频道信息，只能查询用户")
				case *tg.PeerChat:
					return nil, fmt.Errorf("不支持查询群组信息，只能查询用户")
				}
			}

			// 如果都没有，返回错误
			return nil, fmt.Errorf("无法获取被回复消息的发送者信息")
		}
		// 如果ReplyTo不是MessageReplyHeader类型，返回错误
		return nil, fmt.Errorf("回复消息格式错误")
	}

	// 如果没有参数，返回自己的信息
	if len(ctx.Args) == 0 {
		return ip.getSelfUser(ctx.Context)
	}

	arg := ctx.Args[0]

	// 检查是否是数字ID
	if userID, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return ip.getUserByID(ctx.Context, userID)
	}

	// 检查是否是用户名（以@开头）
	if strings.HasPrefix(arg, "@") {
		username := strings.TrimPrefix(arg, "@")
		return ip.getUserByUsername(ctx.Context, username)
	}

	// 检查是否是用户名（不以@开头）
	return ip.getUserByUsername(ctx.Context, arg)
}

// getSelfUser 获取自己的用户信息
func (ip *IdsPlugin) getSelfUser(ctx context.Context) (*tg.User, error) {
	users, err := ip.telegramAPI.client.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, fmt.Errorf("获取自身用户信息失败: %w", err)
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("未找到自身用户信息")
	}

	if user, ok := users[0].(*tg.User); ok {
		return user, nil
	}

	return nil, fmt.Errorf("用户信息格式错误")
}

// getUserByID 通过用户ID获取用户信息
func (ip *IdsPlugin) getUserByID(ctx context.Context, userID int64) (*tg.User, error) {
	logger.Debugf("尝试获取用户ID: %d", userID)

	// 方法1：尝试使用AccessHash=0直接获取（最简单的方法）
	users, err := ip.telegramAPI.client.UsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUser{UserID: userID, AccessHash: 0},
	})
	if err == nil && len(users) > 0 {
		if user, ok := users[0].(*tg.User); ok {
			logger.Debugf("通过AccessHash=0成功获取用户: %d", userID)
			return user, nil
		}
	}
	logger.Debugf("AccessHash=0方法失败: %v", err)

	// 方法2：使用AccessHashManager获取用户信息
	userPeer, err := ip.accessHashManager.GetUserPeer(ctx, userID)
	if err == nil {
		// 使用获取到的access_hash来获取完整的用户信息
		users, err := ip.telegramAPI.client.UsersGetUsers(ctx, []tg.InputUserClass{
			&tg.InputUser{UserID: userPeer.UserID, AccessHash: userPeer.AccessHash},
		})
		if err == nil && len(users) > 0 {
			if user, ok := users[0].(*tg.User); ok {
				logger.Debugf("通过AccessHashManager成功获取用户: %d", userID)
				return user, nil
			}
		}
	}
	logger.Debugf("AccessHashManager方法失败: %v", err)

	// 方法3：尝试从当前群组获取成员信息
	logger.Debugf("尝试从当前群组获取用户: %d", userID)
	if user := ip.getUserFromCurrentGroup(ctx, userID); user != nil {
		logger.Debugf("从当前群组成功获取用户: %d", userID)
		return user, nil
	}
	logger.Debugf("从当前群组获取用户失败: %d", userID)

	// 方法4：尝试从群组成员中获取用户信息（如果当前在群组中）
	logger.Debugf("尝试从群组成员获取用户: %d", userID)
	if user := ip.getUserFromGroupMembers(ctx, userID); user != nil {
		logger.Debugf("从群组成员成功获取用户: %d", userID)
		return user, nil
	}
	logger.Debugf("从群组成员获取用户失败: %d", userID)

	// 方法5：尝试通过搜索功能获取用户信息
	logger.Debugf("尝试通过搜索获取用户: %d", userID)
	if user := ip.getUserBySearch(ctx, userID); user != nil {
		logger.Debugf("通过搜索成功获取用户: %d", userID)
		return user, nil
	}
	logger.Debugf("通过搜索获取用户失败: %d", userID)

	return nil, fmt.Errorf("用户ID无效或无法获取用户信息")
}

// getUserFromCurrentGroup 从当前群组获取用户信息
func (ip *IdsPlugin) getUserFromCurrentGroup(ctx context.Context, userID int64) *tg.User {
	// 这个方法需要知道当前群组的ID，但我们在ids_plugin中没有直接访问
	// 所以这个方法暂时返回nil，等待后续改进
	logger.Debugf("getUserFromCurrentGroup: 当前实现暂不支持从当前群组获取用户")
	return nil
}

// getUserBySearch 通过搜索功能获取用户信息
func (ip *IdsPlugin) getUserBySearch(ctx context.Context, userID int64) *tg.User {
	logger.Debugf("开始通过搜索获取用户: %d", userID)

	// 尝试通过用户ID搜索
	searchResult, err := ip.telegramAPI.client.ContactsSearch(ctx, &tg.ContactsSearchRequest{
		Q:     fmt.Sprintf("%d", userID), // 使用用户ID作为搜索关键词
		Limit: 10,
	})
	if err != nil {
		logger.Debugf("搜索用户失败: %v", err)
		return nil
	}

	users := searchResult.Users
	logger.Debugf("搜索返回 %d 个用户", len(users))

	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			logger.Debugf("通过搜索找到用户: %d", userID)
			return user
		}
	}

	logger.Debugf("搜索中未找到用户: %d", userID)
	return nil
}

// getUserFromGroupMembers 从群组成员中获取用户信息
func (ip *IdsPlugin) getUserFromGroupMembers(ctx context.Context, userID int64) *tg.User {
	logger.Debugf("开始从群组成员搜索用户: %d", userID)

	// 方法1：尝试获取最近的对话列表，寻找群组
	dialogs, err := ip.telegramAPI.client.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetDate: 0,
		OffsetID:   0,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err != nil {
		logger.Debugf("获取对话列表失败: %v", err)
		return nil
	}

	// 遍历对话列表，寻找群组
	var users []tg.UserClass
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		users = ds.Users
		logger.Debugf("从MessagesDialogs获取到 %d 个用户", len(users))
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		users = ds.Users
		logger.Debugf("从MessagesDialogsSlice获取到 %d 个用户", len(users))
	}

	// 首先检查用户是否在对话列表中
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			logger.Debugf("在对话列表中找到用户: %d", userID)
			return user
		}
	}
	logger.Debugf("在对话列表中未找到用户: %d", userID)

	// 方法2：尝试从群组成员中搜索
	var dialogsList []tg.DialogClass
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		dialogsList = ds.Dialogs
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		dialogsList = ds.Dialogs
	}

	logger.Debugf("开始搜索 %d 个对话中的群组", len(dialogsList))
	for i, dialog := range dialogsList {
		peer := dialog.GetPeer()
		if channelPeer, ok := peer.(*tg.PeerChannel); ok {
			logger.Debugf("检查群组 %d/%d: ChannelID=%d", i+1, len(dialogsList), channelPeer.ChannelID)

			// 尝试获取群组成员
			participants, err := ip.telegramAPI.client.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
				Channel: &tg.InputChannel{
					ChannelID:  channelPeer.ChannelID,
					AccessHash: 0, // 尝试使用0，如果失败则跳过
				},
				Filter: &tg.ChannelParticipantsRecent{},
				Offset: 0,
				Limit:  200,
				Hash:   0,
			})
			if err != nil {
				logger.Debugf("获取群组 %d 成员失败: %v", channelPeer.ChannelID, err)
				continue // 跳过这个群组
			}

			// 检查返回的类型
			var groupUsers []tg.UserClass
			switch p := participants.(type) {
			case *tg.ChannelsChannelParticipants:
				groupUsers = p.Users
				logger.Debugf("群组 %d 有 %d 个成员", channelPeer.ChannelID, len(groupUsers))
			default:
				logger.Debugf("群组 %d 返回了不支持的参与者类型", channelPeer.ChannelID)
				continue
			}

			// 在群组成员中搜索目标用户
			for _, u := range groupUsers {
				if user, ok := u.(*tg.User); ok && user.ID == userID {
					logger.Debugf("在群组 %d 中找到用户: %d", channelPeer.ChannelID, userID)
					return user
				}
			}
		}
	}

	logger.Debugf("在所有群组中都未找到用户: %d", userID)
	return nil
}

// getUserByUsername 通过用户名获取用户信息
func (ip *IdsPlugin) getUserByUsername(ctx context.Context, username string) (*tg.User, error) {
	// 使用ContactsResolveUsername解析用户名
	resolved, err := ip.telegramAPI.client.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: username,
	})
	if err != nil {
		return nil, fmt.Errorf("用户名无效或者并未被使用: %w", err)
	}

	// 从解析结果中查找用户
	for _, user := range resolved.Users {
		if tgUser, ok := user.(*tg.User); ok {
			if tgUser.Username == username {
				return tgUser, nil
			}
		}
	}

	return nil, fmt.Errorf("未找到用户: %s", username)
}

// RegisterCommands 实现CommandPlugin接口
func (ip *IdsPlugin) RegisterCommands(parser *command.Parser) error {
	// 注册ids命令
	parser.RegisterCommand("ids", "查询用户ID信息，包括等级、DC位置等", ip.info.Name, ip.handleIds)

	logger.Infof("Ids commands registered successfully")
	return nil
}

// handleIds 处理ids命令
func (ip *IdsPlugin) handleIds(ctx *command.CommandContext) error {
	// 解析用户
	user, err := ip.resolveUser(ctx)
	if err != nil {
		return ip.sendErrorResponse(ctx, err.Error())
	}

	// 构建用户信息
	userID := user.ID
	nickname := user.FirstName
	if user.LastName != "" {
		nickname += " " + user.LastName
	}
	if nickname == "" {
		nickname = "未知用户"
	}

	username := "无"
	if user.Username != "" {
		username = "@" + user.Username
	}

	userLevel := estimateLevel(userID)

	// 获取DC信息
	dc, country := ip.getDCInfo(ctx.Context, user)

	// 构建响应消息
	response := fmt.Sprintf(`ID: %d
DC%s: %s
昵称: %s
等级: %s
用户名: %s
TG链接: tg://user?id=%d`,
		userID, dc, country, nickname, userLevel, username, userID)

	return ip.sendResponse(ctx, response)
}

// sendResponse 发送响应消息
func (ip *IdsPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 私聊：编辑消息，群聊：先尝试编辑，失败则发送新消息
	if ctx.Message.ChatID > 0 {
		// 私聊：编辑原消息
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		return err
	} else {
		// 群聊：先尝试编辑
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		if err != nil {
			// 编辑失败，发送新消息
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  message,
				RandomID: time.Now().UnixNano(),
			})
		}
		return err
	}
}

// sendErrorResponse 发送错误响应
func (ip *IdsPlugin) sendErrorResponse(ctx *command.CommandContext, errorMsg string) error {
	return ip.sendResponse(ctx, "❌ "+errorMsg)
}

// getDCInfo 获取DC信息
func (ip *IdsPlugin) getDCInfo(ctx context.Context, user *tg.User) (string, string) {
	// 如果是查询自己的信息，尝试获取真实的DC信息
	if user.Self {
		if nearestDC, err := ip.telegramAPI.client.HelpGetNearestDC(ctx); err == nil {
			dc := fmt.Sprintf("%d", nearestDC.ThisDC)
			country := dcCountryMapping[nearestDC.ThisDC]
			if country == "" {
				country = "未知"
			}
			return dc, country
		}
	}

	// 对于其他用户，优先尝试通过头像获取DC信息
	if user.Photo != nil {
		if photo, ok := user.Photo.(*tg.UserProfilePhoto); ok {
			dc := photo.DCID
			country := dcCountryMapping[dc]
			if country == "" {
				country = "未知"
			}
			return fmt.Sprintf("%d", dc), country
		}
	}

	// 如果头像信息不可用，尝试通过AccessHash推断DC信息
	// 这是一个简化的方法，实际DC分配更复杂
	dc := ip.inferDCFromAccessHash(user.AccessHash)
	country := dcCountryMapping[dc]
	if country == "" {
		country = "未知"
	}

	return fmt.Sprintf("%d", dc), country
}

// inferDCFromAccessHash 通过AccessHash推断DC信息
// 这是一个简化的推断方法，实际DC分配基于用户注册时的手机号国家代码
func (ip *IdsPlugin) inferDCFromAccessHash(accessHash int64) int {
	// 这是一个简化的推断方法
	// 实际应用中，DC信息通常基于用户注册时的手机号国家代码
	// 这里我们使用AccessHash的一些位来推断，但这不是100%准确的

	if accessHash == 0 {
		return 0 // 无法推断
	}

	// 使用AccessHash的低位来推断DC
	// 这是一个简化的方法，实际DC分配更复杂
	dc := int((accessHash&0xFF)%5) + 1
	if dc > 5 {
		dc = 5
	}

	return dc
}

// extractUserFromMessage 从消息中提取用户信息
func (ip *IdsPlugin) extractUserFromMessage(msg *tg.Message) *tg.User {
	logger.Debugf("尝试从消息中提取用户信息")
	
	// 检查消息是否有发送者信息
	if msg.FromID == nil {
		logger.Debugf("消息没有FromID")
		return nil
	}

	// 检查FromID是否是用户
	peerUser, ok := msg.FromID.(*tg.PeerUser)
	if !ok {
		logger.Debugf("FromID不是用户类型: %T", msg.FromID)
		return nil
	}

	logger.Debugf("消息来自用户ID: %d", peerUser.UserID)
	
	// 尝试从AccessHashManager的缓存中获取用户信息
	if userInfo := ip.accessHashManager.GetCachedUserInfo(peerUser.UserID); userInfo != nil {
		logger.Debugf("从缓存中找到用户信息: %d", peerUser.UserID)
		// 构造一个tg.User对象
		user := &tg.User{
			ID:         userInfo.ID,
			AccessHash: userInfo.AccessHash,
			Username:   userInfo.Username,
			FirstName:  userInfo.FirstName,
			LastName:   userInfo.LastName,
		}
		return user
	}
	
	logger.Debugf("缓存中未找到用户信息: %d", peerUser.UserID)
	return nil
}

// getReplyMessage 获取回复的消息
func (ip *IdsPlugin) getReplyMessage(ctx *command.CommandContext, msgID int) (*tg.Message, error) {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return nil, err
	}

	// 根据peer类型获取消息
	var messages tg.MessagesMessagesClass
	if channelPeer, ok := peer.(*tg.InputPeerChannel); ok {
		// 频道/超级群
		channelInput := &tg.InputChannel{ChannelID: channelPeer.ChannelID, AccessHash: channelPeer.AccessHash}
		messages, err = ctx.API.ChannelsGetMessages(ctx.Context, &tg.ChannelsGetMessagesRequest{
			Channel: channelInput,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
		})
	} else {
		// 普通群组或私聊
		messages, err = ctx.API.MessagesGetMessages(ctx.Context, []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}})
	}

	if err != nil {
		return nil, err
	}

	// 解析消息并缓存用户信息
	var msg *tg.Message
	if messagesSlice, ok := messages.(*tg.MessagesMessages); ok {
		if len(messagesSlice.Messages) > 0 {
			if m, ok := messagesSlice.Messages[0].(*tg.Message); ok {
				msg = m
				// 缓存用户信息到AccessHashManager
				ip.accessHashManager.CacheUsersFromUpdate(messagesSlice.Users)
			}
		}
	} else if channelMessages, ok := messages.(*tg.MessagesChannelMessages); ok {
		if len(channelMessages.Messages) > 0 {
			if m, ok := channelMessages.Messages[0].(*tg.Message); ok {
				msg = m
				// 缓存用户信息到AccessHashManager
				ip.accessHashManager.CacheUsersFromUpdate(channelMessages.Users)
			}
		}
	}

	if msg == nil {
		return nil, fmt.Errorf("消息不存在")
	}

	return msg, nil
}
