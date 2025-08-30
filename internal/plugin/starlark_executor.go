package plugin

import (
	"context"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/core"
	"nexusvalet/pkg/logger"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var startTime = time.Now() // 启动时间

// StarlarkExecutor executes Starlark plugin scripts
type StarlarkExecutor struct {
	manager     *Manager
	parser      *command.Parser
	dispatcher  *core.EventDispatcher
	hookManager *core.HookManager
}

// NewStarlarkExecutor creates a new Starlark executor
func NewStarlarkExecutor(manager *Manager, parser *command.Parser, dispatcher *core.EventDispatcher, hookManager *core.HookManager) *StarlarkExecutor {
	return &StarlarkExecutor{
		manager:     manager,
		parser:      parser,
		dispatcher:  dispatcher,
		hookManager: hookManager,
	}
}

// ExecutePlugin executes a Starlark plugin script
func (se *StarlarkExecutor) ExecutePlugin(pluginInfo *PluginInfo) error {
	// Read plugin script
	script, err := se.manager.readFile(pluginInfo.Dir, "plugin.star")
	if err != nil {
		return fmt.Errorf("failed to read plugin script: %w", err)
	}

	// Create predeclared environment for the plugin
	predeclared := se.createPredeclaredEnvironment(pluginInfo)

	// Create a new thread for this plugin execution
	thread := &starlark.Thread{
		Name: fmt.Sprintf("plugin_%s", pluginInfo.Name),
		Print: func(thread *starlark.Thread, msg string) {
			logger.Debugf("[Plugin:%s] %s", pluginInfo.Name, msg)
		},
	}

	// Execute the plugin script
	globals, err := starlark.ExecFile(thread, "plugin.star", script, predeclared)
	if err != nil {
		return fmt.Errorf("failed to execute plugin script: %w", err)
	}

	// Store globals for later use
	pluginInfo.Globals = globals

	// Call plugin initialization function if it exists
	if initFunc, ok := globals["init"]; ok {
		if callable, ok := initFunc.(starlark.Callable); ok {
			_, err := starlark.Call(thread, callable, nil, nil)
			if err != nil {
				return fmt.Errorf("plugin init function failed: %w", err)
			}
		}
	}

	logger.Debugf("Plugin %s executed successfully", pluginInfo.Name)
	return nil
}

// createPredeclaredEnvironment creates the predeclared environment for Starlark plugins
func (se *StarlarkExecutor) createPredeclaredEnvironment(pluginInfo *PluginInfo) starlark.StringDict {
	// Create bot API module
	botAPI := starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"register_command":  starlark.NewBuiltin("register_command", se.makeRegisterCommand(pluginInfo)),
		"register_listener": starlark.NewBuiltin("register_listener", se.makeRegisterListener(pluginInfo)),
		"register_hook":     starlark.NewBuiltin("register_hook", se.makeRegisterHook(pluginInfo)),
		"log_info":          starlark.NewBuiltin("log_info", se.makeLogInfo()),
		"log_debug":         starlark.NewBuiltin("log_debug", se.makeLogDebug()),
		"log_warn":          starlark.NewBuiltin("log_warn", se.makeLogWarn()),
		"log_error":         starlark.NewBuiltin("log_error", se.makeLogError()),
		"get_version":       starlark.NewBuiltin("get_version", se.makeGetVersion()),
		"get_go_version":    starlark.NewBuiltin("get_go_version", se.makeGetGoVersion()),
		"get_time":          starlark.NewBuiltin("get_time", se.makeGetTime()),
		"get_system_info":   starlark.NewBuiltin("get_system_info", se.makeGetSystemInfo()),
		"get_memory_info":   starlark.NewBuiltin("get_memory_info", se.makeGetMemoryInfo()),
		"get_runtime_info":  starlark.NewBuiltin("get_runtime_info", se.makeGetRuntimeInfo()),
		"get_plugin_info":   starlark.NewBuiltin("get_plugin_info", se.makeGetPluginInfo()),
		"get_kernel_info":   starlark.NewBuiltin("get_kernel_info", se.makeGetKernelInfo()),
		"exec_command":      starlark.NewBuiltin("exec_command", se.makeExecCommand()),
	})

	return starlark.StringDict{
		"bot": botAPI,
	}
}

// makeRegisterCommand creates the register_command builtin function
func (se *StarlarkExecutor) makeRegisterCommand(pluginInfo *PluginInfo) func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var commandName, description string
		var handler starlark.Callable

		if err := starlark.UnpackArgs("register_command", args, kwargs, "name", &commandName, "handler", &handler, "description?", &description); err != nil {
			return nil, err
		}

		// Create command handler wrapper
		cmdHandler := func(ctx *command.CommandContext) error {
			// Create context for Starlark
			starCtx := se.createCommandContext(ctx)

			// Call the Starlark handler
			_, err := starlark.Call(thread, handler, starlark.Tuple{starCtx}, nil)
			return err
		}

		// Register the command
		se.parser.RegisterCommand(commandName, description, pluginInfo.Name, cmdHandler)

		return starlark.None, nil
	}
}

// makeRegisterListener creates the register_listener builtin function
func (se *StarlarkExecutor) makeRegisterListener(pluginInfo *PluginInfo) func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var listenerType, pattern string
		var handler starlark.Callable
		var priority int = 50
		var groupsOnly, privatesOnly, outgoing, incoming, sudoOnly bool

		if err := starlark.UnpackArgs("register_listener", args, kwargs,
			"type", &listenerType,
			"handler", &handler,
			"pattern?", &pattern,
			"priority?", &priority,
			"groups_only?", &groupsOnly,
			"privates_only?", &privatesOnly,
			"outgoing?", &outgoing,
			"incoming?", &incoming,
			"sudo_only?", &sudoOnly); err != nil {
			return nil, err
		}

		// Create event handler wrapper
		eventHandler := func(ctx context.Context, event interface{}) error {
			// Create context for Starlark based on event type
			starEvent := se.createEventContext(event)

			// Call the Starlark handler
			_, err := starlark.Call(thread, handler, starlark.Tuple{starEvent}, nil)
			return err
		}

		// Create filter configuration
		filter := core.ListenerFilter{
			GroupsOnly:   groupsOnly,
			PrivatesOnly: privatesOnly,
			Outgoing:     outgoing,
			Incoming:     incoming,
			SudoOnly:     sudoOnly,
		}

		// If neither outgoing nor incoming is specified, default to outgoing (userbot behavior)
		if !outgoing && !incoming {
			filter.Outgoing = true
		}

		// Register listener based on type
		name := fmt.Sprintf("%s_%s", pluginInfo.Name, listenerType)
		switch listenerType {
		case "message":
			if pattern != "" {
				se.dispatcher.RegisterMessageListenerWithFilter(name, pattern, eventHandler, priority, filter)
			} else {
				se.dispatcher.RegisterPrefixListenerWithFilter(name, "", eventHandler, priority, filter)
			}
		case "raw":
			se.dispatcher.RegisterRawListenerWithFilter(name, eventHandler, priority, filter)
		default:
			return nil, fmt.Errorf("unknown listener type: %s", listenerType)
		}

		return starlark.None, nil
	}
}

// makeRegisterHook creates the register_hook builtin function
func (se *StarlarkExecutor) makeRegisterHook(pluginInfo *PluginInfo) func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var hookType string
		var handler starlark.Callable
		var priority int = 50

		if err := starlark.UnpackArgs("register_hook", args, kwargs, "type", &hookType, "handler", &handler, "priority?", &priority); err != nil {
			return nil, err
		}

		// Create hook handler wrapper
		hookHandler := func(hookCtx *core.HookContext) error {
			// Create context for Starlark
			starCtx := se.createHookContext(hookCtx)

			// Call the Starlark handler
			_, err := starlark.Call(thread, handler, starlark.Tuple{starCtx}, nil)
			return err
		}

		// Register the hook
		name := fmt.Sprintf("%s_%s", pluginInfo.Name, hookType)
		se.hookManager.RegisterHook(core.HookType(hookType), name, hookHandler, priority)

		return starlark.None, nil
	}
}

// Logging functions
func (se *StarlarkExecutor) makeLogInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs("log_info", args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Infof("[Plugin] %s", message)
		return starlark.None, nil
	}
}

func (se *StarlarkExecutor) makeLogDebug() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs("log_debug", args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Debugf("[Plugin] %s", message)
		return starlark.None, nil
	}
}

func (se *StarlarkExecutor) makeLogWarn() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs("log_warn", args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Warnf("[Plugin] %s", message)
		return starlark.None, nil
	}
}

func (se *StarlarkExecutor) makeLogError() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs("log_error", args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Errorf("[Plugin] %s", message)
		return starlark.None, nil
	}
}

// Utility functions
func (se *StarlarkExecutor) makeGetVersion() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return starlark.String("v1.0.0"), nil
	}
}

func (se *StarlarkExecutor) makeGetGoVersion() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return starlark.String(runtime.Version()), nil
	}
}

func (se *StarlarkExecutor) makeGetTime() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return starlark.String(time.Now().Format("2006-01-02 15:04:05")), nil
	}
}

func (se *StarlarkExecutor) makeGetSystemInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"os":        starlark.String(runtime.GOOS),
			"arch":      starlark.String(runtime.GOARCH),
			"cpu_count": starlark.MakeInt(runtime.NumCPU()),
		}), nil
	}
}

func (se *StarlarkExecutor) makeGetMemoryInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"alloc":    starlark.MakeInt64(int64(m.Alloc)),
			"sys":      starlark.MakeInt64(int64(m.Sys)),
			"gc_count": starlark.MakeInt64(int64(m.NumGC)),
		}), nil
	}
}

func (se *StarlarkExecutor) makeGetRuntimeInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		// 获取真实的运行时间
		uptime := time.Since(startTime)

		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"goroutines": starlark.MakeInt(runtime.NumGoroutine()),
			"uptime":     starlark.MakeInt64(int64(uptime.Seconds())),
		}), nil
	}
}

func (se *StarlarkExecutor) makeGetPluginInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		pluginCount := len(se.manager.GetAllPlugins())

		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"loaded_count":   starlark.MakeInt(pluginCount),
			"system_version": starlark.String("1.0.0"),
		}), nil
	}
}

func (se *StarlarkExecutor) makeGetKernelInfo() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		kernelVersion := "N/A"
		kernelName := "Unknown"

		// Try to get kernel version based on OS
		switch runtime.GOOS {
		case "linux":
			// Try uname -r for kernel version
			if cmd := exec.Command("uname", "-r"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			// Try uname -s for kernel name
			if cmd := exec.Command("uname", "-s"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelName = strings.TrimSpace(string(output))
				}
			}
		case "darwin":
			// macOS - get kernel version
			if cmd := exec.Command("uname", "-r"); cmd != nil {
				if output, err := cmd.Output(); err == nil {
					kernelVersion = strings.TrimSpace(string(output))
				}
			}
			kernelName = "Darwin"
		case "windows":
			// Windows - get version info
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

		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"version": starlark.String(kernelVersion),
			"name":    starlark.String(kernelName),
			"os":      starlark.String(runtime.GOOS),
		}), nil
	}
}

func (se *StarlarkExecutor) makeExecCommand() func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var command string
		var cmdArgs starlark.Value
		var timeout int64 = 30 // default 30 seconds timeout

		if err := starlark.UnpackArgs("exec_command", args, kwargs, "command", &command, "args?", &cmdArgs, "timeout?", &timeout); err != nil {
			return nil, err
		}

		// Convert starlark args to string slice
		var argsList []string
		if cmdArgs != nil {
			if list, ok := cmdArgs.(*starlark.List); ok {
				for i := 0; i < list.Len(); i++ {
					if str, ok := list.Index(i).(starlark.String); ok {
						argsList = append(argsList, string(str))
					}
				}
			}
		}

		// Create command with timeout
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		var cmd *exec.Cmd
		if len(argsList) > 0 {
			cmd = exec.CommandContext(ctx, command, argsList...)
		} else {
			cmd = exec.CommandContext(ctx, command)
		}

		// Execute command
		output, err := cmd.CombinedOutput()

		// Create result structure
		resultDict := starlark.StringDict{
			"success": starlark.Bool(err == nil),
			"output":  starlark.String(string(output)),
		}

		if err != nil {
			resultDict["error"] = starlark.String(err.Error())
			resultDict["exit_code"] = starlark.MakeInt(-1)
		} else if cmd.ProcessState != nil {
			resultDict["exit_code"] = starlark.MakeInt(cmd.ProcessState.ExitCode())
		} else {
			resultDict["exit_code"] = starlark.MakeInt(0)
		}

		result := starlarkstruct.FromStringDict(starlarkstruct.Default, resultDict)
		return result, nil
	}
}

// Context creation functions
func (se *StarlarkExecutor) createCommandContext(ctx *command.CommandContext) starlark.Value {
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"command": starlark.String(ctx.Command),
		"args":    se.convertStringSlice(ctx.Args),
		"respond": starlark.NewBuiltin("respond", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var message string
			if err := starlark.UnpackArgs("respond", args, kwargs, "message", &message); err != nil {
				return nil, err
			}
			if ctx.Respond != nil {
				err := ctx.Respond(message)
				return starlark.None, err
			}
			return starlark.None, nil
		}),
	})
}

func (se *StarlarkExecutor) createEventContext(event interface{}) starlark.Value {
	switch e := event.(type) {
	case *core.MessageEvent:
		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"type":    starlark.String("message"),
			"text":    starlark.String(e.Text),
			"user_id": starlark.MakeInt64(e.UserID),
			"chat_id": starlark.MakeInt64(e.ChatID),
		})
	default:
		return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"type": starlark.String("unknown"),
		})
	}
}

func (se *StarlarkExecutor) createHookContext(ctx *core.HookContext) starlark.Value {
	data := starlark.NewDict(len(ctx.Data))
	for k, v := range ctx.Data {
		data.SetKey(starlark.String(k), se.convertGoValue(v))
	}

	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"type": starlark.String(string(ctx.Type)),
		"data": data,
	})
}

// Helper functions
func (se *StarlarkExecutor) convertStringSlice(slice []string) starlark.Value {
	list := make([]starlark.Value, len(slice))
	for i, s := range slice {
		list[i] = starlark.String(s)
	}
	return starlark.NewList(list)
}

func (se *StarlarkExecutor) convertGoValue(value interface{}) starlark.Value {
	switch v := value.(type) {
	case string:
		return starlark.String(v)
	case int:
		return starlark.MakeInt(v)
	case int64:
		return starlark.MakeInt64(v)
	case bool:
		return starlark.Bool(v)
	case []string:
		return se.convertStringSlice(v)
	default:
		return starlark.String(fmt.Sprintf("%v", v))
	}
}
