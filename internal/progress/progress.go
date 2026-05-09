// internal/progress/progress.go

package progress

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// Progress 记录当前测试任务的实时进度
type Progress struct {
	TaskID        string
	TotalSources  int
	TestedSources int
	SuccessCount  int
	FailedCount   int
	CurrentSource string
	Status        string // "running", "finished", "cancelled"
	StartedAt     time.Time
	mu            sync.RWMutex
}

// Manager 管理所有活跃的进度实例，并提供 WebSocket 广播能力
type Manager struct {
	mu        sync.RWMutex
	tasks     map[string]*Progress
	broadcast chan interface{} // 用于向 WebSocket 客户端广播进度快照
}

// NewManager 新建进度管理器
func NewManager() *Manager {
	return &Manager{
		tasks:     make(map[string]*Progress),
		broadcast: make(chan interface{}, 256),
	}
}

// RandomTaskID 生成随机任务ID
func RandomTaskID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateTask 创建任务进度记录
func (m *Manager) CreateTask(id string, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[id] = &Progress{
		TaskID:       id,
		TotalSources: total,
		Status:       "running",
		StartedAt:    time.Now(),
	}
	logger.Info("创建测试任务", "task_id", id, "total", total)
}

// Increment 递增计数并广播（非阻塞发送）
func (m *Manager) Increment(taskID string, success bool) {
	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()
	if !exists {
		return
	}
	task.mu.Lock()
	task.TestedSources++
	if success {
		task.SuccessCount++
	} else {
		task.FailedCount++
	}
	task.mu.Unlock()
	// 使用 select + default 进行非阻塞广播，防止客户端异常导致整个流程卡死
	snapshot := task.snapshot()
	select {
	case m.broadcast <- snapshot:
	default:
	}
}

// FinishTask 标记任务完成
func (m *Manager) FinishTask(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.tasks[taskID]; ok {
		task.mu.Lock()
		task.Status = "finished"
		task.mu.Unlock()

		select {
		case m.broadcast <- task.snapshot():
		default:
		}
	}
}

// BroadcastChan 返回只读广播通道，供 WebSocket 使用
func (m *Manager) BroadcastChan() <-chan interface{} {
	return m.broadcast
}

// 以下 Progress 方法保持不变
func (p *Progress) snapshot() ProgressSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return ProgressSnapshot{
		TaskID:  p.TaskID,
		Total:   p.TotalSources,
		Tested:  p.TestedSources,
		Success: p.SuccessCount,
		Failed:  p.FailedCount,
		Status:  p.Status,
	}
}

// ProgressSnapshot 用于 WebSocket 推送
type ProgressSnapshot struct {
	TaskID  string `json:"task_id"`
	Total   int    `json:"total"`
	Tested  int    `json:"tested"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
	Status  string `json:"status"`
}
