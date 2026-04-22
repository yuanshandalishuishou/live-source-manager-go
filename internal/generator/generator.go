// Package generator 负责根据筛选后的源和显示规则生成 M3U 及 TXT 播放列表文件。
// 支持 EPG URL 声明、台标、回放标签，并按照 display_rule 表进行分组排序。
package generator

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
	"live-source-manager-go/pkg/utils"
)

// Generator M3U/TXT 生成器
type Generator struct {
	cfg *config.Config
	log *logger.Logger
	db  *sql.DB
}

// NewGenerator 创建生成器实例
func NewGenerator(cfg *config.Config, log *logger.Logger, db *sql.DB) *Generator {
	return &Generator{cfg: cfg, log: log, db: db}
}

// DisplayRuleWithCategory 包含分类信息的显示规则
type DisplayRuleWithCategory struct {
	models.DisplayRule
	CategoryName string
}

// Generate 主入口：从数据库获取源和规则，生成基础版和精选版播放列表
func (g *Generator) Generate() error {
	// 确保输出目录存在
	if err := utils.EnsureDir(g.cfg.Output.OutputDir); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 1. 获取所有活跃源
	sources, err := g.fetchActiveSources()
	if err != nil {
		return fmt.Errorf("获取活跃源失败: %w", err)
	}
	g.log.Info("获取到 %d 个活跃源", len(sources))

	// 2. 获取显示规则（带分类名）
	rules, err := g.fetchDisplayRules()
	if err != nil {
		return fmt.Errorf("获取显示规则失败: %w", err)
	}
	if len(rules) == 0 {
		g.log.Warn("没有配置显示规则，将使用默认分组")
	}

	// 3. 按规则对源进行分组和排序
	grouped := g.groupAndSortByRules(sources, rules)

	// 4. 生成基础版（所有源）
	baseFilename := strings.TrimSuffix(g.cfg.Output.Filename, ".m3u")
	if err := g.writePlaylist(grouped, baseFilename, "基础"); err != nil {
		g.log.Error("生成基础播放列表失败: %v", err)
	}

	// 5. 生成精选版（启用过滤的源，实际已在筛选阶段完成，这里可直接复用）
	qualifiedFilename := "qualified_" + baseFilename
	if err := g.writePlaylist(grouped, qualifiedFilename, "精选"); err != nil {
		g.log.Error("生成精选播放列表失败: %v", err)
	}

	return nil
}

// fetchActiveSources 获取所有 status='active' 的源，并关联其分类
func (g *Generator) fetchActiveSources() ([]models.URLSourcePassed, error) {
	query := `
		SELECT DISTINCT s.id, s.url, s.name, s.tvg_id, s.tvg_logo, s.group_title,
			s.catchup, s.catchup_days, s.user_agent, s.source_type,
			s.resolution, s.bitrate, s.response_time_ms, s.download_speed,
			s.epg_id, s.epg_name, s.epg_logo, s.location, s.isp,
			COALESCE(c.name, '') as category_name
		FROM url_sources_passed s
		LEFT JOIN source_categories sc ON s.id = sc.source_id
		LEFT JOIN categories c ON sc.category_id = c.id
		WHERE s.status = 'active'
		ORDER BY s.name
	`
	rows, err := g.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []models.URLSourcePassed
	for rows.Next() {
		var src models.URLSourcePassed
		var categoryName string
		err := rows.Scan(
			&src.ID, &src.URL, &src.Name, &src.TvgID, &src.TvgLogo, &src.GroupTitle,
			&src.Catchup, &src.CatchupDays, &src.UserAgent, &src.SourceType,
			&src.Resolution, &src.Bitrate, &src.ResponseTimeMs, &src.DownloadSpeed,
			&src.EPGID, &src.EPGName, &src.EPGLogo, &src.Location, &src.ISP,
			&categoryName,
		)
		if err != nil {
			g.log.Warn("扫描源记录失败: %v", err)
			continue
		}
		// 附加分类名到扩展属性（方便后续使用）
		if categoryName != "" {
			src.ExtraAttrs = sql.NullString{String: fmt.Sprintf(`{"category":"%s"}`, categoryName), Valid: true}
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// fetchDisplayRules 获取启用的显示规则，按 sort_order 排序
func (g *Generator) fetchDisplayRules() ([]DisplayRuleWithCategory, error) {
	query := `
		SELECT dr.id, dr.category_id, dr.group_name_override, dr.sort_order,
			dr.item_sort_order, dr.hide_empty_groups, dr.max_items_per_category,
			dr.filter_resolution_min, dr.enable,
			c.name as category_name
		FROM display_rule dr
		LEFT JOIN categories c ON dr.category_id = c.id
		WHERE dr.enable = 1
		ORDER BY dr.sort_order ASC
	`
	rows, err := g.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []DisplayRuleWithCategory
	for rows.Next() {
		var r DisplayRuleWithCategory
		err := rows.Scan(
			&r.ID, &r.CategoryID, &r.GroupNameOverride, &r.SortOrder,
			&r.ItemSortOrder, &r.HideEmptyGroups, &r.MaxItemsPerCategory,
			&r.FilterResolutionMin, &r.Enable,
			&r.CategoryName,
		)
		if err != nil {
			g.log.Warn("扫描显示规则失败: %v", err)
			continue
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// groupAndSortByRules 根据显示规则对源进行分组和排序
func (g *Generator) groupAndSortByRules(sources []models.URLSourcePassed, rules []DisplayRuleWithCategory) map[string][]models.URLSourcePassed {
	// 构建分类名到规则的映射
	categoryRuleMap := make(map[string]DisplayRuleWithCategory)
	for _, r := range rules {
		categoryRuleMap[r.CategoryName] = r
	}

	// 按分类分组
	groups := make(map[string][]models.URLSourcePassed)
	for _, src := range sources {
		category := g.extractCategory(src)
		if category == "" {
			category = "其他频道"
		}
		groups[category] = append(groups[category], src)
	}

	// 对每个分组应用规则排序和数量限制
	for cat, list := range groups {
		rule, ok := categoryRuleMap[cat]
		if !ok {
			// 没有规则，按默认排序
			sort.Slice(list, func(i, j int) bool {
				return list[i].Name < list[j].Name
			})
			continue
		}

		// 组内排序
		switch rule.ItemSortOrder {
		case "0": // 字母降序
			sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
		case "2": // 质量优先（速度降序，延迟升序）
			sort.Slice(list, func(i, j int) bool {
				if list[i].DownloadSpeed.Float64 != list[j].DownloadSpeed.Float64 {
					return list[i].DownloadSpeed.Float64 > list[j].DownloadSpeed.Float64
				}
				return list[i].ResponseTimeMs.Int32 < list[j].ResponseTimeMs.Int32
			})
		default: // "1" 字母升序
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		}

		// 数量限制
		if rule.MaxItemsPerCategory > 0 && len(list) > rule.MaxItemsPerCategory {
			list = list[:rule.MaxItemsPerCategory]
		}

		// 应用分组名覆盖
		if rule.GroupNameOverride.Valid && rule.GroupNameOverride.String != "" {
			delete(groups, cat)
			groups[rule.GroupNameOverride.String] = list
		} else {
			groups[cat] = list
		}
	}

	// 移除空分组（如果规则要求）
	for cat, rule := range categoryRuleMap {
		if rule.HideEmptyGroups && len(groups[cat]) == 0 {
			delete(groups, cat)
		}
	}

	return groups
}

func (g *Generator) extractCategory(src models.URLSourcePassed) string {
	if src.ExtraAttrs.Valid && src.ExtraAttrs.String != "" {
		// 简单解析 JSON，实际可引入 encoding/json
		if strings.Contains(src.ExtraAttrs.String, `"category":"`) {
			start := strings.Index(src.ExtraAttrs.String, `"category":"`) + 11
			end := strings.Index(src.ExtraAttrs.String[start:], `"`) + start
			if end > start {
				return src.ExtraAttrs.String[start:end]
			}
		}
	}
	return ""
}

// writePlaylist 写入 M3U 和 TXT 文件
func (g *Generator) writePlaylist(groups map[string][]models.URLSourcePassed, baseFilename, level string) error {
	outputDir := g.cfg.Output.OutputDir

	// 生成 M3U
	m3uContent := g.generateM3U(groups)
	m3uPath := filepath.Join(outputDir, baseFilename+".m3u")
	if err := utils.AtomicWriteFile(m3uPath, []byte(m3uContent)); err != nil {
		return err
	}
	g.log.Info("✓ %s M3U 已生成: %s", level, m3uPath)

	// 生成 TXT
	txtContent := g.generateTXT(groups)
	txtPath := filepath.Join(outputDir, baseFilename+".txt")
	if err := utils.AtomicWriteFile(txtPath, []byte(txtContent)); err != nil {
		return err
	}
	g.log.Info("✓ %s TXT 已生成: %s", level, txtPath)

	return nil
}

// generateM3U 生成 M3U 格式内容
func (g *Generator) generateM3U(groups map[string][]models.URLSourcePassed) string {
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")

	// 添加 EPG URL
	if g.cfg.EPG.IncludeEPGURL && g.cfg.EPG.EPGURL != "" {
		sb.WriteString(fmt.Sprintf("#EXTM3U url-tvg=\"%s\"\n", g.cfg.EPG.EPGURL))
	}

	// 按分类名排序输出（保证每次顺序一致）
	catNames := make([]string, 0, len(groups))
	for cat := range groups {
		catNames = append(catNames, cat)
	}
	sort.Strings(catNames)

	for _, cat := range catNames {
		for _, src := range groups[cat] {
			extinf := g.buildExtInf(src, cat)
			sb.WriteString(extinf + "\n")
			sb.WriteString(src.URL + "\n")
		}
	}
	return sb.String()
}

// buildExtInf 构建单条 EXTINF 行
func (g *Generator) buildExtInf(src models.URLSourcePassed, groupName string) string {
	var parts []string
	parts = append(parts, "#EXTINF:-1")

	// tvg-id
	tvgID := src.TvgID.String
	if tvgID == "" && src.EPGID.Valid {
		tvgID = src.EPGID.String
	}
	if tvgID == "" {
		reg := regexp.MustCompile(`[^a-zA-Z0-9]`)
		tvgID = reg.ReplaceAllString(src.Name, "_")
		tvgID = strings.ToLower(tvgID)
	}
	parts = append(parts, fmt.Sprintf(`tvg-id="%s"`, tvgID))
	parts = append(parts, fmt.Sprintf(`tvg-name="%s"`, src.Name))

	// 台标
	logo := src.TvgLogo.String
	if logo == "" && src.EPGLogo.Valid {
		logo = src.EPGLogo.String
	}
	if logo != "" {
		parts = append(parts, fmt.Sprintf(`tvg-logo="%s"`, logo))
	}

	// 分组
	parts = append(parts, fmt.Sprintf(`group-title="%s"`, groupName))

	// 回放
	if src.Catchup.Valid && src.Catchup.String != "" {
		parts = append(parts, fmt.Sprintf(`catchup="%s"`, src.Catchup.String))
	}
	if src.CatchupDays.Valid && src.CatchupDays.Int32 > 0 {
		parts = append(parts, fmt.Sprintf(`catchup-days="%d"`, src.CatchupDays.Int32))
	}

	// EPG 名称
	if src.EPGName.Valid {
		parts = append(parts, fmt.Sprintf(`tvg-chno="%s"`, src.EPGName.String))
	}

	// 质量信息（可选）
	if src.Resolution.Valid && src.Resolution.String != "" {
		parts = append(parts, fmt.Sprintf(`resolution="%s"`, src.Resolution.String))
	}
	if src.Bitrate.Valid && src.Bitrate.Int32 > 0 {
		parts = append(parts, fmt.Sprintf(`bitrate="%d"`, src.Bitrate.Int32))
	}

	parts = append(parts, fmt.Sprintf(",%s", src.Name))
	return strings.Join(parts, " ")
}

// generateTXT 生成 TXT 格式内容（分组注释）
func (g *Generator) generateTXT(groups map[string][]models.URLSourcePassed) string {
	var sb strings.Builder

	catNames := make([]string, 0, len(groups))
	for cat := range groups {
		catNames = append(catNames, cat)
	}
	sort.Strings(catNames)

	for _, cat := range catNames {
		sb.WriteString(fmt.Sprintf("# %s\n", cat))
		for _, src := range groups[cat] {
			sb.WriteString(fmt.Sprintf("%s,%s\n", src.Name, src.URL))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// GenerateDefaultFiles 生成空的默认文件（用于首次启动无数据时）
func (g *Generator) GenerateDefaultFiles() error {
	outputDir := g.cfg.Output.OutputDir
	baseFilename := strings.TrimSuffix(g.cfg.Output.Filename, ".m3u")

	defaultM3U := `#EXTM3U
#EXTINF:-1 tvg-id="default" tvg-name="默认频道" group-title="系统消息",默认频道
https://example.com/default`

	defaultTXT := `# 系统消息
默认频道,https://example.com/default`

	m3uPath := filepath.Join(outputDir, baseFilename+".m3u")
	txtPath := filepath.Join(outputDir, baseFilename+".txt")

	if err := os.WriteFile(m3uPath, []byte(defaultM3U), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(txtPath, []byte(defaultTXT), 0644); err != nil {
		return err
	}
	g.log.Info("已生成默认播放列表文件")
	return nil
}

// UpdateTimestampFile 在输出目录生成时间戳文件，供外部检测更新
func (g *Generator) UpdateTimestampFile() {
	tsPath := filepath.Join(g.cfg.Output.OutputDir, ".last_update")
	os.WriteFile(tsPath, []byte(time.Now().Format(time.RFC3339)), 0644)
}
