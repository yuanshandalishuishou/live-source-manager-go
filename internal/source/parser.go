// internal/source/parser.go
// 解析 M3U 和 TXT 格式的订阅源文件，输出统一的 URLSource 列表。
// 支持常见的 #EXTINF 标签属性提取：group-title, tvg-id, tvg-logo 等。

package source

import (
	"bufio"
	"io"
	"regexp"
	"strings"

	"live-source-manager-go/internal/models"
)

// Parser 负责解析流。
type Parser struct{}

// NewParser 创建解析器。
func NewParser() *Parser {
	return &Parser{}
}

// extinfLine 用于提取 #EXTINF 行中的属性。
var extinfAttrRegex = regexp.MustCompile(`([\w-]+)="([^"]*)"`)

// Parse M3U 格式的 reader，返回所有解析出的 URLSource。
func (p *Parser) Parse(r io.Reader) ([]models.URLSource, error) {
	var entries []models.URLSource
	scanner := bufio.NewScanner(r)

	var extinf string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			extinf = line
			continue
		}

		// 跳过其他注释行
		if strings.HasPrefix(line, "#") {
			extinf = "" // EXTINF 后必须紧跟 URL，否则忽略
			continue
		}

		// 这是一个 URL 行
		if extinf != "" {
			entry := parseExtinfLine(extinf)
			entry.URL = line
			entries = append(entries, entry)
		} else {
			// 无 EXTINF 的 URL 行，使用默认值
			entries = append(entries, models.URLSource{
				Name: line,
				URL:  line,
			})
		}
		extinf = "" // 重置
	}

	return entries, scanner.Err()
}

// parseExtinfLine 从 #EXTINF:-1 ... 行中提取名称和属性。
func parseExtinfLine(line string) models.URLSource {
	// 移除 "#EXTINF:-1 " 前缀，示例：#EXTINF:-1 tvg-id="cctv1" group-title="央视",CCTV-1
	content := strings.TrimPrefix(line, "#EXTINF:-1 ")
	// 分割逗号，最后一个逗号后为频道名
	commaIdx := strings.LastIndex(content, ",")
	var name string
	if commaIdx != -1 {
		name = strings.TrimSpace(content[commaIdx+1:])
		content = content[:commaIdx]
	} else {
		name = content
		content = ""
	}

	entry := models.URLSource{Name: name}

	// 提取属性
	matches := extinfAttrRegex.FindAllStringSubmatch(content, -1)
	attrs := make(map[string]string)
	for _, m := range matches {
		if len(m) == 3 {
			attrs[strings.ToLower(m[1])] = m[2]
		}
	}
	if v, ok := attrs["group-title"]; ok {
		entry.GroupTitle = v
	}
	if v, ok := attrs["tvg-id"]; ok {
		entry.TvgID = v
	}
	if v, ok := attrs["tvg-logo"]; ok {
		entry.TvgLogo = v
	}
	return entry
}
