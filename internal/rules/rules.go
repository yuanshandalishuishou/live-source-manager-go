// Package rules implements the multi-dimension channel classification engine,
// ported from Python's app/rules.py ChannelRules class.
//
// It loads keyword-based classification rules, province exclusions and the
// available classification dimensions from the SQLite database into memory, then
// classifies channels in place. A manual per-channel-name mapping (DB) always
// wins over the keyword engine, and province exclusions adjust mis-assigned
// region values. SeedDictionary populates the category dictionary (the controlled
// vocabulary surfaced in the UI) from channel_rules.yml.
package rules

import (
	"database/sql"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
)

// dimensions is the fixed ordered list of classification axes.
var dimensions = []string{"content", "region", "language", "quality", "media_type", "genre"}

// ruleTypeMap maps legacy rule_type values to the canonical dimension key.
var ruleTypeMap = map[string]string{
	"category":     "content",
	"channel_type": "media_type",
}

// ruleEntry is one in-memory classification rule for a single dimension.
type ruleEntry struct {
	name      string
	keywords  []string
	priority  int
	sortOrder int
}

// match is a single keyword hit produced while matching a dimension.
type match struct {
	name       string
	keyword    string
	priority   int
	keywordLen int
	sortOrder  int
}

// Engine holds the in-memory classification state loaded from the database.
type Engine struct {
	conn       *sql.DB
	rulesByDim map[string][]ruleEntry
	exclusions []types.ProvinceExclusion
	dimensions []types.ClassificationDimension
}

// NewEngine creates an Engine bound to the given database handle.
func NewEngine(conn *sql.DB) *Engine {
	return &Engine{
		conn:       conn,
		rulesByDim: map[string][]ruleEntry{},
	}
}

// LoadRules reads active classification rules + province exclusions + dimensions
// from the DB into memory.
func (e *Engine) LoadRules() error {
	rules, err := db.GetActiveRules(e.conn)
	if err != nil {
		return err
	}

	e.rulesByDim = map[string][]ruleEntry{}
	for _, r := range rules {
		if len(r.Keywords) == 0 {
			continue
		}
		dim := ruleTypeMap[r.RuleType]
		if dim == "" {
			dim = r.RuleType
		}
		e.rulesByDim[dim] = append(e.rulesByDim[dim], ruleEntry{
			name:      r.Name,
			keywords:  r.Keywords,
			priority:  r.Priority,
			sortOrder: r.SortOrder,
		})
	}

	// Each dimension: order by priority -> sort_order (Python precedence).
	for dim := range e.rulesByDim {
		rs := e.rulesByDim[dim]
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].priority != rs[j].priority {
				return rs[i].priority < rs[j].priority
			}
			return rs[i].sortOrder < rs[j].sortOrder
		})
		e.rulesByDim[dim] = rs
	}

	if e.exclusions, err = db.ListExclusions(e.conn); err != nil {
		return err
	}
	if e.dimensions, err = db.ListDimensions(e.conn); err != nil {
		return err
	}

	logger.L().Info("规则引擎加载完成: 维度规则 %d 项, 排除 %d 条",
		countRules(e.rulesByDim), len(e.exclusions))
	return nil
}

// Classify mutates channels in place, setting channel.Categories.
// Priority: manual channel_name_mapping (DB) > keyword rule engine.
// Province exclusions adjust mis-assigned region values.
func (e *Engine) Classify(channels []types.Channel) error {
	for i := range channels {
		ch := &channels[i]
		mapping, err := db.GetChannelMapping(e.conn, ch.Name)
		if err != nil {
			logger.L().Warning("查询频道映射失败 %q: %v", ch.Name, err)
		}
		if mapping != nil {
			// Manual override wins over the rule engine.
			ch.Categories = mapping.Categories
			continue
		}

		cats := e.determineCategories(ch.Name)

		// Province exclusion: drop a mis-assigned province on the region axis.
		if region, ok := cats["region"]; ok && region != "" && region != "未知" {
			for _, ex := range e.exclusions {
				if ex.ProvinceKeyword == region && strings.Contains(ch.Name, ex.ExcludedKeyword) {
					cats["region"] = "未知"
					break
				}
			}
		}

		ch.Categories = cats
	}
	return nil
}

// determineCategories runs the keyword engine across every dimension.
func (e *Engine) determineCategories(name string) map[string]string {
	result := make(map[string]string, len(dimensions))
	if strings.TrimSpace(name) == "" {
		for _, d := range dimensions {
			result[d] = "未知"
		}
		return result
	}

	nameUpper := strings.ToUpper(name)
	for _, dim := range dimensions {
		rules := e.rulesByDim[dim]
		if len(rules) == 0 {
			result[dim] = "未知"
			continue
		}
		matches := e.matchDimension(nameUpper, rules)
		if len(matches) == 0 {
			result[dim] = "未知"
			continue
		}
		if dim == "content" {
			result[dim] = e.applyDefenseLayers(matches)
		} else {
			result[dim] = matches[0].name
		}
	}
	return result
}

// matchDimension returns every keyword hit for one dimension, sorted by
// (priority, -keyword_len, sort_order) so the best candidate is first.
func (e *Engine) matchDimension(nameUpper string, rules []ruleEntry) []match {
	var matches []match
	for _, r := range rules {
		for _, kw := range r.keywords {
			if kw == "" {
				continue
			}
			if strings.Contains(nameUpper, strings.ToUpper(kw)) {
				matches = append(matches, match{
					name:       r.name,
					keyword:    kw,
					priority:   r.priority,
					keywordLen: len([]rune(kw)),
					sortOrder:  r.sortOrder,
				})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].priority != matches[j].priority {
			return matches[i].priority < matches[j].priority
		}
		if matches[i].keywordLen != matches[j].keywordLen {
			return matches[i].keywordLen > matches[j].keywordLen
		}
		return matches[i].sortOrder < matches[j].sortOrder
	})
	return matches
}

// applyDefenseLayers applies the content-dimension three-layer defense:
// high-priority guard, longest-match override, and province exclusion mapping.
func (e *Engine) applyDefenseLayers(matches []match) string {
	if len(matches) == 0 {
		return "其他频道"
	}

	// Layer 1: high-priority rules (priority <= 5) are never excluded.
	for _, m := range matches {
		if m.priority <= 5 {
			return m.name
		}
	}

	// Collapse to the best candidate per resulting category name.
	candByName := map[string]match{}
	for _, m := range matches {
		if c, ok := candByName[m.name]; !ok || m.priority < c.priority {
			candByName[m.name] = m
		}
	}

	names := make([]string, 0, len(candByName))
	for n := range candByName {
		names = append(names, n)
	}

	// Longer-keyword override: a longer keyword (e.g. "湖南卫视") from a
	// different category should beat its shorter substring (e.g. "湖南").
	longerKWOverride := map[string]string{}
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names); j++ {
			if i == j {
				continue
			}
			kwI := candByName[names[i]].keyword
			kwJ := candByName[names[j]].keyword
			if len([]rune(kwI)) < len([]rune(kwJ)) &&
				strings.Contains(strings.ToUpper(kwJ), strings.ToUpper(kwI)) {
				longerKWOverride[names[i]] = names[j]
			}
		}
	}

	candidates := make([]match, 0, len(candByName))
	for _, m := range candByName {
		candidates = append(candidates, m)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		if candidates[i].keywordLen != candidates[j].keywordLen {
			return candidates[i].keywordLen > candidates[j].keywordLen
		}
		return candidates[i].sortOrder < candidates[j].sortOrder
	})

	if len(candidates) == 1 {
		return candidates[0].name
	}

	best := candidates[0]
	if ov, ok := longerKWOverride[best.name]; ok {
		return ov
	}

	// Layer 3: if the best candidate should yield to another (exclusion map),
	// the other candidate wins.
	for _, other := range candidates[1:] {
		if e.isExcluded(best.keyword, other.keyword, best.name, other.name) {
			return other.name
		}
	}
	return candidates[0].name
}

// isExcluded reports whether otherKW should not be crowded out by candKW.
// Mirrors Python's _is_excluded + check_exclusion semantics.
func (e *Engine) isExcluded(candKW, otherKW, candName, otherName string) bool {
	if candName == otherName {
		return false
	}
	candU := strings.ToUpper(candKW)
	otherU := strings.ToUpper(otherKW)
	if candU == otherU {
		return false
	}
	if strings.Contains(candU, otherU) || strings.Contains(otherU, candU) {
		return false // parent/child keyword relationship
	}
	for _, ex := range e.exclusions {
		if ex.ProvinceKeyword == candKW && ex.ExcludedKeyword == otherKW {
			return true
		}
	}
	return false
}

// ── SeedDictionary ──────────────────────────────────────────────────────────

// yamlRule is one entry under the `categories` section.
type yamlRule struct {
	Name     string   `yaml:"name"`
	Priority int      `yaml:"priority"`
	Keywords []string `yaml:"keywords"`
}

// geoNode captures the nested geography structure (continents/countries/
// provinces/regions all carrying a `name`).
type geoNode struct {
	Name       string    `yaml:"name"`
	Provinces  []geoNode `yaml:"provinces"`
	Regions    []geoNode `yaml:"regions"`
	Countries  []geoNode `yaml:"countries"`
	Continents []geoNode `yaml:"continents"`
}

// yamlRulesFile is the top-level channel_rules.yml structure.
type yamlRulesFile struct {
	Categories   []yamlRule          `yaml:"categories"`
	ChannelTypes map[string][]string `yaml:"channel_types"`
	Geography    *geoNode            `yaml:"geography"`
}

// SeedDictionary parses the YAML and inserts category dictionary values via
// db.AddCategoryValue (dimension, value, label, sortOrder). Idempotent.
func (e *Engine) SeedDictionary(yamlPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}
	var f yamlRulesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return err
	}

	// content dimension <- categories section
	contentVals := make([]string, 0, len(f.Categories))
	for _, c := range f.Categories {
		contentVals = append(contentVals, c.Name)
	}
	if err := e.seedDimension("content", contentVals); err != nil {
		return err
	}

	// media_type dimension <- channel_types section
	mtVals := make([]string, 0, len(f.ChannelTypes))
	for k := range f.ChannelTypes {
		mtVals = append(mtVals, k)
	}
	sort.Strings(mtVals)
	if err := e.seedDimension("media_type", mtVals); err != nil {
		return err
	}

	// region dimension <- flattened geography names
	var regionVals []string
	if f.Geography != nil {
		collectGeoNames(f.Geography, &regionVals)
	}
	if err := e.seedDimension("region", regionVals); err != nil {
		return err
	}

	logger.L().Info("分类词典已初始化: content %d, media_type %d, region %d",
		len(contentVals), len(mtVals), len(regionVals))
	return nil
}

// seedDimension inserts each value with its index as sortOrder (label == value).
func (e *Engine) seedDimension(dim string, values []string) error {
	for i, v := range values {
		if v == "" {
			continue
		}
		if err := db.AddCategoryValue(e.conn, dim, v, v, i); err != nil {
			return err
		}
	}
	return nil
}

// collectGeoNames recursively gathers every `name` under a geography node.
func collectGeoNames(n *geoNode, out *[]string) {
	if n == nil || n.Name == "" {
		// still descend even when the node itself has no name
	}
	if n == nil {
		return
	}
	if n.Name != "" {
		*out = append(*out, n.Name)
	}
	for i := range n.Continents {
		collectGeoNames(&n.Continents[i], out)
	}
	for i := range n.Countries {
		collectGeoNames(&n.Countries[i], out)
	}
	for i := range n.Provinces {
		collectGeoNames(&n.Provinces[i], out)
	}
	for i := range n.Regions {
		collectGeoNames(&n.Regions[i], out)
	}
}

// countRules returns the total number of loaded rules across dimensions.
func countRules(m map[string][]ruleEntry) int {
	n := 0
	for _, rs := range m {
		n += len(rs)
	}
	return n
}
