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

	"github.com/gotd/td/tg"
	lua "github.com/yuin/gopher-lua"
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
	L       *lua.LState // 保存该插件的Lua状态
}

// Manager 管理插件的加载、卸载和执行
type Manager struct {
	plugins     map[string]*PluginInfo
	pluginsDir  string
	executor    *LuaExecutor
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

	// 创建 Lua 执行器
	manager.executor = NewLuaExecutor(manager, parser, dispatcher, hookManager)

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

	// 检查插件目录是否存在
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("plugin directory not found: %s", pluginDir)
	}

	// 加载插件版本信息
	version, err := pm.loadPluginVersion(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to load plugin version: %w", err)
	}

	// 创建插件信息
	pluginInfo := &PluginInfo{
		PluginVersion: version,
		Dir:           pluginDir,
		Enabled:       true,
	}

	// 执行插件
	if err := pm.executor.ExecutePlugin(pluginInfo); err != nil {
		return fmt.Errorf("failed to execute plugin: %w", err)
	}

	// 存储插件信息
	pm.plugins[name] = pluginInfo

	logger.Debugf("Plugin %s loaded successfully", name)
	return nil
}

// UnloadPlugin 卸载一个插件
func (pm *Manager) UnloadPlugin(name string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	// 关闭Lua状态
	if plugin.L != nil {
		plugin.L.Close()
	}

	// 从此插件中注销所有命令
	pm.parser.UnregisterPluginCommands(name)

	// 将插件标记为禁用
	plugin.Enabled = false

	logger.Debugf("Plugin %s unloaded", name)
	return nil
}

// RemovePlugin 完全移除一个插件
func (pm *Manager) RemovePlugin(name string) error {
	// 首先卸载插件
	if err := pm.UnloadPlugin(name); err != nil {
		logger.Warnf("Failed to unload plugin %s before removal: %v", name, err)
	}

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 从内存中移除
	delete(pm.plugins, name)

	// 移除插件目录
	pluginDir := filepath.Join(pm.pluginsDir, name)
	if err := os.RemoveAll(pluginDir); err != nil {
		return fmt.Errorf("failed to remove plugin directory: %w", err)
	}

	logger.Debugf("Plugin %s removed", name)
	return nil
}

// EnablePlugin 启用一个已禁用的插件
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

	// 重新执行插件
	if err := pm.executor.ExecutePlugin(plugin); err != nil {
		return fmt.Errorf("failed to re-execute plugin: %w", err)
	}

	pm.mutex.Lock()
	plugin.Enabled = true
	pm.mutex.Unlock()

	logger.Debugf("Plugin %s enabled", name)
	return nil
}

// DisablePlugin 禁用一个插件
func (pm *Manager) DisablePlugin(name string) error {
	return pm.UnloadPlugin(name)
}

// GetPlugin 返回插件信息
func (pm *Manager) GetPlugin(name string) (*PluginInfo, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	plugin, exists := pm.plugins[name]
	return plugin, exists
}

// GetAllPlugins 返回所有插件信息
func (pm *Manager) GetAllPlugins() map[string]*PluginInfo {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	result := make(map[string]*PluginInfo)
	for name, plugin := range pm.plugins {
		result[name] = plugin
	}
	return result
}

// InstallPlugin 从文件安装插件
func (pm *Manager) InstallPlugin(name string, pluginData []byte, versionData []byte) error {
	if err := pm.ensurePluginsDir(); err != nil {
		return err
	}

	pluginDir := filepath.Join(pm.pluginsDir, name)

	// 创建插件目录
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// 写入 plugin.lua
	pluginFile := filepath.Join(pluginDir, "plugin.lua")
	if err := os.WriteFile(pluginFile, pluginData, 0644); err != nil {
		return fmt.Errorf("failed to write plugin file: %w", err)
	}

	// 写入 version.json
	versionFile := filepath.Join(pluginDir, "version.json")
	if err := os.WriteFile(versionFile, versionData, 0644); err != nil {
		return fmt.Errorf("failed to write version file: %w", err)
	}

	// 加载插件
	if err := pm.LoadPlugin(name); err != nil {
		// 失败时清理
		os.RemoveAll(pluginDir)
		return fmt.Errorf("failed to load installed plugin: %w", err)
	}

	logger.Debugf("Plugin %s installed successfully", name)
	return nil
}

// loadPluginVersion 从 version.json 加载插件版本信息
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

// readFile 从插件目录读取文件
func (pm *Manager) readFile(pluginDir, filename string) ([]byte, error) {
	filePath := filepath.Join(pluginDir, filename)
	return os.ReadFile(filePath)
}

// ensurePluginsDir 确保插件目录存在
func (pm *Manager) ensurePluginsDir() error {
	return os.MkdirAll(pm.pluginsDir, 0755)
}

// validatePluginContent 对插件内容执行基本验证
func (pm *Manager) validatePluginContent(data []byte) bool {
	content := string(data)

	// 对Lua插件结构的基本检查
	requiredPatterns := []string{
		"function init()",  // 必须有init函数
		"function handle_", // 必须有至少一个处理函数
	}

	for _, pattern := range requiredPatterns {
		if !strings.Contains(content, pattern) {
			logger.Debugf("Plugin validation failed: missing pattern %s", pattern)
			return false
		}
	}
	return true
}

// createDefaultVersionJSON 为插件创建默认的 version.json
func (pm *Manager) createDefaultVersionJSON(pluginName, filename string) []byte {
	version := &PluginVersion{
		Name:        pluginName,
		Version:     "1.0.0",
		Author:      "Telegram User",
		Description: fmt.Sprintf("Plugin installed from %s", filename),
	}

	data, err := json.MarshalIndent(version, "", "  ")
	if err != nil {
		logger.Errorf("Failed to create default version.json: %v", err)
		// 返回最小有效JSON
		return []byte(fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0", 
  "author": "Telegram User",
  "description": "Plugin installed from %s"
}`, pluginName, filename))
	}

	return data
}

// getDocumentFilename 从Telegram文档中提取文件名
func (pm *Manager) getDocumentFilename(document *tg.Document) string {
	for _, attr := range document.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			return a.FileName
		}
	}
	return fmt.Sprintf("document_%d", document.ID)
}

// registerAPTCommands 注册APT风格的插件管理命令
func (pm *Manager) registerAPTCommands() {
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

// handleAPTInstall 处理插件安装
func (pm *Manager) handleAPTInstall(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return ctx.Respond("Usage: .apt install <plugin_name> [reply to a message with a .lua file]")
	}

	pluginName := ctx.Args[1]

	if _, exists := pm.GetPlugin(pluginName); exists {
		return ctx.Respond(fmt.Sprintf("Plugin %s already exists. Please remove it first with .apt remove %s", pluginName, pluginName))
	}

	document, err := ctx.GetDocument()
	if err != nil {
		return ctx.Respond("No file found. Please use this command on a message with a .lua plugin file, or reply to a message with a .lua file.")
	}

	filename := pm.getDocumentFilename(document)
	if !strings.HasSuffix(filename, ".lua") {
		return ctx.Respond(fmt.Sprintf("Please provide a .lua plugin file. Current file: %s", filename))
	}

	pluginData, err := ctx.DownloadFile(document)
	if err != nil {
		logger.Errorf("Failed to download plugin file: %v", err)
		return ctx.Respond("Failed to download plugin file: " + err.Error())
	}

	if !pm.validatePluginContent(pluginData) {
		return ctx.Respond("Plugin file is invalid or missing required functions.")
	}

	versionData := pm.createDefaultVersionJSON(pluginName, filename)

	if err := pm.InstallPlugin(pluginName, pluginData, versionData); err != nil {
		logger.Errorf("Failed to install plugin %s: %v", pluginName, err)
		return ctx.Respond(fmt.Sprintf("Failed to install plugin: %v", err))
	}

	return ctx.Respond(fmt.Sprintf("Plugin %s installed successfully!", pluginName))
}

// handleAPTList 处理列出插件
func (pm *Manager) handleAPTList(ctx *command.CommandContext) error {
	plugins := pm.GetAllPlugins()
	if len(plugins) == 0 {
		return ctx.Respond("No plugins installed")
	}

	var response strings.Builder
	response.WriteString("Installed plugins:\n")
	for name, plugin := range plugins {
		status := "enabled"
		if !plugin.Enabled {
			status = "disabled"
		}
		response.WriteString(fmt.Sprintf("• %s v%s (%s) - %s\n",
			name, plugin.Version, status, plugin.Description))
	}

	return ctx.Respond(response.String())
}

// handleAPTEnable 处理启用插件
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

// handleAPTDisable 处理禁用插件
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

// handleAPTRemove 处理移除插件
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
