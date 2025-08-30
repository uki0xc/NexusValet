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

def format_uptime(uptime_seconds):
    """Format uptime in a human-readable way"""
    days = uptime_seconds // 86400
    hours = (uptime_seconds % 86400) // 3600
    minutes = (uptime_seconds % 3600) // 60
    seconds = uptime_seconds % 60
    
    parts = []
    if days > 0:
        parts.append(str(days) + "天")
    if hours > 0:
        parts.append(str(hours) + "小时")
    if minutes > 0:
        parts.append(str(minutes) + "分钟")
    if seconds > 0 or len(parts) == 0:
        parts.append(str(seconds) + "秒")
    
    return " ".join(parts)

def format_memory_size(bytes_size):
    """Format memory size in human-readable format"""
    if bytes_size >= 1024 * 1024 * 1024:  # GB
        size = int(bytes_size / (1024 * 1024 * 1024) * 100) / 100.0
        return str(size) + " GB"
    elif bytes_size >= 1024 * 1024:  # MB
        size = int(bytes_size / (1024 * 1024) * 100) / 100.0
        return str(size) + " MB"
    elif bytes_size >= 1024:  # KB
        size = int(bytes_size / 1024 * 100) / 100.0
        return str(size) + " KB"
    else:
        return str(bytes_size) + " B"

def handle_status(ctx):
    """Handle .status command"""
    # Get system information
    version = bot.get_version()
    go_version = bot.get_go_version()
    system_info = bot.get_system_info()
    memory_info = bot.get_memory_info()
    runtime_info = bot.get_runtime_info()
    plugin_info = bot.get_plugin_info()
    kernel_info = bot.get_kernel_info()
    current_time = bot.get_time()
    
    # Format uptime
    uptime_str = format_uptime(runtime_info.uptime)
    
    # Format memory sizes
    alloc_str = format_memory_size(memory_info.alloc)
    sys_str = format_memory_size(memory_info.sys)
    
    # Get kernel version - safely access the version attribute
    kernel_version = "N/A"
    if kernel_info and kernel_info.version:
        kernel_version = str(kernel_info.version)
    
    # Build status message
    status_msg = """NexusValet 状态报告

运行时间: """ + uptime_str + """
系统信息:
   • Go版本: """ + str(go_version) + """
   • 系统: """ + str(system_info.os) + "/" + str(system_info.arch) + """
   • Kernel 版本: """ + kernel_version + """
   • NexusValet版本: """ + str(version) + """

内存使用:
   • 已分配: """ + alloc_str + """
   • 系统占用: """ + sys_str + """

插件状态:
   • 已加载插件: """ + str(plugin_info.loaded_count) + """ 个

状态检查时间: """ + str(current_time)
    
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
            ctx.respond("未找到该插件的帮助信息: " + plugin_name)
    else:
        ctx.respond(".help 命令参数过多。用法: .help [插件名]")