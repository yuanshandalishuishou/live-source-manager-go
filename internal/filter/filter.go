// internal/filter/filter.go
// 黑白名单过滤器 + 质量筛选器，支持热重载
package filter

import (
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/models"
)

// Rule 内存中的过滤规则
type Rule struct {
	Pattern    *regexp.Regexp
	TargetType string // "url" 或 "name"
	Enable     bool
	Priority   int
}

// Filter 黑白名单过滤器，支持热重载
type Filter struct {
	mu      sync.RWMutex
	whitelist []Rule
	blacklist []Rule
	version   atomic.Int64 // 数据库规则版本号，用于判断是否需要重载
	db        *db.DB
	cfg       *config.Config
}

// NewFilter 初始化并加载规则
func NewFilter(database *db.DB, cfg *config.Config) (*Filter, error) {
	f := &Filter{db: database, cfg: cfg}
	if err := f.reload(); err != nil {
		return nil, err
	}
	return f, nil
}

// ReloadIfNeed 仅在数据库版本变化时重新加载，可定时调用或于生成前调用
func (f *Filter) ReloadIfNeed() error {
	dbVer, err := f.db.GetFilterVersion() // 数据库函数，返回当前规则版本号
	if err != nil {
		return err
	}
	if dbVer > f.version.Load() {
		return f.reload()
	}
	return nil
}

// Reload 强制重新加载（供 API 调用）
func (f *Filter) Reload() error {
	return f.reload()
}

// reload 从数据库读取所有启用的黑白名单规则，编译正则并替换内部列表
func (f *Filter) reload() error {
	wRules, err := f.db.GetActiveWhitelistRules()
	if err != nil {
		return fmt.Errorf("读取白名单规则失败: %w", err)
	}
	bRules, err := f.db.GetActiveBlacklistRules()
	if err != nil {
		return fmt.Errorf("读取黑名单规则失败: %w", err)
	}

	newWhitelist := make([]Rule, 0, len(wRules))
	for _, r := range wRules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			logger.Warn("白名单正则编译错误", "pattern", r.Pattern, "error", err)
			continue
		}
		newWhitelist = append(newWhitelist, Rule{
			Pattern:    re,
			TargetType: r.TargetType,
			Enable:     r.Enable,
			Priority:   r.Priority,
		})
	}

	newBlacklist := make([]Rule, 0, len(bRules))
	for _, r := range bRules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			logger.Warn("黑名单正则编译错误", "pattern", r.Pattern, "error", err)
			continue
		}
		newBlacklist = append(newBlacklist, Rule{
			Pattern:    re,
			TargetType: r.TargetType,
			Enable:     r.Enable,
			Priority:   r.Priority,
		})
	}

	f.mu.Lock()
	f.whitelist = newWhitelist
	f.blacklist = newBlacklist
	f.mu.Unlock()

	dbVer, _ := f.db.GetFilterVersion()
	f.version.Store(dbVer)

	logger.Info("过滤器规则已重载", "whitelist", len(newWhitelist), "blacklist", len(newBlacklist))
	return nil
}

// Apply 对源列表应用黑白名单过滤 + 质量筛选，返回通过过滤的源
func (f *Filter) Apply(sources []models.PassedSource) []models.PassedSource {
	// 第一步：黑白名单过滤
	f.mu.RLock()
	wList := f.whitelist
	bList := f.blacklist
	f.mu.RUnlock()

	filtered := make([]models.PassedSource, 0, len(sources))

sourceLoop:
	for _, src := range sources {
		// 若白名单非空，源必须匹配至少一条白名单规则
		if len(wList) > 0 {
			matched := false
			for _, rule := range wList {
				if rule.Enable && rule.match(src) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// 若匹配任一黑名单规则，则丢弃
		for _, rule := range bList {
			if rule.Enable && rule.match(src) {
				continue sourceLoop
			}
		}
		filtered = append(filtered, src)
	}

	// 第二步：质量筛选（如果配置了质量参数）
	if f.cfg != nil {
		filtered = f.qualityFilter(filtered)
	}

	return filtered
}

// qualityFilter 根据配置的质量门限进一步过滤源
func (f *Filter) qualityFilter(sources []models.PassedSource) []models.PassedSource {
	cfg := f.cfg.Filter
	// 如果没有配置任何质量门限，则跳过
	if cfg.MaxLatency == 0 && cfg.MinBitrate == 0 && cfg.MinResolution == "" {
		return sources
	}

	result := make([]models.PassedSource, 0, len(sources))
	for _, src := range sources {
		// 延迟过滤：如果配置了最大延迟且源延迟超过门限则丢弃
		if cfg.MaxLatency > 0 && src.Latency > float64(cfg.MaxLatency) {
			logger.Debug("过滤掉 %s (延迟 %.0fms > %dms)", src.Name, src.Latency, cfg.MaxLatency)
			continue
		}
		// 比特率过滤
		if cfg.MinBitrate > 0 && src.Bitrate < cfg.MinBitrate {
			logger.Debug("过滤掉 %s (比特率 %d < %d)", src.Name, src.Bitrate, cfg.MinBitrate)
			continue
		}
		// 分辨率过滤（简单字符串比较，格式 "1920x1080"）
		if cfg.MinResolution != "" {
			if !resolutionMeets(src.Resolution, cfg.MinResolution) {
				logger.Debug("过滤掉 %s (分辨率 %s < %s)", src.Name, src.Resolution, cfg.MinResolution)
				continue
			}
		}
		result = append(result, src)
	}
	return result
}

// resolutionMeets 判断源分辨率是否满足最低要求（简化为像素数比较）
func resolutionMeets(srcRes, minRes string) bool {
	var w1, h1, w2, h2 int
	_, _ = fmt.Sscanf(srcRes, "%dx%d", &w1, &h1)
	_, _ = fmt.Sscanf(minRes, "%dx%d", &w2, &h2)
	return w1*h1 >= w2*h2
}

// match 根据 targetType 判断源是否匹配该规则
func (r *Rule) match(src models.PassedSource) bool {
	switch r.TargetType {
	case "url":
		return r.Pattern.MatchString(src.URL)
	case "name":
		return r.Pattern.MatchString(src.Name)
	default:
		return r.Pattern.MatchString(src.URL)
	}
}
