package plugin

import (
	"context"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

// DeleteMyMessagesPlugin 删除我的消息插件
type DeleteMyMessagesPlugin struct {
	*BasePlugin
	telegramAPI *tg.Client
	deleteMutex sync.Mutex // 防止并发删除操作
}

// NewDeleteMyMessagesPlugin 创建删除我的消息插件
func NewDeleteMyMessagesPlugin() *DeleteMyMessagesPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "dme",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "删除当前对话中您发送的特定数量的消息插件",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	plugin := &DeleteMyMessagesPlugin{
		BasePlugin: NewBasePlugin(info),
	}

	return plugin
}

// Initialize 初始化插件
func (dmp *DeleteMyMessagesPlugin) Initialize(ctx context.Context, manager interface{}) error {
	if err := dmp.BasePlugin.Initialize(ctx, manager); err != nil {
		return err
	}

	logger.Infof("DeleteMyMessages plugin initialized successfully")
	return nil
}

// Shutdown 关闭插件
func (dmp *DeleteMyMessagesPlugin) Shutdown(ctx context.Context) error {
	return dmp.BasePlugin.Shutdown(ctx)
}

// SetTelegramClient 设置Telegram客户端
func (dmp *DeleteMyMessagesPlugin) SetTelegramClient(client *tg.Client) {
	dmp.telegramAPI = client
	logger.Infof("DeleteMyMessages plugin: Telegram client set successfully")
}

// RegisterCommands 注册命令
func (dmp *DeleteMyMessagesPlugin) RegisterCommands(parser *command.Parser) error {
	// 注册主命令
	parser.RegisterCommand("dme", "删除当前对话中您发送的特定数量的消息", dmp.info.Name, dmp.handleDeleteMyMessages)

	logger.Infof("DeleteMyMessages commands registered successfully")
	return nil
}

// handleDeleteMyMessages 处理删除我的消息命令
func (dmp *DeleteMyMessagesPlugin) handleDeleteMyMessages(ctx *command.CommandContext) error {
	// 检查Telegram API是否可用
	if dmp.telegramAPI == nil {
		return nil
	}

	// 防止并发删除操作
	dmp.deleteMutex.Lock()
	defer dmp.deleteMutex.Unlock()

	// 解析删除数量参数，默认为1
	deleteCount := 1
	if len(ctx.Args) > 0 {
		if count, err := strconv.Atoi(ctx.Args[0]); err == nil && count > 0 {
			deleteCount = count
		} else {
			return nil
		}
	}

	// 限制删除数量，防止误操作
	if deleteCount > 100 {
		return nil
	}

	// 获取当前用户ID
	currentUserID := ctx.Message.UserID
	if currentUserID == 0 {
		return nil
	}

	// 预先解析带 access hash 的 peer，避免异步阶段问题
	chatID := ctx.Message.ChatID
	commandMsgID := ctx.Message.Message.ID
	resolvedPeer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, chatID)
	if err != nil {
		return nil
	}

	// 异步执行：先删除命令消息，再删除用户历史消息，不发送任何提示
	go func() {
		asyncCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// 删除命令消息本身
		_ = dmp.deleteCommandMessage(asyncCtx, resolvedPeer, commandMsgID)

		// 后台删除指定数量的用户消息（排除命令消息）
		_ = dmp.deleteMyMessagesAsync(asyncCtx, resolvedPeer, currentUserID, chatID, commandMsgID, deleteCount)
	}()

	return nil
}

// deleteMyMessages 删除指定数量的我的消息（同步版本，用于直接调用）
func (dmp *DeleteMyMessagesPlugin) deleteMyMessages(ctx *command.CommandContext, userID int64, count int) int {
	if dmp.telegramAPI == nil {
		logger.Errorf("Telegram API is nil")
		return 0
	}

	logger.Debugf("Starting deletion process for user %d, count %d", userID, count)

	// 解析聊天peer
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		logger.Errorf("Failed to resolve peer for chat %d: %v", ctx.Message.ChatID, err)
		return 0
	}

	return dmp.findAndDeleteMessages(ctx.Context, peer, userID, ctx.Message.ChatID, ctx.Message.Message.ID, count)
}

// deleteMyMessagesAsync 删除指定数量的我的消息（异步版本，用于后台操作）
func (dmp *DeleteMyMessagesPlugin) deleteMyMessagesAsync(ctx context.Context, peer tg.InputPeerClass, userID int64, chatID int64, excludeMessageID int, count int) int {
	if dmp.telegramAPI == nil {
		logger.Errorf("Telegram API is nil")
		return 0
	}

	logger.Debugf("Starting async deletion process for user %d in chat %d, count %d", userID, chatID, count)

	return dmp.findAndDeleteMessages(ctx, peer, userID, chatID, excludeMessageID, count)
}

// findAndDeleteMessages 查找并删除消息（共享逻辑）
func (dmp *DeleteMyMessagesPlugin) findAndDeleteMessages(ctx context.Context, peer tg.InputPeerClass, userID int64, chatID int64, excludeMessageID int, count int) int {
	logger.Infof("Finding messages from user %d in chat %d, target count: %d", userID, chatID, count)

	// 获取消息历史并筛选用户消息
	var myMessages []int
	maxBatchSize := 100 // 每批获取的消息数量
	maxBatches := 10    // 最多获取的批次数
	lastMessageID := 0  // 用于分页

	// 分批获取消息历史
	for batch := 0; batch < maxBatches; batch++ {
		// 如果已经找到足够的消息，停止搜索
		if len(myMessages) >= count {
			break
		}

		// 获取一批消息
		messages, err := dmp.getRecentMessagesAsync(ctx, peer, maxBatchSize, lastMessageID)
		if err != nil {
			logger.Errorf("Failed to get message history (batch %d): %v", batch+1, err)
			break
		}

		// 如果没有更多消息，停止搜索
		if len(messages) == 0 {
			logger.Infof("No more messages found after batch %d", batch+1)
			break
		}

		// 记录最后一条消息的ID，用于下一批查询
		lastMessageID = messages[len(messages)-1].ID

		// 筛选当前用户发送的消息
		foundInBatch := 0
		for _, msg := range messages {
			// 排除命令消息，避免后续无法编辑/删除提示
			if excludeMessageID != 0 && msg.ID == excludeMessageID {
				continue
			}
			// 检查是否是当前用户发送的消息
			if msg.FromID != nil {
				if peerUser, ok := msg.FromID.(*tg.PeerUser); ok && peerUser.UserID == userID {
					myMessages = append(myMessages, msg.ID)
					foundInBatch++

					// 如果已经找到足够的消息，停止搜索
					if len(myMessages) >= count {
						break
					}
				}
			}
		}

		logger.Infof("Batch %d: found %d user messages out of %d total messages",
			batch+1, foundInBatch, len(messages))

		// 如果这批消息中没有找到任何用户消息，可能是已经搜索到很早的历史了
		if foundInBatch == 0 && batch > 0 {
			logger.Infof("No user messages found in batch %d, stopping search", batch+1)
			break
		}
	}

	if len(myMessages) == 0 {
		logger.Infof("No messages found to delete")
		return 0
	}

	// 删除消息
	deleted := dmp.deleteMessageBatchAsync(ctx, peer, myMessages)

	logger.Infof("Successfully deleted %d messages out of %d found", deleted, len(myMessages))
	return deleted
}

// getRecentMessagesAsync 获取最近的消息，支持分页（异步版本）
func (dmp *DeleteMyMessagesPlugin) getRecentMessagesAsync(ctx context.Context, peer tg.InputPeerClass, limit int, offsetID int) ([]*tg.Message, error) {
	// 限制单次获取数量，防止API限制
	if limit > 100 {
		limit = 100
	}

	var messages []*tg.Message

	// 构建通用的请求参数
	req := &tg.MessagesGetHistoryRequest{
		Peer:       peer,
		OffsetID:   offsetID, // 使用上一批最后一条消息的ID作为偏移
		OffsetDate: 0,
		AddOffset:  0,
		Limit:      limit,
		MaxID:      0,
		MinID:      0,
		Hash:       0,
	}

	// 如果提供了offsetID，记录日志
	if offsetID > 0 {
		logger.Debugf("Fetching messages with offsetID: %d", offsetID)
	}

	// 发送请求获取消息历史
	resp, err := dmp.telegramAPI.MessagesGetHistory(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get message history: %w", err)
	}

	// 根据不同的响应类型处理结果
	switch result := resp.(type) {
	case *tg.MessagesChannelMessages:
		// 频道/超级群组消息
		logger.Debugf("Got channel messages: %d total, %d messages in response",
			result.Count, len(result.Messages))

		for _, msg := range result.Messages {
			if message, ok := msg.(*tg.Message); ok {
				messages = append(messages, message)
			}
		}

	case *tg.MessagesMessages:
		// 普通群组或私聊消息
		logger.Debugf("Got regular messages: %d messages in response", len(result.Messages))

		for _, msg := range result.Messages {
			if message, ok := msg.(*tg.Message); ok {
				messages = append(messages, message)
			}
		}

	case *tg.MessagesMessagesSlice:
		// 消息切片（部分结果）
		logger.Debugf("Got message slice: %d total, %d messages in response",
			result.Count, len(result.Messages))

		for _, msg := range result.Messages {
			if message, ok := msg.(*tg.Message); ok {
				messages = append(messages, message)
			}
		}

	default:
		logger.Warnf("Unknown response type: %T", resp)
	}

	logger.Debugf("Retrieved %d messages", len(messages))
	return messages, nil
}

// getRecentMessages 获取最近的消息，支持分页（同步版本，保留兼容性）
func (dmp *DeleteMyMessagesPlugin) getRecentMessages(ctx *command.CommandContext, peer tg.InputPeerClass, limit int, offsetID int) ([]*tg.Message, error) {
	return dmp.getRecentMessagesAsync(ctx.Context, peer, limit, offsetID)
}

// deleteMessageBatchAsync 批量删除消息（异步版本）
func (dmp *DeleteMyMessagesPlugin) deleteMessageBatchAsync(ctx context.Context, peer tg.InputPeerClass, messageIDs []int) int {
	if len(messageIDs) == 0 {
		return 0
	}

	deleted := 0
	batchSize := 100 // Telegram API限制

	// 分批删除
	for i := 0; i < len(messageIDs); i += batchSize {
		end := i + batchSize
		if end > len(messageIDs) {
			end = len(messageIDs)
		}

		batch := messageIDs[i:end]
		if dmp.deleteMessagesBatchAsync(ctx, peer, batch) {
			deleted += len(batch)
		}

		// 添加延迟避免API限制
		if end < len(messageIDs) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return deleted
}

// deleteMessagesBatchAsync 删除一批消息（异步版本）
func (dmp *DeleteMyMessagesPlugin) deleteMessagesBatchAsync(ctx context.Context, peer tg.InputPeerClass, messageIDs []int) bool {
	var err error

	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		// 频道/超级群组使用ChannelsDeleteMessages
		channelPeer := &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
		_, err = dmp.telegramAPI.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: channelPeer,
			ID:      messageIDs,
		})

	case *tg.InputPeerChat, *tg.InputPeerUser:
		// 普通群组和私聊使用MessagesDeleteMessages
		_, err = dmp.telegramAPI.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			ID:     messageIDs,
			Revoke: true, // 对所有人删除
		})

	default:
		logger.Warnf("Unsupported peer type for message deletion")
		return false
	}

	if err != nil {
		logger.Errorf("Failed to delete message batch: %v", err)
		return false
	}

	logger.Debugf("Successfully deleted batch of %d messages", len(messageIDs))
	return true
}

// deleteMessageBatch 批量删除消息（同步版本，保留兼容性）
func (dmp *DeleteMyMessagesPlugin) deleteMessageBatch(ctx *command.CommandContext, peer tg.InputPeerClass, messageIDs []int) int {
	return dmp.deleteMessageBatchAsync(ctx.Context, peer, messageIDs)
}

// deleteMessagesBatch 删除一批消息（同步版本，保留兼容性）
func (dmp *DeleteMyMessagesPlugin) deleteMessagesBatch(ctx *command.CommandContext, peer tg.InputPeerClass, messageIDs []int) bool {
	return dmp.deleteMessagesBatchAsync(ctx.Context, peer, messageIDs)
}

// deleteMessage 删除单个消息（同步版本，保留兼容性）
func (dmp *DeleteMyMessagesPlugin) deleteMessage(ctx *command.CommandContext, messageID int) {
	if dmp.telegramAPI == nil {
		return
	}

	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		logger.Debugf("Failed to resolve peer for message deletion: %v", err)
		return
	}

	if err := dmp.deleteCommandMessage(ctx.Context, peer, messageID); err != nil {
		logger.Debugf("Failed to delete message: %v", err)
	}
}

// sendResponse 发送响应消息（编辑原始消息）
func (dmp *DeleteMyMessagesPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 尝试编辑消息，如果失败则发送新消息
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

// updateCommandMessage 更新命令消息内容（用于异步操作）
func (dmp *DeleteMyMessagesPlugin) updateCommandMessage(ctx context.Context, peer tg.InputPeerClass, messageID int, message string) error {
	if dmp.telegramAPI == nil {
		return fmt.Errorf("telegram API not available")
	}

	// 编辑消息
	_, err := dmp.telegramAPI.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      messageID,
		Message: message,
	})

	return err
}

// deleteCommandMessage 删除命令消息（用于异步操作）
func (dmp *DeleteMyMessagesPlugin) deleteCommandMessage(ctx context.Context, peer tg.InputPeerClass, messageID int) error {
	if dmp.telegramAPI == nil {
		return fmt.Errorf("telegram API not available")
	}

	var err error
	// 根据聊天类型选择不同的删除方法
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		// 频道/超级群组使用ChannelsDeleteMessages
		channelPeer := &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
		_, err = dmp.telegramAPI.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: channelPeer,
			ID:      []int{messageID},
		})

	case *tg.InputPeerChat, *tg.InputPeerUser:
		// 普通群组和私聊使用MessagesDeleteMessages
		_, err = dmp.telegramAPI.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			ID:     []int{messageID},
			Revoke: true, // 对所有人删除
		})

	default:
		return fmt.Errorf("unsupported peer type for message deletion")
	}

	return err
}
