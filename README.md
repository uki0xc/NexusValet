# NexusValet

NexusValet 是一个基于 Go 语言和 gotd/td 库构建的 Telegram 人形机器人框架。

## 特性

- **事件驱动架构**: 支持消息事件、命令事件和原始事件的分层处理
- **钩子系统**: 提供生命周期钩子，支持优先级排序
- **插件系统**: 基于 Starlark 脚本语言的动态插件加载
- **命令解析**: 支持 `.command` 格式的命令解析和执行
- **会话管理**: 基于 SQLite 的会话持久化存储
- **插件管理**: 类似 APT 的插件管理命令

## 项目结构

```
NexusValet/
├── cmd/nexusvalet/           # 主程序入口
├── internal/
│   ├── core/                 # 核心系统（事件总线、钩子）
│   ├── plugin/               # 插件管理器和执行器
│   ├── command/              # 命令解析器
│   └── session/              # 会话管理
├── pkg/logger/               # 日志系统
├── plugins/                  # 插件目录
│   └── core_commands/        # 核心命令插件
└── Makefile                  # 构建脚本
```

## 快速开始

### 1. 克隆并构建

```bash
git clone https://github.com/uki0xc/NexusValet.git
cd NexusValet
make build
```

### 2. 配置 Telegram API

在 `config.json` 中设置您的 API 凭证(复制[config.example.json](config.example.json)文件)：

```JSON
{
  "telegram": {
    "api_id": YOUR_API_ID,
    "api_hash": "YOUR_API_HASH",
    ...
  }
}
```

### 3. 运行

```bash
make run
```

首次运行时，程序会提示您输入手机号码进行 Telegram 认证。

## 可用命令

### 系统命令

- `.status` - 显示系统状态信息
- `.help` - 显示帮助信息
- `.help <插件名>` - 显示特定插件的帮助

### 插件管理命令

- `.apt list` - 列出所有已安装插件
- `.apt enable <插件名>` - 启用插件
- `.apt disable <插件名>` - 禁用插件
- `.apt remove <插件名>` - 删除插件


## 依赖

- [gotd/td](https://github.com/gotd/td) - Telegram MTProto API 库
- [go.starlark.net](https://github.com/google/starlark-go) - Starlark 脚本语言

## 灵感来源
- [PagerMaid](https://github.com/TeamPGM/PagerMaid-Pyro) - PagerMaid-Pyro

## 许可证

本项目采用AGPL-3.0许可证[LICENSE](LICENSE)。