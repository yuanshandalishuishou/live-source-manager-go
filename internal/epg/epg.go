// internal/epg/epg.go
// EPG 管理器（优化版）—— 修复 exportXML 和 parseXMLTV 空实现问题。
package epg

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// ...（保留原有 Manager、NewManager、Start、Stop、loop、UpdateNow、update 等方法）...

// Program EPG 节目结构体
type Program struct {
	XMLName     xml.Name `xml:"programme"`
	Channel     string   `xml:"channel,attr"`
	StartTime   XmlTime  `xml:"start,attr"`
	StopTime    XmlTime  `xml:"stop,attr"`
	Title       string   `xml:"title"`
	Description string   `xml:"desc,omitempty"`
	Category    string   `xml:"category,omitempty"`
}

// XmlTime 自定义时间类型，用于 XML 序列化
type XmlTime struct{ time.Time }

func (t XmlTime) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {
	return xml.Attr{
		Name:  name,
		Value: t.Format("20060102150405 -0700"),
	}, nil
}

// exportXML 导出标准 XMLTV 格式的 EPG 文件
func (m *Manager) exportXML(progs []Program) error {
	if len(progs) == 0 {
		logger.Info("没有 EPG 数据需要导出")
		return nil
	}

	// 确定输出路径
	outDir := m.cfg.Output.Directory
	if outDir == "" {
		outDir = "/www/output"
	}
	outPath := filepath.Join(outDir, "epg.xml")

	// 构建 XMLTV 结构
	type TV struct {
		XMLName  xml.Name  `xml:"tv"`
		Channels []Channel `xml:"channel"`
		Programs []Program `xml:"programme"`
	}

	// 收集所有不重复的频道 ID
	channelSet := make(map[string]bool)
	for _, p := range progs {
		channelSet[p.Channel] = true
	}
	channels := make([]Channel, 0, len(channelSet))
	for id := range channelSet {
		channels = append(channels, Channel{
			ID:          id,
			DisplayName: id,
		})
	}

	tv := TV{
		Channels: channels,
		Programs: progs,
	}

	// 序列化为 XML
	output, err := xml.MarshalIndent(tv, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 EPG XML 失败: %w", err)
	}

	// 添加 XML 声明
	finalOutput := append([]byte(xml.Header), output...)

	// 写入文件
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if err := os.WriteFile(outPath, finalOutput, 0644); err != nil {
		return fmt.Errorf("写入 EPG 文件失败: %w", err)
	}

	logger.Info("EPG XML 文件已导出: %s (节目数: %d)", outPath, len(progs))
	return nil
}

// Channel XMLTV 频道定义
type Channel struct {
	XMLName     xml.Name `xml:"channel"`
	ID          string   `xml:"id,attr"`
	DisplayName string   `xml:"display-name"`
}

// parseXMLTV 解析 XMLTV 格式的 EPG 数据
func parseXMLTV(data []byte) ([]Program, error) {
	type TV struct {
		XMLName  xml.Name  `xml:"tv"`
		Programs []Program `xml:"programme"`
	}

	var tv TV
	if err := xml.Unmarshal(data, &tv); err != nil {
		return nil, fmt.Errorf("解析 XMLTV 失败: %w", err)
	}

	// 过滤掉没有标题的节目
	progs := make([]Program, 0, len(tv.Programs))
	for _, p := range tv.Programs {
		if p.Title != "" {
			progs = append(progs, p)
		}
	}

	logger.Info("解析 EPG 数据: %d 个节目", len(progs))
	return progs, nil
}

// updateChannelMapping 将 EPG 频道名称与已通过的源进行模糊匹配并更新映射
func (m *Manager) updateChannelMapping(progs []Program) {
	// 提取唯一的 EPG 频道 ID 列表
	epgIDs := make(map[string]bool)
	for _, p := range progs {
		epgIDs[p.Channel] = true
	}
	if len(epgIDs) == 0 {
		return
	}

	// 查询所有活跃源
	sources, err := m.db.GetActivePassedSources()
	if err != nil {
		logger.Error("获取活跃源失败，跳过频道映射: %v", err)
		return
	}

	// 模糊匹配：对每个源名称，查找匹配的 EPG 频道
	for _, src := range sources {
		matchedID := ""
		for id := range epgIDs {
			if contains(src.Name, id) || contains(id, src.Name) {
				matchedID = id
				break
			}
		}
		if matchedID != "" {
			if err := m.db.UpdateSourceEPGID(src.ID, matchedID); err != nil {
				logger.Warn("更新源 EPG 映射失败: %v", err)
			}
		}
	}
	logger.Info("频道映射更新完成")
}

// contains 简单的子串匹配（不区分大小写）
func contains(a, b string) bool {
	return len(a) >= len(b) && searchSubstring(a, b)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c1 := s[i+j]
			c2 := substr[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
