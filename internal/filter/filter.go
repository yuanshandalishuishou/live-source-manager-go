// Package filter 负责对测试通过的源进行多层筛选，包括黑白名单过滤、归属地/运营商优选、
// 分辨率/码率过滤，以及按频道分组并保留质量最佳的若干源。
package filter

import (
	"database/sql"
	"regexp"
	"sort"
	"strings"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// Filter 筛选器
type Filter struct {
	cfg          *config.Config
	log          *logger.Logger
	db           *sql.DB
	whitelist    []FilterRule
	blacklist    []FilterRule
	locationPref []string // 归属地偏好列表
	ispPref      []string // 运营商偏好列表
}

// FilterRule 表示一条黑白名单规则
type FilterRule struct {
	ID         int64
	Pattern    string
	TargetType string // url / channel_name / tvg_id
	Priority   int    // 仅白名单有效
	Regex      *regexp.Regexp
}

// NewFilter 创建筛选器实例，并从数据库加载黑白名单规则
func NewFilter(cfg *config.Config, log *logger.Logger, db *sql.DB) *Filter {
	f := &Filter{
		cfg:          cfg,
		log:          log,
		db:           db,
		locationPref: splitTrim(cfg.Filter.Location, ","),
		ispPref:      splitTrim(cfg.Filter.ISP, ","),
	}
	f.loadWhitelist()
	f.loadBlacklist()
	return f
}

func splitTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	var res []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

// loadWhitelist 从数据库加载白名单规则
func (f *Filter) loadWhitelist() {
	rows, err := f.db.Query(`SELECT id, pattern, target_type, priority FROM whitelist WHERE enable = 1 ORDER BY priority DESC`)
	if err != nil {
		f.log.Error("加载白名单失败: %v", err)
		return
	}
	defer rows.Close()

	var rules []FilterRule
	for rows.Next() {
		var r FilterRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType, &r.Priority); err != nil {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			f.log.Warn("白名单正则编译失败 [%s]: %v", r.Pattern, err)
			continue
		}
		r.Regex = re
		rules = append(rules, r)
	}
	f.whitelist = rules
	f.log.Info("加载了 %d 条白名单规则", len(rules))
}

// loadBlacklist 从数据库加载黑名单规则
func (f *Filter) loadBlacklist() {
	rows, err := f.db.Query(`SELECT id, pattern, target_type FROM blacklist WHERE enable = 1`)
	if err != nil {
		f.log.Error("加载黑名单失败: %v", err)
		return
	}
	defer rows.Close()

	var rules []FilterRule
	for rows.Next() {
		var r FilterRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType); err != nil {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			f.log.Warn("黑名单正则编译失败 [%s]: %v", r.Pattern, err)
			continue
		}
		r.Regex = re
		rules = append(rules, r)
	}
	f.blacklist = rules
	f.log.Info("加载了 %d 条黑名单规则", len(rules))
}

// ApplyWhitelist 应用白名单过滤。若白名单非空，则仅保留匹配任一白名单规则的源。
func (f *Filter) ApplyWhitelist(sources []models.URLSourcePassed) []models.URLSourcePassed {
	if len(f.whitelist) == 0 {
		return sources
	}
	var filtered []models.URLSourcePassed
	for _, src := range sources {
		if f.matchAnyRule(src, f.whitelist) {
			filtered = append(filtered, src)
		}
	}
	f.log.Info("白名单过滤: %d -> %d", len(sources), len(filtered))
	return filtered
}

// ApplyBlacklist 应用黑名单过滤。移除匹配任一黑名单规则的源。
func (f *Filter) ApplyBlacklist(sources []models.URLSourcePassed) []models.URLSourcePassed {
	if len(f.blacklist) == 0 {
		return sources
	}
	var filtered []models.URLSourcePassed
	for _, src := range sources {
		if !f.matchAnyRule(src, f.blacklist) {
			filtered = append(filtered, src)
		}
	}
	f.log.Info("黑名单过滤: %d -> %d", len(sources), len(filtered))
	return filtered
}

func (f *Filter) matchAnyRule(src models.URLSourcePassed, rules []FilterRule) bool {
	for _, rule := range rules {
		var target string
		switch rule.TargetType {
		case "url":
			target = src.URL
		case "channel_name":
			target = src.Name
		case "tvg_id":
			target = src.TvgID.String
		default:
			target = src.URL
		}
		if rule.Regex.MatchString(target) {
			return true
		}
	}
	return false
}

// ApplyQualityFilter 应用质量过滤（延迟、分辨率、码率等）
func (f *Filter) ApplyQualityFilter(sources []models.URLSourcePassed) []models.URLSourcePassed {
	if !f.cfg.Output.EnableFilter {
		return sources
	}
	var filtered []models.URLSourcePassed
	for _, src := range sources {
		if f.isQualified(src) {
			filtered = append(filtered, src)
		}
	}
	f.log.Info("质量过滤: %d -> %d", len(sources), len(filtered))
	return filtered
}

func (f *Filter) isQualified(src models.URLSourcePassed) bool {
	fc := f.cfg.Filter

	// 延迟
	if src.ResponseTimeMs.Valid && int(src.ResponseTimeMs.Int32) > fc.MaxLatency {
		return false
	}
	// 比特率
	if src.Bitrate.Valid && int(src.Bitrate.Int32) < fc.MinBitrate {
		return false
	}
	// HD/4K 要求
	if fc.MustHD && !f.isHD(src.Resolution.String) {
		return false
	}
	if fc.Must4K && !f.is4K(src.Resolution.String) {
		return false
	}
	// 速度
	if src.DownloadSpeed.Valid && src.DownloadSpeed.Float64 < float64(fc.MinSpeed) {
		return false
	}
	// 分辨率范围（简化，可扩展）
	if fc.MinResolution != "" || fc.MaxResolution != "" {
		if !f.checkResolution(src.Resolution.String, fc.MinResolution, fc.MaxResolution, fc.ResolutionFilterMode) {
			return false
		}
	}
	return true
}

func (f *Filter) isHD(res string) bool {
	return strings.Contains(res, "720") || strings.Contains(res, "1080") || strings.Contains(res, "1920")
}

func (f *Filter) is4K(res string) bool {
	return strings.Contains(res, "2160") || strings.Contains(res, "3840") || strings.Contains(res, "4096")
}

func (f *Filter) checkResolution(res, minRes, maxRes, mode string) bool {
	// 简化实现，生产环境可扩展为解析数字比较
	return true
}

// GroupAndSort 按频道分组，每组内按质量排序，并限制每组最大源数量。
// 排序规则：优先归属地/运营商匹配，其次下载速度降序，再次延迟升序。
func (f *Filter) GroupAndSort(sources []models.URLSourcePassed) map[string][]models.URLSourcePassed {
	// 按频道名分组
	groups := make(map[string][]models.URLSourcePassed)
	for _, src := range sources {
		groups[src.Name] = append(groups[src.Name], src)
	}

	maxPerChannel := f.cfg.Output.MaxSourcesPerChannel
	for name, list := range groups {
		// 排序
		sort.Slice(list, func(i, j int) bool {
			return f.compareSource(list[i], list[j]) < 0
		})
		// 截断
		if maxPerChannel > 0 && len(list) > maxPerChannel {
			list = list[:maxPerChannel]
		}
		groups[name] = list
	}
	f.log.Info("分组排序完成，共 %d 个频道", len(groups))
	return groups
}

// compareSource 比较两个源的质量，返回值 < 0 表示 a 优于 b。
func (f *Filter) compareSource(a, b models.URLSourcePassed) int {
	// 1. 归属地/运营商偏好匹配得分
	scoreA := f.calcPreferenceScore(a)
	scoreB := f.calcPreferenceScore(b)
	if scoreA != scoreB {
		return scoreB - scoreA // 得分高的排前面
	}

	// 2. 下载速度
	speedA := a.DownloadSpeed.Float64
	speedB := b.DownloadSpeed.Float64
	if speedA != speedB {
		if speedA > speedB {
			return -1
		}
		return 1
	}

	// 3. 响应时间
	latA := a.ResponseTimeMs.Int32
	latB := b.ResponseTimeMs.Int32
	if latA != latB {
		if latA < latB {
			return -1
		}
		return 1
	}

	// 4. 比特率
	brA := a.Bitrate.Int32
	brB := b.Bitrate.Int32
	if brA != brB {
		if brA > brB {
			return -1
		}
		return 1
	}
	return 0
}

// calcPreferenceScore 计算源在归属地和运营商偏好上的匹配得分。
func (f *Filter) calcPreferenceScore(src models.URLSourcePassed) int {
	score := 0
	loc := src.Location.String
	isp := src.ISP.String

	for _, pref := range f.locationPref {
		if strings.Contains(loc, pref) {
			score += 10
		}
	}
	for _, pref := range f.ispPref {
		if strings.Contains(isp, pref) {
			score += 5
		}
	}
	return score
}

// HierarchicalFilter 执行完整的筛选流程：黑名单 -> 质量过滤 -> 白名单 -> 分组排序
func (f *Filter) HierarchicalFilter(sources []models.URLSourcePassed) []models.URLSourcePassed {
	// 1. 黑名单过滤
	sources = f.ApplyBlacklist(sources)

	// 2. 质量过滤
	sources = f.ApplyQualityFilter(sources)

	// 3. 白名单过滤
	sources = f.ApplyWhitelist(sources)

	// 4. 分组排序
	grouped := f.GroupAndSort(sources)

	// 5. 展开为扁平列表（供生成器使用）
	var result []models.URLSourcePassed
	for _, list := range grouped {
		result = append(result, list...)
	}
	return result
}

// Reload 重新加载黑白名单规则（供 Web 修改后调用）
func (f *Filter) Reload() {
	f.loadWhitelist()
	f.loadBlacklist()
}
