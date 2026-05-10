// internal/classifier/classifier.go
// 频道分类器：基于规则文件或数据库规则对频道名进行归类
package classifier

import (
	"bufio"
	"os"
	"strings"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// Classifier 根据名称关键词分配分类
type Classifier struct {
	rulesFile string
	rules     map[string]string // keyword -> category name
	db        *db.DB
}

// NewClassifier 创建分类器，优先使用数据库规则，若为空则回退到规则文件
func NewClassifier(rulesFile string, database *db.DB) *Classifier {
	c := &Classifier{
		rulesFile: rulesFile,
		rules:     make(map[string]string),
		db:        database,
	}
	c.loadRules()
	return c
}

// loadRules 从规则文件读取分类规则
func (c *Classifier) loadRules() {
	// 优先从数据库加载
	if c.db != nil {
		dbRules, err := c.db.GetCategoryRules()
		if err == nil && len(dbRules) > 0 {
			for _, r := range dbRules {
				c.rules[strings.ToLower(r.Keyword)] = r.CategoryName
			}
			logger.Info("已从数据库加载 %d 条分类规则", len(dbRules))
			return
		}
	}

	// 回退到规则文件
	if c.rulesFile == "" {
		logger.Warn("未配置分类规则文件，且数据库无规则，分类器将返回默认分类")
		return
	}
	file, err := os.Open(c.rulesFile)
	if err != nil {
		logger.Warn("分类规则文件未找到: %s", c.rulesFile)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		keyword := strings.TrimSpace(parts[0])
		category := strings.TrimSpace(parts[1])
		if keyword != "" && category != "" {
			c.rules[strings.ToLower(keyword)] = category
		}
	}
	logger.Info("已从文件加载 %d 条分类规则", len(c.rules))
}

// Apply 对源列表进行分类，返回带有分类的列表
func (c *Classifier) Apply(sources []models.PassedSource) []models.PassedSource {
	result := make([]models.PassedSource, 0, len(sources))
	for _, src := range sources {
		// 如果源已经有分类名且不是“未分类”，则保留
		if src.GroupName != "" && src.GroupName != "未分类" {
			result = append(result, src)
			continue
		}
		src.GroupName = c.classify(src.Name)
		result = append(result, src)
	}
	return result
}

// classify 根据名称返回分类
func (c *Classifier) classify(name string) string {
	nameLower := strings.ToLower(name)
	for keyword, category := range c.rules {
		if strings.Contains(nameLower, keyword) {
			return category
		}
	}
	return "未分类" // 默认分类
}
