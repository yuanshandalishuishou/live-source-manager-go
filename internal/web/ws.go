package web

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/progress"
)

// WSHandler 管理 WebSocket 连接和广播
type WSHandler struct {
	upgrader websocket.Upgrader
	clients  map[*WSClient]bool
	mu       sync.RWMutex
	progMgr  *progress.Manager
	register chan *WSClient
}

// WSClient 封装连接
type WSClient struct {
	conn *websocket.Conn
	send chan []byte
}

// NewWSHandler 创建 WebSocket 处理器
func NewWSHandler(progMgr *progress.Manager) *WSHandler {
	h := &WSHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // 配置层应做白名单检查
			},
		},
		clients:  make(map[*WSClient]bool),
		progMgr:  progMgr,
		register: make(chan *WSClient),
	}
	go h.run()
	return h
}

// ServeWS 处理 WebSocket 升级请求
func (h *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("WebSocket 升级失败", err)
		return
	}
	client := &WSClient{
		conn: conn,
		send: make(chan []byte, 256),
	}
	h.register <- client

	go client.writePump()
	go client.readPump(h)
}

// run 广播循环，将进度更新推送到所有客户端
func (h *WSHandler) run() {
	// 注意：原先 progress.Manager 中 broadcast 是一个 chan interface{}，这里需对接
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			logger.Debug("WebSocket 客户端已连接", "remote", client.conn.RemoteAddr())
		case msg := <-h.progMgr.BroadcastChan(): // 需在 progress.Manager 中暴露
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- data:
				default:
					// 发送缓冲区满，断开
					h.removeClient(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *WSHandler) removeClient(client *WSClient) {
	h.mu.Lock()
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
	}
	h.mu.Unlock()
}

// writePump 将消息写入 WebSocket
func (c *WSClient) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// readPump 读取客户端消息（本例中只处理关闭）
func (c *WSClient) readPump(h *WSHandler) {
	defer func() {
		h.removeClient(c)
		c.conn.Close()
	}()
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
