package command

import (
	"context"
	"fmt"

	"nexusvalet/internal/core"
	"nexusvalet/internal/peers"
	"nexusvalet/internal/session"
	"nexusvalet/pkg/logger"
	"strings"
	"sync"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// writerFunc is a helper type to implement io.Writer
type writerFunc struct {
	fn func(string) (int, error)
}

func (w *writerFunc) Write(p []byte) (int, error) {
	return w.fn(string(p))
}

// Command 代表一个已注册的命令
type Command struct {
	Name        string
	Description string
	Handler     CommandHandler
	Plugin      string
}

// CommandHandler 是处理命令执行的函数
type CommandHandler func(*CommandContext) error

// CommandContext 为命令执行提供上下文
type CommandContext struct {
	Command      string
	Args         []string
	Message      *core.MessageEvent
	Session      *session.SessionContext
	API          *tg.Client
	Context      context.Context // 添加context用于API调用
	PeerResolver *peers.Resolver // 添加peer resolver用于解析聊天ID
	DownloadFile func(document *tg.Document) ([]byte, error)
	GetDocument  func() (*tg.Document, error)
}

// Parser 处理命令解析和执行
type Parser struct {
	commands     map[string]*Command
	prefix       string
	mutex        sync.RWMutex
	dispatcher   *core.EventDispatcher
	hookManager  *core.HookManager
	sessionMgr   *session.Manager
	telegramAPI  *tg.Client
	peerResolver *peers.Resolver
}

// NewParser 创建一个新的命令解析器
func NewParser(prefix string, dispatcher *core.EventDispatcher, hookManager *core.HookManager) *Parser {
	parser := &Parser{
		commands:    make(map[string]*Command),
		prefix:      prefix,
		dispatcher:  dispatcher,
		hookManager: hookManager,
	}

	// 将解析器注册为消息监听器 - 只处理发出消息（userbot 模式）
	filter := core.ListenerFilter{
		Outgoing: true,
		Incoming: false,
	}
	dispatcher.RegisterPrefixListenerWithFilter("command_parser", prefix, parser.handleMessage, 100, filter)

	logger.Infof("Command parser initialized with prefix: %s", prefix)
	return parser
}

// SetSessionManager 设置会话管理器
func (p *Parser) SetSessionManager(sessionMgr *session.Manager) {
	p.sessionMgr = sessionMgr
}

// SetTelegramAPI 设置 Telegram API 客户端和 Peer 解析器
func (p *Parser) SetTelegramAPI(api *tg.Client, peerResolver *peers.Resolver) {
	p.telegramAPI = api
	p.peerResolver = peerResolver
}

// GetTelegramAPI 返回 Telegram API 客户端实例
func (p *Parser) GetTelegramAPI() *tg.Client {
	return p.telegramAPI
}

// RegisterCommand 注册一个新命令
func (p *Parser) RegisterCommand(name, description, plugin string, handler CommandHandler) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	command := &Command{
		Name:        name,
		Description: description,
		Handler:     handler,
		Plugin:      plugin,
	}

	p.commands[name] = command
	logger.Debugf("Registered command: %s from plugin: %s", name, plugin)
}

// UnregisterCommand 移除一个命令
func (p *Parser) UnregisterCommand(name string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _, exists := p.commands[name]; exists {
		delete(p.commands, name)
		logger.Debugf("Unregistered command: %s", name)
	}
}

// UnregisterPluginCommands 从特定插件中移除所有命令
func (p *Parser) UnregisterPluginCommands(plugin string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for name, command := range p.commands {
		if command.Plugin == plugin {
			delete(p.commands, name)
			logger.Debugf("Unregistered command: %s from plugin: %s", name, plugin)
		}
	}
}

// GetCommand 根据名称检索命令
func (p *Parser) GetCommand(name string) (*Command, bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	command, exists := p.commands[name]
	return command, exists
}

// GetAllCommands 返回所有已注册的命令
func (p *Parser) GetAllCommands() map[string]*Command {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	result := make(map[string]*Command)
	for name, command := range p.commands {
		result[name] = command
	}
	return result
}

// GetCommandsByPlugin 返回来自特定插件的所有命令
func (p *Parser) GetCommandsByPlugin(plugin string) map[string]*Command {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	result := make(map[string]*Command)
	for name, command := range p.commands {
		if command.Plugin == plugin {
			result[name] = command
		}
	}
	return result
}

// handleMessage 处理以命令前缀开始的传入消息
func (p *Parser) handleMessage(ctx context.Context, event interface{}) error {
	msgEvent, ok := event.(*core.MessageEvent)
	if !ok {
		return nil
	}

	logger.Infof("Command parser received message: '%s'", msgEvent.Text)

	// 检查消息是否以命令前缀开始
	if !strings.HasPrefix(msgEvent.Text, p.prefix) {
		logger.Debugf("Message does not start with prefix '%s'", p.prefix)
		return nil
	}

	logger.Infof("Processing command message: '%s'", msgEvent.Text)

	// 解析命令和参数
	commandText := strings.TrimPrefix(msgEvent.Text, p.prefix)
	parts := strings.Fields(commandText)
	if len(parts) == 0 {
		return nil
	}

	commandName := parts[0]
	args := parts[1:]

	// 创建命令事件
	cmdEvent := &core.CommandEvent{
		Command: commandName,
		Args:    args,
		Message: msgEvent,
	}

	// 首先分发给命令监听器
	if err := p.dispatcher.DispatchCommand(ctx, cmdEvent); err != nil {
		logger.Errorf("Failed to dispatch command event: %v", err)
	}

	// 如果命令存在则执行它
	return p.executeCommand(ctx, commandName, args, msgEvent)
}

// executeCommand executes a registered command
func (p *Parser) executeCommand(ctx context.Context, commandName string, args []string, msgEvent *core.MessageEvent) error {
	command, exists := p.GetCommand(commandName)
	if !exists {
		logger.Debugf("Unknown command: %s", commandName)
		return nil // Don't treat unknown commands as errors
	}

	// Execute BeforeCommand hooks
	hookData := map[string]interface{}{
		"command": commandName,
		"args":    args,
		"message": msgEvent,
		"plugin":  command.Plugin,
	}

	if err := p.hookManager.ExecuteHooksWithContext(ctx, core.BeforeCommand, hookData); err != nil {
		logger.Errorf("BeforeCommand hook failed: %v", err)
		return err
	}

	// Get or create session
	var sessionCtx *session.SessionContext
	if p.sessionMgr != nil {
		sess, err := p.sessionMgr.GetSession(msgEvent.UserID, msgEvent.ChatID)
		if err != nil {
			logger.Errorf("Failed to get session: %v", err)
		} else {
			sessionCtx = session.NewSessionContext(sess, p.sessionMgr)
		}
	}

	// Create command context - 直接提供gotd API访问
	cmdCtx := &CommandContext{
		Command:      commandName,
		Args:         args,
		Message:      msgEvent,
		Session:      sessionCtx,
		API:          p.telegramAPI,
		Context:      ctx,
		PeerResolver: p.peerResolver,
		GetDocument: func() (*tg.Document, error) {
			// First, check if the current message has media
			if msgEvent.Message != nil && msgEvent.Message.Media != nil {
				switch media := msgEvent.Message.Media.(type) {
				case *tg.MessageMediaDocument:
					if doc, ok := media.Document.(*tg.Document); ok {
						return doc, nil
					}
				}
			}

			// If no media in current message, check if this message is replying to a message with media
			if msgEvent.Message != nil && msgEvent.Message.ReplyTo != nil {
				if replyToMsg, ok := msgEvent.Message.ReplyTo.(*tg.MessageReplyHeader); ok {
					logger.Debugf("Message is replying to message ID %d, checking for media", replyToMsg.ReplyToMsgID)

					// Try to get the replied message from Telegram API
					if p.telegramAPI != nil {
						// Get the message using the message ID
						resp, err := p.telegramAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{
							&tg.InputMessageID{ID: replyToMsg.ReplyToMsgID},
						})
						if err != nil {
							logger.Errorf("Failed to get replied message: %v", err)
							return nil, fmt.Errorf("failed to get replied message: %w", err)
						}

						// Check if we got a valid response
						if messages, ok := resp.(*tg.MessagesMessages); ok && len(messages.Messages) > 0 {
							if repliedMsg, ok := messages.Messages[0].(*tg.Message); ok && repliedMsg.Media != nil {
								switch media := repliedMsg.Media.(type) {
								case *tg.MessageMediaDocument:
									if doc, ok := media.Document.(*tg.Document); ok {
										logger.Debugf("Found document in replied message")
										return doc, nil
									}
								}
							}
						} else if channelMessages, ok := resp.(*tg.MessagesChannelMessages); ok && len(channelMessages.Messages) > 0 {
							if repliedMsg, ok := channelMessages.Messages[0].(*tg.Message); ok && repliedMsg.Media != nil {
								switch media := repliedMsg.Media.(type) {
								case *tg.MessageMediaDocument:
									if doc, ok := media.Document.(*tg.Document); ok {
										logger.Debugf("Found document in replied channel message")
										return doc, nil
									}
								}
							}
						}
					}
				}
			}

			return nil, fmt.Errorf("no document found in message or replied message")
		},
		DownloadFile: func(document *tg.Document) ([]byte, error) {
			if p.telegramAPI == nil {
				return nil, fmt.Errorf("telegram API client not available")
			}

			// Create downloader
			d := downloader.NewDownloader()

			// Create a buffer to store the downloaded data
			buf := &strings.Builder{}

			// Download the file to the buffer
			_, err := d.Download(p.telegramAPI, &tg.InputDocumentFileLocation{
				ID:            document.ID,
				AccessHash:    document.AccessHash,
				FileReference: document.FileReference,
			}).Stream(ctx, &writerFunc{fn: func(s string) (int, error) {
				return buf.WriteString(s)
			}})

			if err != nil {
				return nil, fmt.Errorf("failed to download file: %w", err)
			}

			return []byte(buf.String()), nil
		},
	}

	// Execute the command
	var executeErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				executeErr = fmt.Errorf("command panicked: %v", r)
				logger.Errorf("Command %s panicked: %v", commandName, r)
			}
		}()

		executeErr = command.Handler(cmdCtx)
	}()

	// Execute AfterCommand hooks
	hookData["error"] = executeErr
	if err := p.hookManager.ExecuteHooksWithContext(ctx, core.AfterCommand, hookData); err != nil {
		logger.Errorf("AfterCommand hook failed: %v", err)
	}

	if executeErr != nil {
		logger.Errorf("Command %s failed: %v", commandName, executeErr)
		return executeErr
	}

	logger.Debugf("Command %s executed successfully", commandName)
	return nil
}

// ParseCommand parses a command string into command name and arguments
func (p *Parser) ParseCommand(text string) (string, []string, bool) {
	if !strings.HasPrefix(text, p.prefix) {
		return "", nil, false
	}

	commandText := strings.TrimPrefix(text, p.prefix)
	parts := strings.Fields(commandText)
	if len(parts) == 0 {
		return "", nil, false
	}

	return parts[0], parts[1:], true
}

// IsCommand checks if a text is a command
func (p *Parser) IsCommand(text string) bool {
	_, _, isCmd := p.ParseCommand(text)
	return isCmd
}

// GetPrefix returns the command prefix
func (p *Parser) GetPrefix() string {
	return p.prefix
}

// SetPrefix sets the command prefix
func (p *Parser) SetPrefix(prefix string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	oldPrefix := p.prefix
	p.prefix = prefix

	// Unregister old listener and register new one
	p.dispatcher.UnregisterListener(core.MessageListener, "command_parser")
	filter := core.ListenerFilter{
		Outgoing: true,
		Incoming: false,
	}
	p.dispatcher.RegisterPrefixListenerWithFilter("command_parser", prefix, p.handleMessage, 100, filter)

	logger.Debugf("Command prefix changed from %s to %s", oldPrefix, prefix)
}
