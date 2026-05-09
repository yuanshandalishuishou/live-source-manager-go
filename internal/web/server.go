// internal/web/server.go

package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"golang.org/x/time/rate"
)

// Server 封装 HTTP 服务，支持速率限制与 CORS
type Server struct {
	router   *mux.Router
	cfg      *config.Config
	limiters sync.Map
}

// NewServer 创建服务器并注册全局中间件和路由
func NewServer(cfg *config.Config, router *mux.Router) *Server {
	s := &Server{
		cfg:      cfg,
		router:   router,
		limiters: sync.Map{},
	}
	// 全局中间件
	router.Use(s.corsMiddleware)
	router.Use(s.rateLimitMiddleware)
	// 静态文件服务（如果存在内嵌资源）
	router.PathPrefix("/static/").Handler(ServeStaticHandler())
	return s
}

// Serve 启动 HTTP 服务
func (s *Server) Serve() error {
	addr := fmt.Sprintf(":%d", s.cfg.WebServer.Port)
	log.Printf("Web 服务启动于 %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// corsMiddleware 处理跨域请求（文本指定白名单）
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := s.cfg.WebServer.AllowedOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allow := false
		for _, o := range allowedOrigins {
			if o == "*" || o == origin {
				allow = true
				break
			}
		}
		if allow {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware 基于客户端 IP 的令牌桶限流
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	limitPerSec := rate.Limit(s.cfg.WebServer.RateLimit)
	burst := s.cfg.WebServer.RateBurst
	if limitPerSec <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := readUserIP(r)
		limiter, _ := s.limiters.LoadOrStore(ip, rate.NewLimiter(limitPerSec, burst))
		if !limiter.(*rate.Limiter).Allow() {
			http.Error(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readUserIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		return strings.Split(xff, ",")[0]
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}
