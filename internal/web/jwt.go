// internal/web/jwt.go
// JWT 令牌生成与验证，用于 Web API 的认证中间件。
// 该文件为 server.go 中引用的 JWTManager 提供具体实现。
package web

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTManager 管理 JWT 令牌的创建和验证。
type JWTManager struct {
	secretKey []byte
}

// NewJWTManager 创建 JWT 管理器，需要传入一个签名密钥。
func NewJWTManager(secret string) *JWTManager {
	return &JWTManager{secretKey: []byte(secret)}
}

// Claims 自定义 JWT 载荷，包含用户基本信息。
type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	jwt.RegisteredClaims
}

// GenerateToken 为用户生成 JWT 令牌，有效期 24 小时。
func (m *JWTManager) GenerateToken(userID int, username string, isAdmin bool) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secretKey)
}

// ValidateToken 验证并解析令牌，返回 Claims。
func (m *JWTManager) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("无效的令牌")
}
