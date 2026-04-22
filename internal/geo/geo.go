// Package geo 提供基于纯真 IP 数据库的 IP 归属地和运营商查询功能。
// 使用 github.com/zu1k/nali 库，自动下载并更新数据库文件。
package geo

import (
	"fmt"
	"net"
	"net/url"
	"sync"

	"live-source-manager-go/pkg/logger"

	"github.com/zu1k/nali/pkg/qqwry"
)

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
	log *logger.Logger
	db  *qqwry.QQwry
	mu  sync.RWMutex
}

// NewResolver 创建归属地解析器实例。
// 使用 nali/qqwry 库，它会自动下载并更新 IP 数据库到本地缓存目录。
// dataDir 参数保留以兼容现有调用，实际不再需要手动管理文件路径。
func NewResolver(dataDir string, log *logger.Logger) (*Resolver, error) {
	// NewQQwry 会自动检查本地缓存，若文件不存在或过期则自动下载
	db, err := qqwry.NewQQwry()
	if err != nil {
		return nil, fmt.Errorf("初始化 IP 数据库失败: %w", err)
	}

	log.Info("IP 归属地解析器初始化成功（使用 nali 自动下载）")
	return &Resolver{
		log: log,
		db:  db,
	}, nil
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
		return nil, fmt.Errorf("查询 IP 归属地失败: %w", err)
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

// Close 关闭解析器，释放资源（nali 库无需显式关闭）
func (r *Resolver) Close() error {
	return nil
}

// Reload 重新加载数据库（强制更新）
func (r *Resolver) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 使用 Download 方法强制重新下载最新数据库
	newDB, err := qqwry.Download()
	if err != nil {
		return fmt.Errorf("下载 IP 数据库失败: %w", err)
	}
	r.db = newDB
	r.log.Info("IP 数据库已重新加载")
	return nil
}
