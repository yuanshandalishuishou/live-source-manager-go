// Package source 负责源的下载、解析、别名应用、去重与入库。
package source

import (
	"bufio"
	"regexp"
	"strings"

	"live-source-manager-go/internal/models"
)

// ExtInfInfo 从 #EXTINF 行提取的信息
type ExtInfInfo struct {
	Name        string
	Logo        string
	Group       string
	TvgID       string
	TvgName     string
	UserAgent   string
	Catchup     string
	CatchupDays string
}

// ParseM3U 解析 M3U 内容，返回原始源列表
func ParseM3U(content, sourceType, sourcePath string) []models.URLSource {
	var sources []models.URLSource
	lines := strings.Split(content, "\n")

	extInfRegex := regexp.MustCompile(`^#EXTINF:`)
	attrRegex := regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)

	var currentExtInf *ExtInfInfo

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#EXTM3U") {
			continue
		}

		if extInfRegex.MatchString(line) {
			currentExtInf = parseExtInfLine(line, attrRegex)
			continue
		}

		if currentExtInf != nil && !strings.HasPrefix(line, "#") && line != "" {
			urlParts := strings.Split(line, "|")
			streamURL := urlParts[0]
			userAgent := currentExtInf.UserAgent
			if len(urlParts) > 1 {
				for _, part := range urlParts[1:] {
					if strings.HasPrefix(part, "User-Agent=") {
						userAgent = strings.TrimPrefix(part, "User-Agent=")
					}
				}
			}

			src := models.URLSource{
				URL:           streamURL,
				Name:          currentExtInf.Name,
				TvgID:         toNullString(currentExtInf.TvgID),
				TvgLogo:       toNullString(currentExtInf.Logo),
				GroupTitle:    toNullString(currentExtInf.Group),
				Catchup:       toNullString(currentExtInf.Catchup),
				CatchupDays:   toNullInt32(currentExtInf.CatchupDays),
				UserAgent:     toNullString(userAgent),
				SourceType:    "video",
				RawAttributes: toNullString(""),
			}
			sources = append(sources, src)
			currentExtInf = nil
		}
	}
	return sources
}

// ParseTXT 解析 TXT 格式（格式：频道名,URL）
func ParseTXT(content, sourceType, sourcePath string) []models.URLSource {
	var sources []models.URLSource
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		urlStr := strings.TrimSpace(parts[1])

		urlParts := strings.Split(urlStr, "|")
		streamURL := urlParts[0]
		userAgent := ""
		if len(urlParts) > 1 {
			for _, part := range urlParts[1:] {
				if strings.HasPrefix(part, "User-Agent=") {
					userAgent = strings.TrimPrefix(part, "User-Agent=")
				}
			}
		}
		src := models.URLSource{
			URL:        streamURL,
			Name:       name,
			UserAgent:  toNullString(userAgent),
			SourceType: "video",
		}
		sources = append(sources, src)
	}
	return sources
}

func parseExtInfLine(line string, attrRegex *regexp.Regexp) *ExtInfInfo {
	info := &ExtInfInfo{}
	matches := attrRegex.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) == 3 {
			key, value := match[1], match[2]
			switch strings.ToLower(key) {
			case "tvg-logo":
				info.Logo = value
			case "group-title":
				info.Group = value
			case "tvg-id":
				info.TvgID = value
			case "tvg-name":
				info.TvgName = value
			case "user-agent":
				info.UserAgent = value
			case "catchup":
				info.Catchup = value
			case "catchup-days":
				info.CatchupDays = value
			}
		}
	}
	commaIdx := strings.LastIndex(line, ",")
	if commaIdx != -1 && commaIdx < len(line)-1 {
		info.Name = strings.TrimSpace(line[commaIdx+1:])
	} else {
		info.Name = "Unknown"
	}
	if idx := strings.Index(info.Name, "|"); idx != -1 {
		info.Name = info.Name[:idx]
	}
	return info
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func toNullInt32(s string) sql.NullInt32 {
	if s == "" {
		return sql.NullInt32{}
	}
	var i int32
	// 简单转换，忽略错误
	fmt.Sscanf(s, "%d", &i)
	return sql.NullInt32{Int32: i, Valid: true}
}
