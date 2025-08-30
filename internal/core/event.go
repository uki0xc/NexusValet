package core

import (
	"context"
	"nexusvalet/pkg/logger"
	"regexp"
	"strings"
	"sync"

	"github.com/gotd/td/tg"
)

// MessageEvent 代表 Telegram 消息事件
type MessageEvent struct {
	Update  *tg.UpdateNewMessage
	Message *tg.Message
	Text    string
	UserID  int64
	ChatID  int64
}

// CommandEvent 代表命令执行事件
type CommandEvent struct {
	Command string
	Args    []string
	Message *MessageEvent
}

// EventHandler 是处理事件的函数
type EventHandler func(context.Context, interface{}) error

// ListenerType 代表监听器的类型
type ListenerType string

const (
	MessageListener ListenerType = "message"
	RawListener     ListenerType = "raw"
	CommandListener ListenerType = "command"
)

// ListenerFilter 定义监听器的过滤选项
type ListenerFilter struct {
	GroupsOnly   bool // Only handle group messages
	PrivatesOnly bool // Only handle private messages
	Outgoing     bool // Handle outgoing messages (from self)
	Incoming     bool // Handle incoming messages (from others)
	SudoOnly     bool // Only handle messages from sudo users
}

// Listener 代表一个事件监听器
type Listener struct {
	Type     ListenerType
	Handler  EventHandler
	Pattern  *regexp.Regexp // For regex matching
	Prefix   string         // For prefix matching
	Command  string         // For command matching
	Priority int
	Name     string
	Filter   *ListenerFilter // Optional filter for message handling
}

// EventDispatcher 管理事件分发
type EventDispatcher struct {
	listeners map[ListenerType][]*Listener
	mutex     sync.RWMutex
}

// NewEventDispatcher 创建一个新的事件分发器
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		listeners: make(map[ListenerType][]*Listener),
	}
}

// RegisterMessageListener 注册具有模式匹配的消息监听器
func (ed *EventDispatcher) RegisterMessageListener(name string, pattern string, handler EventHandler, priority int) error {
	var regex *regexp.Regexp
	var err error

	if pattern != "" {
		regex, err = regexp.Compile(pattern)
		if err != nil {
			return err
		}
	}

	listener := &Listener{
		Type:     MessageListener,
		Handler:  handler,
		Pattern:  regex,
		Priority: priority,
		Name:     name,
	}

	ed.addListener(listener)
	logger.Debugf("Registered message listener: %s with pattern: %s", name, pattern)
	return nil
}

// RegisterPrefixListener 注册具有前缀匹配的消息监听器
func (ed *EventDispatcher) RegisterPrefixListener(name string, prefix string, handler EventHandler, priority int) {
	listener := &Listener{
		Type:     MessageListener,
		Handler:  handler,
		Prefix:   prefix,
		Priority: priority,
		Name:     name,
	}

	ed.addListener(listener)
	logger.Debugf("Registered prefix listener: %s with prefix: %s", name, prefix)
}

// RegisterCommandListener 注册命令监听器
func (ed *EventDispatcher) RegisterCommandListener(name string, command string, handler EventHandler, priority int) {
	listener := &Listener{
		Type:     CommandListener,
		Handler:  handler,
		Command:  command,
		Priority: priority,
		Name:     name,
	}

	ed.addListener(listener)
	logger.Debugf("Registered command listener: %s for command: %s", name, command)
}

// RegisterRawListener 注册原始事件监听器
func (ed *EventDispatcher) RegisterRawListener(name string, handler EventHandler, priority int) {
	listener := &Listener{
		Type:     RawListener,
		Handler:  handler,
		Priority: priority,
		Name:     name,
	}

	ed.addListener(listener)
	logger.Debugf("Registered raw listener: %s", name)
}

// RegisterMessageListenerWithFilter 注册具有模式匹配和过滤器的消息监听器
func (ed *EventDispatcher) RegisterMessageListenerWithFilter(name string, pattern string, handler EventHandler, priority int, filter ListenerFilter) error {
	var regex *regexp.Regexp
	var err error

	if pattern != "" {
		regex, err = regexp.Compile(pattern)
		if err != nil {
			return err
		}
	}

	listener := &Listener{
		Type:     MessageListener,
		Handler:  handler,
		Pattern:  regex,
		Priority: priority,
		Name:     name,
		Filter:   &filter,
	}

	ed.addListener(listener)
	logger.Debugf("Registered message listener with filter: %s with pattern: %s", name, pattern)
	return nil
}

// RegisterPrefixListenerWithFilter 注册具有前缀匹配和过滤器的消息监听器
func (ed *EventDispatcher) RegisterPrefixListenerWithFilter(name string, prefix string, handler EventHandler, priority int, filter ListenerFilter) {
	listener := &Listener{
		Type:     MessageListener,
		Handler:  handler,
		Prefix:   prefix,
		Priority: priority,
		Name:     name,
		Filter:   &filter,
	}

	ed.addListener(listener)
	logger.Debugf("Registered prefix listener with filter: %s with prefix: %s", name, prefix)
}

// RegisterRawListenerWithFilter 注册具有过滤器的原始事件监听器
func (ed *EventDispatcher) RegisterRawListenerWithFilter(name string, handler EventHandler, priority int, filter ListenerFilter) {
	listener := &Listener{
		Type:     RawListener,
		Handler:  handler,
		Priority: priority,
		Name:     name,
		Filter:   &filter,
	}

	ed.addListener(listener)
	logger.Debugf("Registered raw listener with filter: %s", name)
}

// addListener 添加监听器并按优先级排序
func (ed *EventDispatcher) addListener(listener *Listener) {
	ed.mutex.Lock()
	defer ed.mutex.Unlock()

	ed.listeners[listener.Type] = append(ed.listeners[listener.Type], listener)

	// 按优先级排序（高优先级在前）
	listeners := ed.listeners[listener.Type]
	for i := len(listeners) - 1; i > 0; i-- {
		if listeners[i].Priority > listeners[i-1].Priority {
			listeners[i], listeners[i-1] = listeners[i-1], listeners[i]
		} else {
			break
		}
	}
}

// UnregisterListener 根据名称和类型移除监听器
func (ed *EventDispatcher) UnregisterListener(listenerType ListenerType, name string) {
	ed.mutex.Lock()
	defer ed.mutex.Unlock()

	listeners := ed.listeners[listenerType]
	for i, listener := range listeners {
		if listener.Name == name {
			ed.listeners[listenerType] = append(listeners[:i], listeners[i+1:]...)
			logger.Debugf("Unregistered listener: %s:%s", listenerType, name)
			return
		}
	}
}

// DispatchMessage 将消息事件分发给相关监听器
func (ed *EventDispatcher) DispatchMessage(ctx context.Context, event *MessageEvent) error {
	// First dispatch to raw listeners
	if err := ed.dispatchToListeners(ctx, RawListener, event); err != nil {
		return err
	}

	// Then dispatch to message listeners
	return ed.dispatchToListeners(ctx, MessageListener, event)
}

// DispatchCommand dispatches a command event to command listeners
func (ed *EventDispatcher) DispatchCommand(ctx context.Context, event *CommandEvent) error {
	return ed.dispatchToListeners(ctx, CommandListener, event)
}

// DispatchRaw dispatches any event to raw listeners
func (ed *EventDispatcher) DispatchRaw(ctx context.Context, event interface{}) error {
	return ed.dispatchToListeners(ctx, RawListener, event)
}

// dispatchToListeners dispatches an event to all matching listeners of a type
func (ed *EventDispatcher) dispatchToListeners(ctx context.Context, listenerType ListenerType, event interface{}) error {
	ed.mutex.RLock()
	listeners := make([]*Listener, len(ed.listeners[listenerType]))
	copy(listeners, ed.listeners[listenerType])
	ed.mutex.RUnlock()

	for _, listener := range listeners {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if ed.shouldHandleEvent(listener, event) {
				if err := listener.Handler(ctx, event); err != nil {
					logger.Errorf("Listener %s failed: %v", listener.Name, err)
					// Continue with other listeners
				}
			}
		}
	}

	return nil
}

// shouldHandleEvent determines if a listener should handle an event
func (ed *EventDispatcher) shouldHandleEvent(listener *Listener, event interface{}) bool {
	switch listener.Type {
	case RawListener:
		// Apply filter if exists
		if listener.Filter != nil {
			return ed.passesFilter(listener.Filter, event)
		}
		return true
	case MessageListener:
		if msgEvent, ok := event.(*MessageEvent); ok {
			// First check filter if exists
			if listener.Filter != nil && !ed.passesFilter(listener.Filter, event) {
				return false
			}

			// Check pattern matching
			if listener.Pattern != nil {
				return listener.Pattern.MatchString(msgEvent.Text)
			}
			// Check prefix matching
			if listener.Prefix != "" {
				return strings.HasPrefix(msgEvent.Text, listener.Prefix)
			}
			// No specific filter, handle all messages
			return true
		}
	case CommandListener:
		if cmdEvent, ok := event.(*CommandEvent); ok {
			// First check filter if exists (apply to underlying message)
			if listener.Filter != nil && !ed.passesFilter(listener.Filter, cmdEvent.Message) {
				return false
			}
			return listener.Command == cmdEvent.Command
		}
	}
	return false
}

// passesFilter checks if an event passes the listener filter
func (ed *EventDispatcher) passesFilter(filter *ListenerFilter, event interface{}) bool {
	msgEvent, ok := event.(*MessageEvent)
	if !ok {
		// For non-message events, only apply Outgoing/Incoming filter
		return true
	}

	// Check group/private filter
	isGroup := msgEvent.ChatID < 0 // Negative chat IDs are groups/channels
	if filter.GroupsOnly && !isGroup {
		return false
	}
	if filter.PrivatesOnly && isGroup {
		return false
	}

	// Check outgoing/incoming filter
	// We need to determine if this is our message or someone else's
	// For now, we'll use a simple heuristic based on the message source
	isOutgoing := msgEvent.Message != nil && msgEvent.Message.Out

	if filter.Outgoing && !isOutgoing {
		return false
	}
	if filter.Incoming && isOutgoing {
		return false
	}

	// TODO: Implement SudoOnly filter when sudo user system is available
	// if filter.SudoOnly && !isSudoUser(msgEvent.UserID) {
	//     return false
	// }

	return true
}

// GetListeners returns all listeners of a given type
func (ed *EventDispatcher) GetListeners(listenerType ListenerType) []*Listener {
	ed.mutex.RLock()
	defer ed.mutex.RUnlock()

	listeners := make([]*Listener, len(ed.listeners[listenerType]))
	copy(listeners, ed.listeners[listenerType])
	return listeners
}

// GetAllListeners returns all registered listeners
func (ed *EventDispatcher) GetAllListeners() map[ListenerType][]*Listener {
	ed.mutex.RLock()
	defer ed.mutex.RUnlock()

	result := make(map[ListenerType][]*Listener)
	for listenerType, listeners := range ed.listeners {
		result[listenerType] = make([]*Listener, len(listeners))
		copy(result[listenerType], listeners)
	}
	return result
}

// Clear removes all listeners
func (ed *EventDispatcher) Clear() {
	ed.mutex.Lock()
	defer ed.mutex.Unlock()

	ed.listeners = make(map[ListenerType][]*Listener)
	logger.Debugf("Cleared all listeners")
}
