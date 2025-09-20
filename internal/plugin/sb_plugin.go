package plugin

import (
	"context"
	"database/sql"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// SBPlugin 超级封禁插件
type SBPlugin struct {
	*BasePlugin
	db                *sql.DB
	accessHashManager *AccessHashManager
}

// NewSBPlugin 创建超级封禁插件
func NewSBPlugin(db *sql.DB) *SBPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "sb",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "超级封禁插件，支持封禁用户并删除消息历史",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	return &SBPlugin{
		BasePlugin: NewBasePlugin(info),
		db:         db,
		// accessHashManager 将在SetTelegramClient中设置
	}
}

// Initialize 初始化插件时设置AccessHashManager
func (sp *SBPlugin) Initialize(ctx context.Context, manager interface{}) error {
	// 调用父类的Initialize
	if err := sp.BasePlugin.Initialize(ctx, manager); err != nil {
		return err
	}

	// 这里需要获取Telegram API客户端来初始化AccessHashManager
	// 我们将在后续的集成中处理这个

	return nil
}

// SetTelegramClient 设置Telegram客户端并初始化AccessHashManager
func (sp *SBPlugin) SetTelegramClient(client *tg.Client) {
	// 使用带数据库持久化的AccessHashManager
	sp.accessHashManager = NewAccessHashManagerWithDB(client, sp.db)
	logger.Infof("SB插件AccessHashManager已初始化（带持久化）")
}

// RegisterCommands 实现CommandPlugin接口
func (sp *SBPlugin) RegisterCommands(parser *command.Parser) error {
	parser.RegisterCommand("sb", "超级封禁用户并删除消息历史", sp.info.Name, sp.handleSuperBan)
	logger.Infof("SB commands registered successfully")
	return nil
}

// handleSuperBan 处理超级封禁命令
func (sp *SBPlugin) handleSuperBan(ctx *command.CommandContext) error {
	// 检查是否在群组中
	if ctx.Message.ChatID > 0 {
		return sp.sendResponse(ctx, "❌ 使用限制\n\n💬 此命令只能在群组中使用")
	}

	// 检查是否有管理员权限
	hasPermission, err := sp.checkAdminPermission(ctx)
	if err != nil {
		return sp.sendResponse(ctx, fmt.Sprintf("❌ 权限检查失败\n\n⚠️ 错误信息: %v", err))
	}
	if !hasPermission {
		return sp.sendResponse(ctx, "❌ 权限不足\n\n🔒 您需要管理员权限才能使用此命令")
	}

	// 获取目标用户信息
	uid, deleteAll, targetUser, err := sp.getTargetUser(ctx)
	if err != nil {
		return sp.sendResponse(ctx, fmt.Sprintf("参数错误：%v", err))
	}

	if uid == 0 {
		return sp.sendResponse(ctx, "❌ 参数错误\n\n📝 请回复一条消息或提供用户ID/用户名\n\n💡 使用方法:\n• 回复消息: .sb\n• 用户ID: .sb 123456789\n• 用户名: .sb @username")
	}

	// 处理用户封禁
	return sp.handleUserBan(ctx, uid, deleteAll, targetUser)
}

// getTargetUser 获取目标用户信息
func (sp *SBPlugin) getTargetUser(ctx *command.CommandContext) (int64, bool, *tg.User, error) {
	var uid int64
	var deleteAll = true
	var targetUser *tg.User

	// 检查是否回复了消息
	if ctx.Message.Message.ReplyTo != nil {
		if replyTo, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
			// 获取被回复的消息
			replyMsg, err := sp.getReplyMessage(ctx, replyTo.ReplyToMsgID)
			if err != nil {
				return 0, false, nil, fmt.Errorf("获取回复消息失败: %v", err)
			}

			if replyMsg.FromID != nil {
				switch fromID := replyMsg.FromID.(type) {
				case *tg.PeerUser:
					uid = fromID.UserID
					// 尝试获取用户信息
					user, err := sp.getUserInfo(ctx, uid)
					if err == nil {
						targetUser = user
					}
				case *tg.PeerChannel:
					// 不支持封禁频道
					return 0, false, nil, fmt.Errorf("不支持封禁频道，只能封禁用户")
				}
			}

			// 如果有额外参数，则不删除所有消息
			if len(ctx.Args) > 0 {
				deleteAll = false
			}
		}
	} else if len(ctx.Args) >= 1 {
		// 解析用户ID或用户名
		var err error
		uid, err = sp.checkUID(ctx, ctx.Args[0])
		if err != nil {
			return 0, false, nil, err
		}

		// 如果有第二个参数，则不删除所有消息
		if len(ctx.Args) >= 2 {
			deleteAll = false
		} else {
			deleteAll = true
		}
	}

	return uid, deleteAll, targetUser, nil
}

// checkUID 检查用户ID或用户名，只处理用户，不处理频道
func (sp *SBPlugin) checkUID(ctx *command.CommandContext, input string) (int64, error) {
	var uid int64

	// 尝试解析为数字ID
	if id, err := strconv.ParseInt(input, 10, 64); err == nil {
		if id < 0 {
			return 0, fmt.Errorf("不支持封禁频道，只能封禁用户")
		}
		uid = id
	} else {
		// 尝试解析为用户名
		username := strings.TrimPrefix(input, "@")
		resolved, err := ctx.API.ContactsResolveUsername(ctx.Context, &tg.ContactsResolveUsernameRequest{
			Username: username,
		})
		if err != nil {
			return 0, fmt.Errorf("无法解析用户名: %v", err)
		}

		if len(resolved.Users) > 0 {
			if user, ok := resolved.Users[0].(*tg.User); ok {
				uid = user.ID
			}
		} else if len(resolved.Chats) > 0 {
			return 0, fmt.Errorf("不支持封禁频道，只能封禁用户")
		} else {
			return 0, fmt.Errorf("用户名不存在")
		}
	}

	return uid, nil
}

// checkAdminPermission 检查管理员权限
func (sp *SBPlugin) checkAdminPermission(ctx *command.CommandContext) (bool, error) {
	// 获取当前用户在群组中的权限
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return false, err
	}

	// 获取自己的用户信息
	self, err := ctx.API.UsersGetUsers(ctx.Context, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return false, err
	}

	if len(self) == 0 {
		return false, fmt.Errorf("无法获取自己的用户信息")
	}

	selfUser, ok := self[0].(*tg.User)
	if !ok {
		return false, fmt.Errorf("用户信息格式错误")
	}

	// 检查在群组中的权限
	var channelPeer tg.InputChannelClass
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		channelPeer = &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
	default:
		return false, fmt.Errorf("不是频道或超级群组")
	}

	participant, err := ctx.API.ChannelsGetParticipant(ctx.Context, &tg.ChannelsGetParticipantRequest{
		Channel:     channelPeer,
		Participant: &tg.InputPeerUser{UserID: selfUser.ID, AccessHash: selfUser.AccessHash},
	})
	if err != nil {
		return false, err
	}

	// 检查是否是管理员或创建者
	switch participant.Participant.(type) {
	case *tg.ChannelParticipantCreator, *tg.ChannelParticipantAdmin:
		return true, nil
	default:
		return false, nil
	}
}

// handleUserBan 处理用户封禁
func (sp *SBPlugin) handleUserBan(ctx *command.CommandContext, uid int64, deleteAll bool, targetUser *tg.User) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("解析群组失败: %v", err)
	}

	count := 0
	var groups []string
	groupName := sp.getGroupName(ctx)

	// 在当前群组执行封禁（类似Python版本的直接尝试）
	banResult, banError := sp.banUserInGroupWithError(ctx, peer, uid, deleteAll)
	if banResult {
		count++
		groups = append(groups, groupName)
	}

	// 在最后才获取用户信息用于显示（类似Python版本）
	if targetUser == nil {
		user, err := sp.getUserInfo(ctx, uid)
		if err == nil {
			targetUser = user
		}
	}

	// 构建响应消息
	var text string
	if targetUser != nil {
		userMention := sp.getUserMention(targetUser)
		if count == 0 {
			text = fmt.Sprintf("❌ 封禁失败\n\n👤 目标用户: %s", userMention)
		} else {
			// 构建成功消息
			var actionText string
			if deleteAll {
				actionText = "🚫 已封禁用户并清除消息历史"
			} else {
				actionText = "🚫 已封禁用户"
			}

			text = fmt.Sprintf("%s\n\n👤 目标用户: %s\n⏰ 操作时间: %s",
				actionText, userMention, time.Now().Format("15:04:05"))
		}
	} else {
		if count == 0 {
			text = fmt.Sprintf("❌ 封禁失败\n\n🆔 用户ID: %d", uid)
		} else {
			// 构建成功消息
			var actionText string
			if deleteAll {
				actionText = "🚫 已封禁用户并清除消息历史"
			} else {
				actionText = "🚫 已封禁用户"
			}

			text = fmt.Sprintf("%s\n\n🆔 用户ID: %d\n⏰ 操作时间: %s",
				actionText, uid, time.Now().Format("15:04:05"))
		}
	}

	// 记录详细日志（包括错误信息）
	groupsInfo := ""
	if len(groups) > 0 {
		groupsInfo = fmt.Sprintf("\n封禁群组:\n%s", strings.Join(groups, "\n"))
	}
	if banError != nil {
		logger.Infof("%s\nuid: %d\n错误: %v%s", text, uid, banError, groupsInfo)
	} else {
		logger.Infof("%s\nuid: %d%s", text, uid, groupsInfo)
	}

	// 如果是成功的封禁消息，30秒后自动删除
	if count > 0 {
		return sp.sendResponseWithAutoDelete(ctx, text, 30)
	}
	// 错误消息不自动删除
	return sp.sendResponse(ctx, text)
}

// banUserInGroupWithError 在指定群组中封禁用户，返回详细错误信息
func (sp *SBPlugin) banUserInGroupWithError(ctx *command.CommandContext, peer tg.InputPeerClass, uid int64, deleteAll bool) (bool, error) {
	// 检查AccessHashManager是否已初始化
	if sp.accessHashManager == nil {
		logger.Errorf("AccessHashManager未初始化")
		return false, fmt.Errorf("AccessHashManager未初始化")
	}

	// 转换为ChannelClass
	var channelPeer tg.InputChannelClass
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		channelPeer = &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
	default:
		logger.Warnf("不支持的群组类型进行封禁操作")
		return false, fmt.Errorf("不支持的群组类型")
	}

	// 使用AccessHashManager获取用户Peer
	var userPeer *tg.InputPeerUser
	var err error

	// 优先方法：如果是回复消息，从消息中获取用户信息
	if ctx.Message.Message.ReplyTo != nil {
		if replyTo, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
			userPeer, err = sp.accessHashManager.GetUserPeerFromMessage(ctx.Context, peer, replyTo.ReplyToMsgID, uid)
			if err != nil {
				logger.Warnf("从回复消息获取用户%d失败: %v", uid, err)
			} else {
				logger.Infof("从回复消息成功获取用户%d的access_hash", uid)
			}
		}
	}

	// 回退方法1：尝试从群组参与者中获取
	if userPeer == nil {
		userPeer, err = sp.accessHashManager.GetUserPeerFromParticipant(ctx.Context, channelPeer, uid)
		if err != nil {
			logger.Warnf("从群组参与者获取用户%d失败: %v", uid, err)
		} else {
			logger.Infof("从群组参与者成功获取用户%d的access_hash", uid)
		}
	}

	// 回退方法2：使用带回退策略的获取方法
	if userPeer == nil {
		userPeer, err = sp.accessHashManager.GetUserPeerWithFallback(ctx.Context, uid, channelPeer)
		if err != nil {
			logger.Errorf("所有方法都失败，无法获取用户%d: %v", uid, err)
			return false, fmt.Errorf("无法获取用户信息: %v", err)
		}
		logger.Infof("通过回退策略获取用户%d的access_hash: %d", uid, userPeer.AccessHash)
	}

	// 封禁用户
	_, err = ctx.API.ChannelsEditBanned(ctx.Context, &tg.ChannelsEditBannedRequest{
		Channel:     channelPeer,
		Participant: userPeer,
		BannedRights: tg.ChatBannedRights{
			ViewMessages: true,
			SendMessages: true,
			SendMedia:    true,
			SendStickers: true,
			SendGifs:     true,
			SendGames:    true,
			SendInline:   true,
			SendPolls:    true,
			ChangeInfo:   true,
			InviteUsers:  true,
			PinMessages:  true,
			UntilDate:    0, // 永久封禁
		},
	})

	if err != nil {
		logger.Warnf("封禁用户%d失败: %v", uid, err)
		return false, err
	}

	logger.Infof("成功封禁用户%d", uid)

	// 删除消息历史
	if deleteAll {
		sp.deleteUserHistory(ctx, peer, uid)
	}

	return true, nil
}

// banUserInGroup 在指定群组中封禁用户
func (sp *SBPlugin) banUserInGroup(ctx *command.CommandContext, peer tg.InputPeerClass, uid int64, deleteAll bool) bool {
	result, _ := sp.banUserInGroupWithError(ctx, peer, uid, deleteAll)
	return result
}

// deleteUserHistory 删除用户消息历史
func (sp *SBPlugin) deleteUserHistory(ctx *command.CommandContext, peer tg.InputPeerClass, uid int64) {
	if sp.accessHashManager == nil {
		logger.Warnf("AccessHashManager未初始化，跳过删除消息历史")
		return
	}

	// 转换为ChannelClass
	var channelPeer tg.InputChannelClass
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		channelPeer = &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
	default:
		logger.Warnf("不支持的群组类型进行删除消息历史操作")
		return
	}

	// 使用AccessHashManager获取用户Peer
	userPeer, err := sp.accessHashManager.GetUserPeer(ctx.Context, uid)
	if err != nil {
		logger.Warnf("删除消息历史时获取用户%d失败，使用默认AccessHash: %v", uid, err)
		userPeer = &tg.InputPeerUser{UserID: uid, AccessHash: 0}
	}

	_, err = ctx.API.ChannelsDeleteParticipantHistory(ctx.Context, &tg.ChannelsDeleteParticipantHistoryRequest{
		Channel:     channelPeer,
		Participant: userPeer,
	})

	if err != nil {
		logger.Errorf("删除用户%d消息历史失败: %v", uid, err)
	} else {
		logger.Infof("成功删除用户%d的消息历史", uid)
	}
}

// getUserInfo 获取用户信息
func (sp *SBPlugin) getUserInfo(ctx *command.CommandContext, uid int64) (*tg.User, error) {
	// 如果AccessHashManager可用，先尝试从缓存获取
	if sp.accessHashManager != nil {
		if cachedInfo := sp.accessHashManager.GetCachedUserInfo(uid); cachedInfo != nil {
			// 从缓存信息构建User对象
			return &tg.User{
				ID:         cachedInfo.ID,
				AccessHash: cachedInfo.AccessHash,
				Username:   cachedInfo.Username,
				FirstName:  cachedInfo.FirstName,
				LastName:   cachedInfo.LastName,
			}, nil
		}

		// 缓存中没有，通过AccessHashManager获取
		userPeer, err := sp.accessHashManager.GetUserPeer(ctx.Context, uid)
		if err == nil {
			// 通过获取到的peer再次查询完整信息
			users, err := ctx.API.UsersGetUsers(ctx.Context, []tg.InputUserClass{
				&tg.InputUser{UserID: userPeer.UserID, AccessHash: userPeer.AccessHash},
			})
			if err == nil && len(users) > 0 {
				if user, ok := users[0].(*tg.User); ok {
					return user, nil
				}
			}
		}
	}

	// 回退到原始方法
	users, err := ctx.API.UsersGetUsers(ctx.Context, []tg.InputUserClass{
		&tg.InputUser{UserID: uid},
	})
	if err != nil {
		return nil, err
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("用户不存在")
	}

	user, ok := users[0].(*tg.User)
	if !ok {
		return nil, fmt.Errorf("用户信息格式错误")
	}

	return user, nil
}

// getReplyMessage 获取回复的消息
func (sp *SBPlugin) getReplyMessage(ctx *command.CommandContext, msgID int) (*tg.Message, error) {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return nil, err
	}

	// 转换为ChannelClass
	var channelPeer tg.InputChannelClass
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		channelPeer = &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
	default:
		return nil, fmt.Errorf("不支持的群组类型")
	}

	messages, err := ctx.API.ChannelsGetMessages(ctx.Context, &tg.ChannelsGetMessagesRequest{
		Channel: channelPeer,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	if err != nil {
		return nil, err
	}

	if messagesSlice, ok := messages.(*tg.MessagesChannelMessages); ok {
		if len(messagesSlice.Messages) > 0 {
			if msg, ok := messagesSlice.Messages[0].(*tg.Message); ok {
				return msg, nil
			}
		}
	}

	return nil, fmt.Errorf("消息不存在")
}

// getUserMention 获取用户提及格式
func (sp *SBPlugin) getUserMention(user *tg.User) string {
	var name string
	if user.FirstName != "" {
		name = user.FirstName
		if user.LastName != "" {
			name += " " + user.LastName
		}
	} else {
		name = "未知用户"
	}

	if user.Username != "" {
		return fmt.Sprintf("%s (@%s)", name, user.Username)
	}
	return fmt.Sprintf("%s (ID: %d)", name, user.ID)
}

// getGroupName 获取群组名称
func (sp *SBPlugin) getGroupName(ctx *command.CommandContext) string {
	// 尝试获取群组信息
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Sprintf("群组 (ID: %d)", ctx.Message.ChatID)
	}

	if channelPeer, ok := peer.(*tg.InputPeerChannel); ok {
		chats, err := ctx.API.ChannelsGetChannels(ctx.Context, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: channelPeer.ChannelID, AccessHash: channelPeer.AccessHash},
		})
		if err == nil {
			if chatSlice, ok := chats.(*tg.MessagesChats); ok && len(chatSlice.Chats) > 0 {
				if channel, ok := chatSlice.Chats[0].(*tg.Channel); ok {
					if channel.Username != "" {
						return fmt.Sprintf(`<a href="https://t.me/%s">%s</a>`, channel.Username, channel.Title)
					}
					return fmt.Sprintf(`<code>%s</code>`, channel.Title)
				}
			}
		}
	}

	return fmt.Sprintf("群组 (ID: %d)", ctx.Message.ChatID)
}

// friendlyErrorMessage 将错误转换为用户友好的消息
func (sp *SBPlugin) friendlyErrorMessage(err error) string {
	errStr := err.Error()

	if strings.Contains(errStr, "PARTICIPANT_ID_INVALID") {
		return "❌ 用户不在群组中或已离开群组"
	}
	if strings.Contains(errStr, "USER_NOT_PARTICIPANT") {
		return "❌ 用户不是群组成员"
	}
	if strings.Contains(errStr, "CHAT_ADMIN_REQUIRED") {
		return "❌ 需要管理员权限"
	}
	if strings.Contains(errStr, "USER_ADMIN_INVALID") {
		return "❌ 无法封禁管理员"
	}
	if strings.Contains(errStr, "用户不在群组中") {
		return "❌ 用户不在群组中，无法封禁"
	}
	if strings.Contains(errStr, "用户不存在或已离开群组") {
		return "❌ 用户不存在或已离开群组"
	}
	if strings.Contains(errStr, "无法解析用户") {
		return "❌ 无法找到该用户"
	}

	// 默认错误消息
	return fmt.Sprintf("❌ 操作失败: %s", errStr)
}

// sendResponse 发送响应消息
func (sp *SBPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	return sp.sendResponseWithAutoDelete(ctx, message, 0)
}

// sendResponseWithAutoDelete 发送响应消息并支持自动删除
func (sp *SBPlugin) sendResponseWithAutoDelete(ctx *command.CommandContext, message string, deleteAfterSeconds int) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	var messageID int
	var isNewMessage bool

	// 尝试编辑原消息
	_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      ctx.Message.Message.ID,
		Message: message,
	})
	if err != nil {
		// 编辑失败，发送新消息
		result, err := ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  message,
			RandomID: time.Now().UnixNano(),
		})
		if err != nil {
			return err
		}

		// 获取新消息的ID
		if updates, ok := result.(*tg.Updates); ok {
			for _, update := range updates.Updates {
				if msgUpdate, ok := update.(*tg.UpdateNewMessage); ok {
					if msg, ok := msgUpdate.Message.(*tg.Message); ok {
						messageID = msg.ID
						isNewMessage = true
						break
					}
				}
			}
		}
	} else {
		// 编辑成功，使用原消息ID
		messageID = ctx.Message.Message.ID
	}

	// 如果需要自动删除消息
	if deleteAfterSeconds > 0 && messageID != 0 {
		go sp.scheduleMessageDeletion(ctx, peer, messageID, deleteAfterSeconds, isNewMessage)
	}

	return nil
}

// scheduleMessageDeletion 安排消息删除
func (sp *SBPlugin) scheduleMessageDeletion(ctx *command.CommandContext, peer tg.InputPeerClass, messageID int, seconds int, isNewMessage bool) {
	// 等待指定时间
	time.Sleep(time.Duration(seconds) * time.Second)

	var err error

	// 根据群组类型选择不同的删除方法
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		// 超级群组/频道使用ChannelsDeleteMessages
		channelPeer := &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
		_, err = ctx.API.ChannelsDeleteMessages(ctx.Context, &tg.ChannelsDeleteMessagesRequest{
			Channel: channelPeer,
			ID:      []int{messageID},
		})
	case *tg.InputPeerChat:
		// 普通群组使用MessagesDeleteMessages
		_, err = ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
			ID:     []int{messageID},
			Revoke: true, // 对所有人删除
		})
	case *tg.InputPeerUser:
		// 私聊使用MessagesDeleteMessages
		_, err = ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
			ID:     []int{messageID},
			Revoke: true, // 对所有人删除
		})
	default:
		logger.Warnf("不支持的peer类型进行消息删除")
		return
	}

	if err != nil {
		logger.Warnf("自动删除消息%d失败: %v", messageID, err)
	} else {
		logger.Debugf("成功自动删除消息%d", messageID)
	}
}
