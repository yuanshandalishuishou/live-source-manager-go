// Package logger 提供统一的日志记录功能，支持日志级别、文件轮转和控制台输出。
package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// Level 定义日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

// Logger 封装了多个级别的 log.Logger 实例，支持分级输出。
type Logger struct {
	debugLog *log.Logger
	infoLog  *log.Logger
	warnLog  *log.Logger
	errorLog *log.Logger
	level    Level
	out      io.Writer
}

// New 创建一个新的 Logger 实例。
// logPath: 日志文件路径，如果为空则仅输出到标准输出。
// level: 日志级别，低于此级别的日志将不会输出。
func New(logPath string, level Level) (*Logger, error) {
	var writers []io.Writer

	// 控制台输出
	writers = append(writers, os.Stdout)

	// 文件输出
	if logPath != "" {
		dir := filepath.Dir(logPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, err
		}
		writers = append(writers, file)
	}

	multiWriter := io.MultiWriter(writers...)
	flags := log.Ldate | log.Ltime | log.Lshortfile

	return &Logger{
		debugLog: log.New(multiWriter, "[DEBUG] ", flags),
		infoLog:  log.New(multiWriter, "[INFO]  ", flags),
		warnLog:  log.New(multiWriter, "[WARN]  ", flags),
		errorLog: log.New(multiWriter, "[ERROR] ", flags),
		level:    level,
		out:      multiWriter,
	}, nil
}

// Debug 输出调试级别日志
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.level <= DEBUG {
		l.debugLog.Printf(format, v...)
	}
}

// Info 输出信息级别日志
func (l *Logger) Info(format string, v ...interface{}) {
	if l.level <= INFO {
		l.infoLog.Printf(format, v...)
	}
}

// Warn 输出警告级别日志
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.level <= WARN {
		l.warnLog.Printf(format, v...)
	}
}

// Error 输出错误级别日志
func (l *Logger) Error(format string, v ...interface{}) {
	if l.level <= ERROR {
		l.errorLog.Printf(format, v...)
	}
}

// Fatal 输出错误级别日志并退出程序
func (l *Logger) Fatal(format string, v ...interface{}) {
	l.errorLog.Printf(format, v...)
	os.Exit(1)
}
