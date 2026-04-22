package public

import (
	"database/sql"
	"net/http"
	"path/filepath"
)

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) ServeM3U(w http.ResponseWriter, r *http.Request) {
	// 直接返回生成好的文件
	http.ServeFile(w, r, filepath.Join("outfile", "live.m3u"))
}
