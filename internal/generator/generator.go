// internal/generator/generator.go
// 播放列表生成器，负责根据过滤后的数据生成 M3U/TXT 文件

package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// Generator 负责生成各种格式的播放列表
type Generator struct {
	cfg     *config.Config
	db      *db.DB
	filter  *filter.Filter
}

// NewGenerator 创建生成器实例
func NewGenerator(cfg *config.Config, database *db.DB, f *filter.Filter) *Generator {
	return &Generator{
		cfg:    cfg,
		db:     database,
		filter: f,
	}
}

// Generate 执行完整的生成流程
func (g *Generator) Generate() error {
	logger.Info("开始生成播放列表...")
	sources, err := g.db.GetActiveSources()
	if err != nil {
		return fmt.Errorf("获取活跃源失败: %w", err)
	}

	// 应用全局过滤器（黑白名单）
	sources = g.filter.Apply(sources)

	// 根据显示规则分组
	groups, err := g.groupByDisplayRules(sources)
	if err != nil {
		return fmt.Errorf("显示规则分组失败: %w", err)
	}

	// 生成 M3U 文件
	if err := g.generateM3U(groups); err != nil {
		return fmt.Errorf("生成M3U失败: %w", err)
	}

	// 生成 TXT 文件
	if err := g.generateTXT(groups); err != nil {
		return fmt.Errorf("生成TXT失败: %w", err)
	}

	logger.Info("播放列表生成完成")
	return nil
}

// groupByDisplayRules 根据数据库中的显示规则对源进行分组
// 规则按优先级排序，第一个匹配到的规则决定该源所属的显示组。
// 未匹配任何规则的源归入“未分类”。
func (g *Generator) groupByDisplayRules(sources []models.Source) (map[string][]models.Source, error) {
	rules, err := g.db.GetDisplayRules()
	if err != nil {
		return nil, fmt.Errorf("获取显示规则失败: %w", err)
	}

	// 编译正则表达式，缓存避免重复编译
	compiledRules := make([]struct {
		regex   *regexp.Regexp
		group   string
		pattern string
	}, 0, len(rules))

	for _, r := range rules {
		re, err := regexp.Compile(r.MatchPattern)
		if err != nil {
			logger.Warn("无效的显示规则正则 '%s': %v", r.MatchPattern, err)
			continue
		}
		compiledRules = append(compiledRules, struct {
			regex   *regexp.Regexp
			group   string
			pattern string
		}{
			regex:   re,
			group:   r.DisplayGroup,
			pattern: r.MatchPattern,
		})
	}

	groups := make(map[string][]models.Source)
	for _, src := range sources {
		assigned := false
		for _, cr := range compiledRules {
			if cr.regex.MatchString(src.Name) {
				groups[cr.group] = append(groups[cr.group], src)
				assigned = true
				break
			}
		}
		if !assigned {
			groups["未分类"] = append(groups["未分类"], src)
		}
	}
	return groups, nil
}

// generateM3U 将分组数据写入 M3U 文件
func (g *Generator) generateM3U(groups map[string][]models.Source) error {
	filePath := filepath.Join(g.cfg.Generator.OutputDir, "playlist.m3u")
	os.MkdirAll(filepath.Dir(filePath), 0o755)
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	f.WriteString("#EXTM3U\n")
	for group, srcs := range groups {
		f.WriteString(fmt.Sprintf("#PLAYLIST:%s\n", group))
		for _, s := range srcs {
			// #EXTINF:-1 group-title="xxx",Channel Name
			f.WriteString(fmt.Sprintf("#EXTINF:-1 group-title=\"%s\",%s\n%s\n", group, s.Name, s.URL))
		}
	}
	return nil
}

// generateTXT 生成简单的 TXT 列表（每行：名称,URL）
func (g *Generator) generateTXT(groups map[string][]models.Source) error {
	filePath := filepath.Join(g.cfg.Generator.OutputDir, "playlist.txt")
	os.MkdirAll(filepath.Dir(filePath), 0o755)
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	for group, srcs := range groups {
		f.WriteString(fmt.Sprintf("# %s\n", group))
		for _, s := range srcs {
			f.WriteString(fmt.Sprintf("%s,%s\n", s.Name, s.URL))
		}
	}
	return nil
}
