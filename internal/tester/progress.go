// Package tester 负责流媒体源的测试、进度管理和 WebSocket 实时推送。
package tester

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"

	"github.com/gorilla/websocket"
)

// ProgressManager 测试进度管理器，负责记录进度、更新数据库、向 WebSocket 客户端广播。
type ProgressManager struct {
	db          *sql.DB
	log         *logger.Logger
	mu          sync.RWMutex
	taskID      string
	progress    *models.TestProgress
	clients     map[*websocket.Conn]bool
	clientsMu   sync.RWMutex
	broadcastCh chan []byte
}

// NewProgressManager 创建进度管理器，同时创建一条进度记录并返回 taskID。
func NewProgressManager(db *sql.DB, log *logger.Logger) *ProgressManager {
	taskID := time.Now().Format("20060102150405") + "-" + randomString(6)
	pm := &ProgressManager{
		db:          db,
		log:         log,
		taskID:      taskID,
		progress:    &models.TestProgress{TaskID: taskID, Status: "running", StartedAt: time.Now()},
		clients:     make(map[*websocket.Conn]bool),
		broadcastCh: make(chan []byte, 100),
	}

	// 插入初始进度记录
	_, err := db.Exec(`INSERT INTO test_progress 
		(task_id, total_sources, tested_sources, success_count, failed_count, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, 0, 0, 0, 0, "running", pm.progress.StartedAt)
	if err != nil {
		log.Error("创建进度记录失败: %v", err)
	}

	go pm.broadcastLoop()
	return pm
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}

// SetTotal 设置总源数
func (pm *ProgressManager) SetTotal(total int) {
	pm.mu.Lock()
	pm.progress.TotalSources = total
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// IncrementTested 增加已测试计数，并更新当前测试的源名称
func (pm *ProgressManager) IncrementTested(currentSource string) {
	pm.mu.Lock()
	pm.progress.TestedSources++
	pm.progress.CurrentSource = sql.NullString{String: currentSource, Valid: true}
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// IncrementSuccess 增加成功计数
func (pm *ProgressManager) IncrementSuccess() {
	pm.mu.Lock()
	pm.progress.SuccessCount++
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// IncrementFailed 增加失败计数
func (pm *ProgressManager) IncrementFailed() {
	pm.mu.Lock()
	pm.progress.FailedCount++
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// SetCompleted 标记任务完成
func (pm *ProgressManager) SetCompleted() {
	pm.mu.Lock()
	pm.progress.Status = "completed"
	pm.progress.UpdatedAt = time.Now()
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// SetFailed 标记任务失败
func (pm *ProgressManager) SetFailed(errMsg string) {
	pm.mu.Lock()
	pm.progress.Status = "failed"
	pm.progress.CurrentSource = sql.NullString{String: errMsg, Valid: true}
	pm.progress.UpdatedAt = time.Now()
	pm.mu.Unlock()
	pm.updateDB()
	pm.broadcast()
}

// GetSnapshot 获取当前进度快照
func (pm *ProgressManager) GetSnapshot() models.TestProgress {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return *pm.progress
}

// TaskID 返回任务标识
func (pm *ProgressManager) TaskID() string {
	return pm.taskID
}

func (pm *ProgressManager) updateDB() {
	pm.mu.RLock()
	p := pm.progress
	pm.mu.RUnlock()

	_, err := pm.db.Exec(`UPDATE test_progress SET 
		total_sources = ?, tested_sources = ?, success_count = ?, failed_count = ?,
		current_source = ?, status = ?, updated_at = ?
		WHERE task_id = ?`,
		p.TotalSources, p.TestedSources, p.SuccessCount, p.FailedCount,
		p.CurrentSource, p.Status, time.Now(), pm.taskID)
	if err != nil {
		pm.log.Error("更新进度记录失败: %v", err)
	}
}

func (pm *ProgressManager) broadcast() {
	data, _ := json.Marshal(pm.GetSnapshot())
	select {
	case pm.broadcastCh <- data:
	default:
		// 避免阻塞
	}
}

func (pm *ProgressManager) broadcastLoop() {
	for data := range pm.broadcastCh {
		pm.clientsMu.RLock()
		for conn := range pm.clients {
			go func(c *websocket.Conn) {
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
					pm.log.Debug("WebSocket 发送失败: %v", err)
					c.Close()
					pm.clientsMu.Lock()
					delete(pm.clients, c)
					pm.clientsMu.Unlock()
				}
			}(conn)
		}
		pm.clientsMu.RUnlock()
	}
}

// RegisterClient 注册 WebSocket 客户端
func (pm *ProgressManager) RegisterClient(conn *websocket.Conn) {
	pm.clientsMu.Lock()
	pm.clients[conn] = true
	pm.clientsMu.Unlock()
	// 发送当前进度
	data, _ := json.Marshal(pm.GetSnapshot())
	conn.WriteMessage(websocket.TextMessage, data)
}

// UnregisterClient 注销客户端
func (pm *ProgressManager) UnregisterClient(conn *websocket.Conn) {
	pm.clientsMu.Lock()
	delete(pm.clients, conn)
	pm.clientsMu.Unlock()
}
