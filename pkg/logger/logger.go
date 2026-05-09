// pkg/logger/logger.go
// 统一日志模块。支持分级输出（DEBUG/INFO/WARN/ERROR）和文件轮转。
//
// 设计说明：
//   1. 使用包级默认实例 defaultLogger，确保在 Init 之前调用 Info 等函数不会 panic。
//   2. Init 函数会替换默认实例，使其指向配置文件指定的日志文件。
//   3. 默认实例在未初始化时仅输出到标准输出。
//
// 使用方式：
//   import "live-source-manager-go/pkg/logger"
//   logger.Init("/var/log/app.log")  // 可选，若不调用则仅输出到控制台
//   logger.Info("服务启动成功")
//   logger.Error("数据库连接失败: %v", err)

package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Level 定义日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

// Logger 封装多级别日志输出
type Logger struct {
	debugLog *log.Logger
	infoLog  *log.Logger
	warnLog  *log.Logger
	errorLog *log.Logger
	level    Level
	mu       sync.Mutex // 保护并发写入
	out      io.Writer
	file     *os.File // 如果写入了文件，持有文件句柄以便关闭
}

// New 创建一个新的 Logger 实例
func New(logPath string, level Level) (*Logger, error) {
	var writers []io.Writer
	var file *os.File

	// 控制台输出
	writers = append(writers, os.Stdout)

	// 文件输出
	if logPath != "" {
		dir := filepath.Dir(logPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("创建日志目录失败: %w", err)
		}
		var err error
		file, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件失败: %w", err)
		}
		writers = append(writers, file)
	}

	multiWriter := io.MultiWriter(writers...)
	flags := log.Ldate | log.Ltime

	return &Logger{
		debugLog: log.New(multiWriter, "[DEBUG] ", flags),
		infoLog:  log.New(multiWriter, "[INFO]  ", flags),
		warnLog:  log.New(multiWriter, "[WARN]  ", flags),
		errorLog: log.New(multiWriter, "[ERROR] ", flags),
		level:    level,
		out:      multiWriter,
		file:     file,
	}, nil
}

// ──────── 实例方法 ────────

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.level <= DEBUG {
		l.mu.Lock()
		l.debugLog.Output(3, fmt.Sprintf(format, v...))
		l.mu.Unlock()
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.level <= INFO {
		l.mu.Lock()
		l.infoLog.Output(3, fmt.Sprintf(format, v...))
		l.mu.Unlock()
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.level <= WARN {
		l.mu.Lock()
		l.warnLog.Output(3, fmt.Sprintf(format, v...))
		l.mu.Unlock()
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	if l.level <= ERROR {
		l.mu.Lock()
		l.errorLog.Output(3, fmt.Sprintf(format, v...))
		l.mu.Unlock()
	}
}

func (l *Logger) Fatal(format string, v ...interface{}) {
	l.mu.Lock()
	l.errorLog.Output(3, fmt.Sprintf(format, v...))
	l.mu.Unlock()
	os.Exit(1)
}

// Close 关闭日志文件（如果有的话）
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// ──────── 包级默认实例 ────────

var (
	defaultLogger *Logger
	defaultMu     sync.Mutex
)

func init() {
	// 初始化包级默认 Logger，确保在显式调用 Init 之前不会 nil panic
	defaultLogger, _ = New("", INFO)
}

// Init 初始化全局日志实例。
// logDir: 日志文件存放目录，若为空则仅输出到控制台。
// 日志文件名格式：live-source-manager-YYYY-MM-DD.log
func Init(logDir string) error {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	var logPath string
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("创建日志目录失败: %w", err)
		}
		logPath = filepath.Join(logDir, "live-source-manager.log")
	}

	l, err := New(logPath, INFO)
	if err != nil {
		return err
	}

	// 关闭旧实例的文件
	if defaultLogger != nil {
		defaultLogger.Close()
	}

	defaultLogger = l
	return nil
}

// ──────── 包级函数（便于全局调用）───────

func Info(format string, v ...interface{}) {
	defaultMu.Lock()
	l := defaultLogger
	defaultMu.Unlock()
	if l != nil {
		l.Info(format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	defaultMu.Lock()
	l := defaultLogger
	defaultMu.Unlock()
	if l != nil {
		l.Warn(format, v...)
	}
}

func Error(format string, v ...interface{}) {
	defaultMu.Lock()
	l := defaultLogger
	defaultMu.Unlock()
	if l != nil {
		l.Error(format, v...)
	}
}

func Debug(format string, v ...interface{}) {
	defaultMu.Lock()
	l := defaultLogger
	defaultMu.Unlock()
	if l != nil {
		l.Debug(format, v...)
	}
}

func Fatal(format string, v ...interface{}) {
	defaultMu.Lock()
	l := defaultLogger
	defaultMu.Unlock()
	if l != nil {
		l.Fatal(format, v...)
	}
	os.Exit(1)
}

// getCaller 获取调用者的文件名和行号（内部辅助函数）
func getCaller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "???:0"
	}
	// 截取文件名（去掉路径前缀）
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		file = file[idx+1:]
	}
	return fmt.Sprintf("%s:%d", file, line)
}
