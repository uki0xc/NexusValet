package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

// LogLevel 代表日志级别
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

var levelStrings = []string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

// Logger 是一个简单的日志记录器实现
type Logger struct {
	level  LogLevel
	logger *log.Logger
}

var defaultLogger *Logger

func init() {
	defaultLogger = &Logger{
		level:  INFO,
		logger: log.New(os.Stdout, "", 0),
	}
}

// NewLogger 创建一个新的日志记录器实例
func NewLogger(level LogLevel) *Logger {
	return &Logger{
		level:  level,
		logger: log.New(os.Stdout, "", 0),
	}
}

// SetLevel 设置日志级别
func SetLevel(level LogLevel) {
	defaultLogger.level = level
}

// ParseLevel 解析字符串日志级别并返回相应的 LogLevel
func ParseLevel(levelStr string) LogLevel {
	switch levelStr {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN":
		return WARN
	case "ERROR":
		return ERROR
	case "FATAL":
		return FATAL
	default:
		return INFO // Default to INFO level
	}
}

// logf formats and logs a message at the specified level
func (l *Logger) logf(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	prefix := fmt.Sprintf("[%s] [%s] ", timestamp, levelStrings[level])
	message := fmt.Sprintf(format, args...)
	l.logger.Print(prefix + message)
}

// Debugf logs a debug message
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.logf(DEBUG, format, args...)
}

// Infof logs an info message
func (l *Logger) Infof(format string, args ...interface{}) {
	l.logf(INFO, format, args...)
}

// Warnf logs a warning message
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.logf(WARN, format, args...)
}

// Errorf logs an error message
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.logf(ERROR, format, args...)
}

// Fatalf logs a fatal message and exits
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.logf(FATAL, format, args...)
	os.Exit(1)
}

// Global logger functions
func Debugf(format string, args ...interface{}) {
	defaultLogger.Debugf(format, args...)
}

func Infof(format string, args ...interface{}) {
	defaultLogger.Infof(format, args...)
}

func Warnf(format string, args ...interface{}) {
	defaultLogger.Warnf(format, args...)
}

func Errorf(format string, args ...interface{}) {
	defaultLogger.Errorf(format, args...)
}

func Fatalf(format string, args ...interface{}) {
	defaultLogger.Fatalf(format, args...)
}
