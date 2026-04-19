// Package geo 提供基于纯真 IP 数据库的 IP 归属地和运营商查询功能。
// 用于在测试流媒体源时，解析源服务器 IP 的地理位置和运营商信息，
// 以便在生成播放列表时优先选择同城或同运营商的源，提升播放体验。
package geo

import (
	"embed"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/zu1k/nali/pkg/qqwry"
	"live-source-manager-go/pkg/logger"
)

//go:embed qqwry.dat
var qqwryData embed.FS

const qqwryFileName = "qqwry.dat"

// Info 表示 IP 归属地信息
type Info struct {
	IP       string `json:"ip"`       // IP 地址
	Country  string `json:"country"`  // 国家
	Province string `json:"province"` // 省份
	City     string `json:"city"`     // 城市
	ISP      string `json:"isp"`      // 运营商
	Location string `json:"location"` // 完整归属地字符串
}

// Resolver IP 归属地解析器
type Resolver struct {
	log    *logger.Logger
	db     *qqwry.QQwry
	dbPath string
	mu     sync.RWMutex
}

// NewResolver 创建归属地解析器实例。
// dataDir 为数据文件存放目录，若文件不存在则从嵌入的资源中释放。
func NewResolver(dataDir string, log *logger.Logger) (*Resolver, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, qqwryFileName)

	// 如果数据库文件不存在，从嵌入资源释放
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if err := extractEmbeddedDB(dbPath); err != nil {
			return nil, fmt.Errorf("释放 IP 数据库失败: %w", err)
		}
		log.Info("IP 数据库已释放到 %s", dbPath)
	}

	// 加载纯真数据库
	db, err := qqwry.NewQQwry(dbPath)
	if err != nil {
		return nil, fmt.Errorf("加载 IP 数据库失败: %w", err)
	}

	log.Info("IP 归属地解析器初始化成功，数据文件: %s", dbPath)
	return &Resolver{
		log:    log,
		db:     db,
		dbPath: dbPath,
	}, nil
}

// extractEmbeddedDB 从嵌入的文件系统中提取 qqwry.dat 到目标路径
func extractEmbeddedDB(dstPath string) error {
	srcFile, err := qqwryData.Open(qqwryFileName)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Resolve 解析 IP 地址的归属地信息
func (r *Resolver) Resolve(ipStr string) (*Info, error) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, fmt.Errorf("无效的 IP 地址: %s", ipStr)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	result, err := r.db.Find(ipStr)
	if err != nil {
		return nil, err
	}

	info := &Info{
		IP:       ipStr,
		Country:  result.Country,
		Province: result.Province,
		City:     result.City,
		ISP:      result.ISP,
		Location: result.String(),
	}
	return info, nil
}

// ResolveFromURL 从 URL 中提取主机名（IP 或域名），解析其归属地。
// 如果主机名为域名，则先解析为 IP 地址。
func (r *Resolver) ResolveFromURL(rawURL string) (*Info, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败: %w", err)
	}
	hostname := u.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("URL 中无有效主机名")
	}

	// 尝试直接作为 IP 解析
	if ip := net.ParseIP(hostname); ip != nil {
		return r.Resolve(hostname)
	}

	// 域名解析为 IP（取第一个 IPv4 地址）
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, fmt.Errorf("域名解析失败: %w", err)
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			return r.Resolve(ipv4.String())
		}
	}
	return nil, fmt.Errorf("未找到 IPv4 地址")
}

// GetLocation 获取简化的归属地字符串（省份+城市）
func (r *Resolver) GetLocation(rawURL string) string {
	info, err := r.ResolveFromURL(rawURL)
	if err != nil {
		r.log.Debug("获取归属地失败 %s: %v", rawURL, err)
		return ""
	}
	if info.City != "" {
		return info.City
	}
	if info.Province != "" {
		return info.Province
	}
	return info.Country
}

// GetISP 获取运营商信息
func (r *Resolver) GetISP(rawURL string) string {
	info, err := r.ResolveFromURL(rawURL)
	if err != nil {
		return ""
	}
	return info.ISP
}

// Close 关闭解析器，释放资源
func (r *Resolver) Close() error {
	// qqwry 库不需要显式关闭
	return nil
}

// Reload 重新加载数据库（例如更新了 qqwry.dat 文件后）
func (r *Resolver) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newDB, err := qqwry.NewQQwry(r.dbPath)
	if err != nil {
		return fmt.Errorf("重新加载 IP 数据库失败: %w", err)
	}
	r.db = newDB
	r.log.Info("IP 数据库已重新加载")
	return nil
}
