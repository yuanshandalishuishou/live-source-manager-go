// internal/tester/progress.go
// 为测试器提供进度管理功能，支持 WebSocket 推送。

package tester

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"live-source-manager-go/internal/models"
)

// ProgressManager 管理测试进度，线程安全。
type ProgressManager struct {
	mu            sync.RWMutex
	total         int32
	tested        int32
	success       int32
	failed        int32
	currentSource string
	status        string // running, completed, failed
	startTime     time.Time
	updateTime    time.Time
	subscribers   map[chan []byte]struct{}
}

// NewProgressManager 创建新的进度管理器实例。
func NewProgressManager() *ProgressManager {
	return &ProgressManager{
		status:      "idle",
		subscribers: make(map[chan []byte]struct{}),
	}
}

// SetTotal 设置待测试源总数。
func (pm *ProgressManager) SetTotal(total int) {
	atomic.StoreInt32(&pm.total, int32(total))
	pm.mu.Lock()
	pm.status = "running"
	pm.startTime = time.Now()
	pm.mu.Unlock()
	pm.broadcast()
}

// IncrementTested 增加已测试计数并更新当前测试源。
func (pm *ProgressManager) IncrementTested(currentSource string) {
	atomic.AddInt32(&pm.tested, 1)
	pm.mu.Lock()
	pm.currentSource = currentSource
	pm.updateTime = time.Now()
	pm.mu.Unlock()
	pm.broadcast()
}

// IncrementSuccess 增加成功计数。
func (pm *ProgressManager) IncrementSuccess() {
	atomic.AddInt32(&pm.success, 1)
	pm.broadcast()
}

// IncrementFailed 增加失败计数。
func (pm *ProgressManager) IncrementFailed() {
	atomic.AddInt32(&pm.failed, 1)
	pm.broadcast()
}

// SetCompleted 标记测试完成。
func (pm *ProgressManager) SetCompleted() {
	pm.mu.Lock()
	pm.status = "completed"
	pm.updateTime = time.Now()
	pm.mu.Unlock()
	pm.broadcast()
}

// GetProgress 返回当前进度快照。
func (pm *ProgressManager) GetProgress() models.TestProgress {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return models.TestProgress{
		TotalSources:  int(atomic.LoadInt32(&pm.total)),
		TestedSources: int(atomic.LoadInt32(&pm.tested)),
		SuccessCount:  int(atomic.LoadInt32(&pm.success)),
		FailedCount:   int(atomic.LoadInt32(&pm.failed)),
		Status:        pm.status,
		StartedAt:     pm.startTime,
		UpdatedAt:     pm.updateTime,
	}
}

// Subscribe 订阅进度更新事件，返回一个接收 []byte 的通道。
func (pm *ProgressManager) Subscribe() chan []byte {
	ch := make(chan []byte, 16)
	pm.mu.Lock()
	pm.subscribers[ch] = struct{}{}
	pm.mu.Unlock()
	// 立即发送一次当前状态
	go func() {
		data, _ := json.Marshal(pm.GetProgress())
		ch <- data
	}()
	return ch
}

// Unsubscribe 取消订阅。
func (pm *ProgressManager) Unsubscribe(ch chan []byte) {
	pm.mu.Lock()
	delete(pm.subscribers, ch)
	pm.mu.Unlock()
	close(ch)
}

// broadcast 向所有订阅者推送当前进度。
func (pm *ProgressManager) broadcast() {
	data, err := json.Marshal(pm.GetProgress())
	if err != nil {
		return
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for ch := range pm.subscribers {
		select {
		case ch <- data:
		default:
			// 接收方处理太慢，丢弃本次更新
		}
	}
}
