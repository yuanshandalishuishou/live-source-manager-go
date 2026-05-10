// internal/logger/logger.go
// 基于标准库的并发安全日志器，支持分级输出、文件轮转及 Web 端实时查看
package logger

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	mu      sync.Mutex
	logger  *log.Logger
	file    *os.File
	level   Level
	logDir  string
	logFile string
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
		logger:  log.New(multiWriter, "", 0),
		file:    file,
		level:   level,
		logDir:  dir,
		logFile: logFile,
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

	file, err := os.Open(defaultLogger.logFile)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	defer file.Close()

	// 从文件末尾读取最近的行
	var allLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}

	if len(allLines) == 0 {
		return []string{}, nil
	}

	// 取最近 N 行
	start := 0
	if len(allLines) > lines {
		start = len(allLines) - lines
	}

	recent := allLines[start:]

	// 如果指定了级别，进行过滤
	if level != "" && level != "ALL" {
		var filtered []string
		prefix := "[" + strings.ToUpper(level) + "]"
		for _, line := range recent {
			if strings.Contains(line, prefix) {
				filtered = append(filtered, line)
			}
		}
		return filtered, nil
	}

	return recent, nil
}
