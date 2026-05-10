// internal/tester/progress.go
// 测试进度管理器，实现 ProgressNotifier 接口
package tester

import (
	"fmt"
	"live-source-manager-go/internal/models"
	"sync/atomic"
)

// ProgressManager 管理测试进度
type ProgressManager struct {
	total      int64
	tested     int64
	success    int64
	failed     int64
	completed  bool
	// 可以添加订阅者通知等高级功能
}

// NewProgressManager 创建进度管理器实例
func NewProgressManager() *ProgressManager {
	return &ProgressManager{}
}

// SetTotal 设置待测源总数
func (pm *ProgressManager) SetTotal(total int) {
	atomic.StoreInt64(&pm.total, int64(total))
}

// IncrementTested 增加已测试计数，并记录当前正在测试的源
func (pm *ProgressManager) IncrementTested(currentSource string) {
	atomic.AddInt64(&pm.tested, 1)
}

// IncrementSuccess 增加成功计数
func (pm *ProgressManager) IncrementSuccess() {
	atomic.AddInt64(&pm.success, 1)
}

// IncrementFailed 增加失败计数
func (pm *ProgressManager) IncrementFailed() {
	atomic.AddInt64(&pm.failed, 1)
}

// SetCompleted 标记测试完成
func (pm *ProgressManager) SetCompleted() {
	pm.completed = true
}

// Broadcast 广播事件（基础实现，可以扩展为 WebSocket 等）
func (pm *ProgressManager) Broadcast(event string, message string) {
	// 基础实现：记录日志
	// 在实际 Web 模块中，这里可以连接 WebSocket 管理器
	fmt.Printf("[Progress][%s] %s\n", event, message)
}

// GetProgress 返回当前进度快照
func (pm *ProgressManager) GetProgress() models.TestProgress {
	return models.TestProgress{
		TotalSources:   int(atomic.LoadInt64(&pm.total)),
		TestedSources:  int(atomic.LoadInt64(&pm.tested)),
		SuccessCount:   int(atomic.LoadInt64(&pm.success)),
		FailedCount:    int(atomic.LoadInt64(&pm.failed)),
		Status:         "running",
	}
}
