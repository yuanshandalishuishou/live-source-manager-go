package epg

// XMLTV 流式解析器。
//
// 相比 Python 版（xml.etree.iterparse）的改进：
//   - 用 encoding/xml.Decoder 逐 token 解析，内存占用与文件大小无关；
//   - gzip 按魔数嗅探（不依赖 .gz 后缀），Python 版同样做了，这里保留；
//   - 解析出错时只丢弃当前节点、继续往下走，不让一条脏数据毁掉整个源。

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"live-source-manager-go/internal/types"
)

// xmlTVTimeLayouts 是 XMLTV 时间戳的候选格式（带/不带时区、长短形式）。
var xmlTVTimeLayouts = []string{
	"20060102150405 -0700",
	"20060102150405-0700",
	"20060102150405 -07:00",
	"20060102150405Z",
	"20060102150405",
	"200601021504 -0700",
	"200601021504",
	"2006010215 -0700",
	"2006010215",
}

// ParseXMLTVTime 解析 XMLTV 的时间字符串，例如 "20240115120000 +0800"。
// 无时区信息时按 defaultLoc 解释。
func ParseXMLTVTime(s string, defaultLoc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("空时间戳")
	}
	if defaultLoc == nil {
		defaultLoc = time.UTC
	}
	for _, layout := range xmlTVTimeLayouts {
		if strings.Contains(layout, "-0700") || strings.Contains(layout, "Z") {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, s, defaultLoc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间戳: %q", s)
}

// ToUTCStr 把时间转成库内统一存储格式（UTC，"2006-01-02 15:04:05"）。
func ToUTCStr(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// ParseUTCStr 把库内 UTC 字符串还原为 time.Time。
func ParseUTCStr(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.UTC)
}

// UTCStrToXMLTV 把库内 UTC 字符串转成 XMLTV 时间戳（带目标时区偏移）。
func UTCStrToXMLTV(s string, loc *time.Location) string {
	t, err := ParseUTCStr(s)
	if err != nil {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("20060102150405 -0700")
}

// LoadLocation 加载时区，失败时回落到东八区固定偏移（与 Python 版一致，
// 避免容器里缺 tzdata 直接把整个 EPG 打挂）。
func LoadLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		return time.FixedZone("CST", 8*3600)
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}

// cnDigits 用于把中文数字频道名归一（"央视一套" → "央视1套"）。
var cnDigits = strings.NewReplacer(
	"零", "0", "一", "1", "二", "2", "三", "3", "四", "4",
	"五", "5", "六", "6", "七", "7", "八", "8", "九", "9",
)

// qualitySuffixes 是需要在归一化时剥掉的画质后缀。
var qualitySuffixes = []string{
	"超高清", "超清", "高清", "标清", "蓝光", "1080p", "1080i", "720p", "576i", "480p",
	"4k", "8k", "uhd", "fhd", "hd", "sd", "iptv", "直播", "频道",
}

// NormalizeChannelName 把频道名归一，用于 EPG 频道与本地频道的模糊对齐。
// 步骤：全角转半角 → 去所有空白与常见分隔符 → 中文数字转阿拉伯 → 剥画质后缀 → 转小写。
func NormalizeChannelName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		// 全角字符转半角
		switch {
		case r == 0x3000:
			continue // 全角空格直接丢
		case r > 0xFF00 && r < 0xFF5F:
			r -= 0xFEE0
		}
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '-', '_', '.', '·', '|', '(', ')', '[', ']', '（', '）', '【', '】', ':', '：', '/', '\\':
			continue
		}
		b.WriteRune(r)
	}
	s := cnDigits.Replace(b.String())
	s = strings.ToLower(s)
	// 反复剥离尾部画质后缀（"cctv1高清hd" → "cctv1"）
	for changed := true; changed; {
		changed = false
		for _, suf := range qualitySuffixes {
			if len(s) > len(suf) && strings.HasSuffix(s, suf) {
				s = s[:len(s)-len(suf)]
				changed = true
			}
		}
	}
	return s
}

// ParseResult 是一次 XMLTV 解析的产物。
type ParseResult struct {
	Channels   []types.EPGChannel
	Programmes []types.EPGProgramme
	Skipped    int // 因时间窗/脏数据被丢弃的节目数
}

// OpenStream 按魔数判断是否 gzip，返回可直接读取 XML 的 Reader。
// 调用方负责关闭返回的 closer（可能为 nil）。
func OpenStream(r io.Reader) (io.Reader, io.Closer, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	magic, err := br.Peek(2)
	if err != nil && err != io.EOF {
		return nil, nil, err
	}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip 解压失败: %w", err)
		}
		return gz, gz, nil
	}
	return br, nil, nil
}

// ParseStream 流式解析 XMLTV，仅保留 [windowStart, windowStop] 内有重叠的节目。
// windowStart/windowStop 为零值时不做时间过滤。
func ParseStream(r io.Reader, defaultLoc *time.Location, windowStart, windowStop time.Time) (*ParseResult, error) {
	src, closer, err := OpenStream(r)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}

	res := &ParseResult{Channels: []types.EPGChannel{}, Programmes: []types.EPGProgramme{}}
	dec := xml.NewDecoder(src)
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	filterTime := !windowStart.IsZero() && !windowStop.IsZero()

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// 遇到脏 XML 时，已解析的部分仍然可用，直接返回而不是全盘丢弃。
			if len(res.Channels) > 0 || len(res.Programmes) > 0 {
				return res, nil
			}
			return nil, fmt.Errorf("XML 解析失败: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			var c xmlChannel
			if err := dec.DecodeElement(&c, &se); err != nil {
				continue
			}
			ch := c.toEPGChannel()
			if ch.TVGID != "" {
				res.Channels = append(res.Channels, ch)
			}
		case "programme":
			var p xmlProgramme
			if err := dec.DecodeElement(&p, &se); err != nil {
				continue
			}
			prog, ok := p.toEPGProgramme(defaultLoc)
			if !ok {
				res.Skipped++
				continue
			}
			if filterTime {
				st, err1 := ParseUTCStr(prog.StartUTC)
				sp, err2 := ParseUTCStr(prog.StopUTC)
				if err1 == nil && err2 == nil {
					// 只保留与窗口有重叠的节目
					if !st.Before(windowStop) || !sp.After(windowStart) {
						res.Skipped++
						continue
					}
				}
			}
			res.Programmes = append(res.Programmes, prog)
		}
	}
	return res, nil
}

// ── XML 结构体 ─────────────────────────────────────────────────────────────

type xmlIcon struct {
	Src string `xml:"src,attr"`
}

type xmlText struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type xmlChannel struct {
	ID          string    `xml:"id,attr"`
	DisplayName []xmlText `xml:"display-name"`
	Icon        []xmlIcon `xml:"icon"`
}

func (c xmlChannel) toEPGChannel() types.EPGChannel {
	ch := types.EPGChannel{TVGID: strings.TrimSpace(c.ID)}
	for i, dn := range c.DisplayName {
		v := strings.TrimSpace(dn.Value)
		if v == "" {
			continue
		}
		if i == 0 || ch.DisplayName == "" {
			ch.DisplayName = v
		} else {
			ch.Aliases = append(ch.Aliases, v)
		}
	}
	if ch.DisplayName == "" {
		ch.DisplayName = ch.TVGID
	}
	for _, ic := range c.Icon {
		if s := strings.TrimSpace(ic.Src); s != "" {
			ch.Icon = s
			break
		}
	}
	return ch
}

type xmlEpisodeNum struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}

type xmlProgramme struct {
	Start      string          `xml:"start,attr"`
	Stop       string          `xml:"stop,attr"`
	Channel    string          `xml:"channel,attr"`
	Title      []xmlText       `xml:"title"`
	SubTitle   []xmlText       `xml:"sub-title"`
	Desc       []xmlText       `xml:"desc"`
	Category   []xmlText       `xml:"category"`
	EpisodeNum []xmlEpisodeNum `xml:"episode-num"`
	Icon       []xmlIcon       `xml:"icon"`
}

func firstText(list []xmlText) string {
	for _, t := range list {
		if v := strings.TrimSpace(t.Value); v != "" {
			return v
		}
	}
	return ""
}

func (p xmlProgramme) toEPGProgramme(loc *time.Location) (types.EPGProgramme, bool) {
	tvgID := strings.TrimSpace(p.Channel)
	if tvgID == "" {
		return types.EPGProgramme{}, false
	}
	start, err := ParseXMLTVTime(p.Start, loc)
	if err != nil {
		return types.EPGProgramme{}, false
	}
	stop, err := ParseXMLTVTime(p.Stop, loc)
	if err != nil {
		// 缺 stop 的节目按 1 小时兜底，避免整条丢失。
		stop = start.Add(time.Hour)
	}
	if !stop.After(start) {
		stop = start.Add(time.Hour)
	}
	prog := types.EPGProgramme{
		TVGID:       tvgID,
		StartUTC:    ToUTCStr(start),
		StopUTC:     ToUTCStr(stop),
		Title:       firstText(p.Title),
		SubTitle:    firstText(p.SubTitle),
		Description: firstText(p.Desc),
		Category:    firstText(p.Category),
	}
	for _, e := range p.EpisodeNum {
		if v := strings.TrimSpace(e.Value); v != "" {
			prog.Episode = v
			break
		}
	}
	for _, ic := range p.Icon {
		if s := strings.TrimSpace(ic.Src); s != "" {
			prog.Icon = s
			break
		}
	}
	if prog.Title == "" {
		prog.Title = "未知节目"
	}
	return prog, true
}

// WriteXMLTV 把频道与节目导出为标准 XMLTV 文档。
func WriteXMLTV(w io.Writer, channels []types.EPGChannel, programmes []types.EPGProgramme, loc *time.Location) error {
	bw := bufio.NewWriterSize(w, 128*1024)
	defer bw.Flush()

	if _, err := bw.WriteString(xml.Header); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(bw,
		"<tv generator-info-name=\"live-source-manager-go\" generator-info-url=\"https://github.com/yuanshandalishuishou/live-source-manager-go\">\n"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range channels {
		if c.TVGID == "" || seen[c.TVGID] {
			continue
		}
		seen[c.TVGID] = true
		fmt.Fprintf(bw, "  <channel id=%s>\n", xmlAttr(c.TVGID))
		fmt.Fprintf(bw, "    <display-name>%s</display-name>\n", xmlEscape(c.DisplayName))
		if c.MatchedChannel != "" && c.MatchedChannel != c.DisplayName {
			fmt.Fprintf(bw, "    <display-name>%s</display-name>\n", xmlEscape(c.MatchedChannel))
		}
		if c.Icon != "" {
			fmt.Fprintf(bw, "    <icon src=%s />\n", xmlAttr(c.Icon))
		}
		bw.WriteString("  </channel>\n")
	}
	for _, p := range programmes {
		start := UTCStrToXMLTV(p.StartUTC, loc)
		stop := UTCStrToXMLTV(p.StopUTC, loc)
		if start == "" {
			continue
		}
		fmt.Fprintf(bw, "  <programme start=%s stop=%s channel=%s>\n",
			xmlAttr(start), xmlAttr(stop), xmlAttr(p.TVGID))
		fmt.Fprintf(bw, "    <title>%s</title>\n", xmlEscape(p.Title))
		if p.SubTitle != "" {
			fmt.Fprintf(bw, "    <sub-title>%s</sub-title>\n", xmlEscape(p.SubTitle))
		}
		if p.Description != "" {
			fmt.Fprintf(bw, "    <desc>%s</desc>\n", xmlEscape(p.Description))
		}
		if p.Category != "" {
			fmt.Fprintf(bw, "    <category>%s</category>\n", xmlEscape(p.Category))
		}
		if p.Episode != "" {
			fmt.Fprintf(bw, "    <episode-num system=\"onscreen\">%s</episode-num>\n", xmlEscape(p.Episode))
		}
		if p.Icon != "" {
			fmt.Fprintf(bw, "    <icon src=%s />\n", xmlAttr(p.Icon))
		}
		bw.WriteString("  </programme>\n")
	}
	_, err := bw.WriteString("</tv>\n")
	return err
}

// xmlEscape 转义 XML 文本内容。
func xmlEscape(s string) string {
	var b strings.Builder
	// 剔除 XML 1.0 非法控制字符，避免生成出播放器读不了的文档。
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			return -1
		}
		return r
	}, s)
	_ = xml.EscapeText(&b, []byte(clean))
	return b.String()
}

// xmlAttr 生成带引号的属性值（xml.EscapeText 已把 " 转成 &#34;）。
func xmlAttr(s string) string {
	return `"` + xmlEscape(s) + `"`
}
