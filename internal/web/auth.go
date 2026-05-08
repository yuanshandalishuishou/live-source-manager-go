package web

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// Claims 自定义 JWT 声明
type Claims struct {
	UserID   int    `json:"uid"`
	Username string `json:"uname"`
	IsAdmin  bool   `json:"admin"`
	jwt.RegisteredClaims
}

// JWTManager 负责 token 签发与校验
type JWTManager struct {
	secret        []byte
	tokenDuration time.Duration
}

// NewJWTManager 从配置初始化密钥与过期时间
func NewJWTManager(cfg *config.Config) *JWTManager {
	secret := cfg.WebServer.JWTSecret
	if secret == "" {
		secret = fmt.Sprintf("live-source-manager-default-%d", time.Now().UnixNano())
		logger.Warn("JWT Secret 未在配置中设置，使用随机值（每次重启 token 失效）")
	}
	tokenDuration := time.Duration(cfg.WebServer.TokenExpireHours) * time.Hour
	if tokenDuration <= 0 {
		tokenDuration = 24 * time.Hour
	}
	return &JWTManager{
		secret:        []byte(secret),
		tokenDuration: tokenDuration,
	}
}

// GenerateToken 为用户生成 JWT
func (m *JWTManager) GenerateToken(userID int, username string, isAdmin bool) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "live-source-manager",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ValidateToken 验证 token 字符串，返回解析后的 Claims
// 改进：首先检查 token 解析错误，然后进行类型断言，避免 nil panic
func (m *JWTManager) ValidateToken(tokenStr string) (*Claims, error) {
	if tokenStr == "" {
		return nil, errors.New("缺失 token")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// 验证签名算法
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名方法: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("token 解析失败: %w", err)
	}

	// 安全断言，防止 token.Claims 为 nil
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("无效的 token 或 Claims 类型错误")
	}
	return claims, nil
}
