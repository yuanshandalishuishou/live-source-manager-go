package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocketHandler 处理 WebSocket 连接，用于实时推送测试进度
func (s *Server) WebSocketHandler(c *gin.Context) {
	conn, err := s.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.log.Error("WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	// 获取当前进度管理器
	pm := s.tester.GetCurrentProgress()
	if pm == nil {
		conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"idle"}`))
		return
	}

	// 注册客户端
	pm.RegisterClient(conn)
	defer pm.UnregisterClient(conn)

	// 保持连接，等待客户端关闭
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
