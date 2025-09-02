package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/core"
	"nexusvalet/pkg/logger"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	lua "github.com/yuin/gopher-lua"
)

var startTime = time.Now() // 启动时间

// LuaExecutor 执行 Lua 插件脚本
type LuaExecutor struct {
	manager     *Manager
	parser      *command.Parser
	dispatcher  *core.EventDispatcher
	hookManager *core.HookManager
}

// NewLuaExecutor 创建一个新的 Lua 执行器
func NewLuaExecutor(manager *Manager, parser *command.Parser, dispatcher *core.EventDispatcher, hookManager *core.HookManager) *LuaExecutor {
	return &LuaExecutor{
		manager:     manager,
		parser:      parser,
		dispatcher:  dispatcher,
		hookManager: hookManager,
	}
}

// ExecutePlugin 执行一个 Lua 插件脚本
func (le *LuaExecutor) ExecutePlugin(pluginInfo *PluginInfo) error {
	// 读取插件脚本
	script, err := le.manager.readFile(pluginInfo.Dir, "plugin.lua")
	if err != nil {
		return fmt.Errorf("failed to read plugin script: %w", err)
	}

	// 为此插件创建一个新的 Lua 状态
	L := lua.NewState()
	pluginInfo.L = L

	// 为插件创建预定义环境
	le.createPredeclaredEnvironment(pluginInfo)

	// 执行插件脚本
	if err := L.DoString(string(script)); err != nil {
		return fmt.Errorf("failed to execute plugin script: %w", err)
	}

	// 如果存在，则调用插件初始化函数
	if initFunc := L.GetGlobal("init"); initFunc.Type() == lua.LTFunction {
		if err := L.CallByParam(lua.P{
			Fn:      initFunc,
			NRet:    0,
			Protect: true,
		}); err != nil {
			return fmt.Errorf("plugin init function failed: %w", err)
		}
	}

	logger.Debugf("Plugin %s executed successfully", pluginInfo.Name)
	return nil
}

// createPredeclaredEnvironment 为 Lua 插件创建预定义环境
func (le *LuaExecutor) createPredeclaredEnvironment(pluginInfo *PluginInfo) {
	// 创建 bot API 模块
	botAPITable := le.createBotAPITable(pluginInfo)
	pluginInfo.L.SetGlobal("bot", botAPITable)
}

func (le *LuaExecutor) createBotAPITable(pluginInfo *PluginInfo) *lua.LTable {
	botAPI := pluginInfo.L.NewTable()
	botAPI.RawSetString("register_command", pluginInfo.L.NewFunction(le.makeRegisterCommand(pluginInfo)))
	botAPI.RawSetString("register_listener", pluginInfo.L.NewFunction(le.makeRegisterListener(pluginInfo)))
	botAPI.RawSetString("register_hook", pluginInfo.L.NewFunction(le.makeRegisterHook(pluginInfo)))
	botAPI.RawSetString("log_info", pluginInfo.L.NewFunction(le.makeLogInfo()))
	botAPI.RawSetString("log_debug", pluginInfo.L.NewFunction(le.makeLogDebug()))
	botAPI.RawSetString("log_warn", pluginInfo.L.NewFunction(le.makeLogWarn()))
	botAPI.RawSetString("log_error", pluginInfo.L.NewFunction(le.makeLogError()))
	botAPI.RawSetString("get_version", pluginInfo.L.NewFunction(le.makeGetVersion()))
	botAPI.RawSetString("get_go_version", pluginInfo.L.NewFunction(le.makeGetGoVersion()))
	botAPI.RawSetString("get_time", pluginInfo.L.NewFunction(le.makeGetTime()))
	botAPI.RawSetString("get_system_info", pluginInfo.L.NewFunction(le.makeGetSystemInfo()))
	botAPI.RawSetString("get_memory_info", pluginInfo.L.NewFunction(le.makeGetMemoryInfo()))
	botAPI.RawSetString("get_runtime_info", pluginInfo.L.NewFunction(le.makeGetRuntimeInfo()))
	botAPI.RawSetString("get_plugin_info", pluginInfo.L.NewFunction(le.makeGetPluginInfo()))
	botAPI.RawSetString("get_kernel_info", pluginInfo.L.NewFunction(le.makeGetKernelInfo()))
	botAPI.RawSetString("exec_command", pluginInfo.L.NewFunction(le.makeExecCommand()))
	botAPI.RawSetString("get_self_user", pluginInfo.L.NewFunction(le.makeBotGetSelfUser()))

	// ===================== 新增 JSON 函数 =====================
	jsonModule := pluginInfo.L.NewTable()
	jsonModule.RawSetString("decode", pluginInfo.L.NewFunction(le.jsonDecode))
	jsonModule.RawSetString("encode", pluginInfo.L.NewFunction(le.jsonEncode))
	pluginInfo.L.SetGlobal("json", jsonModule)
	// =======================================================

	// ===================== Telegram 模块 =====================
	telegramModule := pluginInfo.L.NewTable()
	telegramModule.RawSetString("get_self", pluginInfo.L.NewFunction(le.makeTelegramGetSelf()))
	pluginInfo.L.SetGlobal("telegram", telegramModule)
	// =======================================================

	return botAPI
}

// makeTelegramGetSelf 暴露 telegram.get_self()
func (le *LuaExecutor) makeTelegramGetSelf() lua.LGFunction {
	return func(L *lua.LState) int {
		if le.parser == nil || le.parser.GetTelegramAPI() == nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("telegram api not available"))
			return 2
		}
		api := le.parser.GetTelegramAPI()
		ctx := context.Background()
		users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil || len(users) == 0 {
			L.Push(lua.LNil)
			if err != nil {
				L.Push(lua.LString(err.Error()))
			} else {
				L.Push(lua.LString("empty users response"))
			}
			return 2
		}
		if u, ok := users[0].(*tg.User); ok {
			tbl := L.NewTable()
			if u.Username != "" {
				tbl.RawSetString("username", lua.LString(u.Username))
			}
			tbl.RawSetString("id", lua.LNumber(u.ID))
			L.Push(tbl)
			return 1
		}
		L.Push(lua.LNil)
		L.Push(lua.LString("unexpected user type"))
		return 2
	}
}

// makeBotGetSelfUser 暴露 bot.get_self_user()（与 telegram.get_self 等价）
func (le *LuaExecutor) makeBotGetSelfUser() lua.LGFunction {
	return func(L *lua.LState) int {
		if le.parser == nil || le.parser.GetTelegramAPI() == nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("telegram api not available"))
			return 2
		}
		api := le.parser.GetTelegramAPI()
		ctx := context.Background()
		users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil || len(users) == 0 {
			L.Push(lua.LNil)
			if err != nil {
				L.Push(lua.LString(err.Error()))
			} else {
				L.Push(lua.LString("empty users response"))
			}
			return 2
		}
		if u, ok := users[0].(*tg.User); ok {
			tbl := L.NewTable()
			if u.Username != "" {
				tbl.RawSetString("username", lua.LString(u.Username))
			}
			tbl.RawSetString("id", lua.LNumber(u.ID))
			L.Push(tbl)
			return 1
		}
		L.Push(lua.LNil)
		L.Push(lua.LString("unexpected user type"))
		return 2
	}
}

// makeRegisterCommand 创建 register_command 内置函数
func (le *LuaExecutor) makeRegisterCommand(pluginInfo *PluginInfo) lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		commandName := tbl.RawGetString("name").String()
		description := tbl.RawGetString("description").String()
		handler := tbl.RawGetString("handler")

		cmdHandler := func(ctx *command.CommandContext) error {
			luaCtx := le.createCommandContext(L, ctx)
			if err := L.CallByParam(lua.P{
				Fn:      handler,
				NRet:    0,
				Protect: true,
			}, luaCtx); err != nil {
				return err
			}
			return nil
		}

		le.parser.RegisterCommand(commandName, description, pluginInfo.Name, cmdHandler)
		return 0
	}
}

// makeRegisterListener 创建 register_listener 内置函数
func (le *LuaExecutor) makeRegisterListener(pluginInfo *PluginInfo) lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		listenerType := tbl.RawGetString("type").String()
		pattern := tbl.RawGetString("pattern").String()
		handler := tbl.RawGetString("handler")
		priority := int(tbl.RawGetString("priority").(lua.LNumber))

		eventHandler := func(ctx context.Context, event interface{}) error {
			luaEvent := le.createEventContext(L, event)
			if err := L.CallByParam(lua.P{
				Fn:      handler,
				NRet:    0,
				Protect: true,
			}, luaEvent); err != nil {
				return err
			}
			return nil
		}
		name := fmt.Sprintf("%s_%s", pluginInfo.Name, listenerType)
		switch listenerType {
		case "message":
			if pattern != "" {
				le.dispatcher.RegisterMessageListener(name, pattern, eventHandler, priority)
			} else {
				le.dispatcher.RegisterPrefixListener(name, "", eventHandler, priority)
			}
		case "raw":
			le.dispatcher.RegisterRawListener(name, eventHandler, priority)
		default:
			L.RaiseError("unknown listener type: %s", listenerType)
		}

		return 0
	}
}

// makeRegisterHook 创建 register_hook 内置函数
func (le *LuaExecutor) makeRegisterHook(pluginInfo *PluginInfo) lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		hookType := tbl.RawGetString("type").String()
		handler := tbl.RawGetString("handler")
		priority := int(tbl.RawGetString("priority").(lua.LNumber))

		hookHandler := func(hookCtx *core.HookContext) error {
			luaCtx := le.createHookContext(L, hookCtx)
			if err := L.CallByParam(lua.P{
				Fn:      handler,
				NRet:    0,
				Protect: true,
			}, luaCtx); err != nil {
				return err
			}
			return nil
		}

		name := fmt.Sprintf("%s_%s", pluginInfo.Name, hookType)
		le.hookManager.RegisterHook(core.HookType(hookType), name, hookHandler, priority)
		return 0
	}
}

// 日志函数
func (le *LuaExecutor) makeLogInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		logger.Infof("[Plugin] %s", msg)
		return 0
	}
}

func (le *LuaExecutor) makeLogDebug() lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		logger.Debugf("[Plugin] %s", msg)
		return 0
	}
}

func (le *LuaExecutor) makeLogWarn() lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		logger.Warnf("[Plugin] %s", msg)
		return 0
	}
}

func (le *LuaExecutor) makeLogError() lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		logger.Errorf("[Plugin] %s", msg)
		return 0
	}
}

// 实用函数
func (le *LuaExecutor) makeGetVersion() lua.LGFunction {
	return func(L *lua.LState) int {
		L.Push(lua.LString("v1.0.0"))
		return 1
	}
}

func (le *LuaExecutor) makeGetGoVersion() lua.LGFunction {
	return func(L *lua.LState) int {
		L.Push(lua.LString(runtime.Version()))
		return 1
	}
}

func (le *LuaExecutor) makeGetTime() lua.LGFunction {
	return func(L *lua.LState) int {
		L.Push(lua.LString(time.Now().Format("2006-01-02 15:04:05")))
		return 1
	}
}
func (le *LuaExecutor) makeGetSystemInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.NewTable()
		tbl.RawSetString("os", lua.LString(runtime.GOOS))
		tbl.RawSetString("arch", lua.LString(runtime.GOARCH))
		tbl.RawSetString("cpu_count", lua.LNumber(runtime.NumCPU()))
		L.Push(tbl)
		return 1
	}
}
func (le *LuaExecutor) makeGetMemoryInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		tbl := L.NewTable()
		tbl.RawSetString("alloc", lua.LNumber(m.Alloc))
		tbl.RawSetString("sys", lua.LNumber(m.Sys))
		tbl.RawSetString("gc_count", lua.LNumber(m.NumGC))
		L.Push(tbl)
		return 1
	}
}

func (le *LuaExecutor) makeGetRuntimeInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		uptime := time.Since(startTime)
		tbl := L.NewTable()
		tbl.RawSetString("goroutines", lua.LNumber(runtime.NumGoroutine()))
		tbl.RawSetString("uptime", lua.LNumber(uptime.Seconds()))
		L.Push(tbl)
		return 1
	}
}

func (le *LuaExecutor) makeGetPluginInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		pluginCount := len(le.manager.GetAllPlugins())
		tbl := L.NewTable()
		tbl.RawSetString("loaded_count", lua.LNumber(pluginCount))
		tbl.RawSetString("system_version", lua.LString("1.0.0"))
		L.Push(tbl)
		return 1
	}
}
func (le *LuaExecutor) makeGetKernelInfo() lua.LGFunction {
	return func(L *lua.LState) int {
		kernelVersion := "N/A"
		kernelName := "Unknown"

		switch runtime.GOOS {
		case "linux":
			if cmd := exec.Command("uname", "-r"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			if cmd := exec.Command("uname", "-s"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelName = strings.TrimSpace(string(output))
				}
			}
		case "darwin":
			if cmd := exec.Command("uname", "-r"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			kernelName = "Darwin"
		case "windows":
			if cmd := exec.Command("cmd", "/C", "ver"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			kernelName = "Windows NT"
		case "freebsd":
			if cmd := exec.Command("uname", "-r"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			kernelName = "FreeBSD"
		}

		tbl := L.NewTable()
		tbl.RawSetString("version", lua.LString(kernelVersion))
		tbl.RawSetString("name", lua.LString(kernelName))
		tbl.RawSetString("os", lua.LString(runtime.GOOS))
		L.Push(tbl)
		return 1
	}
}

func (le *LuaExecutor) makeExecCommand() lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		command := tbl.RawGetString("command").String()
		timeoutVal := tbl.RawGetString("timeout")
		timeout := 120 // 默认超时
		if timeoutVal.Type() == lua.LTNumber {
			timeout = int(lua.LVAsNumber(timeoutVal))
		}

		var argsList []string
		argsTbl := tbl.RawGetString("args")
		if argsTbl.Type() == lua.LTTable {
			argsTbl.(*lua.LTable).ForEach(func(_, value lua.LValue) {
				argsList = append(argsList, value.String())
			})
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		var cmd *exec.Cmd
		if len(argsList) > 0 {
			cmd = exec.CommandContext(ctx, command, argsList...)
		} else {
			cmd = exec.CommandContext(ctx, command)
		}

		// 分别捕获 stdout 和 stderr
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		resultTbl := L.NewTable()
		resultTbl.RawSetString("success", lua.LBool(err == nil))
		resultTbl.RawSetString("output", lua.LString(stdout.String()))       // 使用 stdout 作为 output
		resultTbl.RawSetString("error_output", lua.LString(stderr.String())) // 单独提供 stderr

		if err != nil {
			resultTbl.RawSetString("error", lua.LString(err.Error()))
			resultTbl.RawSetString("exit_code", lua.LNumber(-1))
			if exitErr, ok := err.(*exec.ExitError); ok {
				resultTbl.RawSetString("exit_code", lua.LNumber(exitErr.ExitCode()))
			}
		} else {
			resultTbl.RawSetString("exit_code", lua.LNumber(0))
		}
		L.Push(resultTbl)
		return 1
	}
}

// 上下文创建函数
func (le *LuaExecutor) createCommandContext(L *lua.LState, ctx *command.CommandContext) *lua.LTable {
	tbl := L.NewTable()
	tbl.RawSetString("command", lua.LString(ctx.Command))

	argsTbl := L.NewTable()
	for i, arg := range ctx.Args {
		argsTbl.RawSetInt(i+1, lua.LString(arg))
	}
	tbl.RawSetString("args", argsTbl)
	tbl.RawSetString("message", le.createEventContext(L, ctx.Message))
	tbl.RawSetString("respond", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		if ctx.Respond != nil {
			err := ctx.Respond(msg)
			if err != nil {
				L.RaiseError("respond failed: %s", err.Error())
			}
		}
		return 0
	}))
	return tbl
}

func (le *LuaExecutor) createEventContext(L *lua.LState, event interface{}) lua.LValue {
	switch e := event.(type) {
	case *core.MessageEvent:
		tbl := L.NewTable()
		tbl.RawSetString("type", lua.LString("message"))
		tbl.RawSetString("text", lua.LString(e.Text))
		tbl.RawSetString("user_id", lua.LNumber(e.UserID))
		tbl.RawSetString("chat_id", lua.LNumber(e.ChatID))
		return tbl
	default:
		tbl := L.NewTable()
		tbl.RawSetString("type", lua.LString("unknown"))
		return tbl
	}
}

func (le *LuaExecutor) createHookContext(L *lua.LState, ctx *core.HookContext) *lua.LTable {
	tbl := L.NewTable()
	tbl.RawSetString("type", lua.LString(string(ctx.Type)))
	dataTbl := L.NewTable()
	for k, v := range ctx.Data {
		dataTbl.RawSetString(k, le.convertGoValue(L, v))
	}
	tbl.RawSetString("data", dataTbl)
	return tbl
}

// ===================== JSON 转换辅助函数 =====================
func (le *LuaExecutor) luaValueToGo(value lua.LValue) interface{} {
	switch value.Type() {
	case lua.LTNil:
		return nil
	case lua.LTBool:
		return lua.LVAsBool(value)
	case lua.LTNumber:
		return float64(lua.LVAsNumber(value))
	case lua.LTString:
		return lua.LVAsString(value)
	case lua.LTTable:
		tbl := value.(*lua.LTable)
		// 检查是数组还是 map
		isArr := true
		i := 1
		tbl.ForEach(func(k, _ lua.LValue) {
			if k.Type() != lua.LTNumber || lua.LVAsNumber(k) != lua.LNumber(i) {
				isArr = false
			}
			i++
		})
		if isArr {
			arr := make([]interface{}, 0, tbl.Len())
			tbl.ForEach(func(_, v lua.LValue) {
				arr = append(arr, le.luaValueToGo(v))
			})
			return arr
		} else {
			m := make(map[string]interface{})
			tbl.ForEach(func(k, v lua.LValue) {
				m[k.String()] = le.luaValueToGo(v)
			})
			return m
		}
	default:
		return value.String()
	}
}

func (le *LuaExecutor) goValueToLua(L *lua.LState, value interface{}) lua.LValue {
	switch v := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(v)
	case float64:
		return lua.LNumber(v)
	case int:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []interface{}:
		tbl := L.NewTable()
		for _, item := range v {
			tbl.Append(le.goValueToLua(L, item))
		}
		return tbl
	case map[string]interface{}:
		tbl := L.NewTable()
		for key, item := range v {
			tbl.RawSetString(key, le.goValueToLua(L, item))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}

func (le *LuaExecutor) jsonDecode(L *lua.LState) int {
	str := L.CheckString(1)
	var data interface{}
	err := json.Unmarshal([]byte(str), &data)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(le.goValueToLua(L, data))
	return 1
}

func (le *LuaExecutor) jsonEncode(L *lua.LState) int {
	value := le.luaValueToGo(L.CheckAny(1))
	bytes, err := json.Marshal(value)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LString(string(bytes)))
	return 1
}

// 辅助函数
func (le *LuaExecutor) convertGoValue(L *lua.LState, value interface{}) lua.LValue {
	switch v := value.(type) {
	case string:
		return lua.LString(v)
	case int:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case bool:
		return lua.LBool(v)
	case []string:
		tbl := L.NewTable()
		for i, s := range v {
			tbl.RawSetInt(i+1, lua.LString(s))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}
