package main

import (
	"bufio"
	"context"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/config"
	"nexusvalet/internal/core"
	"nexusvalet/internal/peers"
	"nexusvalet/internal/plugin"
	"nexusvalet/internal/session"
	"nexusvalet/pkg/logger"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// Bot 代表主要的机器人应用程序
type Bot struct {
	config        *config.Config
	client        *telegram.Client
	api           *tg.Client
	dispatcher    *core.EventDispatcher
	hookManager   *core.HookManager
	commandParser *command.Parser
	pluginManager *plugin.GoManager
	sessionMgr    *session.Manager
	ctx           context.Context
	cancel        context.CancelFunc
	currentPeer   tg.InputPeerClass // 存储当前对等体用于回复
	selfUserID    int64             // 机器人自己的用户ID
	peerResolver  *peers.Resolver
	// 存储最后处理的消息用于编辑上下文
	lastMessage *tg.Message
	lastUpdate  interface{} // 存储原始更新
	startTime   time.Time   // 机器人启动时间
}

// NewBot 创建一个新的机器人实例
func NewBot(cfg *config.Config) (*Bot, error) {
	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 初始化会话管理器
	sessionMgr, err := session.NewManager(cfg.Telegram.Database)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}

	// 初始化核心组件
	dispatcher := core.NewEventDispatcher()
	hookManager := core.NewHookManager()
	commandParser := command.NewParser(cfg.Bot.CommandPrefix, dispatcher, hookManager)

	// 初始化Go插件管理器
	pluginManager := plugin.NewGoManager(commandParser, dispatcher, hookManager, sessionMgr.GetDB())

	bot := &Bot{
		config:        cfg,
		dispatcher:    dispatcher,
		hookManager:   hookManager,
		commandParser: commandParser,
		pluginManager: pluginManager,
		sessionMgr:    sessionMgr,
		ctx:           ctx,
		cancel:        cancel,
		startTime:     time.Now(),
	}

	// 创建 Telegram 客户端
	if err := bot.createTelegramClient(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Telegram client: %w", err)
	}

	// 为命令解析器设置会话管理器
	commandParser.SetSessionManager(sessionMgr)

	return bot, nil
}

// UpdateHandler 实现 telegram.UpdateHandler
type UpdateHandler struct {
	bot *Bot
}

func (h *UpdateHandler) Handle(ctx context.Context, update tg.UpdatesClass) error {
	return h.bot.handleUpdates(ctx, update)
}

// createTelegramClient 创建并配置 Telegram 客户端
func (b *Bot) createTelegramClient() error {
	options := telegram.Options{
		Device: telegram.DeviceConfig{
			DeviceModel:    "NexusValet Bot",
			SystemVersion:  "1.0.0",
			AppVersion:     "1.0.0",
			SystemLangCode: "en",
			LangPack:       "",
			LangCode:       "en",
		},
		SessionStorage: &telegram.FileSessionStorage{
			Path: b.config.Telegram.Session,
		},
		RetryInterval: time.Second,
		MaxRetries:    -1, // 无限重试
		DialTimeout:   10 * time.Second,
		UpdateHandler: &UpdateHandler{bot: b},
	}

	client := telegram.NewClient(b.config.Telegram.APIID, b.config.Telegram.APIHash, options)
	b.client = client
	b.api = client.API()

	// 初始化统一的 Peer 解析器
	b.peerResolver = peers.NewResolver(b.api)

	// Set the Telegram API for the command parser to enable file operations
	b.commandParser.SetTelegramAPI(b.api, b.peerResolver)

	return nil
}

// Start 启动机器人
func (b *Bot) Start() error {
	logger.Debugf("Starting NexusValet...")

	// 执行 BeforeStart 钩子
	if err := b.hookManager.ExecuteHooks(core.BeforeStart, map[string]interface{}{
		"version": "v1.0.0",
	}); err != nil {
		return fmt.Errorf("beforeStart hooks failed: %w", err)
	}

	// 注册所有内置插件
	if err := plugin.RegisterBuiltinPlugins(b.pluginManager); err != nil {
		logger.Errorf("Failed to register builtin plugins: %v", err)
		// 仍然继续
	}

	// 启动 Telegram 客户端
	if err := b.client.Run(b.ctx, func(ctx context.Context) error {
		logger.Debugf("Telegram client connected")

		// 获取自身用户ID
		self, err := b.api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			logger.Errorf("Failed to get self user info: %v", err)
		} else if len(self) > 0 {
			if user, ok := self[0].(*tg.User); ok {
				b.selfUserID = user.ID
				logger.Debugf("Bot user ID: %d", b.selfUserID)
			}
		}

		// 为插件设置Telegram客户端
		b.pluginManager.SetTelegramClient(b.api)

		// 执行 AfterStart 钩子
		if err := b.hookManager.ExecuteHooks(core.AfterStart, map[string]interface{}{
			"client": b.api,
		}); err != nil {
			logger.Errorf("AfterStart hooks failed: %v", err)
		}

		// 等待上下文取消
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		return fmt.Errorf("telegram client failed: %w", err)
	}

	return nil
}

// Stop 停止机器人
func (b *Bot) Stop() error {
	logger.Debugf("Stopping NexusValet...")

	// 执行 BeforeStop 钩子
	if err := b.hookManager.ExecuteHooks(core.BeforeStop, nil); err != nil {
		logger.Errorf("BeforeStop hooks failed: %v", err)
	}

	// 取消上下文以停止客户端
	b.cancel()

	// 关闭插件管理器
	if err := b.pluginManager.Shutdown(); err != nil {
		logger.Errorf("Failed to shutdown plugin manager: %v", err)
	}

	// 关闭会话管理器
	if err := b.sessionMgr.Close(); err != nil {
		logger.Errorf("Failed to close session manager: %v", err)
	}

	// 执行 AfterStop 钩子
	if err := b.hookManager.ExecuteHooks(core.AfterStop, nil); err != nil {
		logger.Errorf("AfterStop hooks failed: %v", err)
	}

	logger.Debugf("NexusValet stopped")
	return nil
}

// handleUpdates 处理传入的 Telegram 更新
func (b *Bot) handleUpdates(ctx context.Context, updates tg.UpdatesClass) error {
	// 从更新类中提取单个更新
	switch u := updates.(type) {
	case *tg.Updates:
		for _, update := range u.Updates {
			if err := b.handleSingleUpdate(ctx, update); err != nil {
				logger.Errorf("Failed to handle update: %v", err)
			}
		}
	case *tg.UpdateShort:
		return b.handleSingleUpdate(ctx, u.Update)
	case *tg.UpdateShortMessage:
		// 转换为 UpdateNewMessage 进行处理
		message := &tg.Message{
			ID:      u.ID,
			Message: u.Message,
			Date:    u.Date,
			PeerID:  &tg.PeerUser{UserID: u.UserID},
		}
		return b.handleNewMessage(ctx, &tg.UpdateNewMessage{Message: message})
	case *tg.UpdateShortChatMessage:
		// 转换为 UpdateNewMessage 进行处理
		message := &tg.Message{
			ID:      u.ID,
			Message: u.Message,
			Date:    u.Date,
			PeerID:  &tg.PeerChat{ChatID: u.ChatID},
			FromID:  &tg.PeerUser{UserID: u.FromID},
		}
		return b.handleNewMessage(ctx, &tg.UpdateNewMessage{Message: message})
	}
	return nil
}

// handleSingleUpdate 处理单个更新
func (b *Bot) handleSingleUpdate(ctx context.Context, update tg.UpdateClass) error {
	// 将原始更新分发给原始监听器
	if err := b.dispatcher.DispatchRaw(ctx, update); err != nil {
		logger.Errorf("Failed to dispatch raw update: %v", err)
	}

	// 处理特定的更新类型
	switch upd := update.(type) {
	case *tg.UpdateNewMessage:
		return b.handleNewMessage(ctx, upd)
	case *tg.UpdateNewChannelMessage:
		return b.handleNewChannelMessage(ctx, upd)
	default:
		// 其他更新类型可以在这里处理
		logger.Debugf("Unhandled update type: %T", update)
	}

	return nil
}

// handleNewMessage 处理新消息更新
func (b *Bot) handleNewMessage(ctx context.Context, update *tg.UpdateNewMessage) error {
	message, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}

	// 提取消息文本
	text := ""
	switch {
	case message.Message != "":
		text = message.Message
	default:
		// 如需要处理其他消息类型
		return nil
	}

	// 获取用户和聊天ID
	userID := getUserID(message)
	chatID := getChatID(message)

	// 处理 getUserID 返回 0 的情况（可能是我们的发出消息）
	if userID == 0 {
		// 检查这是否是我们的发出消息
		if message.Out && b.selfUserID != 0 {
			userID = b.selfUserID
			logger.Debugf("Detected outgoing message, setting userID to self: %d", userID)
		} else {
			logger.Debugf("Unknown user ID and not outgoing message, ignoring")
			return nil
		}
	}

	// 只处理机器人自己发送的消息（userbot 模式）
	if b.selfUserID != 0 && userID != b.selfUserID {
		logger.Debugf("Ignoring message from user %d (not self %d)", userID, b.selfUserID)
		return nil
	}

	logger.Debugf("Processing self message from userID=%d", userID)

	// 记录消息详情用于调试
	logger.Debugf("Processing self message: text='%s', userID=%d, chatID=%d, peerType=%T",
		text, userID, chatID, message.PeerID)

	// 存储当前对等体用于回复
	b.currentPeer = createInputPeerFromMessage(message)

	// 创建消息事件
	msgEvent := &core.MessageEvent{
		Update:  update,
		Message: message,
		Text:    text,
		UserID:  userID,
		ChatID:  chatID,
	}

	// 获取或创建会话
	sess, err := b.sessionMgr.GetSession(userID, chatID)
	if err != nil {
		logger.Errorf("Failed to get session: %v", err)
		return err
	}
	sess.Timestamp = time.Now().Unix()

	// 创建会话上下文
	sessionCtx := session.NewSessionContext(sess, b.sessionMgr)

	// 为命令处理设置响应函数
	if b.commandParser.IsCommand(text) {
		// 我们将在命令解析器中处理这个
	}

	// 分发消息事件
	if err := b.dispatcher.DispatchMessage(ctx, msgEvent); err != nil {
		logger.Errorf("Failed to dispatch message: %v", err)
	}

	// 保存会话
	if err := sessionCtx.Save(); err != nil {
		logger.Errorf("Failed to save session: %v", err)
	}

	return nil
}

// handleNewChannelMessage 处理新的频道/超级群组消息更新
func (b *Bot) handleNewChannelMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) error {
	message, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}

	logger.Debugf("Processing channel message: ID=%d, text='%s'", message.ID, message.Message)

	// 存储消息和更新用于编辑上下文
	b.lastMessage = message
	b.lastUpdate = update

	// 转换为 UpdateNewMessage 格式用于统一处理
	newMessageUpdate := &tg.UpdateNewMessage{
		Message:  message,
		Pts:      update.Pts,
		PtsCount: update.PtsCount,
	}

	return b.handleNewMessage(ctx, newMessageUpdate)
}

// sendMessage 使用统一解析器解析对等体后发送文本消息
func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	peer, err := b.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return err
	}
	_, err = b.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: time.Now().UnixNano(),
	})
	return err
}

// replyToMessage 回复特定消息
func (b *Bot) replyToMessage(ctx context.Context, chatID int64, messageID int, text string) error {
	peer, err := b.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return err
	}
	_, err = b.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		ReplyTo:  &tg.InputReplyToMessage{ReplyToMsgID: messageID},
		RandomID: time.Now().UnixNano(),
	})
	return err
}

// sendTyping 发送打字动作
func (b *Bot) sendTyping(ctx context.Context, chatID int64) error {
	peer, err := b.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return err
	}
	_, err = b.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{
		Peer:   peer,
		Action: &tg.SendMessageTypingAction{},
	})
	return err
}

// sendPhoto 发送图片
func (b *Bot) sendPhoto(ctx context.Context, chatID int64, imagePath string, caption string) error {
	peer, err := b.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return err
	}

	// 读取图片文件
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("failed to read image file: %w", err)
	}

	// 上传图片文件
	uploader := uploader.NewUploader(b.api)
	file, err := uploader.FromBytes(ctx, fmt.Sprintf("speedtest_%d.png", time.Now().Unix()), imageData)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	// 发送图片消息
	_, err = b.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedPhoto{
			File: file,
		},
		Message:  caption,
		RandomID: time.Now().UnixNano(),
	})

	if err != nil {
		return fmt.Errorf("failed to send photo: %w", err)
	}

	logger.Infof("Successfully sent photo to chatID=%d", chatID)
	return nil
}

// editWithPhoto 编辑消息为图片
func (b *Bot) editWithPhoto(ctx context.Context, chatID int64, messageID int, imagePath string, caption string) error {
	peer, err := b.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return err
	}

	// 读取图片文件
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("failed to read image file: %w", err)
	}

	// 上传图片文件
	uploader := uploader.NewUploader(b.api)
	file, err := uploader.FromBytes(ctx, fmt.Sprintf("speedtest_%d.png", time.Now().Unix()), imageData)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	// 编辑消息为图片
	_, err = b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer: peer,
		ID:   messageID,
		Media: &tg.InputMediaUploadedPhoto{
			File: file,
		},
		Message: caption,
	})

	if err != nil {
		return fmt.Errorf("failed to edit message with photo: %w", err)
	}

	logger.Infof("Successfully edited message %d with photo in chatID=%d", messageID, chatID)
	return nil
}

// editMessage 编辑现有消息
func (b *Bot) editMessage(ctx context.Context, chatID int64, messageID int, text string) error {
	if chatID > 0 {
		// 用户 ID: 正整数 - 私聊
		peer := &tg.InputPeerUser{UserID: chatID}
		logger.Debugf("Attempting to edit message %d in private chat with peer type %T", messageID, peer)

		_, err := b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      messageID,
			Message: text,
		})

		if err != nil {
			logger.Errorf("Failed to edit message %d in chatID=%d: %v", messageID, chatID, err)
		} else {
			logger.Debugf("Successfully edited message %d in chatID=%d", messageID, chatID)
		}
		return err

	} else if chatID > -1000000000000 {
		// 普通群组 ID: 负整数 (e.g., -123456789)
		peer := &tg.InputPeerChat{ChatID: -chatID}
		logger.Debugf("Attempting to edit message %d in group chat with peer type %T", messageID, peer)

		_, err := b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      messageID,
			Message: text,
		})

		if err != nil {
			logger.Errorf("Failed to edit message %d in chatID=%d: %v", messageID, chatID, err)
		} else {
			logger.Debugf("Successfully edited message %d in chatID=%d", messageID, chatID)
		}
		return err

	} else {
		// 超级群组和频道 ID: 以 -100 开头的长负整数 (e.g., -1001234567890)
		channelID := -chatID - 1000000000000
		logger.Debugf("Supergroup/Channel edit detected: chatID=%d, channelID=%d", chatID, channelID)

		// 对于频道消息编辑，我们需要检查是否从更新中获得了正确的对等体
		var peer tg.InputPeerClass

		// 检查我们是否有来自原始频道消息的存储更新
		if b.lastUpdate != nil {
			if channelUpdate, ok := b.lastUpdate.(*tg.UpdateNewChannelMessage); ok {
				if lastMsg, ok := channelUpdate.Message.(*tg.Message); ok && lastMsg.ID == messageID {
					// 我们正在编辑刚刚收到的消息，使用消息中的对等体
					if peerChannel, ok := lastMsg.PeerID.(*tg.PeerChannel); ok {
						peer = &tg.InputPeerChannel{
							ChannelID:  peerChannel.ChannelID,
							AccessHash: 0, // Try with 0 first for own messages
						}
						logger.Debugf("Using InputPeerChannel from stored update: ChannelID=%d", peerChannel.ChannelID)
					} else {
						peer = &tg.InputPeerSelf{}
						logger.Debugf("Fallback to InputPeerSelf")
					}
				} else {
					peer = &tg.InputPeerSelf{}
					logger.Debugf("Message ID mismatch, using InputPeerSelf")
				}
			} else {
				peer = &tg.InputPeerSelf{}
				logger.Debugf("Not a channel update, using InputPeerSelf")
			}
		} else {
			peer = &tg.InputPeerSelf{}
			logger.Debugf("No stored update, using InputPeerSelf")
		}

		logger.Debugf("Attempting to edit message %d using messages.editMessage with peer type %T", messageID, peer)

		_, err := b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      messageID,
			Message: text,
		})

		// 实现针对频道相关错误的重试机制
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "CHANNEL_INVALID") || strings.Contains(errStr, "PEER_ID_INVALID") || strings.Contains(errStr, "ACCESS_HASH_INVALID") {
				logger.Debugf("Peer error detected (%v), attempting to resolve with alternative methods", err)

				// 重试：尝试获取新的频道信息
				if retryPeer, retryErr := b.resolveChannelPeer(ctx, channelID); retryErr == nil {
					logger.Debugf("Retrying with resolved peer: %T", retryPeer)
					_, retryEditErr := b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
						Peer:    retryPeer,
						ID:      messageID,
						Message: text,
					})

					if retryEditErr == nil {
						logger.Debugf("Successfully edited message %d after peer resolution", messageID)
						return nil
					} else {
						logger.Debugf("Retry with resolved peer failed: %v", retryEditErr)
					}
				} else {
					logger.Debugf("Failed to resolve peer: %v", retryErr)
				}
			}

			logger.Errorf("Failed to edit message %d in chatID=%d: %v", messageID, chatID, err)
		} else {
			logger.Debugf("Successfully edited message %d in chatID=%d", messageID, chatID)
		}
		return err
	}
}

// resolveChannelPeer 尝试使用新的访问哈希解析频道对等体
func (b *Bot) resolveChannelPeer(ctx context.Context, channelID int64) (tg.InputPeerClass, error) {
	logger.Debugf("Attempting to resolve channel peer for channelID: %d", channelID)

	// 尝试使用 MessagesGetChannels 获取频道信息
	channels, err := b.api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{
			ChannelID:  channelID,
			AccessHash: 0, // Try with 0 first
		},
	})

	if err != nil {
		logger.Debugf("ChannelsGetChannels failed with access hash 0: %v", err)

		// 替代方案：尝试从可能有缓存信息的对话/聊天中获取
		dialogs, err := b.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: 0,
			OffsetID:   0,
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      100,
		})

		if err != nil {
			return nil, fmt.Errorf("failed to get dialogs: %w", err)
		}

		// 在对话中查找我们的频道
		if dialogsSlice, ok := dialogs.(*tg.MessagesDialogs); ok {
			for _, chat := range dialogsSlice.Chats {
				if channel, ok := chat.(*tg.Channel); ok && channel.ID == channelID {
					logger.Debugf("Found channel in dialogs with AccessHash: %d", channel.AccessHash)
					return &tg.InputPeerChannel{
						ChannelID:  channel.ID,
						AccessHash: channel.AccessHash,
					}, nil
				}
			}
		} else if dialogsSlice, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
			for _, chat := range dialogsSlice.Chats {
				if channel, ok := chat.(*tg.Channel); ok && channel.ID == channelID {
					logger.Debugf("Found channel in dialogs slice with AccessHash: %d", channel.AccessHash)
					return &tg.InputPeerChannel{
						ChannelID:  channel.ID,
						AccessHash: channel.AccessHash,
					}, nil
				}
			}
		}

		return nil, fmt.Errorf("channel not found in dialogs")
	}

	// 从响应中提取频道信息
	if channelsSlice, ok := channels.(*tg.MessagesChats); ok {
		for _, chat := range channelsSlice.Chats {
			if channel, ok := chat.(*tg.Channel); ok && channel.ID == channelID {
				logger.Debugf("Resolved channel with AccessHash: %d", channel.AccessHash)
				return &tg.InputPeerChannel{
					ChannelID:  channel.ID,
					AccessHash: channel.AccessHash,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("channel not found in response")
}

// 提取用户和聊天ID的辅助函数
func getUserID(message *tg.Message) int64 {
	// 对于发出消息（由我们发送），我们应该返回 0 让调用者使用 selfUserID 处理
	if message.Out {
		logger.Debugf("Outgoing message detected, returning 0 for caller to handle with selfUserID")
		return 0
	}

	// 对于传入消息，尝试从 FromID 获取实际发送者用户ID
	if message.FromID != nil {
		switch from := message.FromID.(type) {
		case *tg.PeerUser:
			logger.Debugf("Incoming message from user ID: %d", from.UserID)
			return from.UserID
		case *tg.PeerChannel:
			// 对于频道消息，FromID 可能是频道本身
			logger.Debugf("Message from channel ID: %d", from.ChannelID)
		case *tg.PeerChat:
			// 对于聊天消息，FromID 可能是聊天本身
			logger.Debugf("Message from chat ID: %d", from.ChatID)
		}
	}

	// 如果我们无法从 FromID 确定用户ID，记录消息详情
	logger.Debugf("Could not determine user ID from incoming message. FromID: %T, PeerID: %T",
		message.FromID, message.PeerID)
	return 0
}

func getChatID(message *tg.Message) int64 {
	switch peer := message.PeerID.(type) {
	case *tg.PeerChat:
		// 普通群组 ID: 负整数 (e.g., -123456789)
		return -peer.ChatID
	case *tg.PeerChannel:
		// 超级群组和频道 ID: 以 -100 开头的长负整数 (e.g., -1001234567890)
		return -1000000000000 - peer.ChannelID
	case *tg.PeerUser:
		// 用户 ID: 正整数 (e.g., 123456789)
		return peer.UserID
	}
	return 0
}

// createInputPeerFromMessage 从消息的对等体创建 InputPeer
func createInputPeerFromMessage(message *tg.Message) tg.InputPeerClass {
	logger.Debugf("Creating input peer from message. PeerID type: %T", message.PeerID)

	switch peer := message.PeerID.(type) {
	case *tg.PeerUser:
		// 对于用户聊天，使用 InputPeerSelf 因为我们作为机器人响应
		logger.Debugf("User chat detected, using InputPeerSelf")
		return &tg.InputPeerSelf{}
	case *tg.PeerChat:
		logger.Debugf("Group chat detected, ChatID: %d", peer.ChatID)
		return &tg.InputPeerChat{ChatID: peer.ChatID}
	case *tg.PeerChannel:
		logger.Debugf("Channel detected, ChannelID: %d", peer.ChannelID)
		// 对于频道/超级群组，使用 InputPeerSelf 编辑我们自己的消息
		// 这避免了访问哈希问题，因为我们正在编辑我们自己的消息
		logger.Debugf("Using InputPeerSelf for channel to avoid access hash issues")
		return &tg.InputPeerSelf{}
	default:
		logger.Debugf("Unknown peer type: %T, using InputPeerSelf", message.PeerID)
		return &tg.InputPeerSelf{}
	}
}

// checkAndCreateSession 检查会话是否存在，如果不存在，引导登录
func checkAndCreateSession(cfg *config.Config) error {
	sessionFile := cfg.Telegram.Session

	// 检查会话是否存在
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		logger.Infof("Session file not found, starting authentication process...")
		return performAuthentication(cfg)
	}

	logger.Infof("Session file found, will attempt to use existing session")
	return nil
}

// performAuthentication 执行 Telegram 身份验证流程
func performAuthentication(cfg *config.Config) error {
	logger.Infof("请输入您的手机号码 (格式: +1234567890):")

	reader := bufio.NewReader(os.Stdin)
	phone, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read phone number: %w", err)
	}
	phone = strings.TrimSpace(phone)

	// 为身份验证创建临时客户端
	options := telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{
			Path: cfg.Telegram.Session,
		},
	}

	client := telegram.NewClient(cfg.Telegram.APIID, cfg.Telegram.APIHash, options)

	return client.Run(context.Background(), func(ctx context.Context) error {
		// 开始身份验证流程
		codeAuth := auth.CodeAuthenticatorFunc(func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
			logger.Infof("验证码已发送，请输入验证码:")
			code, err := reader.ReadString('\n')
			if err != nil {
				return "", fmt.Errorf("failed to read verification code: %w", err)
			}
			return strings.TrimSpace(code), nil
		})

		flow := auth.NewFlow(
			auth.Constant(phone, "Bot", auth.CodeOnly(phone, codeAuth)),
			auth.SendCodeOptions{},
		)

		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}

		logger.Infof("认证成功，会话已保存")
		return nil
	})
}

func main() {
	logger.Infof("NexusValet v1.0.0 starting...")

	// 加载配置
	cfg, err := config.LoadConfig(config.GetConfigPath())
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// 从配置设置日志级别
	logger.SetLevel(logger.ParseLevel(cfg.Logger.Level))

	logger.Infof("Configuration loaded successfully")

	// 检查并在需要时创建会话
	if err := checkAndCreateSession(cfg); err != nil {
		logger.Fatalf("Authentication failed: %v", err)
	}

	// 创建机器人实例
	bot, err := NewBot(cfg)
	if err != nil {
		logger.Fatalf("Failed to create bot: %v", err)
	}

	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 在 goroutine 中启动机器人
	errChan := make(chan error, 1)
	go func() {
		errChan <- bot.Start()
	}()

	// 等待信号或错误
	select {
	case sig := <-sigChan:
		logger.Infof("Received signal: %v", sig)
	case err := <-errChan:
		if err != nil {
			logger.Errorf("Bot error: %v", err)
		}
	}

	// 停止机器人
	if err := bot.Stop(); err != nil {
		logger.Errorf("Failed to stop bot: %v", err)
	}
}
