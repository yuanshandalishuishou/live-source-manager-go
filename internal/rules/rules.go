// internal/rules/rules.go
package rules

import (
	"database/sql"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"live-source-manager-go/pkg/logger"
	"gopkg.in/yaml.v3"
)

type CategoryRule struct {
	Name     string   `yaml:"name"`
	Priority int      `yaml:"priority"`
	Keywords []string `yaml:"keywords"`
}

type ChannelTypeRules map[string][]string

type Rules struct {
	Categories   []CategoryRule   `yaml:"categories"`
	ChannelTypes ChannelTypeRules `yaml:"channel_types"`
}

type AliasRule struct {
	ID         int64
	Pattern    string
	TargetName string
	Priority   int
	Regex      *regexp.Regexp
}

type CategoryMatcher struct {
	ID            int64
	Name          string
	Priority      int
	KeywordRules  *KeywordRulesConfig
	CompiledRegex []*regexp.Regexp
	Keywords      []string
}

type KeywordRulesConfig struct {
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
}

type Manager struct {
	logger           *logger.Logger
	db               *sql.DB
	mu               sync.RWMutex
	yamlCategories   []CategoryRule
	aliasRules       []*AliasRule
	categoryMatchers []*CategoryMatcher
	defaultCategory  string
}

func NewManager(db *sql.DB, yamlPath string, log *logger.Logger) (*Manager, error) {
	m := &Manager{
		logger:          log,
		db:              db,
		defaultCategory: "其他频道",
	}
	if err := m.loadYAMLRules(yamlPath); err != nil {
		log.Warn("加载 YAML 规则失败，使用内置默认: %v", err)
		m.initDefaultCategories()
	} else {
		sort.Slice(m.yamlCategories, func(i, j int) bool {
			return m.yamlCategories[i].Priority < m.yamlCategories[j].Priority
		})
	}
	m.loadAliasRules()
	m.loadCategoryMatchers()
	return m, nil
}

func (m *Manager) initDefaultCategories() {
	m.yamlCategories = []CategoryRule{
		{Name: "央视频道", Priority: 1, Keywords: []string{"CCTV", "央视", "CGTN"}},
		{Name: "卫视频道", Priority: 10, Keywords: []string{"卫视", "TV"}},
		{Name: m.defaultCategory, Priority: 100, Keywords: []string{}},
	}
}

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

func (m *Manager) loadAliasRules() {
	rows, err := m.db.Query(`SELECT id, pattern, target_name, priority FROM channel_alias WHERE enable = 1 ORDER BY priority ASC`)
	if err != nil {
		m.logger.Error("加载别名规则失败: %v", err)
		return
	}
	defer rows.Close()
	var rules []*AliasRule
	for rows.Next() {
		var r AliasRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetName, &r.Priority); err != nil {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			m.logger.Warn("别名规则正则编译失败: %s", r.Pattern)
			continue
		}
		r.Regex = re
		rules = append(rules, &r)
	}
	m.mu.Lock()
	m.aliasRules = rules
	m.mu.Unlock()
	m.logger.Info("加载了 %d 条频道别名规则", len(rules))
}

func (m *Manager) loadCategoryMatchers() {
	rows, err := m.db.Query(`SELECT id, name, priority, keyword_rules FROM categories ORDER BY priority ASC`)
	if err != nil {
		m.logger.Error("加载分类规则失败: %v", err)
		return
	}
	defer rows.Close()
	var matchers []*CategoryMatcher
	for rows.Next() {
		var cm CategoryMatcher
		var rulesJSON sql.NullString
		if err := rows.Scan(&cm.ID, &cm.Name, &cm.Priority, &rulesJSON); err != nil {
			continue
		}
		if rulesJSON.Valid && rulesJSON.String != "" {
			var cfg KeywordRulesConfig
			if err := json.Unmarshal([]byte(rulesJSON.String), &cfg); err == nil {
				cm.KeywordRules = &cfg
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
		matchers = append(matchers, &cm)
	}
	m.mu.Lock()
	m.categoryMatchers = matchers
	m.mu.Unlock()
	m.logger.Info("加载了 %d 条数据库分类规则", len(matchers))
}

func (m *Manager) ApplyAlias(channelName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rule := range m.aliasRules {
		if rule.Regex.MatchString(channelName) {
			return rule.TargetName
		}
	}
	return channelName
}

func (m *Manager) DetermineCategory(channelName string) string {
	upperName := strings.ToUpper(channelName)
	m.mu.RLock()
	matchers := m.categoryMatchers
	yamlCats := m.yamlCategories
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
	}
	for _, rule := range yamlCats {
		for _, kw := range rule.Keywords {
			if strings.Contains(upperName, strings.ToUpper(kw)) {
				return rule.Name
			}
		}
	}
	return m.defaultCategory
}

func (m *Manager) ExtractChannelInfo(channelName string) map[string]interface{} {
	return map[string]interface{}{
		"country":   "CN",
		"region":    nil,
		"language":  "zh",
		"province":  nil,
		"continent": "Asia",
	}
}

func (m *Manager) Reload() error {
	m.loadAliasRules()
	m.loadCategoryMatchers()
	return nil
}

func (m *Manager) GetCategories() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.categoryMatchers))
	for i, cm := range m.categoryMatchers {
		names[i] = cm.Name
	}
	return names
}
