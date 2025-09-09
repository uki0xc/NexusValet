package plugin

import (
	"context"
	"nexusvalet/internal/command"
	"nexusvalet/internal/core"
)

// PluginVersion 代表插件版本信息
type PluginVersion struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
}

// PluginInfo 包含插件信息和运行时数据
type PluginInfo struct {
	*PluginVersion
	Dir     string
	Enabled bool
}

// Plugin 是Go插件必须实现的接口
type Plugin interface {
	// GetInfo 返回插件信息
	GetInfo() *PluginInfo

	// Initialize 初始化插件
	Initialize(ctx context.Context, manager interface{}) error

	// Shutdown 关闭插件
	Shutdown(ctx context.Context) error

	// IsEnabled 返回插件是否启用
	IsEnabled() bool

	// SetEnabled 设置插件启用状态
	SetEnabled(enabled bool)
}

// CommandPlugin 是提供命令功能的插件接口
type CommandPlugin interface {
	Plugin

	// RegisterCommands 注册插件命令
	RegisterCommands(parser *command.Parser) error
}

// EventPlugin 是处理事件的插件接口
type EventPlugin interface {
	Plugin

	// RegisterEventHandlers 注册事件处理器
	RegisterEventHandlers(dispatcher *core.EventDispatcher) error
}

// HookPlugin 是提供钩子功能的插件接口
type HookPlugin interface {
	Plugin

	// RegisterHooks 注册钩子
	RegisterHooks(hookManager *core.HookManager) error
}

// BasePlugin 提供插件的基础实现
type BasePlugin struct {
	info    *PluginInfo
	enabled bool
	manager interface{}
}

// NewBasePlugin 创建基础插件实例
func NewBasePlugin(info *PluginInfo) *BasePlugin {
	return &BasePlugin{
		info:    info,
		enabled: true,
	}
}

// GetInfo 实现Plugin接口
func (bp *BasePlugin) GetInfo() *PluginInfo {
	return bp.info
}

// Initialize 实现Plugin接口
func (bp *BasePlugin) Initialize(ctx context.Context, manager interface{}) error {
	bp.manager = manager
	return nil
}

// Shutdown 实现Plugin接口
func (bp *BasePlugin) Shutdown(ctx context.Context) error {
	return nil
}

// IsEnabled 实现Plugin接口
func (bp *BasePlugin) IsEnabled() bool {
	return bp.enabled
}

// SetEnabled 实现Plugin接口
func (bp *BasePlugin) SetEnabled(enabled bool) {
	bp.enabled = enabled
}

// GetManager 返回插件管理器
func (bp *BasePlugin) GetManager() interface{} {
	return bp.manager
}
