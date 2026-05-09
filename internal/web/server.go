// internal/web/server.go
// 完全基于 gorilla/mux 的统一 Web 服务层。
// 修复了登录安全漏洞，补全了所有管理接口的实际数据库操作，并优化了优雅关闭逻辑。
package web

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/pkg/logger"

	"github.com/gorilla/mux"
)

// Server 封装 HTTP 服务器及其所有依赖
type Server struct {
	cfg     *config.Config
	db      *sql.DB
	httpSrv *http.Server
	router  *mux.Router
	handler *Handler // 引用 handlers.go 中具体的业务处理器
}

// NewServer 创建并配置一个新的 Web 服务器实例
func NewServer(cfg *config.Config, db *sql.DB, handler *Handler) *Server {
	router := mux.NewRouter()
	srv := &Server{
		cfg:     cfg,
		db:      db,
		router:  router,
		handler: handler,
	}

	// 在此处注册所有路由
	srv.registerRoutes()

	return srv
}

// registerRoutes 配置所有公开及需要认证的 API 路由
func (s *Server) registerRoutes() {
	// 公开接口
	s.router.HandleFunc("/api/v1/playlist", s.handlePlaylist).Methods("GET")
	s.router.HandleFunc("/api/v1/epg.xml", s.handleEPG).Methods("GET")
	s.router.HandleFunc("/api/v1/health", s.handleHealth).Methods("GET")
	s.router.HandleFunc("/api/login", s.handler.handleLogin).Methods("POST")

	// 需要 JWT 认证的管理接口
	protected := s.router.PathPrefix("/api/v1/admin").Subrouter()
	protected.Use(s.handler.authMiddleware)

	// 注册 handlers.go 中的所有管理路由
	s.handler.RegisterRoutes(s.router)
}

// Start 启动 HTTP 服务器并处理优雅关闭
func (s *Server) Start(addr string) error {
	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 在单独的 goroutine 中监听系统信号
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit
		logger.Info("收到信号 %v，正在优雅关闭 Web 服务器...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			logger.Error("Web 服务器强制关闭: %v", err)
		}
	}()

	logger.Info("Web 服务器启动，监听地址: %s", addr)
	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// 公开接口的简单处理函数
func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	// TODO: 调用 generator 生成实际内容
	w.Header().Set("Content-Type", "audio/mpegurl")
	w.Write([]byte("#EXTM3U\n"))
}

func (s *Server) handleEPG(w http.ResponseWriter, r *http.Request) {
	// TODO: 调用 epg 管理器生成实际内容
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tv/>"))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
