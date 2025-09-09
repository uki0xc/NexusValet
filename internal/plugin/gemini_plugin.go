package plugin

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
	"github.com/gotd/td/tg"
)

// GeminiPlugin Gemini AI插件
type GeminiPlugin struct {
	*BasePlugin
	db         *sql.DB
	httpClient *http.Client
}

// GeminiRequest 发送给Gemini API的请求结构
type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}

// GeminiContent 内容结构
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart 内容部分
type GeminiPart struct {
	Text       string           `json:"text,omitempty"`
	InlineData *GeminiImageData `json:"inline_data,omitempty"`
}

// GeminiImageData 图片数据
type GeminiImageData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

// GeminiResponse Gemini API响应结构
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
	Error      *GeminiError      `json:"error,omitempty"`
}

// GeminiCandidate 候选回答
type GeminiCandidate struct {
	Content GeminiContent `json:"content"`
}

// GeminiError 错误信息
type GeminiError struct {
	Message string `json:"message"`
}

// NewGeminiPlugin 创建Gemini插件
func NewGeminiPlugin(db *sql.DB) *GeminiPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "gemini",
			Version:     "1.0.0",
			Description: "Gemini AI 智能问答插件",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	plugin := &GeminiPlugin{
		BasePlugin: NewBasePlugin(info),
		db:         db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// 初始化数据库表
	plugin.initDatabase()

	return plugin
}

// initDatabase 初始化数据库表
func (gp *GeminiPlugin) initDatabase() {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS gemini_config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`

	_, err := gp.db.Exec(createTableSQL)
	if err != nil {
		logger.Errorf("Failed to create gemini_config table: %v", err)
	}
}

// RegisterCommands 实现CommandPlugin接口
func (gp *GeminiPlugin) RegisterCommands(parser *command.Parser) error {
	// 注册简化的gemini命令 - 智能判断文本/图片模式
	parser.RegisterCommand("gemini", "Gemini AI智能问答 - 自动识别文本/图片", gp.info.Name, gp.handleGeminiSmart)
	parser.RegisterCommand("gm", "Gemini AI智能问答 - gemini的简写", gp.info.Name, gp.handleGeminiSmart)

	logger.Infof("Gemini commands registered successfully")
	return nil
}

// handleGeminiSmart 智能处理gemini命令 - 自动判断文本/图片模式
func (gp *GeminiPlugin) handleGeminiSmart(ctx *command.CommandContext) error {
	// 智能判断是否为图片模式
	hasMedia := ctx.Message.Message.Media != nil

	// 判断是否为回复模式 - 检查参数中是否有 "reply" 或 "r"
	shouldReply := false
	for _, arg := range ctx.Args {
		if arg == "reply" || arg == "r" {
			shouldReply = true
			// 从参数中移除 reply/r 标记
			newArgs := make([]string, 0, len(ctx.Args)-1)
			for _, a := range ctx.Args {
				if a != "reply" && a != "r" {
					newArgs = append(newArgs, a)
				}
			}
			ctx.Args = newArgs
			break
		}
	}

	return gp.processGeminiRequest(ctx, hasMedia, shouldReply)
}

// processGeminiRequest 处理Gemini请求的核心逻辑
func (gp *GeminiPlugin) processGeminiRequest(ctx *command.CommandContext, isVision bool, shouldReply bool) error {
	// 处理设置命令 - 简化语法
	if len(ctx.Args) >= 1 {
		switch ctx.Args[0] {
		case "key", "k":
			if len(ctx.Args) >= 2 {
				return gp.setAPIKey(ctx, ctx.Args[1])
			}
			return gp.sendResponse(ctx, "❌ 请提供API密钥\n\n使用方法：`.gemini key 你的API密钥`", false)
		case "model", "m":
			if len(ctx.Args) >= 2 {
				return gp.setModel(ctx, ctx.Args[1])
			}
			return gp.sendResponse(ctx, "❌ 请提供模型名称\n\n使用方法：`.gemini model gemini-1.5-pro`", false)
		case "auto", "a":
			if len(ctx.Args) >= 2 {
				return gp.setAutoRemove(ctx, ctx.Args[1])
			}
			return gp.sendResponse(ctx, "❌ 请提供设置值\n\n使用方法：`.gemini auto True` 或 `.gemini auto False`", false)
		case "config", "c":
			return gp.showConfig(ctx)
		}
	}

	// 获取配置
	apiKey, err := gp.getConfig("gemini_key")
	if err != nil || apiKey == "" {
		return gp.sendResponse(ctx, "❌ 错误：未设置 API key\n\n使用方法：`.gemini key 你的API密钥`", false)
	}

	model, err := gp.getConfig("gemini_model")
	if err != nil || model == "" {
		model = "gemini-1.5-flash" // 默认模型
	}

	autoRemove, _ := gp.getConfig("gemini_auto_remove")

	// 获取问题文本和媒体
	text := strings.Join(ctx.Args, " ")
	var mediaData string
	var questionType string
	var replyText string
	var replyUserInfo string

	// 处理回复消息
	if ctx.Message.Message.ReplyTo != nil {
		if replyToMsg, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
			// 这里需要通过API获取被回复的消息
			// 为了简化，我们先跳过回复消息的处理
			replyUserInfo = fmt.Sprintf("回复消息ID: %d", replyToMsg.ReplyToMsgID)
		}
	}

	// 处理图片模式
	if isVision {
		var mediaMsg *tg.Message

		// 检查当前消息是否有媒体
		if ctx.Message.Message.Media != nil {
			mediaMsg = ctx.Message.Message
		} else {
			// 简化处理：如果当前消息没有媒体，返回错误
			return gp.sendResponse(ctx, "❌ 请直接带图提问", false)
		}

		// 下载并处理图片
		mediaData, err = gp.downloadAndProcessImage(ctx, mediaMsg)
		if err != nil {
			return gp.sendResponse(ctx, fmt.Sprintf("❌ 图片处理失败：%v", err), false)
		}

		if text == "" {
			text = "用中文描述此图片"
			questionType = "empty"
		}
	} else {
		// 文本模式
		if text == "" {
			questionType = "empty"
			if replyText == "" {
				return gp.sendResponse(ctx, "❌ 请直接提问或回复一条有文字内容的消息", false)
			}
			text = replyText
		}
	}

	// 构建问题
	question := text
	if replyText != "" && questionType != "empty" {
		question = fmt.Sprintf("%s: \n%s\n\n------\n\n%s", replyUserInfo, replyText, text)
	} else if questionType == "empty" {
		question = "尽可能简短地回答"
	}

	// 发送处理中消息
	processingMsg := "🤔..."
	if isVision {
		processingMsg = "📷 处理图片中..."
	}

	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 编辑原消息显示处理状态
	_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      ctx.Message.Message.ID,
		Message: processingMsg,
	})
	if err != nil {
		logger.Errorf("Failed to edit message: %v", err)
	}

	// 调用Gemini API
	answer, err := gp.callGeminiAPI(apiKey, model, question, mediaData, isVision)
	if err != nil {
		errorMsg := fmt.Sprintf("❌ 错误：%v", err)

		// 编辑消息显示错误
		_, editErr := ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: errorMsg,
		})

		if editErr != nil {
			// 如果编辑失败，发送新消息
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  errorMsg,
				RandomID: time.Now().UnixNano(),
			})
		}

		// 延迟删除错误消息
		go func() {
			time.Sleep(10 * time.Second)
			ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
				ID: []int{ctx.Message.Message.ID},
			})
		}()

		// 自动删除空提问
		if autoRemove == "True" && questionType == "empty" {
			go func() {
				time.Sleep(1 * time.Second)
				ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
					ID: []int{ctx.Message.Message.ID},
				})
			}()
		}

		return err
	}

	// 发送回答
	if shouldReply && ctx.Message.Message.ReplyTo != nil {
		// 回复到原消息
		if replyToMsg, ok := ctx.Message.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:      peer,
				Message:   answer,
				RandomID:  time.Now().UnixNano(),
				ReplyTo:   &tg.InputReplyToMessage{ReplyToMsgID: replyToMsg.ReplyToMsgID},
				NoWebpage: true,
			})
		}

		// 删除处理消息
		ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
			ID: []int{ctx.Message.Message.ID},
		})
	} else {
		// 编辑原消息显示回答
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: answer,
		})

		if err != nil {
			// 如果编辑失败，发送新消息
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:      peer,
				Message:   answer,
				RandomID:  time.Now().UnixNano(),
				NoWebpage: true,
			})
		}
	}

	// 自动删除空提问
	if autoRemove == "True" && questionType == "empty" {
		go func() {
			time.Sleep(1 * time.Second)
			ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
				ID: []int{ctx.Message.Message.ID},
			})
		}()
	}

	return err
}

// setAPIKey 设置API密钥
func (gp *GeminiPlugin) setAPIKey(ctx *command.CommandContext, key string) error {
	key = strings.TrimSpace(key)
	err := gp.setConfig("gemini_key", key)
	if err != nil {
		return gp.sendResponse(ctx, fmt.Sprintf("❌ 设置API密钥失败：%v", err), true)
	}

	return gp.sendResponse(ctx, fmt.Sprintf("✅ 已设置 API key: `%s`", key), true)
}

// setModel 设置模型
func (gp *GeminiPlugin) setModel(ctx *command.CommandContext, model string) error {
	model = strings.TrimSpace(model)
	err := gp.setConfig("gemini_model", model)
	if err != nil {
		return gp.sendResponse(ctx, fmt.Sprintf("❌ 设置模型失败：%v", err), true)
	}

	return gp.sendResponse(ctx, fmt.Sprintf("✅ 已设置 model: `%s`", model), true)
}

// setAutoRemove 设置自动删除
func (gp *GeminiPlugin) setAutoRemove(ctx *command.CommandContext, autoRemove string) error {
	autoRemove = strings.TrimSpace(autoRemove)
	err := gp.setConfig("gemini_auto_remove", autoRemove)
	if err != nil {
		return gp.sendResponse(ctx, fmt.Sprintf("❌ 设置自动删除失败：%v", err), true)
	}

	return gp.sendResponse(ctx, fmt.Sprintf("✅ 已设置自动删除空提问: `%s`", autoRemove), true)
}

// downloadAndProcessImage 下载并处理图片
func (gp *GeminiPlugin) downloadAndProcessImage(ctx *command.CommandContext, mediaMsg *tg.Message) (string, error) {
	// 创建临时文件
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("gemini_tmp_%s.png", uuid.New().String()))
	defer os.Remove(tmpFile)

	// 下载媒体文件
	location, err := gp.getMediaLocation(mediaMsg)
	if err != nil {
		return "", fmt.Errorf("获取媒体位置失败: %w", err)
	}

	// 下载文件
	fileBytes, err := gp.downloadFile(ctx, location)
	if err != nil {
		return "", fmt.Errorf("下载文件失败: %w", err)
	}

	// 解码图片
	img, _, err := image.Decode(bytes.NewReader(fileBytes))
	if err != nil {
		return "", fmt.Errorf("解码图片失败: %w", err)
	}

	// 保存为PNG格式
	file, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		return "", fmt.Errorf("编码PNG失败: %w", err)
	}

	// 读取文件并转换为base64
	imageData, err := os.ReadFile(tmpFile)
	if err != nil {
		return "", fmt.Errorf("读取图片文件失败: %w", err)
	}

	return base64.StdEncoding.EncodeToString(imageData), nil
}

// getMediaLocation 获取媒体文件位置
func (gp *GeminiPlugin) getMediaLocation(msg *tg.Message) (tg.InputFileLocationClass, error) {
	if msg.Media == nil {
		return nil, fmt.Errorf("消息不包含媒体")
	}

	switch media := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := media.Photo.(*tg.Photo); ok {
			if len(photo.Sizes) > 0 {
				// 获取最大尺寸的图片
				var maxSize *tg.PhotoSize
				for _, size := range photo.Sizes {
					if photoSize, ok := size.(*tg.PhotoSize); ok {
						if maxSize == nil || photoSize.Size > maxSize.Size {
							maxSize = photoSize
						}
					}
				}
				if maxSize != nil {
					return &tg.InputPhotoFileLocation{
						ID:            photo.ID,
						AccessHash:    photo.AccessHash,
						FileReference: photo.FileReference,
						ThumbSize:     maxSize.Type,
					}, nil
				}
			}
		}
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.(*tg.Document); ok {
			return &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
			}, nil
		}
	}

	return nil, fmt.Errorf("不支持的媒体类型")
}

// downloadFile 下载文件
func (gp *GeminiPlugin) downloadFile(ctx *command.CommandContext, location tg.InputFileLocationClass) ([]byte, error) {
	// 使用gotd API下载文件
	const chunkSize = 512 * 1024 // 512KB chunks

	var result []byte
	offset := 0

	for {
		resp, err := ctx.API.UploadGetFile(ctx.Context, &tg.UploadGetFileRequest{
			Location: location,
			Offset:   int64(offset),
			Limit:    chunkSize,
		})
		if err != nil {
			return nil, err
		}

		if file, ok := resp.(*tg.UploadFile); ok {
			result = append(result, file.Bytes...)
			if len(file.Bytes) < chunkSize {
				break // 最后一块
			}
			offset += len(file.Bytes)
		} else {
			return nil, fmt.Errorf("意外的响应类型")
		}
	}

	return result, nil
}

// callGeminiAPI 调用Gemini API
func (gp *GeminiPlugin) callGeminiAPI(apiKey, model, question, mediaData string, isVision bool) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	var request GeminiRequest

	if isVision && mediaData != "" {
		// 图片模式
		request = GeminiRequest{
			Contents: []GeminiContent{
				{
					Parts: []GeminiPart{
						{Text: question},
						{
							InlineData: &GeminiImageData{
								MimeType: "image/png",
								Data:     mediaData,
							},
						},
					},
				},
			},
		}
	} else {
		// 文本模式
		request = GeminiRequest{
			Contents: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "尽可能简单且快速地回答"}}},
				{Role: "model", Parts: []GeminiPart{{Text: "好的 我会尽可能简单且快速地回答"}}},
				{Role: "user", Parts: []GeminiPart{{Text: question}}},
			},
		}
	}

	// 序列化请求
	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建HTTP请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := gp.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		var errorResp GeminiResponse
		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error != nil {
			return "", fmt.Errorf("%s", errorResp.Error.Message)
		}
		return "", fmt.Errorf("响应异常 (状态码: %d)", resp.StatusCode)
	}

	// 解析响应
	var response GeminiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("解析JSON出错: %w", err)
	}

	// 提取回答
	if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("有响应但是无法获取回答，可能是未通过谷歌的审核")
	}

	answer := response.Candidates[0].Content.Parts[0].Text
	if answer == "" {
		return "", fmt.Errorf("回答为空，可能是未通过谷歌的审核")
	}

	return answer, nil
}

// sendResponse 发送响应消息
func (gp *GeminiPlugin) sendResponse(ctx *command.CommandContext, message string, autoDelete bool) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 编辑原消息
	_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      ctx.Message.Message.ID,
		Message: message,
	})

	if err != nil {
		// 如果编辑失败，发送新消息
		_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  message,
			RandomID: time.Now().UnixNano(),
		})
	}

	// 自动删除消息
	if autoDelete {
		go func() {
			time.Sleep(5 * time.Second)
			ctx.API.MessagesDeleteMessages(ctx.Context, &tg.MessagesDeleteMessagesRequest{
				ID: []int{ctx.Message.Message.ID},
			})
		}()
	}

	return err
}

// getConfig 获取配置
func (gp *GeminiPlugin) getConfig(key string) (string, error) {
	var value string
	err := gp.db.QueryRow("SELECT value FROM gemini_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// setConfig 设置配置
func (gp *GeminiPlugin) setConfig(key, value string) error {
	_, err := gp.db.Exec("INSERT OR REPLACE INTO gemini_config (key, value) VALUES (?, ?)", key, value)
	return err
}

// showConfig 显示当前配置
func (gp *GeminiPlugin) showConfig(ctx *command.CommandContext) error {
	apiKey, _ := gp.getConfig("gemini_key")
	model, _ := gp.getConfig("gemini_model")
	autoRemove, _ := gp.getConfig("gemini_auto_remove")

	if model == "" {
		model = "gemini-1.5-flash (默认)"
	}
	if autoRemove == "" {
		autoRemove = "False (默认)"
	}

	// 隐藏API密钥的大部分内容
	maskedKey := "未设置"
	if apiKey != "" {
		if len(apiKey) > 8 {
			maskedKey = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
		} else {
			maskedKey = "****"
		}
	}

	configMsg := fmt.Sprintf(`🤖 Gemini AI 配置信息

🔑 API密钥: %s
🧠 模型: %s  
🗑️ 自动删除: %s

💡 修改配置:
• .gemini key <新密钥>
• .gemini model <新模型>  
• .gemini auto <True/False>`, maskedKey, model, autoRemove)

	return gp.sendResponse(ctx, configMsg, false)
}
