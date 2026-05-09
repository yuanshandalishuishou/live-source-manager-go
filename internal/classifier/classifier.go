// internal/classifier/classifier.go
package classifier

import (
    "bufio"
    "os"
    "strings"

    "live-source-manager-go/internal/models"
    "live-source-manager-go/pkg/logger"
)

// Classifier 根据名称关键词分配分类
type Classifier struct {
    rulesFile string
    rules     map[string]string // keyword -> category name
}

// NewClassifier 创建分类器
func NewClassifier(rulesFile string) *Classifier {
    c := &Classifier{
        rulesFile: rulesFile,
        rules:     make(map[string]string),
    }
    c.loadRules()
    return c
}

// loadRules 从规则文件读取分类规则
func (c *Classifier) loadRules() {
    if c.rulesFile == "" {
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
            c.rules[keyword] = category
        }
    }
    logger.Info("已加载 %d 条分类规则", len(c.rules))
}

// Apply 对源列表进行分类，返回带有分类的列表
func (c *Classifier) Apply(sources []models.PassedSource) []models.PassedSource {
    result := make([]models.PassedSource, 0, len(sources))
    for _, src := range sources {
        src.GroupName = c.classify(src.Name)
        result = append(result, src)
    }
    return result
}

// classify 根据名称返回分类
func (c *Classifier) classify(name string) string {
    nameLower := strings.ToLower(name)
    for keyword, category := range c.rules {
        if strings.Contains(nameLower, strings.ToLower(keyword)) {
            return category
        }
    }
    return "未分类" // 默认分类
}
