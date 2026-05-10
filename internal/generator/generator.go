// internal/generator/generator.go
// 播放列表生成器，负责根据过滤后的数据生成 M3U/TXT 文件
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// Generator 负责生成各种格式的播放列表
type Generator struct {
	cfg    *config.Config
	db     *db.DB
	filter *filter.Filter
}

// NewGenerator 创建生成器实例
func NewGenerator(cfg *config.Config, database *db.DB, f *filter.Filter) *Generator {
	return &Generator{
		cfg:    cfg,
		db:     database,
		filter: f,
	}
}

// Generate 执行完整的生成流程，输出 M3U 和 TXT 格式文件
func (g *Generator) Generate() error {
	logger.Info("开始生成播放列表...")

	// 1. 获取活跃源
	sources, err := g.db.GetActiveSources()
	if err != nil {
		return fmt.Errorf("获取活跃源失败: %w", err)
	}

	// 2. 应用全局过滤器（黑白名单）
	sources = g.filter.Apply(sources)

	// 3. 根据显示规则分组
	groups, err := g.groupByDisplayRules(sources)
	if err != nil {
		return fmt.Errorf("显示规则分组失败: %w", err)
	}

	// 4. 确保输出目录存在（使用配置中的路径）
	outputDir := g.cfg.Generator.OutputDir
	if outputDir == "" {
		outputDir = "www/output/"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 5. 生成完整版 M3U 文件（包含所有有效源）
	if err := g.generateM3U(groups, filepath.Join(outputDir, "live.m3u")); err != nil {
		return fmt.Errorf("生成完整版 M3U 失败: %w", err)
	}

	// 6. 生成精选版 M3U（仅保留高质量源，根据配置限制每个频道数量）
	qualifiedGroups := g.getQualifiedSources(groups)
	if err := g.generateM3U(qualifiedGroups, filepath.Join(outputDir, "qualified_live.m3u")); err != nil {
		return fmt.Errorf("生成精选版 M3U 失败: %w", err)
	}

	// 7. 生成 TXT 格式文件
	if err := g.generateTXT(groups, filepath.Join(outputDir, "live.txt")); err != nil {
		return fmt.Errorf("生成 TXT 失败: %w", err)
	}

	logger.Info("播放列表生成完成")
	return nil
}

// groupByDisplayRules 根据数据库中的显示规则对源进行分组
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

// getQualifiedSources 根据质量筛选条件和每频道最大源数限制生成精选源
func (g *Generator) getQualifiedSources(groups map[string][]models.Source) map[string][]models.Source {
	qualified := make(map[string][]models.Source)
	maxPerChannel := g.cfg.Generator.MaxSourcesPerChannel
	if maxPerChannel <= 0 {
		maxPerChannel = 3
	}

	for group, sources := range groups {
		// 按延迟升序排序（延迟低的优先）
		sort.Slice(sources, func(i, j int) bool {
			return sources[i].Latency < sources[j].Latency
		})

		// 应用分辨率过滤（如果配置了最低分辨率要求）
		filtered := make([]models.Source, 0)
		for _, src := range sources {
			if g.cfg.Filter.MinResolution != "" {
				// 简单的分辨率比较，实际应解析后比较
				if !strings.Contains(src.Resolution, strings.ReplaceAll(g.cfg.Filter.MinResolution, "x", "×")) {
					continue
				}
			}
			filtered = append(filtered, src)
		}

		// 限制每个频道的源数量
		if len(filtered) > maxPerChannel {
			filtered = filtered[:maxPerChannel]
		}

		if len(filtered) > 0 {
			qualified[group] = filtered
		}
	}

	return qualified
}

// generateM3U 将分组数据写入 M3U 文件（带 EXTM3U 头部和分组信息）
func (g *Generator) generateM3U(groups map[string][]models.Source, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// 写入 M3U 头部
	f.WriteString("#EXTM3U\n")
	f.WriteString("#PLAYLIST: 直播源管理工具 - 自动生成\n")
	f.WriteString(fmt.Sprintf("#DATE: %s\n\n", fmt.Sprintf("%s", "..."))) // 可添加日期信息

	for group, srcs := range groups {
		// 写入分组标题
		f.WriteString(fmt.Sprintf("#PLAYLIST:%s\n", group))
		for _, s := range srcs {
			// 构建 EXINF 标签
			extinf := fmt.Sprintf("#EXTINF:-1 group-title=\"%s\"", group)
			if s.Resolution != "" {
				extinf += fmt.Sprintf(" resolution=\"%s\"", s.Resolution)
			}
			if s.Bitrate > 0 {
				bitrateMbps := float64(s.Bitrate) / 1000000.0
				extinf += fmt.Sprintf(" bitrate=\"%.1fMbps\"", bitrateMbps)
			}
			extinf += "," + s.Name + "\n"
			f.WriteString(extinf)
			f.WriteString(s.URL + "\n")
		}
		f.WriteString("\n") // 分组之间空行分隔
	}

	return nil
}

// generateTXT 生成简单的 TXT 格式播放列表（频道名,URL）
func (g *Generator) generateTXT(groups map[string][]models.Source, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	for group, srcs := range groups {
		f.WriteString(fmt.Sprintf("# 分类: %s\n", group))
		for _, s := range srcs {
			f.WriteString(fmt.Sprintf("%s,%s\n", s.Name, s.URL))
		}
		f.WriteString("\n")
	}

	return nil
}
