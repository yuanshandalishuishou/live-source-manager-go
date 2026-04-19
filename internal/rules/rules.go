// Package rules 负责频道分类规则的管理，包括从 YAML 文件加载分类规则，
// 从数据库加载正则别名，以及基于优先级进行频道名称匹配和分类判定。
package rules

import (
	"database/sql"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
	"gopkg.in/yaml.v3"
)

// CategoryRule 从 YAML 加载的分类规则结构
type CategoryRule struct {
	Name     string   `yaml:"name"`
	Priority int      `yaml:"priority"`
	Keywords []string `yaml:"keywords"`
}

// ChannelTypeRules 频道类型关键词映射
type ChannelTypeRules map[string][]string

// GeographyRules 地理位置规则（简化，暂未使用）
type GeographyRules struct {
	Continents []Continent `yaml:"continents"`
}
type Continent struct {
	Name      string    `yaml:"name"`
	Code      string    `yaml:"code"`
	Countries []Country `yaml:"countries"`
}
type Country struct {
	Name      string     `yaml:"name"`
	Code      string     `yaml:"code"`
	Keywords  []string   `yaml:"keywords"`
	Provinces []Province `yaml:"provinces"`
	Regions   []Region   `yaml:"regions"`
}
type Province struct {
	Name     string   `yaml:"name"`
	Code     string   `yaml:"code"`
	Keywords []string `yaml:"keywords"`
}
type Region struct {
	Name     string   `yaml:"name"`
	Code     string   `yaml:"code"`
	Keywords []string `yaml:"keywords"`
}

// Rules 整体规则结构（从 YAML 加载）
type Rules struct {
	Categories   []CategoryRule   `yaml:"categories"`
	ChannelTypes ChannelTypeRules `yaml:"channel_types"`
	Geography    GeographyRules   `yaml:"geography"`
}

// AliasRule 频道别名规则（从数据库加载）
type AliasRule struct {
	ID         int64
	Pattern    string
	TargetName string
	Priority   int
	Regex      *regexp.Regexp // 编译后的正则对象
}

// CategoryMatcher 分类匹配器，包含从 YAML 加载的分类规则和数据库中的自定义规则
type CategoryMatcher struct {
	ID            int64
	Name          string
	Priority      int
	KeywordRules  *KeywordRulesConfig // 数据库配置的匹配规则
	CompiledRegex []*regexp.Regexp    // 编译后的正则列表
	Keywords      []string            // 普通关键词列表
}

// KeywordRulesConfig 表示 categories.keyword_rules 字段的 JSON 结构
type KeywordRulesConfig struct {
	Type     string   `json:"type"`     // "keyword" 或 "regex"
	Keywords []string `json:"keywords"` // 当 type=keyword 时使用
	Patterns []string `json:"patterns"` // 当 type=regex 时使用
}

// Manager 规则管理器，整合 YAML 规则、数据库别名、数据库分类规则
type Manager struct {
	logger *logger.Logger
	db     *sql.DB

	mu               sync.RWMutex
	yamlCategories   []CategoryRule              // YAML 中的分类规则（优先级已排序）
	aliasRules       []*AliasRule                // 正则别名规则（按优先级排序）
	categoryMatchers []*CategoryMatcher          // 数据库中的分类规则
	defaultCategory  string
}

// NewManager 创建规则管理器实例
func NewManager(db *sql.DB, yamlPath string, log *logger.Logger) (*Manager, error) {
	m := &Manager{
		logger:          log,
		db:              db,
		defaultCategory: "其他频道",
	}

	// 加载 YAML 规则
	if err := m.loadYAMLRules(yamlPath); err != nil {
		log.Warn("加载 YAML 规则失败，将使用内置默认规则: %v", err)
		m.initDefaultCategories()
	} else {
		// 按优先级排序
		sort.Slice(m.yamlCategories, func(i, j int) bool {
			return m.yamlCategories[i].Priority < m.yamlCategories[j].Priority
		})
	}

	// 从数据库加载别名和分类规则
	if err := m.loadAliasRules(); err != nil {
		log.Error("加载频道别名规则失败: %v", err)
	}
	if err := m.loadCategoryMatchers(); err != nil {
		log.Error("加载数据库分类规则失败: %v", err)
	}

	return m, nil
}

// initDefaultCategories 初始化内置默认分类（当 YAML 加载失败时使用）
func (m *Manager) initDefaultCategories() {
	m.yamlCategories = []CategoryRule{
		{Name: "央视频道", Priority: 1, Keywords: []string{"CCTV", "央视", "CGTN"}},
		{Name: "卫视频道", Priority: 10, Keywords: []string{"卫视", "TV"}},
		{Name: "影视频道", Priority: 15, Keywords: []string{"电影", "影院", "影视"}},
		{Name: "体育频道", Priority: 15, Keywords: []string{"体育", "NBA", "足球"}},
		{Name: "少儿频道", Priority: 15, Keywords: []string{"少儿", "卡通", "动画"}},
		{Name: "新闻频道", Priority: 15, Keywords: []string{"新闻", "资讯", "时事"}},
		{Name: "音乐频道", Priority: 15, Keywords: []string{"音乐", "MTV", "演唱会"}},
		{Name: "综艺频道", Priority: 15, Keywords: []string{"综艺", "娱乐", "真人秀"}},
		{Name: "收音机", Priority: 2, Keywords: []string{"广播", "电台", "FM", "AM"}},
		{Name: "港澳台", Priority: 5, Keywords: []string{"TVB", "凤凰", "台湾", "香港", "澳门"}},
		{Name: m.defaultCategory, Priority: 100, Keywords: []string{}},
	}
}

// loadYAMLRules 从 YAML 文件加载分类规则
func (m *Manager) loadYAMLRules(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rules Rules
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return err
	}
	m.yamlCategories = rules.Categories
	return nil
}

// loadAliasRules 从数据库加载频道别名规则，并预编译正则表达式
func (m *Manager) loadAliasRules() error {
	rows, err := m.db.Query(`SELECT id, pattern, target_name, priority 
		FROM channel_alias WHERE enable = 1 ORDER BY priority ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var rules []*AliasRule
	for rows.Next() {
		var r AliasRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetName, &r.Priority); err != nil {
			m.logger.Warn("扫描别名规则失败: %v", err)
			continue
		}
		// 编译正则表达式
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			m.logger.Warn("别名规则正则编译失败 [%s]: %v", r.Pattern, err)
			continue
		}
		r.Regex = re
		rules = append(rules, &r)
	}

	m.mu.Lock()
	m.aliasRules = rules
	m.mu.Unlock()

	m.logger.Info("加载了 %d 条频道别名规则", len(rules))
	return nil
}

// loadCategoryMatchers 从数据库加载分类匹配规则
func (m *Manager) loadCategoryMatchers() error {
	rows, err := m.db.Query(`SELECT id, name, priority, keyword_rules 
		FROM categories ORDER BY priority ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var matchers []*CategoryMatcher
	for rows.Next() {
		var cm CategoryMatcher
		var rulesJSON sql.NullString
		if err := rows.Scan(&cm.ID, &cm.Name, &cm.Priority, &rulesJSON); err != nil {
			m.logger.Warn("扫描分类规则失败: %v", err)
			continue
		}
		if rulesJSON.Valid && rulesJSON.String != "" {
			var cfg KeywordRulesConfig
			if err := json.Unmarshal([]byte(rulesJSON.String), &cfg); err == nil {
				cm.KeywordRules = &cfg
				// 根据类型预编译正则或提取关键词
				if cfg.Type == "regex" {
					for _, p := range cfg.Patterns {
						re, err := regexp.Compile(p)
						if err == nil {
							cm.CompiledRegex = append(cm.CompiledRegex, re)
						}
					}
				} else {
					cm.Keywords = cfg.Keywords
				}
			}
		}
		// 如果没有配置 keyword_rules，则回退到 YAML 规则（后续在匹配时处理）
		matchers = append(matchers, &cm)
	}

	m.mu.Lock()
	m.categoryMatchers = matchers
	m.mu.Unlock()

	m.logger.Info("加载了 %d 条数据库分类规则", len(matchers))
	return nil
}

// ApplyAlias 对频道名称应用正则别名替换，返回替换后的名称。
// 按优先级依次匹配，匹配成功则立即返回。
func (m *Manager) ApplyAlias(channelName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, rule := range m.aliasRules {
		if rule.Regex.MatchString(channelName) {
			m.logger.Debug("别名匹配: %s -> %s (规则ID=%d)", channelName, rule.TargetName, rule.ID)
			return rule.TargetName
		}
	}
	return channelName
}

// DetermineCategory 确定频道所属分类。
// 优先使用数据库分类规则，若未匹配则回退到 YAML 规则。
func (m *Manager) DetermineCategory(channelName string) string {
	upperName := strings.ToUpper(channelName)

	// 1. 尝试数据库规则（优先）
	m.mu.RLock()
	matchers := m.categoryMatchers
	m.mu.RUnlock()

	for _, cm := range matchers {
		if cm.KeywordRules != nil {
			if cm.KeywordRules.Type == "regex" {
				for _, re := range cm.CompiledRegex {
					if re.MatchString(upperName) {
						return cm.Name
					}
				}
			} else {
				for _, kw := range cm.Keywords {
					if strings.Contains(upperName, strings.ToUpper(kw)) {
						return cm.Name
					}
				}
			}
		}
		// 若没有 keyword_rules，则跳过，交由 YAML 处理
	}

	// 2. 回退到 YAML 规则
	m.mu.RLock()
	yamlCats := m.yamlCategories
	m.mu.RUnlock()

	for _, rule := range yamlCats {
		for _, kw := range rule.Keywords {
			if strings.Contains(upperName, strings.ToUpper(kw)) {
				return rule.Name
			}
		}
	}

	return m.defaultCategory
}

// ExtractChannelInfo 提取频道的地理和类型信息（简化实现，可后续扩展）
func (m *Manager) ExtractChannelInfo(channelName string) map[string]interface{} {
	return map[string]interface{}{
		"country":   "CN",
		"region":    nil,
		"language":  "zh",
		"province":  nil,
		"continent": "Asia",
	}
}

// Reload 重新加载数据库规则（用于 Web 配置修改后刷新）
func (m *Manager) Reload() error {
	if err := m.loadAliasRules(); err != nil {
		return err
	}
	if err := m.loadCategoryMatchers(); err != nil {
		return err
	}
	return nil
}

// GetCategories 返回所有分类名称列表（用于 Web 界面）
func (m *Manager) GetCategories() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.categoryMatchers))
	for _, cm := range m.categoryMatchers {
		names = append(names, cm.Name)
	}
	return names
}
