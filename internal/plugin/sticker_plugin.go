package plugin

import (
	"archive/zip"
	"fmt"
	"io"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"os"
	"path/filepath"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// StickerPlugin 贴纸包下载插件
type StickerPlugin struct {
	*BasePlugin
}

// StickerSetInfo 贴纸包信息
type StickerSetInfo struct {
	Set       *tg.StickerSet
	Packs     []*tg.StickerPack
	Documents []*tg.Document
}

// NewStickerPlugin 创建贴纸插件
func NewStickerPlugin() *StickerPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "sticker",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "获取整个贴纸包的贴纸",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	return &StickerPlugin{
		BasePlugin: NewBasePlugin(info),
	}
}

// RegisterCommands 实现CommandPlugin接口
func (sp *StickerPlugin) RegisterCommands(parser *command.Parser) error {
	parser.RegisterCommand("getstickers", "获取整个贴纸包的贴纸", sp.info.Name, sp.handleGetStickers)
	parser.RegisterCommand("gs", "获取整个贴纸包的贴纸(简写)", sp.info.Name, sp.handleGetStickers)
	logger.Infof("Sticker commands registered successfully")
	return nil
}

// handleGetStickers 处理获取贴纸包命令
func (sp *StickerPlugin) handleGetStickers(ctx *command.CommandContext) error {
	// 检查是否有回复消息
	if ctx.Message.Message.ReplyTo == nil {
		return sp.sendResponse(ctx, "请回复一张贴纸。")
	}

	// 获取回复消息中的贴纸
	replyMsg, err := sp.getReplyMessage(ctx)
	if err != nil {
		return sp.sendResponse(ctx, fmt.Sprintf("获取回复消息失败: %v", err))
	}
	sticker, err := sp.getStickerFromMessage(ctx, replyMsg)
	if err != nil {
		return sp.sendResponse(ctx, fmt.Sprintf("获取贴纸失败: %v", err))
	}

	if sticker == nil {
		return sp.sendResponse(ctx, "请回复一张贴纸。")
	}

	// 检查贴纸是否属于贴纸包
	if sticker.Document == nil {
		return sp.sendResponse(ctx, "回复的贴纸不属于任何贴纸包。")
	}

	// 获取贴纸包信息
	doc, ok := sticker.Document.(*tg.Document)
	if !ok {
		return sp.sendResponse(ctx, "文档类型错误")
	}

	// 从文档属性中获取贴纸包信息
	var stickerSet tg.InputStickerSetClass
	for _, attr := range doc.Attributes {
		if stickerAttr, ok := attr.(*tg.DocumentAttributeSticker); ok {
			if stickerAttr.Stickerset != nil {
				stickerSet = stickerAttr.Stickerset
				break
			}
		}
	}

	if stickerSet == nil {
		return sp.sendResponse(ctx, "回复的贴纸不属于任何贴纸包。")
	}

	stickerSetInfo, err := sp.getStickerSetInfo(ctx, stickerSet)
	if err != nil {
		return sp.sendResponse(ctx, "回复的贴纸不存在于任何贴纸包中。")
	}

	// 下载贴纸包
	return sp.downloadStickerSet(ctx, stickerSetInfo)
}

// getReplyMessage 获取回复的消息
func (sp *StickerPlugin) getReplyMessage(ctx *command.CommandContext) (*tg.Message, error) {
	if ctx.Message.Message.ReplyTo == nil {
		return nil, fmt.Errorf("没有回复消息")
	}

	replyTo, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader)
	if !ok {
		return nil, fmt.Errorf("回复消息格式错误")
	}

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
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: replyTo.ReplyToMsgID}},
		})
	} else {
		// 普通群组或私聊
		messages, err = ctx.API.MessagesGetMessages(ctx.Context, []tg.InputMessageClass{&tg.InputMessageID{ID: replyTo.ReplyToMsgID}})
	}

	if err != nil {
		return nil, err
	}

	// 解析消息
	var msg *tg.Message
	if messagesSlice, ok := messages.(*tg.MessagesMessages); ok {
		if len(messagesSlice.Messages) > 0 {
			if m, ok := messagesSlice.Messages[0].(*tg.Message); ok {
				msg = m
			}
		}
	} else if channelMessages, ok := messages.(*tg.MessagesChannelMessages); ok {
		if len(channelMessages.Messages) > 0 {
			if m, ok := channelMessages.Messages[0].(*tg.Message); ok {
				msg = m
			}
		}
	}

	if msg == nil {
		return nil, fmt.Errorf("消息不存在")
	}

	return msg, nil
}

// getStickerFromMessage 从消息中获取贴纸
func (sp *StickerPlugin) getStickerFromMessage(ctx *command.CommandContext, message *tg.Message) (*tg.MessageMediaDocument, error) {
	if message.Media == nil {
		return nil, fmt.Errorf("消息不包含媒体")
	}

	media, ok := message.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, fmt.Errorf("消息不是文档类型")
	}

	document, ok := media.Document.(*tg.Document)
	if !ok {
		return nil, fmt.Errorf("文档类型错误")
	}

	// 检查是否是贴纸
	isSticker := false
	for _, attr := range document.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeSticker); ok {
			isSticker = true
			break
		}
	}

	if !isSticker {
		return nil, fmt.Errorf("不是贴纸")
	}

	return media, nil
}

// getStickerSetInfo 获取贴纸包信息
func (sp *StickerPlugin) getStickerSetInfo(ctx *command.CommandContext, stickerSet tg.InputStickerSetClass) (*StickerSetInfo, error) {
	// 调用API获取贴纸包信息
	result, err := ctx.API.MessagesGetStickerSet(ctx.Context, &tg.MessagesGetStickerSetRequest{
		Stickerset: stickerSet,
		Hash:       0,
	})
	if err != nil {
		return nil, fmt.Errorf("获取贴纸包信息失败: %w", err)
	}

	set, ok := result.(*tg.MessagesStickerSet)
	if !ok {
		return nil, fmt.Errorf("贴纸包响应格式错误")
	}

	// 提取文档列表
	var documents []*tg.Document
	for _, doc := range set.Documents {
		if document, ok := doc.(*tg.Document); ok {
			documents = append(documents, document)
		}
	}

	// 转换Packs类型
	var packs []*tg.StickerPack
	for _, pack := range set.Packs {
		packs = append(packs, &pack)
	}

	return &StickerSetInfo{
		Set:       &set.Set,
		Packs:     packs,
		Documents: documents,
	}, nil
}

// downloadStickerSet 下载贴纸包
func (sp *StickerPlugin) downloadStickerSet(ctx *command.CommandContext, stickerSetInfo *StickerSetInfo) error {
	// 创建临时目录
	tempDir := filepath.Join(os.TempDir(), "sticker_download", stickerSetInfo.Set.ShortName)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 更新状态消息
	statusMsg := fmt.Sprintf("正在下载 %s 中的 %d 张贴纸...", stickerSetInfo.Set.ShortName, stickerSetInfo.Set.Count)
	if err := sp.sendResponse(ctx, statusMsg); err != nil {
		logger.Warnf("发送状态消息失败: %v", err)
	}

	// 创建emoji映射
	emojiMap := make(map[int64]string)
	for _, pack := range stickerSetInfo.Packs {
		for _, docID := range pack.Documents {
			emojiMap[docID] += pack.Emoticon
		}
	}

	// 下载所有贴纸
	for i, document := range stickerSetInfo.Documents {
		// 确定文件扩展名
		ext := "webp"
		for _, attr := range document.Attributes {
			if stickerAttr, ok := attr.(*tg.DocumentAttributeSticker); ok {
				if stickerAttr.Mask {
					// 这是动画贴纸
					ext = "tgs"
				}
			}
		}

		// 检查是否是视频贴纸
		if document.MimeType == "video/mp4" {
			ext = "mp4"
		}

		// 下载贴纸
		filename := fmt.Sprintf("%03d.%s", i, ext)
		filePath := filepath.Join(tempDir, filename)

		if err := sp.downloadSticker(ctx, document, filePath); err != nil {
			logger.Warnf("下载贴纸 %s 失败: %v", filename, err)
			continue
		}

		// 写入pack.txt文件
		packFile := filepath.Join(tempDir, "pack.txt")
		emoji := emojiMap[document.ID]
		if emoji == "" {
			emoji = "😀" // 默认emoji
		}

		packEntry := fmt.Sprintf("{'image_file': '%s','emojis':%s},", filename, emoji)
		if err := sp.appendToFile(packFile, packEntry); err != nil {
			logger.Warnf("写入pack.txt失败: %v", err)
		}
	}

	// 打包并上传
	return sp.packageAndUpload(ctx, tempDir, stickerSetInfo.Set.ShortName)
}

// downloadSticker 下载单个贴纸
func (sp *StickerPlugin) downloadSticker(ctx *command.CommandContext, document *tg.Document, filepath string) error {
	// 创建文件
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	// 使用gotd API下载文件
	location := &tg.InputDocumentFileLocation{
		ID:            document.ID,
		AccessHash:    document.AccessHash,
		FileReference: document.FileReference,
	}

	const chunkSize = 512 * 1024 // 512KB chunks
	offset := int64(0)

	for {
		resp, err := ctx.API.UploadGetFile(ctx.Context, &tg.UploadGetFileRequest{
			Location: location,
			Offset:   offset,
			Limit:    chunkSize,
		})
		if err != nil {
			return fmt.Errorf("下载文件失败: %w", err)
		}

		if fileResp, ok := resp.(*tg.UploadFile); ok {
			if _, err := file.Write(fileResp.Bytes); err != nil {
				return fmt.Errorf("写入文件失败: %w", err)
			}

			if len(fileResp.Bytes) < chunkSize {
				break // 最后一块
			}
			offset += int64(len(fileResp.Bytes))
		} else {
			return fmt.Errorf("意外的响应类型")
		}
	}

	return nil
}

// appendToFile 追加内容到文件
func (sp *StickerPlugin) appendToFile(filepath, content string) error {
	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(content)
	return err
}

// packageAndUpload 打包并上传
func (sp *StickerPlugin) packageAndUpload(ctx *command.CommandContext, tempDir, setName string) error {
	// 更新状态消息
	if err := sp.sendResponse(ctx, "下载完毕，打包上传中。"); err != nil {
		logger.Warnf("发送状态消息失败: %v", err)
	}

	// 创建ZIP文件
	zipPath := filepath.Join(os.TempDir(), setName+".zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("创建ZIP文件失败: %w", err)
	}
	defer zipFile.Close()

	// 创建ZIP写入器
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// 添加文件到ZIP
	if err := sp.addFilesToZip(zipWriter, tempDir, ""); err != nil {
		return fmt.Errorf("添加文件到ZIP失败: %w", err)
	}

	// 确保ZIP写入器关闭
	zipWriter.Close()
	zipFile.Close()

	// 上传ZIP文件
	err = sp.uploadZipFile(ctx, zipPath, setName)

	// 上传完成后删除临时文件
	os.Remove(zipPath)

	return err
}

// addFilesToZip 添加文件到ZIP
func (sp *StickerPlugin) addFilesToZip(zipWriter *zip.Writer, basePath, zipPath string) error {
	return filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// 计算ZIP中的相对路径
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}

		// 使用正斜杠作为ZIP内部路径分隔符
		zipFilePath := filepath.ToSlash(relPath)

		// 创建ZIP文件条目
		zipFile, err := zipWriter.Create(zipFilePath)
		if err != nil {
			return err
		}

		// 打开源文件
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// 复制文件内容
		_, err = io.Copy(zipFile, srcFile)
		return err
	})
}

// uploadZipFile 上传ZIP文件
func (sp *StickerPlugin) uploadZipFile(ctx *command.CommandContext, zipPath, setName string) error {
	// 获取peer
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("解析peer失败: %w", err)
	}

	// 读取ZIP文件
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("读取ZIP文件失败: %w", err)
	}

	// 使用uploader上传文件
	uploader := uploader.NewUploader(ctx.API)
	file, err := uploader.FromBytes(ctx.Context, setName+".zip", zipData)
	if err != nil {
		return fmt.Errorf("上传ZIP文件失败: %w", err)
	}

	// 发送文档消息
	_, err = ctx.API.MessagesSendMedia(ctx.Context, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: "application/zip",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{
					FileName: setName + ".zip",
				},
			},
		},
		Message:  setName,
		RandomID: time.Now().UnixNano(),
		ReplyTo: &tg.InputReplyToMessage{
			ReplyToMsgID: ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader).ReplyToMsgID,
		},
	})

	if err != nil {
		return fmt.Errorf("发送文档失败: %w", err)
	}

	// 删除原消息
	peer, err = ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err == nil {
		ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
			ID: []int{ctx.Message.Message.ID},
		})
	}

	return nil
}

// sendResponse 发送响应消息
func (sp *StickerPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("解析peer失败: %w", err)
	}

	if ctx.Message.ChatID > 0 {
		// 私聊：编辑消息
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
