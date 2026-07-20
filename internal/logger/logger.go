// Package logger provides a minimal leveled logger used across the application.
//
// It mirrors the Python project's logging behaviour: console output plus an
// optional rotating file handler (max_size MB × backup_count files). The
// rotation parameters come from the Logging config section, so log growth is
// bounded and old logs are retained for forensics instead of growing forever.
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

// Logger is a concurrency-safe leveled logger writing to stdout and an optional
// rotating file.
type Logger struct {
	mu     sync.Mutex
	level  Level
	out    io.Writer
	closer io.Closer // non-nil when a rotating file writer is attached
}

var defaultLogger = New(LevelInfo, "")

// New creates a Logger. If filePath is non-empty, logs are also appended to that
// file (no rotation). Prefer NewRotating when a bounded log file is desired.
func New(level Level, filePath string) *Logger {
	l := &Logger{level: level, out: os.Stdout}
	if filePath != "" {
		if w, err := newRotatingWriter(filePath, 0, 0); err == nil {
			l.closer = w
			l.out = io.MultiWriter(os.Stdout, w)
		}
	}
	return l
}

// NewRotating creates a Logger that writes to stdout and a rotating file.
// maxSizeMB <= 0 disables rotation (file grows unbounded, legacy behaviour).
// backupCount is the number of rotated ".1", ".2", ... copies to retain.
func NewRotating(level Level, filePath string, maxSizeMB, backupCount int) *Logger {
	l := &Logger{level: level, out: os.Stdout}
	if filePath == "" {
		return l
	}
	var maxBytes int64
	if maxSizeMB > 0 {
		maxBytes = int64(maxSizeMB) * 1024 * 1024
	}
	w, err := newRotatingWriter(filePath, maxBytes, backupCount)
	if err != nil {
		// Fall back to stdout-only if the file cannot be opened; never fatal.
		return l
	}
	l.closer = w
	l.out = io.MultiWriter(os.Stdout, w)
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

// Close releases the underlying file (if any). Safe to call multiple times.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closer != nil {
		_ = l.closer.Close()
		l.closer = nil
	}
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

// ── rotating file writer ────────────────────────────────────────────────────

// rotatingWriter is an io.Writer that rolls the underlying file over when it
// exceeds maxBytes, keeping up to backupCount historical copies named
// "<path>.1", "<path>.2", ... (newest is ".1"). A maxBytes of 0 disables
// rotation (the file is opened in append mode and grows unbounded).
type rotatingWriter struct {
	mu          sync.Mutex
	path        string
	maxBytes    int64
	backupCount int
	file        *os.File
	size        int64
}

func newRotatingWriter(path string, maxBytes int64, backupCount int) (*rotatingWriter, error) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, backupCount: backupCount, file: f}
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes {
		// Best-effort rotation; if it fails we still write to the current file.
		_ = w.rotate()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file and shifts backups. Caller must hold w.mu.
func (w *rotatingWriter) rotate() error {
	_ = w.file.Close()
	// Shift: app.log.<backupCount> = app.log.<backupCount-1> ... app.log.1 = app.log
	for i := w.backupCount; i >= 1; i-- {
		src := w.path
		if i > 1 {
			src = fmt.Sprintf("%s.%d", w.path, i-1)
		}
		dst := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
