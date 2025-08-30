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

// validatePluginContent performs basic validation on plugin content
func (pm *Manager) validatePluginContent(data []byte) bool {
	content := string(data)

	// Basic checks for Starlark plugin structure
	requiredPatterns := []string{
		"def init(",   // Must have init function
		"def handle_", // Must have at least one handler function
	}

	for _, pattern := range requiredPatterns {
		if !strings.Contains(content, pattern) {
			logger.Debugf("Plugin validation failed: missing pattern %s", pattern)
			return false
		}
	}

	// Check for dangerous patterns (basic security)
	dangerousPatterns := []string{
		"import os",
		"import sys",
		"__import__",
		"eval(",
		"exec(",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(content, pattern) {
			logger.Warnf("Plugin validation failed: dangerous pattern detected: %s", pattern)
			return false
		}
	}

	// Check for unsupported Python syntax that's not valid in Starlark
	unsupportedPatterns := []string{
		"try:",
		"except:",
		"except ",
		"finally:",
		"raise ",
		"class ",
		"import ",
		"yield ",
		"async ",
		"await ",
		"while ", // while loops
		"global ",
		"nonlocal ",
		"@", // decorators
		"lambda ",
	}

	for _, pattern := range unsupportedPatterns {
		if strings.Contains(content, pattern) {
			logger.Warnf("Plugin validation failed: unsupported Python syntax detected: %s", pattern)
			logger.Warnf("Starlark doesn't support this Python feature. Please rewrite using Starlark-compatible syntax.")
			return false
		}
	}

	return true
}

// checkStarlarkCompatibility provides detailed compatibility check for Starlark
func (pm *Manager) checkStarlarkCompatibility(content string) []string {
	var issues []string

	// Line-by-line check for unsupported syntax
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		lineNum := i + 1

		if strings.HasPrefix(line, "try:") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'try:' 语句不被支持，请使用 if/else 替代", lineNum))
		}
		if strings.HasPrefix(line, "except") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'except' 语句不被支持，请使用 if/else 替代", lineNum))
		}
		if strings.HasPrefix(line, "class ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'class' 定义不被支持，请使用字典或函数替代", lineNum))
		}
		if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "from ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'import' 语句不被支持，请使用内置函数", lineNum))
		}
		if strings.Contains(line, "lambda ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'lambda' 表达式不被支持，请使用命名函数", lineNum))
		}
		if strings.HasPrefix(line, "@") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 装饰器不被支持", lineNum))
		}
		if strings.HasPrefix(line, "with ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'with' 语句不被支持", lineNum))
		}
		if strings.HasPrefix(line, "while ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'while' 循环不被支持，请使用递归或 for 循环替代", lineNum))
		}
		if strings.Contains(line, "yield ") {
			issues = append(issues, fmt.Sprintf("第 %d 行: 'yield' 不被支持，Starlark 不支持生成器", lineNum))
		}
	}

	return issues
}

// createDefaultVersionJSON creates a default version.json for a plugin
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
		// Return minimal valid JSON
		return []byte(fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0", 
  "author": "Telegram User",
  "description": "Plugin installed from %s"
}`, pluginName, filename))
	}

	return data
}

// getDocumentFilename extracts filename from a Telegram document
func (pm *Manager) getDocumentFilename(document *tg.Document) string {
	// Search through document attributes for filename
	for _, attr := range document.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			return a.FileName
		}
	}

	// If no filename attribute found, generate a default name
	return fmt.Sprintf("document_%d", document.ID)
}

// registerAPTCommands registers the APT-style plugin management commands
func (pm *Manager) registerAPTCommands() {
	// .apt install command
	pm.parser.RegisterCommand("apt", "Plugin management commands", "system", func(ctx *command.CommandContext) error {
		if len(ctx.Args) == 0 {
			return ctx.Respond("Usage: .apt <install|list|enable|disable|remove|check> [plugin_name]\n\n命令说明:\n• install <名称> - 安装插件 (需要文件或回复文件)\n• list - 列出已安装插件\n• enable <名称> - 启用插件\n• disable <名称> - 禁用插件\n• remove <名称> - 删除插件\n• check - 检查插件语法 (需要文件或回复文件)")
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
		case "check":
			return pm.handleAPTCheck(ctx)
		default:
			return ctx.Respond(fmt.Sprintf("Unknown subcommand: %s\n\n可用命令:\n• install <名称> - 安装插件\n• list - 列出插件\n• enable <名称> - 启用插件\n• disable <名称> - 禁用插件\n• remove <名称> - 删除插件\n• check - 检查插件语法", subcommand))
		}
	})
}

// handleAPTInstall handles plugin installation
func (pm *Manager) handleAPTInstall(ctx *command.CommandContext) error {
	// Check if a plugin name is provided
	if len(ctx.Args) < 2 {
		return ctx.Respond("用法: .apt install <插件名> [回复包含.star文件的消息]")
	}

	pluginName := ctx.Args[1]

	// Check if plugin already exists
	if _, exists := pm.GetPlugin(pluginName); exists {
		return ctx.Respond(fmt.Sprintf("插件 %s 已存在。请先使用 .apt remove %s 移除现有插件。", pluginName, pluginName))
	}

	// Try to get document from the message or replied message
	document, err := ctx.GetDocument()
	if err != nil {
		return ctx.Respond("未找到文件。请在包含 .star 插件文件的消息上使用此命令，或回复包含 .star 文件的消息。\n\n使用方法:\n1. 直接在包含 .star 文件的消息上运行 .apt install <插件名>\n2. 回复包含 .star 文件的消息，然后运行 .apt install <插件名>")
	}

	// Check if it's a .star file
	filename := pm.getDocumentFilename(document)
	if !strings.HasSuffix(filename, ".star") {
		return ctx.Respond(fmt.Sprintf("请提供 .star 格式的插件文件。当前文件: %s", filename))
	}

	// Download the file
	pluginData, err := ctx.DownloadFile(document)
	if err != nil {
		logger.Errorf("Failed to download plugin file: %v", err)
		return ctx.Respond("下载插件文件失败: " + err.Error())
	}

	// Validate plugin content (basic check)
	if !pm.validatePluginContent(pluginData) {
		// Provide detailed compatibility check
		issues := pm.checkStarlarkCompatibility(string(pluginData))
		var errorMsg strings.Builder
		errorMsg.WriteString("❌ 插件文件包含不兼容的语法\n\n发现的问题:\n")

		if len(issues) > 0 {
			for _, issue := range issues {
				errorMsg.WriteString("• " + issue + "\n")
			}
		} else {
			errorMsg.WriteString("• 文件格式无效或缺少必需函数\n")
		}

		errorMsg.WriteString("\n💡 Starlark 兼容性提示:\n")
		errorMsg.WriteString("• 使用 if/else 替代 try/except\n")
		errorMsg.WriteString("• 使用函数和字典替代类\n")
		errorMsg.WriteString("• 使用内置的 bot API 替代 import\n")
		errorMsg.WriteString("• 参考: https://github.com/bazelbuild/starlark/blob/master/spec.md")

		return ctx.Respond(errorMsg.String())
	}

	// Create default version.json
	versionData := pm.createDefaultVersionJSON(pluginName, filename)

	// Install the plugin
	if err := pm.InstallPlugin(pluginName, pluginData, versionData); err != nil {
		logger.Errorf("Failed to install plugin %s: %v", pluginName, err)

		// Check if it's a syntax error
		errorStr := err.Error()
		if strings.Contains(errorStr, "got try, want primary expression") {
			return ctx.Respond(fmt.Sprintf("❌ 插件安装失败：Starlark 语法错误\n\n错误: %s\n\n💡 解决方案:\n• 将 try/except 语句替换为 if/else 条件判断\n• 使用函数返回值来处理错误情况\n• 参考 Starlark 语法文档重写代码", errorStr))
		} else if strings.Contains(errorStr, "syntax error") || strings.Contains(errorStr, "want primary expression") {
			return ctx.Respond(fmt.Sprintf("❌ 插件安装失败：语法错误\n\n错误: %s\n\n请检查 Starlark 语法并修正错误", errorStr))
		}

		return ctx.Respond(fmt.Sprintf("❌ 插件安装失败: %v", err))
	}

	return ctx.Respond(fmt.Sprintf("✅ 插件 %s 安装成功！\n\n使用 .apt list 查看已安装插件\n使用 .help %s 查看插件帮助", pluginName, pluginName))
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

// handleAPTCheck handles plugin syntax checking
func (pm *Manager) handleAPTCheck(ctx *command.CommandContext) error {
	// Try to get document from the message or replied message
	document, err := ctx.GetDocument()
	if err != nil {
		return ctx.Respond("未找到文件。请在包含 .star 插件文件的消息上使用此命令，或回复包含 .star 文件的消息。")
	}

	// Check if it's a .star file
	filename := pm.getDocumentFilename(document)
	if !strings.HasSuffix(filename, ".star") {
		return ctx.Respond(fmt.Sprintf("请提供 .star 格式的插件文件。当前文件: %s", filename))
	}

	// Download the file
	pluginData, err := ctx.DownloadFile(document)
	if err != nil {
		logger.Errorf("Failed to download plugin file: %v", err)
		return ctx.Respond("下载插件文件失败: " + err.Error())
	}

	content := string(pluginData)

	// Check basic structure
	hasInit := strings.Contains(content, "def init(")
	hasHandler := strings.Contains(content, "def handle_")

	// Check for compatibility issues
	issues := pm.checkStarlarkCompatibility(content)

	// Build response
	var response strings.Builder
	response.WriteString(fmt.Sprintf("🔍 插件语法检查结果: %s\n\n", filename))

	// Structure check
	response.WriteString("📋 结构检查:\n")
	if hasInit {
		response.WriteString("✅ 包含 init() 函数\n")
	} else {
		response.WriteString("❌ 缺少 init() 函数\n")
	}

	if hasHandler {
		response.WriteString("✅ 包含处理函数\n")
	} else {
		response.WriteString("❌ 缺少处理函数 (def handle_*)\n")
	}

	// Compatibility check
	response.WriteString("\n🔧 兼容性检查:\n")
	if len(issues) == 0 {
		response.WriteString("✅ 未发现兼容性问题\n")
	} else {
		response.WriteString("❌ 发现兼容性问题:\n")
		for _, issue := range issues {
			response.WriteString("  • " + issue + "\n")
		}
		response.WriteString("\n💡 修复建议:\n")
		response.WriteString("• 将 try/except 替换为 if/else\n")
		response.WriteString("• 使用函数和字典替代类定义\n")
		response.WriteString("• 使用 bot API 替代 import 语句\n")
	}

	// Overall result
	response.WriteString("\n📊 总体评估:\n")
	if hasInit && hasHandler && len(issues) == 0 {
		response.WriteString("✅ 插件语法正确，可以安装")
	} else {
		response.WriteString("❌ 插件需要修复后才能安装")
	}

	return ctx.Respond(response.String())
}
