// internal/logger/logger.go

package logger

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
)

var (
	defaultLogger *Logger
	once          sync.Once
)

// Logger 封装日志输出
type Logger struct {
	stdLogger *log.Logger
	db        interface {
		InsertSystemLog(level, module, message, details string) error
	}
}

// Init 初始化全局日志实例（应在 main 中调用）
func Init(cfg *config.Config) {
	once.Do(func() {
		l := &Logger{
			stdLogger: log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile),
		}
		defaultLogger = l
	})
}

// SetDBInserter 注入数据库插入器
func SetDBInserter(db interface {
	InsertSystemLog(level, module, message, details string) error
}) {
	if defaultLogger != nil {
		defaultLogger.db = db
	}
}

func (l *Logger) log(level, module, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	l.stdLogger.Printf("[%s] %s: %s", level, module, msg)
	if l.db != nil {
		_ = l.db.InsertSystemLog(level, module, msg, "")
	}
}

// Debug 输出调试日志
func Debug(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log("DEBUG", "general", format, v...)
	}
}

// Info 输出信息日志
func Info(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log("INFO", "general", format, v...)
	}
}

// Warn 输出警告日志
func Warn(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log("WARN", "general", format, v...)
	}
}

// Error 输出错误日志
func Error(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log("ERROR", "general", format, v...)
	}
}

// Fatal 输出致命错误并退出
func Fatal(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log("FATAL", "general", format, v...)
	}
	log.Fatalf(format, v...)
}
