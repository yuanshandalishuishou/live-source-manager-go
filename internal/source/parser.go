package source

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// Parser 解析 M3U / TXT 格式的源列表
type Parser struct{}

// NewParser 创建解析器
func NewParser() *Parser {
	return &Parser{}
}

// Parse 从字节内容解析为 URLSource 列表
func (p *Parser) Parse(content []byte) ([]models.URLSource, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("空内容")
	}

	// 移除 BOM
	content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})

	scanner := bufio.NewScanner(bytes.NewReader(content))
	var entries []models.URLSource

	var currentName, currentTvgID, currentTvgLogo, currentGroup, currentURL string
	var currentUA, currentRawAttrs string

parseLoop:
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// M3U 头部
		if strings.HasPrefix(line, "#EXTINF:") {
			// 例如: #EXTINF:-1 tvg-id="cctv1" tvg-name="CCTV1" group-title="央视",CCTV1
			infoPart := line[len("#EXTINF:"):]

			// 解析属性
			attrs, displayName := extractAttributes(infoPart)
			currentName = displayName
			currentTvgID = attrs["tvg-id"]
			currentTvgLogo = attrs["tvg-logo"]
			currentGroup = attrs["group-title"]
			currentUA = attrs["user-agent"]
			currentRawAttrs = marshalAttrs(attrs)

		} else if !strings.HasPrefix(line, "#") {
			// URL 行
			currentURL = line
			if currentURL != "" && currentName != "" {
				entries = append(entries, models.URLSource{
					URL:           currentURL,
					Name:          currentName,
					TvgID:         currentTvgID,
					TvgLogo:       currentTvgLogo,
					GroupTitle:    currentGroup,
					UserAgent:     currentUA,
					RawAttributes: currentRawAttrs,
					SourceType:    guessType(currentURL),
				})
			}
			// 重置
			currentName, currentTvgID, currentTvgLogo, currentGroup, currentURL, currentUA, currentRawAttrs = "", "", "", "", "", "", ""
		} else if strings.HasPrefix(line, "#EXTINF") {
			// 另一形式，按需处理
			continue
		}
	}

	// 处理 TXT 格式：每行可能是 url,name 或 name,url
	if len(entries) == 0 {
		entries = p.parseTXT(content)
	}

	return entries, scanner.Err()
}

// parseTXT 针对简单逗号分隔文本
func (p *Parser) parseTXT(content []byte) []models.URLSource {
	var entries []models.URLSource
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) == 2 {
			url := strings.TrimSpace(parts[0])
			name := strings.TrimSpace(parts[1])
			if strings.HasPrefix(url, "http") || strings.HasPrefix(url, "rtmp") || strings.HasPrefix(url, "udp") {
				entries = append(entries, models.URLSource{
					URL:  url,
					Name: name,
				})
			} else {
				// 可能顺序颠倒
				entries = append(entries, models.URLSource{
					URL:  name,
					Name: url,
				})
			}
		}
	}
	return entries
}

// extractAttributes 从 EXTINF 字符串中解析键值属性和显示名称
// 例如: -1 tvg-id="cctv1" group-title="央视",CCTV1
func extractAttributes(input string) (map[string]string, string) {
	attrs := make(map[string]string)
	// 提取逗号后的显示名称
	commaIdx := strings.LastIndex(input, ",")
	displayName := ""
	if commaIdx >= 0 {
		displayName = strings.TrimSpace(input[commaIdx+1:])
		input = input[:commaIdx]
	}
	// 简单解析 key="value"
	for {
		eqIdx := strings.Index(input, "=")
		if eqIdx < 0 {
			break
		}
		// 获取 key
		keyEnd := eqIdx
		keyStart := strings.LastIndexAny(input[:keyEnd], " ") + 1
		key := strings.TrimSpace(input[keyStart:keyEnd])
		// 获取 value
		rest := input[eqIdx+1:]
		if len(rest) > 0 && rest[0] == '"' {
			rest = rest[1:]
			quoteEnd := strings.Index(rest, "\"")
			if quoteEnd < 0 {
				// 未闭合引号，取到空格
				quoteEnd = strings.Index(rest, " ")
				if quoteEnd < 0 {
					quoteEnd = len(rest)
				}
			}
			value := rest[:quoteEnd]
			attrs[key] = value
			input = rest[quoteEnd:]
		} else {
			// 无引号值，取到下一个空格
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				attrs[key] = rest
				break
			}
			attrs[key] = rest[:spaceIdx]
			input = rest[spaceIdx:]
		}
	}
	return attrs, displayName
}

// guessType 根据 URL 判断源类型
func guessType(url string) string {
	if strings.HasPrefix(url, "rtmp") {
		return "rtmp"
	}
	if strings.HasPrefix(url, "udp") || strings.HasPrefix(url, "rtp") {
		return "multicast"
	}
	return "video"
}

// marshalAttrs 将属性 map 序列化为 JSON 字符串（偷懒实现）
func marshalAttrs(attrs map[string]string) string {
	if len(attrs) == 0 {
		return "{}"
	}
	var parts []string
	for k, v := range attrs {
		parts = append(parts, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
