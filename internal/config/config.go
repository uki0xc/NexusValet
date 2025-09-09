package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config 代表应用程序配置
type Config struct {
	Telegram TelegramConfig `json:"telegram"`
	Bot      BotConfig      `json:"bot"`
	Logger   LoggerConfig   `json:"logger"`
}

// TelegramConfig 包含 Telegram API 配置
type TelegramConfig struct {
	APIID    int    `json:"api_id"`
	APIHash  string `json:"api_hash"`
	Session  string `json:"session_file"`
	Database string `json:"database_file"`
}

// BotConfig 包含机器人特定配置
type BotConfig struct {
	CommandPrefix string `json:"command_prefix"`
	PluginsDir    string `json:"plugins_dir"`
}

// LoggerConfig 包含日志配置
type LoggerConfig struct {
	Level string `json:"level"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Telegram: TelegramConfig{
			APIID:    0,  // 需要用户提供
			APIHash:  "", // 需要用户提供
			Session:  "session/session.json",
			Database: "session/sessions.db",
		},
		Bot: BotConfig{
			CommandPrefix: ".",
			PluginsDir:    "plugins",
		},
		Logger: LoggerConfig{
			Level: "INFO",
		},
	}
}

// LoadConfig 从指定文件加载配置
func LoadConfig(configPath string) (*Config, error) {
	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// 如果不存在，创建默认配置
		defaultConfig := DefaultConfig()
		if err := SaveConfig(configPath, defaultConfig); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		return nil, fmt.Errorf("config file created at %s, please fill in your API credentials", configPath)
	}

	// 读取配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse JSON
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate required fields
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &config, nil
}

// SaveConfig saves configuration to the specified file
func SaveConfig(configPath string, config *Config) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Telegram.APIID == 0 {
		return fmt.Errorf("telegram.api_id is required")
	}
	if c.Telegram.APIHash == "" {
		return fmt.Errorf("telegram.api_hash is required")
	}
	if c.Bot.CommandPrefix == "" {
		return fmt.Errorf("bot.command_prefix is required")
	}
	if c.Bot.PluginsDir == "" {
		return fmt.Errorf("bot.plugins_dir is required")
	}
	return nil
}

// GetConfigPath returns the default config file path
func GetConfigPath() string {
	// 首先检查当前目录
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}

	// 检查上级目录（适用于从bin目录启动的情况）
	if _, err := os.Stat("../config.json"); err == nil {
		return "../config.json"
	}

	// 默认返回当前目录
	return "config.json"
}

// NormalizePath 将相对于配置文件的路径转换为绝对路径
func NormalizePath(configPath, relativePath string) string {
	if filepath.IsAbs(relativePath) {
		return relativePath
	}

	// 获取配置文件所在目录
	configDir := filepath.Dir(configPath)

	// 如果配置文件在当前目录，直接返回相对路径
	if configDir == "." {
		return relativePath
	}

	// 将路径相对于配置文件目录
	return filepath.Join(configDir, relativePath)
}
