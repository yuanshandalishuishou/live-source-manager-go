//go:build windows

package web

import (
	"syscall"
	"unsafe"
)

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

type memStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

// cpuTimes 返回 (idle, total) 累计 CPU 时间计数（单位：100ns）。
func cpuTimes() (idle, total int64) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetSystemTimes")
	var idleFT, kernelFT, userFT filetime
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&idleFT)), uintptr(unsafe.Pointer(&kernelFT)), uintptr(unsafe.Pointer(&userFT)))
	if r == 0 {
		return 0, 0
	}
	idle = int64(idleFT.HighDateTime)<<32 | int64(idleFT.LowDateTime)
	k := int64(kernelFT.HighDateTime)<<32 | int64(kernelFT.LowDateTime)
	u := int64(userFT.HighDateTime)<<32 | int64(userFT.LowDateTime)
	return idle, k + u
}

// memUsagePercent 返回物理内存占用百分比（0-100）。
func memUsagePercent() float64 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	var m memStatusEx
	m.dwLength = uint32(unsafe.Sizeof(m))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0
	}
	if m.dwMemoryLoad > 100 {
		return 100
	}
	return float64(m.dwMemoryLoad)
}
