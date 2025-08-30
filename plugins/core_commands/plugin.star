# Core Commands Plugin for NexusValet
# Provides basic commands: .status and .help

def init():
    """Initialize the core commands plugin"""
    bot.log_info("Core commands plugin loaded")
    
    # Register .status command
    bot.register_command(
        name="status",
        handler=handle_status,
        description="显示系统状态信息"
    )
    
    # Register .help command
    bot.register_command(
        name="help",
        handler=handle_help,
        description="显示帮助信息"
    )

def handle_status(ctx):
    """Handle .status command"""
    # Get system information
    version = bot.get_version()
    go_version = bot.get_go_version()
    system_info = bot.get_system_info()
    memory_info = bot.get_memory_info()
    runtime_info = bot.get_runtime_info()
    plugin_info = bot.get_plugin_info()
    
    # Format uptime
    uptime_seconds = runtime_info.uptime
    uptime_minutes = uptime_seconds // 60
    uptime_secs = uptime_seconds % 60
    uptime_str = str(uptime_minutes) + "分钟 " + str(uptime_secs) + "秒"
    
    # Format memory (convert bytes to MB, rounded)
    alloc_mb = int(memory_info.alloc / (1024 * 1024) * 100) / 100.0
    sys_mb = int(memory_info.sys / (1024 * 1024) * 100) / 100.0
    
    # Build status message
    status_msg = """🤖 NexusValet 状态报告

**运行时间**: """ + uptime_str + """
**系统信息**:
   • Go版本: """ + str(go_version) + """
   • 系统: """ + str(system_info.os) + "/" + str(system_info.arch) + """
   • CPU核心: """ + str(system_info.cpu_count) + """
   • Goroutines: """ + str(runtime_info.goroutines) + """

**内存使用**:
   • 已分配: """ + str(alloc_mb) + """ MB
   • 系统占用: """ + str(sys_mb) + """ MB
   • GC次数: """ + str(memory_info.gc_count) + """

**插件状态**:
   • 已加载插件: """ + str(plugin_info.loaded_count) + """ 个
   • 系统插件版本: """ + str(plugin_info.system_version)
    
    ctx.respond(status_msg)

def handle_help(ctx):
    """Handle .help command"""
    if len(ctx.args) == 0:
        # Show all commands
        help_msg = """📖 NexusValet 帮助信息

🔧 可用命令:
• .status - 显示系统状态信息
• .help - 显示此帮助信息
• .help <插件名> - 显示特定插件的帮助
• .apt list - 列出所有插件
• .apt enable <插件名> - 启用插件
• .apt disable <插件名> - 禁用插件
• .apt remove <插件名> - 删除插件

💡 提示: 使用 .help core_commands 查看核心命令的详细信息"""
        
        ctx.respond(help_msg)
        
    elif len(ctx.args) == 1:
        # Show specific plugin help
        plugin_name = ctx.args[0]
        
        if plugin_name == "core_commands":
            detailed_help = """📋 核心命令插件详细帮助

🔍 .status 命令:
  显示 NexusValet 的系统状态信息，包括:
  • 应用版本号
  • Go 运行时版本
  • 当前系统时间
  • 运行状态

❓ .help 命令:
  • .help - 显示所有可用命令列表
  • .help <插件名> - 显示特定插件的详细帮助信息
  
🔌 插件信息:
  • 名称: core_commands
  • 版本: v1.0.0
  • 作者: NexusValet
  • 描述: 提供基础的系统命令功能"""
            
            ctx.respond(detailed_help)
        else:
            ctx.respond("❌ 未找到该插件的帮助信息: " + plugin_name)
    else:
        ctx.respond("❌ .help 命令参数过多。用法: .help [插件名]")