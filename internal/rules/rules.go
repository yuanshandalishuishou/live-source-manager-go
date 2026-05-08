package rules

import (
	"regexp"
	"sync"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// AliasMatcher 应用正则别名替换频道名称
type AliasMatcher struct {
	mu      sync.RWMutex
	rules   []compiledRule
	db      *db.DB
}

type compiledRule struct {
	Pattern    *regexp.Regexp
	TargetName string
	Priority   int
	Enabled    bool
}

// NewAliasMatcher 初始化并加载规则
func NewAliasMatcher(database *db.DB) (*AliasMatcher, error) {
	m := &AliasMatcher{db: database}
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload 重新加载规则（可暴露为 API）
func (m *AliasMatcher) Reload() error {
	return m.reload()
}

func (m *AliasMatcher) reload() error {
	dbRules, err := m.db.GetChannelAliases()
	if err != nil {
		return err
	}
	var compiled []compiledRule
	for _, r := range dbRules {
		if !r.Enable {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			logger.Warn("别名正则编译失败", "pattern", r.Pattern, "error", err)
			continue
		}
		compiled = append(compiled, compiledRule{
			Pattern:    re,
			TargetName: r.TargetName,
			Priority:   r.Priority,
			Enabled:    true,
		})
	}
	m.mu.Lock()
	m.rules = compiled
	m.mu.Unlock()
	logger.Info("别名规则已重载", "count", len(compiled))
	return nil
}

// Apply 遍历所有源，按优先级应用别名替换，返回新名称
func (m *AliasMatcher) Apply(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rule := range m.rules {
		if rule.Pattern.MatchString(name) {
			return rule.TargetName
		}
	}
	return name
}

// ChannelAlias 数据库模型
type ChannelAlias struct {
	ID          int
	Pattern     string
	TargetName  string
	Priority    int
	Enable      bool
	Description string
}
