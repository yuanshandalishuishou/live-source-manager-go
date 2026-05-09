// internal/web/embed.go

package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

// GetStaticFS 返回静态文件系统（去除 static/ 前缀）
func GetStaticFS() http.FileSystem {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

// ServeStaticHandler 处理 /static/ 下的静态文件
func ServeStaticHandler() http.Handler {
	fs := GetStaticFS()
	return http.StripPrefix("/static/", http.FileServer(fs))
}
