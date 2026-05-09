// internal/web/server.go
// 完全基于 gorilla/mux 的 HTTP 服务器，负责启动服务、注册路由和优雅关闭。
// 修正了构造函数命名，与 main.go 保持协调一致。
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

// Server 封装 HTTP 服务器及其所有依赖。
type Server struct {
	cfg     *config.Config
	db      *sql.DB
	httpSrv *http.Server
	router  *mux.Router
	handler *Handler // 内部管理的业务处理器
}

// NewServer 创建一个新的 Web 服务器实例并注册所有路由。
// 注意：main.go 中调用此函数前，需先创建 JWTManager 和 Handler 实例。
func NewServer(cfg *config.Config, db *sql.DB, handler *Handler) *Server {
	router := mux.NewRouter()
	srv := &Server{
		cfg:     cfg,
		db:      db,
		router:  router,
		handler: handler,
	}

	// 路由注册
	srv.registerRoutes()

	return srv
}

// registerRoutes 配置公开及受保护的全部 API 路由
func (s *Server) registerRoutes() {
	// 公开接口
	s.router.HandleFunc("/api/v1/playlist", s.handlePlaylist).Methods("GET")
	s.router.HandleFunc("/api/v1/epg.xml", s.handleEPG).Methods("GET")
	s.router.HandleFunc("/api/v1/health", s.handleHealth).Methods("GET")
	s.router.HandleFunc("/api/login", s.handler.handleLogin).Methods("POST")

	// JWT 保护的管理接口
	protected := s.router.PathPrefix("/api/v1/admin").Subrouter()
	protected.Use(s.handler.authMiddleware)

	// 注册所有管理路由（含仪表盘、分类、配置、日志等）
	s.handler.RegisterRoutes(s.router)
}

// Start 启动 HTTP 服务器并支持优雅关闭
func (s *Server) Start(addr string) error {
	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 监听系统信号以触发优雅关闭
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logger.Info("Web 服务器正在优雅关闭...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			logger.Error("Web 服务器关闭异常: %v", err)
		}
	}()

	logger.Info("Web 服务器启动，监听地址: %s", addr)
	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// 公开接口的简单处理函数（后续可集成 generator 等模块以实现动态数据）
func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/mpegurl")
	w.Write([]byte("#EXTM3U\n"))
}

func (s *Server) handleEPG(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tv/>"))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
