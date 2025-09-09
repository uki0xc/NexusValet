package plugin

import (
	"context"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

var startTime = time.Now() // 启动时间

// CoreCommandsPlugin 核心命令插件
type CoreCommandsPlugin struct {
	*BasePlugin
	telegramAPI *TelegramAPI // Telegram API用于获取账号信息
}

// TelegramAPI 包装Telegram API调用
type TelegramAPI struct {
	client *tg.Client
}

// NewCoreCommandsPlugin 创建核心命令插件
func NewCoreCommandsPlugin() *CoreCommandsPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "core",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "提供基础的系统命令功能",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	return &CoreCommandsPlugin{
		BasePlugin:  NewBasePlugin(info),
		telegramAPI: &TelegramAPI{},
	}
}

// SetTelegramClient 设置Telegram客户端
func (cp *CoreCommandsPlugin) SetTelegramClient(client *tg.Client) {
	cp.telegramAPI.client = client
}

// getTelegramAccountInfo 获取Telegram账号信息
func (cp *CoreCommandsPlugin) getTelegramAccountInfo() string {
	if cp.telegramAPI.client == nil {
		return "Telegram API 未初始化"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 获取自己的用户信息
	users, err := cp.telegramAPI.client.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return fmt.Sprintf("获取账号信息失败: %v", err)
	}

	if len(users) == 0 {
		return "未找到账号信息"
	}

	if user, ok := users[0].(*tg.User); ok {
		var username string
		if user.Username != "" {
			username = "@" + user.Username
		} else {
			username = "(无用户名)"
		}

		var name string
		if user.FirstName != "" {
			name = user.FirstName
			if user.LastName != "" {
				name += " " + user.LastName
			}
		} else {
			name = "未知用户"
		}

		return fmt.Sprintf("%s %s (ID: %d)", name, username, user.ID)
	}

	return "账号信息格式错误"
}

// RegisterCommands 实现CommandPlugin接口
func (cp *CoreCommandsPlugin) RegisterCommands(parser *command.Parser) error {
	// 注册status命令
	parser.RegisterCommand("status", "显示系统状态信息", cp.info.Name, cp.handleStatus)

	// 注册help命令
	parser.RegisterCommand("help", "显示帮助信息", cp.info.Name, cp.handleHelp)

	logger.Infof("Core commands registered successfully")
	return nil
}

// handleStatus 处理status命令
func (cp *CoreCommandsPlugin) handleStatus(ctx *command.CommandContext) error {
	// 获取系统信息
	version := "v1.0.0"
	goVersion := runtime.Version()
	currentTime := time.Now().Format("2006-01-02 15:04:05")
	uptime := time.Since(startTime)

	// 系统信息
	systemOS := runtime.GOOS
	systemArch := runtime.GOARCH

	// 内存信息
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 内核信息
	kernelVersion := cp.getKernelVersion()

	// 插件信息
	pluginCount := 0
	if goManager, ok := cp.manager.(*GoManager); ok {
		pluginCount = len(goManager.GetAllPlugins())
	}

	// 格式化运行时间
	uptimeStr := cp.formatUptime(uptime)

	// 格式化内存大小
	sysStr := cp.formatMemorySize(m.Sys)

	// 获取Telegram账号信息
	accountLine := cp.getTelegramAccountInfo()

	// 构建状态消息
	statusMsg := fmt.Sprintf(`NexusValet 状态报告
当前账号: %s
运行时间: %s
系统信息:
   • Go版本: %s
   • 系统: %s/%s
   • Kernel 版本: %s
   • NexusValet版本: %s
内存使用:
   • 系统占用: %s
插件状态:
   • 已加载插件: %d 个
状态检查时间: %s`,
		accountLine, uptimeStr, goVersion, systemOS, systemArch, kernelVersion, version,
		sysStr, pluginCount, currentTime)

	// 直接使用gotd API发送响应
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
			Message: statusMsg,
		})
		return err
	} else {
		// 群聊：先尝试编辑
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: statusMsg,
		})
		if err != nil {
			// 编辑失败，发送新消息
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  statusMsg,
				RandomID: time.Now().UnixNano(),
			})
		}
		return err
	}
}

// handleHelp 处理help命令
func (cp *CoreCommandsPlugin) handleHelp(ctx *command.CommandContext) error {
	if len(ctx.Args) == 0 {
		// 显示所有命令
		helpMsg := `📖 NexusValet 帮助信息

🔧 可用命令:
• .status - 显示系统状态信息
• .help - 显示此帮助信息
• .help <插件名> - 显示特定插件的帮助
• .st [服务器ID] - 网络速度测试
• .st list - 列出附近的测速服务器
• .sb [用户ID/用户名] [不删除消息] - 超级封禁用户并删除消息历史
• .gemini <问题> - Gemini AI智能问答(自动识别文本/图片)
• .gm <问题> - Gemini简写命令

💡 提示: 使用 .help core 或 .help sb 查看详细信息
🚀 新版本: 现在使用Go插件系统，性能更佳！`

		// 直接使用gotd API发送响应
		peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
		if err != nil {
			return fmt.Errorf("failed to resolve peer: %w", err)
		}

		// 私聊：编辑消息，群聊：先尝试编辑，失败则发送新消息
		if ctx.Message.ChatID > 0 {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: helpMsg,
			})
			return err
		} else {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: helpMsg,
			})
			if err != nil {
				_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
					Peer:     peer,
					Message:  helpMsg,
					RandomID: time.Now().UnixNano(),
				})
			}
			return err
		}
	}

	// 显示特定插件帮助
	pluginName := ctx.Args[0]
	if pluginName == "core" {
		detailedHelp := `📋 核心命令插件详细帮助

🔍 .status 命令:
  显示 NexusValet 的系统状态信息，包括:
  • 应用版本号
  • Go 运行时版本
  • 当前系统时间
  • 运行状态
  • 内存使用情况

❓ .help 命令:
  • .help - 显示所有可用命令列表
  • .help <插件名> - 显示特定插件的详细帮助信息

🔌 插件信息:
  • 名称: core
  • 版本: v1.0.0 (Go插件版本)
  • 作者: NexusValet
  • 描述: 提供基础的系统命令功能`

		// 直接使用gotd API发送响应
		peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
		if err != nil {
			return fmt.Errorf("failed to resolve peer: %w", err)
		}

		if ctx.Message.ChatID > 0 {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: detailedHelp,
			})
			return err
		} else {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: detailedHelp,
			})
			if err != nil {
				_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
					Peer:     peer,
					Message:  detailedHelp,
					RandomID: time.Now().UnixNano(),
				})
			}
			return err
		}
	} else if pluginName == "sb" {
		sbHelp := `🚫 超级封禁插件详细帮助

🚫 .sb 命令:
  超级封禁用户并清除消息历史，功能包括:
  • 🔒 永久封禁指定用户
  • 🗑️ 清除用户消息历史（可选）
  • 🎯 支持多种用户指定方式
  • 🛡️ 自动权限验证

📝 使用方法:
  • .sb - 回复消息封禁该用户（推荐）
  • .sb <用户ID> - 通过用户ID封禁
  • .sb @<用户名> - 通过用户名封禁
  • .sb <用户ID/用户名> 0 - 仅封禁不删除历史

⚠️ 注意事项:
  • 仅限群组使用
  • 需要管理员权限
  • 默认会删除该用户的所有消息历史
  • 支持封禁用户

🔌 插件信息:
  • 名称: sb
  • 版本: v1.0.0 (Go插件版本)  
  • 作者: NexusValet
  • 描述: 超级封禁插件，支持封禁用户并删除消息历史`

		// 直接使用gotd API发送响应
		peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
		if err != nil {
			return fmt.Errorf("failed to resolve peer: %w", err)
		}

		if ctx.Message.ChatID > 0 {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: sbHelp,
			})
			return err
		} else {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: sbHelp,
			})
			if err != nil {
				_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
					Peer:     peer,
					Message:  sbHelp,
					RandomID: time.Now().UnixNano(),
				})
			}
			return err
		}
	} else if pluginName == "gemini" {
		geminiHelp := `🤖 Gemini AI插件详细帮助

🚀 智能命令 (自动识别模式):
  • .gemini <问题> - 智能问答，自动识别文本/图片
  • .gm <问题> - 简写命令，功能同上

✨ 智能功能:
  • 📝 文本问答 - 直接提问即可
  • 🖼️ 图片分析 - 发送图片时自动启用vision模式
  • 🔄 回复模式 - 添加 "reply" 或 "r" 参数回复原消息
  • 💬 上下文对话 - 回复消息后提问

⚙️ 配置命令:
  • .gemini config - 查看当前配置
  • .gemini key <API密钥> - 设置API密钥
  • .gemini model <模型名> - 设置模型(默认: gemini-1.5-flash)
  • .gemini auto <True/False> - 设置自动删除空提问

📝 使用示例:
  • .gemini 什么是人工智能？
  • .gm 解释这个概念
  • .gemini reply 请详细说明 (回复到原消息)
  • .gm r 分析这张图片 (发送图片+回复模式)
  • .gemini config (查看配置)
  • .gemini key AIza... (设置API密钥)

🔌 插件信息:
  • 名称: gemini  
  • 版本: v1.0.0
  • 描述: 简化的Gemini AI智能问答插件`

		// 直接使用gotd API发送响应
		peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
		if err != nil {
			return fmt.Errorf("failed to resolve peer: %w", err)
		}

		if ctx.Message.ChatID > 0 {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: geminiHelp,
			})
			return err
		} else {
			_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
				Peer:    peer,
				ID:      ctx.Message.Message.ID,
				Message: geminiHelp,
			})
			if err != nil {
				_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
					Peer:     peer,
					Message:  geminiHelp,
					RandomID: time.Now().UnixNano(),
				})
			}
			return err
		}
	}

	// 直接使用gotd API发送响应
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	errorMsg := "未找到该插件的帮助信息: " + pluginName
	if ctx.Message.ChatID > 0 {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: errorMsg,
		})
		return err
	} else {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: errorMsg,
		})
		if err != nil {
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  errorMsg,
				RandomID: time.Now().UnixNano(),
			})
		}
		return err
	}
}

// 辅助函数

func (cp *CoreCommandsPlugin) formatUptime(uptime time.Duration) string {
	seconds := int(uptime.Seconds())
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d天", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d小时", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d分钟", minutes))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d秒", secs))
	}

	return strings.Join(parts, " ")
}

func (cp *CoreCommandsPlugin) formatMemorySize(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (cp *CoreCommandsPlugin) getKernelVersion() string {
	switch runtime.GOOS {
	case "linux":
		if output, err := exec.Command("uname", "-r").Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	case "darwin":
		if output, err := exec.Command("uname", "-r").Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	case "windows":
		if output, err := exec.Command("cmd", "/C", "ver").Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	case "freebsd":
		if output, err := exec.Command("uname", "-r").Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	}
	return "N/A"
}

// APTPlugin APT风格的插件管理命令
type APTPlugin struct {
	*BasePlugin
}

// NewAPTPlugin 创建APT插件
func NewAPTPlugin() *APTPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "apt",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "APT风格的插件管理命令",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	return &APTPlugin{
		BasePlugin: NewBasePlugin(info),
	}
}

// RegisterCommands 实现CommandPlugin接口
func (ap *APTPlugin) RegisterCommands(parser *command.Parser) error {
	parser.RegisterCommand("apt", "Plugin management commands", ap.info.Name, ap.handleAPT)
	logger.Infof("APT commands registered successfully")
	return nil
}

// handleAPT 处理apt命令
func (ap *APTPlugin) handleAPT(ctx *command.CommandContext) error {
	if len(ctx.Args) == 0 {
		return ap.sendResponse(ctx, "Usage: .apt <list|enable|disable> [plugin_name]")
	}

	subcommand := ctx.Args[0]
	switch subcommand {
	case "list":
		return ap.handleList(ctx)
	case "enable":
		return ap.handleEnable(ctx)
	case "disable":
		return ap.handleDisable(ctx)
	default:
		return ap.sendResponse(ctx, fmt.Sprintf("Unknown subcommand: %s", subcommand))
	}
}

// sendResponse APT插件通用响应函数
func (ap *APTPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	if ctx.Message.ChatID > 0 {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		return err
	} else {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		if err != nil {
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  message,
				RandomID: time.Now().UnixNano(),
			})
		}
		return err
	}
}

// handleList 处理列出插件
func (ap *APTPlugin) handleList(ctx *command.CommandContext) error {
	if ap.manager == nil {
		return ap.sendResponse(ctx, "Plugin manager not available")
	}

	// 类型断言为GoManager
	if goManager, ok := ap.manager.(*GoManager); ok {
		plugins := goManager.GetAllPlugins()
		if len(plugins) == 0 {
			return ap.sendResponse(ctx, "No plugins installed")
		}

		var response strings.Builder
		response.WriteString("Installed plugins (Go版本):\n")
		for name, plugin := range plugins {
			status := "enabled"
			if !plugin.Enabled {
				status = "disabled"
			}
			response.WriteString(fmt.Sprintf("• %s v%s (%s) - %s\n",
				name, plugin.Version, status, plugin.Description))
		}

		return ap.sendResponse(ctx, response.String())
	}

	return ap.sendResponse(ctx, "Unsupported plugin manager type")
}

// handleEnable 处理启用插件
func (ap *APTPlugin) handleEnable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ap.sendResponse(ctx, "Usage: .apt enable <plugin_name>")
	}

	pluginName := ctx.Args[1]
	if goManager, ok := ap.manager.(*GoManager); ok {
		if err := goManager.EnablePlugin(pluginName); err != nil {
			return ap.sendResponse(ctx, fmt.Sprintf("Failed to enable plugin %s: %v", pluginName, err))
		}
		return ap.sendResponse(ctx, fmt.Sprintf("Plugin %s enabled", pluginName))
	}

	return ap.sendResponse(ctx, "Unsupported plugin manager type")
}

// handleDisable 处理禁用插件
func (ap *APTPlugin) handleDisable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ap.sendResponse(ctx, "Usage: .apt disable <plugin_name>")
	}

	pluginName := ctx.Args[1]
	if goManager, ok := ap.manager.(*GoManager); ok {
		if err := goManager.DisablePlugin(pluginName); err != nil {
			return ap.sendResponse(ctx, fmt.Sprintf("Failed to disable plugin %s: %v", pluginName, err))
		}
		return ap.sendResponse(ctx, fmt.Sprintf("Plugin %s disabled", pluginName))
	}

	return ap.sendResponse(ctx, "Unsupported plugin manager type")
}

// RegisterBuiltinPlugins 注册所有内置插件
func RegisterBuiltinPlugins(manager *GoManager) error {
	// 注册核心命令插件
	corePlugin := NewCoreCommandsPlugin()
	if err := manager.RegisterPlugin(corePlugin); err != nil {
		return fmt.Errorf("failed to register core commands plugin: %w", err)
	}

	// 注册APT插件
	aptPlugin := NewAPTPlugin()
	if err := manager.RegisterPlugin(aptPlugin); err != nil {
		return fmt.Errorf("failed to register APT plugin: %w", err)
	}

	// 注册SpeedTest插件
	speedTestPlugin := NewSpeedTestPlugin()
	if err := manager.RegisterPlugin(speedTestPlugin); err != nil {
		return fmt.Errorf("failed to register SpeedTest plugin: %w", err)
	}

	// 注册SB插件
	sbPlugin := NewSBPlugin()
	if err := manager.RegisterPlugin(sbPlugin); err != nil {
		return fmt.Errorf("failed to register SB plugin: %w", err)
	}

	// 注册Gemini插件
	geminiPlugin := NewGeminiPlugin(manager.GetDatabase())
	if err := manager.RegisterPlugin(geminiPlugin); err != nil {
		return fmt.Errorf("failed to register Gemini plugin: %w", err)
	}

	logger.Infof("All builtin plugins registered successfully")
	return nil
}
