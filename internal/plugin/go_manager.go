package plugin

import (
	"context"
	"database/sql"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/core"
	"nexusvalet/internal/peers"
	"nexusvalet/pkg/logger"
	"sync"

	"github.com/gotd/td/tg"
)

// GoManager 管理Go插件的加载、卸载和执行
type GoManager struct {
	plugins      map[string]Plugin
	parser       *command.Parser
	dispatcher   *core.EventDispatcher
	hookManager  *core.HookManager
	db           *sql.DB
	peerResolver *peers.Resolver
	mutex        sync.RWMutex
}

// NewGoManager 创建一个新的Go插件管理器
func NewGoManager(parser *command.Parser, dispatcher *core.EventDispatcher, hookManager *core.HookManager, db *sql.DB) *GoManager {
	manager := &GoManager{
		plugins:     make(map[string]Plugin),
		parser:      parser,
		dispatcher:  dispatcher,
		hookManager: hookManager,
		db:          db,
	}

	logger.Debugf("Go plugin manager initialized")
	return manager
}

// RegisterPlugin 注册一个Go插件
func (gm *GoManager) RegisterPlugin(plugin Plugin) error {
	gm.mutex.Lock()
	defer gm.mutex.Unlock()

	info := plugin.GetInfo()
	if info == nil {
		return fmt.Errorf("plugin info is nil")
	}

	pluginName := info.Name
	if _, exists := gm.plugins[pluginName]; exists {
		return fmt.Errorf("plugin %s already registered", pluginName)
	}

	// 初始化插件
	ctx := context.Background()
	if err := plugin.Initialize(ctx, gm); err != nil {
		return fmt.Errorf("failed to initialize plugin %s: %w", pluginName, err)
	}

	// 注册命令
	if cmdPlugin, ok := plugin.(CommandPlugin); ok {
		if err := cmdPlugin.RegisterCommands(gm.parser); err != nil {
			return fmt.Errorf("failed to register commands for plugin %s: %w", pluginName, err)
		}
	}

	// 注册事件处理器
	if eventPlugin, ok := plugin.(EventPlugin); ok {
		if err := eventPlugin.RegisterEventHandlers(gm.dispatcher); err != nil {
			return fmt.Errorf("failed to register event handlers for plugin %s: %w", pluginName, err)
		}
	}

	// 注册钩子
	if hookPlugin, ok := plugin.(HookPlugin); ok {
		if err := hookPlugin.RegisterHooks(gm.hookManager); err != nil {
			return fmt.Errorf("failed to register hooks for plugin %s: %w", pluginName, err)
		}
	}

	gm.plugins[pluginName] = plugin
	logger.Infof("Plugin %s registered successfully", pluginName)
	return nil
}

// UnregisterPlugin 注销一个插件
func (gm *GoManager) UnregisterPlugin(name string) error {
	gm.mutex.Lock()
	defer gm.mutex.Unlock()

	plugin, exists := gm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	// 关闭插件
	ctx := context.Background()
	if err := plugin.Shutdown(ctx); err != nil {
		logger.Warnf("Failed to shutdown plugin %s: %v", name, err)
	}

	// 从命令解析器中注销命令
	gm.parser.UnregisterPluginCommands(name)

	// 删除插件
	delete(gm.plugins, name)

	logger.Infof("Plugin %s unregistered", name)
	return nil
}

// EnablePlugin 启用一个插件
func (gm *GoManager) EnablePlugin(name string) error {
	gm.mutex.RLock()
	plugin, exists := gm.plugins[name]
	gm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	if plugin.IsEnabled() {
		return fmt.Errorf("plugin %s is already enabled", name)
	}

	plugin.SetEnabled(true)
	logger.Infof("Plugin %s enabled", name)
	return nil
}

// DisablePlugin 禁用一个插件
func (gm *GoManager) DisablePlugin(name string) error {
	gm.mutex.RLock()
	plugin, exists := gm.plugins[name]
	gm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	if !plugin.IsEnabled() {
		return fmt.Errorf("plugin %s is already disabled", name)
	}

	plugin.SetEnabled(false)
	logger.Infof("Plugin %s disabled", name)
	return nil
}

// GetPlugin 返回插件实例
func (gm *GoManager) GetPlugin(name string) (Plugin, bool) {
	gm.mutex.RLock()
	defer gm.mutex.RUnlock()

	plugin, exists := gm.plugins[name]
	return plugin, exists
}

// GetAllPlugins 返回所有插件信息
func (gm *GoManager) GetAllPlugins() map[string]*PluginInfo {
	gm.mutex.RLock()
	defer gm.mutex.RUnlock()

	result := make(map[string]*PluginInfo)
	for name, plugin := range gm.plugins {
		info := plugin.GetInfo()
		// 创建副本以避免并发访问问题
		infoCopy := &PluginInfo{
			PluginVersion: &PluginVersion{
				Name:        info.Name,
				Version:     info.Version,
				Author:      info.Author,
				Description: info.Description,
			},
			Dir:     info.Dir,
			Enabled: plugin.IsEnabled(),
		}
		result[name] = infoCopy
	}
	return result
}

// ListPlugins 列出所有插件的基本信息
func (gm *GoManager) ListPlugins() []string {
	gm.mutex.RLock()
	defer gm.mutex.RUnlock()

	var names []string
	for name := range gm.plugins {
		names = append(names, name)
	}
	return names
}

// Shutdown 关闭所有插件
func (gm *GoManager) Shutdown() error {
	gm.mutex.Lock()
	defer gm.mutex.Unlock()

	ctx := context.Background()
	for name, plugin := range gm.plugins {
		if err := plugin.Shutdown(ctx); err != nil {
			logger.Errorf("Failed to shutdown plugin %s: %v", name, err)
		}
	}

	logger.Infof("All plugins shutdown")
	return nil
}

// GetDatabase 返回数据库连接
func (gm *GoManager) GetDatabase() *sql.DB {
	return gm.db
}

// SetPeerResolver 设置Peer解析器
func (gm *GoManager) SetPeerResolver(peerResolver *peers.Resolver) {
	gm.peerResolver = peerResolver
}

// SetTelegramClient 为所有支持的插件设置Telegram客户端
func (gm *GoManager) SetTelegramClient(client *tg.Client) {
	gm.mutex.RLock()
	defer gm.mutex.RUnlock()

	for name, plugin := range gm.plugins {
		// 检查插件是否是CoreCommandsPlugin类型
		if corePlugin, ok := plugin.(*CoreCommandsPlugin); ok {
			corePlugin.SetTelegramClient(client)
			logger.Debugf("Set Telegram client for plugin %s", name)
		}
		// 检查插件是否是SBPlugin类型
		if sbPlugin, ok := plugin.(*SBPlugin); ok {
			sbPlugin.SetTelegramClient(client)
			logger.Debugf("Set Telegram client for SB plugin %s", name)
		}
		// 检查插件是否是AutoSendPlugin类型
		if autoSendPlugin, ok := plugin.(*AutoSendPlugin); ok {
			// 需要peer resolver
			if gm.peerResolver != nil {
				autoSendPlugin.SetTelegramClient(client, gm.peerResolver)
				logger.Debugf("Set Telegram client for AutoSend plugin %s", name)
			}
		}
		// 检查插件是否是DeleteMyMessagesPlugin类型
		if dmePlugin, ok := plugin.(*DeleteMyMessagesPlugin); ok {
			dmePlugin.SetTelegramClient(client)
			logger.Debugf("Set Telegram client for DeleteMyMessages plugin %s", name)
		}
		// 检查插件是否是IdsPlugin类型
		if idsPlugin, ok := plugin.(*IdsPlugin); ok {
			idsPlugin.SetTelegramClient(client)
			logger.Debugf("Set Telegram client for Ids plugin %s", name)
		}
	}
}
