package admin

import (
	"database/sql"
	"net/http"
	"video-source-manager/internal/collector"
	"video-source-manager/internal/tester"
)

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	// 简单的管理首页，显示提示和链接
	tmpl := `<html><body>
        <h1>视频源管理器</h1>
        <ul>
            <li><a href="/collect">手动触发收集</a></li>
            <li><a href="/test">手动触发测试</a></li>
            <li><a href="/live.m3u" target="_blank">查看生成的 M3U</a></li>
        </ul>
        <p>默认密码提示：请修改默认密码</p>
    </body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(tmpl))
}

func (h *Handler) Collect(w http.ResponseWriter, r *http.Request) {
	// 触发收集（简化：从 live_sources 读取所有 online 源）
	rows, err := h.db.Query("SELECT id, source_path FROM live_sources WHERE source_type='online' AND enabled=1")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err == nil {
			collector.CollectFromURL(h.db, id, path, nil) // cfg 需要传入
		}
	}
	w.Write([]byte("收集任务已触发"))
}

func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	// 触发测试（简化：测试所有 pending 源）
	tester.TestAllPending(h.db, nil) // cfg 需要传入
	w.Write([]byte("测试任务已触发"))
}
