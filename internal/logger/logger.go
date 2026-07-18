// Package logger provides a minimal leveled logger used across the application.
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarning
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarning:
		return "WARNING"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Logger is a concurrency-safe leveled logger writing to stdout and an optional file.
type Logger struct {
	mu    sync.Mutex
	level Level
	out   io.Writer
	file  *os.File
}

var defaultLogger = New(LevelInfo, "")

// New creates a Logger. If filePath is non-empty, logs are also appended to that file.
func New(level Level, filePath string) *Logger {
	l := &Logger{level: level, out: os.Stdout}
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			l.file = f
			l.out = io.MultiWriter(os.Stdout, f)
		}
	}
	return l
}

// SetLevel changes the active log level.
func (l *Logger) SetLevel(lv Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = lv
}

// SetOutput redirects log output (mainly for tests).
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

func (l *Logger) log(lv Level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lv < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_ = log.New(l.out, "", log.LstdFlags).Output(2, "["+lv.String()+"] "+msg)
}

// Debug logs at debug level.
func (l *Logger) Debug(format string, args ...any) { l.log(LevelDebug, format, args...) }

// Info logs at info level.
func (l *Logger) Info(format string, args ...any) { l.log(LevelInfo, format, args...) }

// Warning logs at warning level.
func (l *Logger) Warning(format string, args ...any) { l.log(LevelWarning, format, args...) }

// Error logs at error level.
func (l *Logger) Error(format string, args ...any) { l.log(LevelError, format, args...) }

// SetDefault replaces the package-level default logger.
func SetDefault(l *Logger) { defaultLogger = l }

// L returns the default logger.
func L() *Logger { return defaultLogger }

// Convenience package-level helpers.
func Debug(format string, args ...any)   { defaultLogger.Debug(format, args...) }
func Info(format string, args ...any)    { defaultLogger.Info(format, args...) }
func Warning(format string, args ...any) { defaultLogger.Warning(format, args...) }
func Error(format string, args ...any)   { defaultLogger.Error(format, args...) }
