//go:build windows

// internal/scheduler/lock_windows.go
// 修正 Windows 锁：引入 golang.org/x/sys/windows 包，修复参数错误。
package scheduler

import (
	"os"

	"golang.org/x/sys/windows"
)

// LockFile 在 Windows 上对文件加排他锁（非阻塞）。
func LockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	var ol windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	return windows.LockFileEx(h, flags, 0, 0, 0, &ol)
}

// UnlockFile 释放文件锁。
func UnlockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	var ol windows.Overlapped
	return windows.UnlockFileEx(h, 0, 0, 0, &ol)
}
