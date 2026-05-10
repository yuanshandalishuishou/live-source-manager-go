// internal/progress/manager.go
// 管理测试进度事件，支持订阅和广播。
// 用于协调 tester 模块与前端 WebSocket 之间的进度推送。

package progress

import (
	"sync"

	"live-source-manager-go/internal/models"
)

// Manager 进度管理器，负责收集进度事件并通知所有订阅者。
type Manager struct {
	mu          sync.RWMutex
	subscribers map[chan models.ProgressEvent]struct{}
}

// NewManager 创建进度管理器实例。
func NewManager() *Manager {
	return &Manager{
		subscribers: make(map[chan models.ProgressEvent]struct{}),
	}
}

// Subscribe 订阅进度事件，返回一个通道。
// 调用者通过该通道接收实时进度，取消订阅时关闭通道。
func (m *Manager) Subscribe() chan models.ProgressEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan models.ProgressEvent, 10) // 带缓冲，避免阻塞
	m.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe 取消订阅，关闭并删除通道。
func (m *Manager) Unsubscribe(ch chan models.ProgressEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subscribers[ch]; ok {
		close(ch)
		delete(m.subscribers, ch)
	}
}

// Publish 向所有订阅者广播进度事件。
func (m *Manager) Publish(event models.ProgressEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for ch := range m.subscribers {
		select {
		case ch <- event:
		default:
			// 通道满则丢弃，避免阻塞整体流程
		}
	}
}
