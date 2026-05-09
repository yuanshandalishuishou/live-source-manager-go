//go:build windows

// internal/scheduler/lock_windows.go
// 为 Windows 系统提供基于 LockFileEx API 的文件锁实现。
// 使用了 golang.org/x/sys/windows 包。
package scheduler

import (
	"os"

	"golang.org/x/sys/windows"
)

// LockFile 对文件加排他锁，非阻塞模式。
// 若锁已被其他进程持有，则立即返回错误。
func LockFile(f *os.File) error {
	// 获取文件句柄
	h := windows.Handle(f.Fd())

	// 初始化重叠结构体，用于指定锁定范围（0 表示整个文件）
	var ol windows.Overlapped
	// 尝试加锁，LOCKFILE_EXCLUSIVE_LOCK 表示排他锁，LOCKFILE_FAIL_IMMEDIATELY 表示非阻塞
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)

	err := windows.LockFileEx(h, flags, 0, 0, 0, &ol)
	if err != nil {
		return err
	}
	return nil
}

// UnlockFile 释放文件锁。
func UnlockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	var ol windows.Overlapped
	// 解锁整个文件
	err := windows.UnlockFileEx(h, 0, 0, 0, &ol)
	if err != nil {
		return err
	}
	return nil
}
