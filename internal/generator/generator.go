package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/filter"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// Generator 负责生成最终的直播源播放列表文件
type Generator struct {
	cfg    *config.Config
	db     *db.DB
	filter *filter.Filter
}

// NewGenerator 构造函数
func NewGenerator(cfg *config.Config, database *db.DB, f *filter.Filter) *Generator {
	return &Generator{
		cfg:    cfg,
		db:     database,
		filter: f,
	}
}

// Generate 全量生成输出文件（live.m3u）
func (g *Generator) Generate() error {
	// 确保输出目录存在
	outDir := g.cfg.Output.Directory
	if outDir == "" {
		outDir = "/www/output"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 尝试热重载过滤规则
	if err := g.filter.ReloadIfNeed(); err != nil {
		logger.Warn("重新加载过滤规则失败", "error", err)
	}

	// 查询通过测试的活跃源
	sources, err := g.db.GetActivePassedSources()
	if err != nil {
		return fmt.Errorf("获取活跃源失败: %w", err)
	}
	if len(sources) == 0 {
		logger.Info("没有活跃的源，生成空播放列表")
		return g.writeEmptyM3U(outDir)
	}

	// 应用黑白名单过滤
	sources = g.filter.Apply(sources)

	// 按显示规则排序、分组
	grouped := g.groupByDisplayRules(sources)

	// 写入文件
	outputPath := filepath.Join(outDir, g.cfg.Output.Filename)
	if g.cfg.Output.Filename == "" {
		outputPath = filepath.Join(outDir, "live.m3u")
	}
	return g.writeM3U(outputPath, grouped)
}

// writeM3U 将分组后的源写入 M3U 文件
func (g *Generator) writeM3U(path string, groups []sourceGroup) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	f.WriteString("#EXTM3U\n")
	for _, grp := range groups {
		if grp.Name != "" && len(grp.Sources) > 0 {
			fmt.Fprintf(f, "#EXTINF:-1 group-title=\"%s\",%s\n", grp.Name, grp.Name)
		}
		for _, src := range grp.Sources {
			// 写入 EXTINF
			logo := ""
			if src.TvgLogo != "" {
				logo = fmt.Sprintf("tvg-logo=\"%s\" ", src.TvgLogo)
			}
			tvgID := ""
			if src.TvgID != "" {
				tvgID = fmt.Sprintf("tvg-id=\"%s\" ", src.TvgID)
			}
			epgLine := fmt.Sprintf("#EXTINF:-1 %s%sgroup-title=\"%s\",%s\n", logo, tvgID, grp.Name, src.Name)
			f.WriteString(epgLine)
			f.WriteString(src.URL + "\n")
		}
	}
	return nil
}

// writeEmptyM3U 写入仅有头部的空文件
func (g *Generator) writeEmptyM3U(dir string) error {
	path := filepath.Join(dir, g.cfg.Output.Filename)
	return os.WriteFile(path, []byte("#EXTM3U\n"), 0644)
}

// sourceGroup 分组结构
type sourceGroup struct {
	Name    string
	Sources []models.PassedSource
}

// groupByDisplayRules 根据 display_rule 和 categories 对源排序分组
func (g *Generator) groupByDisplayRules(sources []models.PassedSource) []sourceGroup {
	// 从数据库获取分组规则
	rules, err := g.db.GetDisplayRules()
	if err != nil || len(rules) == 0 {
		// 回退到按现有 group_title 分组
		groupMap := make(map[string][]models.PassedSource)
		for _, s := range sources {
			group := s.GroupTitle
			if group == "" {
				group = "未分组"
			}
			groupMap[group] = append(groupMap[group], s)
		}
		var groups []sourceGroup
		for name, srcs := range groupMap {
			groups = append(groups, sourceGroup{Name: name, Sources: srcs})
		}
		return groups
	}

	// 构建分类 -> 源 的映射
	catSources := make(map[int][]models.PassedSource)
	for _, s := range sources {
		for _, catID := range s.CategoryIDs {
			catSources[catID] = append(catSources[catID], s)
		}
	}

	var groups []sourceGroup
	for _, rule := range rules {
		if !rule.Enable {
			continue
		}
		name := rule.GroupNameOverride
		if name == "" {
			name = rule.CategoryName
		}
		srcs := catSources[rule.CategoryID]
		// 排序
		if rule.ItemSortOrder == "1" {
			sort.Slice(srcs, func(i, j int) bool { return srcs[i].Name < srcs[j].Name })
		}
		if len(srcs) > 0 || !rule.HideEmptyGroups {
			groups = append(groups, sourceGroup{Name: name, Sources: srcs})
		}
	}
	return groups
}
