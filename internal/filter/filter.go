package filter

import (
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
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
	mu        sync.RWMutex
	whitelist []Rule
	blacklist []Rule
	version   atomic.Int64 // 数据库规则版本号，用于判断是否需要重载
	db        *db.DB
}

// NewFilter 初始化并加载规则
func NewFilter(database *db.DB) (*Filter, error) {
	f := &Filter{db: database}
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

// Apply 对源列表应用黑白名单过滤，返回通过过滤的源
func (f *Filter) Apply(sources []models.PassedSource) []models.PassedSource {
	f.mu.RLock()
	wList := f.whitelist
	bList := f.blacklist
	f.mu.RUnlock()

	result := make([]models.PassedSource, 0, len(sources))
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
		result = append(result, src)
	}
	return result
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
