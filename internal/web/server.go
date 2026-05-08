package web

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"golang.org/x/time/rate"
)

// Server 封装 HTTP 服务，支持速率限制与 CORS
type Server struct {
	router   *mux.Router
	cfg      *config.Config
	limiters sync.Map // 存储每个 IP 的 rate.Limiter
}

// NewServer 创建服务器并注册全局中间件
func NewServer(cfg *config.Config) *Server {
	s := &Server{cfg: cfg}
	r := mux.NewRouter()

	// 中间件顺序：日志 -> CORS -> 速率限制 -> 路由
	r.Use(s.loggingMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.rateLimitMiddleware)

	// 注册路由（示例）
	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/login", s.handleLogin).Methods("POST")
	// ... 其他路由

	s.router = r
	return s
}

// Serve 启动 HTTP 服务
func (s *Server) Serve() error {
	addr := fmt.Sprintf(":%d", s.cfg.WebServer.Port)
	log.Printf("Web 服务启动于 %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// ---------- 中间件 ----------

// loggingMiddleware 记录请求基本信息
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
	})
}

// corsMiddleware 处理跨域请求，根据配置白名单放行
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := s.cfg.WebServer.AllowedOrigins
	hasStar := false
	originMap := make(map[string]bool)
	for _, o := range allowedOrigins {
		if o == "*" {
			hasStar = true
			break
		}
		originMap[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allow := false
		if hasStar {
			allow = true
		} else if _, ok := originMap[origin]; ok {
			allow = true
		}
		if allow {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
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
	limitPerSec := rate.Limit(s.cfg.WebServer.RateLimit) // 默认 10
	burst := s.cfg.WebServer.RateBurst
	if limitPerSec <= 0 {
		return next // 不限流
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := readUserIP(r)
		key := "rate:" + ip
		limiter, _ := s.limiters.LoadOrStore(key, rate.NewLimiter(limitPerSec, burst))
		l := limiter.(*rate.Limiter)
		if !l.Allow() {
			http.Error(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// readUserIP 获取用户真实 IP，优先考虑代理头
func readUserIP(r *http.Request) string {
	// X-Forwarded-For 可能包含多个 IP，取第一个
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}
	// 尝试 X-Real-IP
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		return xri
	}
	// 回退到 RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- 示例路由处理 ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// 省略具体实现，使用 auth 模块签发 token
}
