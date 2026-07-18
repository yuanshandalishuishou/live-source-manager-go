//go:build linux

package web

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// cpuTimes 解析 /proc/stat 的 "cpu" 行，返回 (idle, total) 累计 CPU 时间计数。
// idle 包含 iowait（Linux 约定）。
func cpuTimes() (idle, total int64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		for i, fld := range fields[1:] {
			v, err := strconv.ParseInt(fld, 10, 64)
			if err != nil {
				v = 0
			}
			total += v
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
		}
		break
	}
	return idle, total
}

// memUsagePercent 解析 /proc/meminfo 的 MemTotal/MemAvailable，返回内存占用百分比。
func memUsagePercent() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	var total, avail int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total, _ = strconv.ParseInt(fields[1], 10, 64)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if total <= 0 {
		return 0
	}
	return float64(total-avail) / float64(total) * 100
}
