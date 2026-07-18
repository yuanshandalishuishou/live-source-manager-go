package web

import (
	"sync"
	"time"
)

var cpuSampleMu sync.Mutex

// cpuUsagePercent 返回系统 CPU 占用百分比（0-100）。
// 通过两次采样之差计算，避免瞬时误差；内部短暂停顿一次以获得有效差值。
func cpuUsagePercent() float64 {
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()
	idle1, total1 := cpuTimes()
	time.Sleep(250 * time.Millisecond)
	idle2, total2 := cpuTimes()
	dIdle := idle2 - idle1
	dTotal := total2 - total1
	if dTotal <= 0 {
		return 0
	}
	v := float64(dTotal-dIdle) / float64(dTotal) * 100
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v
}

// memoryUsagePercent 返回系统物理内存占用百分比（0-100）。
func memoryUsagePercent() float64 {
	v := memUsagePercent()
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v
}
