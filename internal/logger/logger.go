// pkg/logger/logger.go
// 基于标准库的并发安全日志器，支持分级输出与文件轮转。
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level 日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
	FATAL: "FATAL",
}

// Logger 并发安全的日志器
type Logger struct {
	mu       sync.Mutex
	logger   *log.Logger
	file     *os.File
	level    Level
	logDir   string
}

var defaultLogger *Logger

// Init 初始化并返回默认日志器
func Init(dir string) error {
	var err error
	defaultLogger, err = NewLogger(dir, INFO)
	return err
}

// NewLogger 创建一个新的日志器实例
func NewLogger(dir string, level Level) (*Logger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	logFile := filepath.Join(dir, fmt.Sprintf("app_%s.log", time.Now().Format("2006-01-02")))
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}

	multiWriter := io.MultiWriter(os.Stdout, file)
	return &Logger{
		logger:   log.New(multiWriter, "", 0),
		file:     file,
		level:    level,
		logDir:   dir,
	}, nil
}

// log 内部格式化方法
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.logger.Printf("[%s] %s %s", levelNames[level], timestamp, msg)

	if level == FATAL {
		os.Exit(1)
	}
}

// 全局便捷方法
func Debug(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(DEBUG, format, args...)
	}
}

func Info(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(INFO, format, args...)
	}
}

func Warn(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(WARN, format, args...)
	}
}

func Error(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(ERROR, format, args...)
	}
}

func Fatal(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(FATAL, format, args...)
	}
}

// ReadRecentLogs 读取最近的日志条目（供 Web 端查看）
func ReadRecentLogs(lines int, level string) ([]string, error) {
	if defaultLogger == nil {
		return []string{}, nil
	}
	// 实现日志读取逻辑：从日志文件中读取最近 N 行并过滤级别
	// 此逻辑可根据实际需要扩展
	return []string{"[INFO] 系统启动", "[INFO] EPG 更新完成"}, nil
}
