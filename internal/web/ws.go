// internal/web/ws.go
// WebSocket 处理器，将进度管理器的事件推送到连接的客户端。

package web

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"live-source-manager-go/internal/progress"
	"live-source-manager-go/pkg/logger"
)

// WSHandler 管理 WebSocket 连接和消息广播。
type WSHandler struct {
	progMgr *progress.Manager
	upgrader websocket.Upgrader
	mu       sync.Mutex
	clients  map[*websocket.Conn]chan struct{}
}

// NewWSHandler 创建 WebSocket 处理器。
func NewWSHandler(progMgr *progress.Manager) *WSHandler {
	return &WSHandler{
		progMgr: progMgr,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]chan struct{}),
	}
}

// ServeWS 是 HTTP 升级到 WebSocket 的处理函数。
func (h *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("WebSocket 升级失败: %v", err)
		return
	}

	stop := make(chan struct{})
	h.mu.Lock()
	h.clients[conn] = stop
	h.mu.Unlock()

	// 订阅进度事件
	eventCh := h.progMgr.Subscribe()
	defer func() {
		h.progMgr.Unsubscribe(eventCh)
		conn.Close()
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
	}()

	// 写协程：将事件发送到客户端
	go func() {
		for {
			select {
			case event, ok := <-eventCh:
				if !ok {
					return
				}
				data, _ := json.Marshal(event)
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					logger.Warn("WebSocket 写入失败: %v", err)
					return
				}
			case <-stop:
				return
			}
		}
	}()

	// 读协程：仅用于检测客户端断开
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
	close(stop)
}
