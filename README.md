# NexusValet

NexusValet 是一个基于 Go 语言和 gotd/td 库构建的 Telegram 人形机器人框架，提供强大的插件系统和事件驱动架构。

## ✨ 特性

- **🚀 事件驱动架构**: 支持消息事件、命令事件和原始事件的分层处理
- **🔗 钩子系统**: 提供生命周期钩子，支持优先级排序和自定义处理
- **🔌 插件系统**: 基于 Go 的插件体系，内置多插件（支持命令/事件/钩子）
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
│   ├── plugin/               # 插件管理器与内置插件（core/apt/speedtest/sb）
│   ├── command/              # 命令解析器
│   ├── peers/                # 用户/群组解析器
│   └── session/              # 会话管理
├── pkg/logger/               # 日志系统
├── config.example.json       # 配置文件示例
├── Makefile                  # 构建脚本
└── go.mod                    # Go 模块文件
```

## 📋 系统要求

- **Go**: 1.25.0 或更高版本
- **操作系统**: Linux, macOS, Windows

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

### Gemini AI 命令

- `.gemini <问题>` 或 `.gm <问题>` - 智能问答（自动识别文本/图片模式）
- `.gemini reply <问题>` - 回复模式问答
- `.gemini config` - 查看当前配置
- `.gemini key <API密钥>` - 设置 Gemini API 密钥
- `.gemini model <模型名>` - 设置使用的模型
- `.gemini auto <True/False>` - 设置自动删除空提问

### 封禁（sb）命令

- `.sb`（回复一条消息使用）- 封禁被回复的用户
- `.sb <用户ID>` - 通过用户 ID 封禁用户
- `.sb @<用户名>` - 通过用户名封禁用户
- `.sb <用户ID|@用户名> 0` - 仅封禁，不删除其消息历史

说明：
- 仅限群组/超级群组使用，需要管理员权限
- 成功封禁的提示消息会在 30 秒后自动删除
- 当未提供完整上下文时，系统会自动解析并维护 access_hash 以提升成功率

### 插件管理命令

- `.apt list` - 列出所有已注册插件
- `.apt enable <插件名>` - 启用插件
- `.apt disable <插件名>` - 禁用插件


## 📦 依赖库

- **[gotd/td](https://github.com/gotd/td)** - Telegram MTProto API 库
- **[modernc.org/sqlite](https://modernc.org/sqlite)** - SQLite 数据库驱动

## 🔨 内置插件

- **核心命令（core）**: `.status`, `.help`
- **插件管理（apt）**: `.apt list`, `.apt enable`, `.apt disable`
- **超级封禁（sb）**:
  - 功能：封禁用户、可选清理消息历史
  - AccessHash 管理：内置 AccessHashManager，支持缓存、从回复消息解析、从群成员列表与参与者信息回退获取
  - 输出：纯文本显示用户名与用户名片，不使用超链接
  - 成功提示会在 30 秒后自动撤回
- **Gemini AI（gemini）**:
  - 智能问答：`.gemini <问题>` 或 `.gm <问题>`
  - 自动识别：文本问答 + 图片分析（vision模式）
  - 回复模式：添加 `reply` 或 `r` 参数
  - 配置管理：`.gemini config`, `.gemini key <密钥>`, `.gemini model <模型>`


## 📄 许可证

本项目采用 AGPL-3.0 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🙏 致谢

- **[gotd/td](https://github.com/gotd/td)** - 优秀的 Telegram 库
