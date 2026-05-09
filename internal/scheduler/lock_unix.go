//go:build !windows

// internal/scheduler/lock_unix.go
// 为 Unix-like 系统提供基于 flock 系统的文件锁实现。
package scheduler

import (
	"os"
	"syscall"
)

// LockFile 对文件加排他锁，非阻塞模式。
// 若锁已被其他进程持有，则立即返回错误。
func LockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// UnlockFile 释放文件锁。
func UnlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
