// internal/source/parser.go
// M3U / TXT 格式解析器。

package source

import (
	"bufio"
	"strings"

	"live-source-manager-go/internal/models"
)

// Parser 直播源文本解析器。
type Parser struct{}

// NewParser 创建解析器实例。
func NewParser() *Parser {
	return &Parser{}
}

// Parse 自动识别格式并解析为 URLSource 列表。
func (p *Parser) Parse(content string) []models.URLSource {
	if strings.Contains(content, "#EXTM3U") {
		return p.parseM3U(content)
	}
	return p.parseTXT(content)
}

// parseM3U 解析 M3U/M3U8 格式。
func (p *Parser) parseM3U(content string) []models.URLSource {
	var result []models.URLSource
	scanner := bufio.NewScanner(strings.NewReader(content))
	var name, group string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			name = extractName(line)
			group = extractAttr(line, `group-title="`, `"`)
		} else if !strings.HasPrefix(line, "#") {
			url := line
			if strings.HasPrefix(url, "http") || strings.HasPrefix(url, "rtmp") || strings.HasPrefix(url, "rtsp") {
				result = append(result, models.URLSource{
					URL:        url,
					Name:       name,
					GroupTitle: group,
				})
			}
		}
	}
	return result
}

// parseTXT 解析简单 TXT 格式：名称,URL。
func (p *Parser) parseTXT(content string) []models.URLSource {
	var result []models.URLSource
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) == 2 {
			result = append(result, models.URLSource{
				Name: strings.TrimSpace(parts[0]),
				URL:  strings.TrimSpace(parts[1]),
			})
		}
	}
	return result
}

// ----- 辅助函数 -----

func extractName(line string) string {
	idx := strings.LastIndex(line, ",")
	if idx >= 0 && idx+1 < len(line) {
		return strings.TrimSpace(line[idx+1:])
	}
	start := strings.Index(line, `"`)
	if start >= 0 {
		end := strings.Index(line[start+1:], `"`)
		if end >= 0 {
			return line[start+1 : start+1+end]
		}
	}
	return ""
}

func extractAttr(line, attr, endChar string) string {
	start := strings.Index(line, attr)
	if start < 0 {
		return ""
	}
	start += len(attr)
	end := strings.Index(line[start:], endChar)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}
