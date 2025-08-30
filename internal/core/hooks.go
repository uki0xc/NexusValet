package core

import (
	"context"
	"nexusvalet/pkg/logger"
	"sort"
	"sync"
)

// HookType 代表不同类型的钩子
type HookType string

const (
	BeforeStart   HookType = "before_start"
	AfterStart    HookType = "after_start"
	BeforeStop    HookType = "before_stop"
	AfterStop     HookType = "after_stop"
	BeforeCommand HookType = "before_command"
	AfterCommand  HookType = "after_command"
	OnError       HookType = "on_error"
)

// HookContext 包含传递给钩子处理程序的数据
type HookContext struct {
	Type    HookType
	Data    map[string]interface{}
	Context context.Context
}

// HookHandler 是处理钩子的函数
type HookHandler func(*HookContext) error

// Hook 代表一个带优先级的已注册钩子
type Hook struct {
	Type     HookType
	Handler  HookHandler
	Priority int
	Name     string
}

// HookManager 管理系统中的所有钩子
type HookManager struct {
	hooks map[HookType][]*Hook
	mutex sync.RWMutex
}

// NewHookManager 创建一个新的钩子管理器
func NewHookManager() *HookManager {
	return &HookManager{
		hooks: make(map[HookType][]*Hook),
	}
}

// RegisterHook 注册一个新钩子
func (hm *HookManager) RegisterHook(hookType HookType, name string, handler HookHandler, priority int) {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()

	hook := &Hook{
		Type:     hookType,
		Handler:  handler,
		Priority: priority,
		Name:     name,
	}

	hm.hooks[hookType] = append(hm.hooks[hookType], hook)

	// 按优先级排序钩子（高优先级在前）
	sort.Slice(hm.hooks[hookType], func(i, j int) bool {
		return hm.hooks[hookType][i].Priority > hm.hooks[hookType][j].Priority
	})

	logger.Debugf("Registered hook %s:%s with priority %d", hookType, name, priority)
}

// UnregisterHook 根据名称和类型移除钩子
func (hm *HookManager) UnregisterHook(hookType HookType, name string) {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()

	hooks := hm.hooks[hookType]
	for i, hook := range hooks {
		if hook.Name == name {
			hm.hooks[hookType] = append(hooks[:i], hooks[i+1:]...)
			logger.Debugf("Unregistered hook %s:%s", hookType, name)
			return
		}
	}
}

// ExecuteHooks 执行给定类型的所有钩子
func (hm *HookManager) ExecuteHooks(hookType HookType, data map[string]interface{}) error {
	return hm.ExecuteHooksWithContext(context.Background(), hookType, data)
}

// ExecuteHooksWithContext 使用上下文执行给定类型的所有钩子
func (hm *HookManager) ExecuteHooksWithContext(ctx context.Context, hookType HookType, data map[string]interface{}) error {
	hm.mutex.RLock()
	hooks := make([]*Hook, len(hm.hooks[hookType]))
	copy(hooks, hm.hooks[hookType])
	hm.mutex.RUnlock()

	hookCtx := &HookContext{
		Type:    hookType,
		Data:    data,
		Context: ctx,
	}

	logger.Debugf("Executing %d hooks for type %s", len(hooks), hookType)

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := hook.Handler(hookCtx); err != nil {
				logger.Errorf("Hook %s:%s failed: %v", hookType, hook.Name, err)

				// 如果这不是已经是一个错误钩子，则执行错误钩子
				if hookType != OnError {
					errorData := map[string]interface{}{
						"original_hook_type": hookType,
						"hook_name":          hook.Name,
						"error":              err,
					}
					hm.ExecuteHooksWithContext(ctx, OnError, errorData)
				}
				return err
			}
		}
	}

	return nil
}

// GetHooks 返回给定类型的所有钩子
func (hm *HookManager) GetHooks(hookType HookType) []*Hook {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()

	hooks := make([]*Hook, len(hm.hooks[hookType]))
	copy(hooks, hm.hooks[hookType])
	return hooks
}

// GetAllHooks 返回所有已注册的钩子
func (hm *HookManager) GetAllHooks() map[HookType][]*Hook {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()

	result := make(map[HookType][]*Hook)
	for hookType, hooks := range hm.hooks {
		result[hookType] = make([]*Hook, len(hooks))
		copy(result[hookType], hooks)
	}
	return result
}

// Clear removes all hooks
func (hm *HookManager) Clear() {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()

	hm.hooks = make(map[HookType][]*Hook)
	logger.Debugf("Cleared all hooks")
}
