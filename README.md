# NexusValet

NexusValet 是一个基于 Go 语言和 gotd/td 库构建的 Telegram 人形机器人框架，提供强大的插件系统和事件驱动架构。

## ✨ 特性

- **🚀 事件驱动架构**: 支持消息事件、命令事件和原始事件的分层处理
- **🔗 钩子系统**: 提供生命周期钩子，支持优先级排序和自定义处理
- **🔌 插件系统**: 基于 Lua 脚本语言的动态插件加载和管理
- **⚡ 命令解析**: 支持 `.command` 格式的命令解析和执行
- **💾 会话管理**: 基于 SQLite 的会话持久化存储
- **📦 插件管理**: 类似 APT 的插件管理命令系统
- **📊 系统监控**: 内置系统状态监控和性能指标
- **🔒 安全认证**: 支持 Telegram 官方 API 认证

## 🏗️ 项目结构

```
NexusValet/
├── cmd/nexusvalet/           # 主程序入口
├── internal/
│   ├── core/                 # 核心系统（事件总线、钩子）
│   ├── plugin/               # 插件管理器和执行器
│   ├── command/              # 命令解析器
│   ├── peers/                # 用户/群组解析器
│   └── session/              # 会话管理
├── pkg/logger/               # 日志系统
├── plugins/                  # 插件目录
│   └── core_commands/        # 核心命令插件
├── config.example.json       # 配置文件示例
├── Makefile                  # 构建脚本
└── go.mod                    # Go 模块文件
```

## 📋 系统要求

- **Go**: 1.25.0 或更高版本
- **操作系统**: Linux, macOS, Windows
- **内存**: 至少 128MB RAM
- **存储**: 至少 50MB 可用空间

## 🚀 快速开始

### 1. 环境准备

确保您的系统已安装 Go 1.25.0 或更高版本：

```bash
go version
```

### 2. 克隆项目

```bash
git clone https://github.com/uki0xc/NexusValet.git
cd NexusValet
```

### 3. 安装依赖

```bash
make deps
```

### 4. 构建项目

```bash
make build
```

### 5. 配置 Telegram API

复制配置文件模板：

```bash
cp config.example.json config.json
```

编辑 `config.json` 文件，填入您的 Telegram API 凭证：

```json
{
  "telegram": {
    "api_id": YOUR_API_ID,
    "api_hash": "YOUR_API_HASH",
    "session_file": "session.json",
    "database_file": "sessions.db"
  },
  "bot": {
    "command_prefix": ".",
    "plugins_dir": "plugins"
  },
  "logger": {
    "level": "INFO"
  }
}
```

**获取 API 凭证的步骤：**
1. 访问 [my.telegram.org](https://my.telegram.org)
2. 登录您的 Telegram 账号
3. 进入 "API development tools"
4. 创建新应用并获取 `api_id` 和 `api_hash`

### 6. 运行

```bash
make run
```

首次运行时，程序会提示您输入手机号码进行 Telegram 认证。

## 📚 可用命令

### 系统命令

- `.status` - 显示系统状态信息（运行时间、内存使用、插件状态等）
- `.help` - 显示帮助信息
- `.help <插件名>` - 显示特定插件的帮助

### 插件管理命令

- `.apt list` - 列出所有已安装插件
- `.apt install <插件名>` - 安装插件 
- `.apt enable <插件名>` - 启用插件
- `.apt disable <插件名>` - 禁用插件
- `.apt remove <插件名>` - 删除插件


## 🛠️ 开发命令

```bash
# 开发模式运行
make dev

# 代码格式化
make fmt

# 代码检查
make lint

# 运行测试
make test

# 清理构建文件
make clean

# 跨平台构建
make build-all
```

## 📦 依赖库

- **[gotd/td](https://github.com/gotd/td)** - Telegram MTProto API 库
- **[gopher-lua](https://github.com/yuin/gopher-lua)** - Lua 脚本语言引擎
- **[modernc.org/sqlite](https://modernc.org/sqlite)** - SQLite 数据库驱动

## 🔧 配置选项

### Telegram 配置
- `api_id`: Telegram API ID
- `api_hash`: Telegram API Hash
- `session_file`: 会话文件路径
- `database_file`: 数据库文件路径

### Bot 配置
- `command_prefix`: 命令前缀（默认: "."）
- `plugins_dir`: 插件目录路径

### 日志配置
- `level`: 日志级别（DEBUG, INFO, WARN, ERROR）

## 🐛 故障排除

### 常见问题

1. **认证失败**
   - 检查 API ID 和 Hash 是否正确
   - 确保手机号码格式正确（包含国家代码）

2. **插件加载失败**
   - 检查插件语法是否正确
   - 查看日志文件中的错误信息

3. **连接问题**
   - 检查网络连接
   - 确认防火墙设置

### 日志查看

程序运行时会输出详细日志，包括：
- 插件加载状态
- 命令执行过程
- 错误和警告信息

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

### 贡献指南

1. Fork 项目
2. 创建功能分支
3. 提交更改
4. 推送到分支
5. 创建 Pull Request

## 📄 许可证

本项目采用 AGPL-3.0 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🙏 致谢

- **[gotd/td](https://github.com/gotd/td)** - 优秀的 Telegram 库
- **[gopher-lua](https://github.com/yuin/gopher-lua)** - Lua 引擎
