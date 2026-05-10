// internal/generator/generator.go
// 播放列表生成器，负责根据过滤后的数据生成 M3U/TXT 文件并推送 RTMP 更新
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

	// 1. 获取活跃源（注意：这里应该使用 models.PassedSource，而不是 models.Source）
	sources, err := g.db.GetActiveSources()
	if err != nil {
		return fmt.Errorf("获取活跃源失败: %w", err)
	}

	// 2. 应用全局过滤器（黑白名单 + 质量）
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
func (g *Generator) groupByDisplayRules(sources []models.PassedSource) (map[string][]models.PassedSource, error) {
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

	groups := make(map[string][]models.PassedSource)
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
			// 按照原有 group 名分类，若无则归为“其他”
			grp := src.GroupName
			if grp == "" {
				grp = "其他"
			}
			groups[grp] = append(groups[grp], src)
		}
	}
	return groups, nil
}

// getQualifiedSources 从上一步分组中提取高质量源，并限制每个频道的数量
func (g *Generator) getQualifiedSources(groups map[string][]models.PassedSource) map[string][]models.PassedSource {
	maxPerChannel := g.cfg.Generator.MaxSourcesPerChannel
	if maxPerChannel <= 0 {
		maxPerChannel = 3
	}
	result := make(map[string][]models.PassedSource)
	for grp, srcs := range groups {
		// 按延迟排序，取前 N 个
		sort.Slice(srcs, func(i, j int) bool {
			return srcs[i].Latency < srcs[j].Latency
		})
		if len(srcs) > maxPerChannel {
			result[grp] = srcs[:maxPerChannel]
		} else {
			result[grp] = srcs
		}
	}
	return result
}

// generateM3U 生成 M3U 格式的播放列表文件
func (g *Generator) generateM3U(groups map[string][]models.PassedSource, filePath string) error {
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	// 写入 M3U 头部
	_, _ = f.WriteString("#EXTM3U\n")

	// 收集频道名称并排序，保证输出稳定
	var groupNames []string
	for grp := range groups {
		groupNames = append(groupNames, grp)
	}
	sort.Strings(groupNames)

	for _, grp := range groupNames {
		_, _ = f.WriteString(fmt.Sprintf("\n# 分组: %s\n", grp))
		for _, src := range groups[grp] {
			// 添加台标信息（如有）
			logoAttr := ""
			if src.LogoURL != "" {
				logoAttr = fmt.Sprintf(` tvg-logo="%s"`, src.LogoURL)
			}
			extinf := fmt.Sprintf(`#EXTINF:-1 tvg-name="%s" group-title="%s"%s, %s`,
				src.Name, grp, logoAttr, src.Name)
			_, _ = f.WriteString(extinf + "\n")
			_, _ = f.WriteString(src.URL + "\n")
		}
	}
	return nil
}

// generateTXT 生成 TXT 格式的播放列表文件
func (g *Generator) generateTXT(groups map[string][]models.PassedSource, filePath string) error {
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	var groupNames []string
	for grp := range groups {
		groupNames = append(groupNames, grp)
	}
	sort.Strings(groupNames)

	for _, grp := range groupNames {
		_, _ = f.WriteString(fmt.Sprintf("\n# 分组: %s\n", grp))
		for _, src := range groups[grp] {
			_, _ = f.WriteString(fmt.Sprintf("%s,%s\n", src.Name, src.URL))
		}
	}
	return nil
}
