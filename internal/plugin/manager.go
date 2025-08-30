package plugin

import (
	"encoding/json"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/core"
	"nexusvalet/pkg/logger"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.starlark.net/starlark"
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
	Globals starlark.StringDict
}

// Manager 管理插件的加载、卸载和执行
type Manager struct {
	plugins     map[string]*PluginInfo
	pluginsDir  string
	executor    *StarlarkExecutor
	parser      *command.Parser
	dispatcher  *core.EventDispatcher
	hookManager *core.HookManager
	mutex       sync.RWMutex
}

// NewManager 创建一个新的插件管理器
func NewManager(pluginsDir string, parser *command.Parser, dispatcher *core.EventDispatcher, hookManager *core.HookManager) *Manager {
	manager := &Manager{
		plugins:     make(map[string]*PluginInfo),
		pluginsDir:  pluginsDir,
		parser:      parser,
		dispatcher:  dispatcher,
		hookManager: hookManager,
	}

	// 创建 Starlark 执行器
	manager.executor = NewStarlarkExecutor(manager, parser, dispatcher, hookManager)

	// 注册 APT 命令
	manager.registerAPTCommands()

	logger.Debugf("Plugin manager initialized with directory: %s", pluginsDir)
	return manager
}

// LoadAllPlugins 从插件目录加载所有插件
func (pm *Manager) LoadAllPlugins() error {
	if err := pm.ensurePluginsDir(); err != nil {
		return err
	}

	entries, err := os.ReadDir(pm.pluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			pluginName := entry.Name()
			if err := pm.LoadPlugin(pluginName); err != nil {
				logger.Errorf("Failed to load plugin %s: %v", pluginName, err)
				// 继续加载其他插件
			}
		}
	}

	return nil
}

// LoadPlugin 按名称加载特定插件
func (pm *Manager) LoadPlugin(name string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 检查插件是否已经加载
	if plugin, exists := pm.plugins[name]; exists {
		if plugin.Enabled {
			logger.Warnf("Plugin %s is already loaded", name)
			return nil
		}
	}

	pluginDir := filepath.Join(pm.pluginsDir, name)

	// Check if plugin directory exists
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("plugin directory not found: %s", pluginDir)
	}

	// Load plugin version information
	version, err := pm.loadPluginVersion(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to load plugin version: %w", err)
	}

	// Create plugin info
	pluginInfo := &PluginInfo{
		PluginVersion: version,
		Dir:           pluginDir,
		Enabled:       true,
	}

	// Execute the plugin
	if err := pm.executor.ExecutePlugin(pluginInfo); err != nil {
		return fmt.Errorf("failed to execute plugin: %w", err)
	}

	// Store plugin info
	pm.plugins[name] = pluginInfo

	logger.Debugf("Plugin %s loaded successfully", name)
	return nil
}

// UnloadPlugin unloads a plugin
func (pm *Manager) UnloadPlugin(name string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	// Unregister all commands from this plugin
	pm.parser.UnregisterPluginCommands(name)

	// Unregister all listeners from this plugin (we'd need to track these)
	// For now, we'll mark the plugin as disabled
	plugin.Enabled = false

	logger.Debugf("Plugin %s unloaded", name)
	return nil
}

// RemovePlugin removes a plugin completely
func (pm *Manager) RemovePlugin(name string) error {
	// First unload the plugin
	if err := pm.UnloadPlugin(name); err != nil {
		logger.Warnf("Failed to unload plugin %s before removal: %v", name, err)
	}

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Remove from memory
	delete(pm.plugins, name)

	// Remove plugin directory
	pluginDir := filepath.Join(pm.pluginsDir, name)
	if err := os.RemoveAll(pluginDir); err != nil {
		return fmt.Errorf("failed to remove plugin directory: %w", err)
	}

	logger.Debugf("Plugin %s removed", name)
	return nil
}

// EnablePlugin enables a disabled plugin
func (pm *Manager) EnablePlugin(name string) error {
	pm.mutex.Lock()
	plugin, exists := pm.plugins[name]
	pm.mutex.Unlock()

	if !exists {
		return pm.LoadPlugin(name)
	}

	if plugin.Enabled {
		return fmt.Errorf("plugin %s is already enabled", name)
	}

	// Re-execute the plugin
	if err := pm.executor.ExecutePlugin(plugin); err != nil {
		return fmt.Errorf("failed to re-execute plugin: %w", err)
	}

	pm.mutex.Lock()
	plugin.Enabled = true
	pm.mutex.Unlock()

	logger.Debugf("Plugin %s enabled", name)
	return nil
}

// DisablePlugin disables a plugin
func (pm *Manager) DisablePlugin(name string) error {
	return pm.UnloadPlugin(name)
}

// GetPlugin returns plugin information
func (pm *Manager) GetPlugin(name string) (*PluginInfo, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	plugin, exists := pm.plugins[name]
	return plugin, exists
}

// GetAllPlugins returns all plugin information
func (pm *Manager) GetAllPlugins() map[string]*PluginInfo {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	result := make(map[string]*PluginInfo)
	for name, plugin := range pm.plugins {
		result[name] = plugin
	}
	return result
}

// InstallPlugin installs a plugin from a file
func (pm *Manager) InstallPlugin(name string, pluginData []byte, versionData []byte) error {
	if err := pm.ensurePluginsDir(); err != nil {
		return err
	}

	pluginDir := filepath.Join(pm.pluginsDir, name)

	// Create plugin directory
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// Write plugin.star
	pluginFile := filepath.Join(pluginDir, "plugin.star")
	if err := os.WriteFile(pluginFile, pluginData, 0644); err != nil {
		return fmt.Errorf("failed to write plugin file: %w", err)
	}

	// Write version.json
	versionFile := filepath.Join(pluginDir, "version.json")
	if err := os.WriteFile(versionFile, versionData, 0644); err != nil {
		return fmt.Errorf("failed to write version file: %w", err)
	}

	// Load the plugin
	if err := pm.LoadPlugin(name); err != nil {
		// Clean up on failure
		os.RemoveAll(pluginDir)
		return fmt.Errorf("failed to load installed plugin: %w", err)
	}

	logger.Debugf("Plugin %s installed successfully", name)
	return nil
}

// loadPluginVersion loads plugin version information from version.json
func (pm *Manager) loadPluginVersion(pluginDir string) (*PluginVersion, error) {
	versionFile := filepath.Join(pluginDir, "version.json")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read version.json: %w", err)
	}

	var version PluginVersion
	if err := json.Unmarshal(data, &version); err != nil {
		return nil, fmt.Errorf("failed to parse version.json: %w", err)
	}

	return &version, nil
}

// readFile reads a file from a plugin directory
func (pm *Manager) readFile(pluginDir, filename string) ([]byte, error) {
	filePath := filepath.Join(pluginDir, filename)
	return os.ReadFile(filePath)
}

// ensurePluginsDir ensures the plugins directory exists
func (pm *Manager) ensurePluginsDir() error {
	return os.MkdirAll(pm.pluginsDir, 0755)
}

// registerAPTCommands registers the APT-style plugin management commands
func (pm *Manager) registerAPTCommands() {
	// .apt install command
	pm.parser.RegisterCommand("apt", "Plugin management commands", "system", func(ctx *command.CommandContext) error {
		if len(ctx.Args) == 0 {
			return ctx.Respond("Usage: .apt <install|list|enable|disable|remove> [plugin_name]")
		}

		subcommand := ctx.Args[0]
		switch subcommand {
		case "install":
			return pm.handleAPTInstall(ctx)
		case "list":
			return pm.handleAPTList(ctx)
		case "enable":
			return pm.handleAPTEnable(ctx)
		case "disable":
			return pm.handleAPTDisable(ctx)
		case "remove":
			return pm.handleAPTRemove(ctx)
		default:
			return ctx.Respond(fmt.Sprintf("Unknown subcommand: %s", subcommand))
		}
	})
}

// handleAPTInstall handles plugin installation
func (pm *Manager) handleAPTInstall(ctx *command.CommandContext) error {
	// This is a simplified implementation
	// In a real implementation, you would handle file downloads, etc.
	return ctx.Respond("Plugin installation from chat files is not yet implemented")
}

// handleAPTList handles listing plugins
func (pm *Manager) handleAPTList(ctx *command.CommandContext) error {
	plugins := pm.GetAllPlugins()
	if len(plugins) == 0 {
		return ctx.Respond("No plugins installed")
	}

	var response strings.Builder
	response.WriteString("已安装插件:\n")
	for name, plugin := range plugins {
		status := "启用"
		if !plugin.Enabled {
			status = "禁用"
		}
		response.WriteString(fmt.Sprintf("• %s v%s (%s) - %s\n",
			name, plugin.Version, status, plugin.Description))
	}

	return ctx.Respond(response.String())
}

// handleAPTEnable handles enabling a plugin
func (pm *Manager) handleAPTEnable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ctx.Respond("Usage: .apt enable <plugin_name>")
	}

	pluginName := ctx.Args[1]
	if err := pm.EnablePlugin(pluginName); err != nil {
		return ctx.Respond(fmt.Sprintf("Failed to enable plugin %s: %v", pluginName, err))
	}

	return ctx.Respond(fmt.Sprintf("Plugin %s enabled", pluginName))
}

// handleAPTDisable handles disabling a plugin
func (pm *Manager) handleAPTDisable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ctx.Respond("Usage: .apt disable <plugin_name>")
	}

	pluginName := ctx.Args[1]
	if err := pm.DisablePlugin(pluginName); err != nil {
		return ctx.Respond(fmt.Sprintf("Failed to disable plugin %s: %v", pluginName, err))
	}

	return ctx.Respond(fmt.Sprintf("Plugin %s disabled", pluginName))
}

// handleAPTRemove handles removing a plugin
func (pm *Manager) handleAPTRemove(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ctx.Respond("Usage: .apt remove <plugin_name>")
	}

	pluginName := ctx.Args[1]
	if err := pm.RemovePlugin(pluginName); err != nil {
		return ctx.Respond(fmt.Sprintf("Failed to remove plugin %s: %v", pluginName, err))
	}

	return ctx.Respond(fmt.Sprintf("Plugin %s removed", pluginName))
}
