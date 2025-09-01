-- Core Commands Plugin for NexusValet
-- Provides basic commands: .status and .help

function init()
    bot.log_info("Core commands plugin loaded")

    -- Register .status command
    bot.register_command({
        name = "status",
        handler = handle_status,
        description = "显示系统状态信息"
    })

    -- Register .help command
    bot.register_command({
        name = "help",
        handler = handle_help,
        description = "显示帮助信息"
    })
end

function format_uptime(uptime_seconds)
    local days = math.floor(uptime_seconds / 86400)
    local hours = math.floor((uptime_seconds % 86400) / 3600)
    local minutes = math.floor((uptime_seconds % 3600) / 60)
    local seconds = math.floor(uptime_seconds % 60)

    local parts = {}
    if days > 0 then
        table.insert(parts, tostring(days) .. "天")
    end
    if hours > 0 then
        table.insert(parts, tostring(hours) .. "小时")
    end
    if minutes > 0 then
        table.insert(parts, tostring(minutes) .. "分钟")
    end
    if seconds > 0 or #parts == 0 then
        table.insert(parts, tostring(seconds) .. "秒")
    end

    return table.concat(parts, " ")
end

function format_memory_size(bytes_size)
    if bytes_size >= 1024 * 1024 * 1024 then -- GB
        local size = math.floor(bytes_size / (1024 * 1024 * 1024) * 100) / 100.0
        return tostring(size) .. " GB"
    elseif bytes_size >= 1024 * 1024 then -- MB
        local size = math.floor(bytes_size / (1024 * 1024) * 100) / 100.0
        return tostring(size) .. " MB"
    elseif bytes_size >= 1024 then -- KB
        local size = math.floor(bytes_size / 1024 * 100) / 100.0
        return tostring(size) .. " KB"
    else
        return tostring(bytes_size) .. " B"
    end
end

function handle_status(ctx)
    -- Get system information
    local version = bot.get_version()
    local go_version = bot.get_go_version()
    local system_info = bot.get_system_info()
    local memory_info = bot.get_memory_info()
    local runtime_info = bot.get_runtime_info()
    local plugin_info = bot.get_plugin_info()
    local kernel_info = bot.get_kernel_info()
    local current_time = bot.get_time()

    -- Format uptime
    local uptime_str = format_uptime(runtime_info.uptime)

    -- Format memory sizes
    local alloc_str = format_memory_size(memory_info.alloc)
    local sys_str = format_memory_size(memory_info.sys)

    -- Get kernel version
    local kernel_version = "N/A"
    if kernel_info and kernel_info.version then
        kernel_version = kernel_info.version
    end

    -- Build status message
    local status_msg = string.format([[NexusValet 状态报告

运行时间: %s
系统信息:
   • Go版本: %s
   • 系统: %s/%s
   • Kernel 版本: %s
   • NexusValet版本: %s

内存使用:
   • 已分配: %s
   • 系统占用: %s

插件状态:
   • 已加载插件: %d 个

状态检查时间: %s]], uptime_str, go_version, system_info.os, system_info.arch, kernel_version, version, alloc_str, sys_str, plugin_info.loaded_count, current_time)

    ctx.respond(status_msg)
end

function handle_help(ctx)
    if #ctx.args == 0 then
        -- Show all commands
        local help_msg = [[📖 NexusValet 帮助信息

🔧 可用命令:
• .status - 显示系统状态信息
• .help - 显示此帮助信息
• .help <插件名> - 显示特定插件的帮助
• .apt list - 列出所有插件
• .apt enable <插件名> - 启用插件
• .apt disable <插件名> - 禁用插件
• .apt remove <插件名> - 删除插件

💡 提示: 使用 .help core_commands 查看核心命令的详细信息]]

        ctx.respond(help_msg)

    elseif #ctx.args == 1 then
        -- Show specific plugin help
        local plugin_name = ctx.args[1]

        if plugin_name == "core_commands" then
            local detailed_help = [[📋 核心命令插件详细帮助

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
  • 描述: 提供基础的系统命令功能]]

            ctx.respond(detailed_help)
        else
            ctx.respond("未找到该插件的帮助信息: " .. plugin_name)
        end
    else
        ctx.respond(".help 命令参数过多。用法: .help [插件名]")
    end
end
