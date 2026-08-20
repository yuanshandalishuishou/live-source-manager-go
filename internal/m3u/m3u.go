// Package m3u generates M3U playlist documents from channel records,
// porting the filtering / grouping / sorting logic of the Python
// app/m3u_generator.py (M3UGenerator) into a dependency-light Go API.
package m3u

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
	"live-source-manager-go/internal/util"
)

// Options controls how Generate / WriteFile build the playlist.
type Options struct {
	Filename           string         // e.g. "live.m3u"
	OutputDir          string         // directory to write into (WriteFile only)
	GroupBy            string         // "category" | "source" | "none"
	IncludeFailed      bool           // keep channels whose Status != "success"
	MaxPerChannel      int            // max source URLs kept per channel name
	EnableFilter       bool           // apply quality/latency/bitrate filters
	Filter             map[string]any // from cfg.GetFilterParams()
	WhitelistForceKeep bool           // whitelisted hosts bypass every filter
	SortBy             string         // "speed" | "latency" | "name"
	Whitelist          []string       // host substrings that are always kept
	Blacklist          []string       // host substrings always dropped
	UAEnabled          bool           // inject ch.UserAgent into output (UserAgents.ua_enabled)
	UAPosition         string         // "extinf" (attr) | "url" (|User-Agent= suffix)
	RefererEnabled     bool           // inject ch.Referrer into output (Referrers.referer_enabled)
	RefererPosition    string         // "extinf" (attr) | "url" (|Referer= suffix)
	// EPGURL 非空时会在 #EXTM3U 头注入 url-tvg / x-tvg-url。调用方必须已经确认
	// EPG 总开关与注入开关都为 True，否则播放器会拿到一条死链。
	EPGURL string
	// TVGInfo 是 频道名 → [tvg_id, tvg_logo]，来自 EPG 频道对齐结果。
	// 命中时优先于按频道名生成的兜底 tvg-id，让播放器能正确挂上节目单。
	TVGInfo map[string][2]string
	// SeparateIPv4IPv6 为 True 时，在 live 主文件之外再输出 IPv4 / IPv6
	// 单栈分文件（对齐 Python separate_ipv4_ipv6，默认 True）。
	SeparateIPv4IPv6 bool
	// IPv4Filename / IPv6Filename 为单栈分文件名（含 .m3u 扩展名）。
	IPv4Filename string
	IPv6Filename string
}

// nonAlphaNum matches characters that are not safe inside a tvg-id attribute.
var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Generate returns the full M3U document as a string (does NOT write a file).
// It applies the standard filter pipeline (quality filter + failed drop +
// max-per-channel by name) then renders. Used by the single-file path.
func Generate(channels []types.Channel, opts Options) (string, error) {
	kept := filterChannels(channels, opts)
	kept = applyMaxPerChannel(kept, opts)
	return formatChannels(kept, opts), nil
}

// Format renders channels into an M3U document WITHOUT applying any filter —
// the caller is expected to have already trimmed the set to exactly what should
// be written (e.g. the pre-computed base / qualified / single-stack subsets
// produced by WriteMultiFiles).
func Format(channels []types.Channel, opts Options) (string, error) {
	return formatChannels(channels, opts), nil
}

// formatChannels groups, sorts and serialises channels to an M3U string. It
// performs no quality / status filtering — filtering is the caller's concern.
func formatChannels(channels []types.Channel, opts Options) string {
	lines := []string{buildHeader(opts)}

	if opts.GroupBy == "" || opts.GroupBy == "none" {
		// Flat output: no #EXTGRP headers, empty group-title.
		sortChannels(channels, opts.SortBy)
		for _, c := range channels {
			lines = append(lines, buildExtinf(c, "", opts))
			lines = append(lines, channelURL(c, opts))
		}
	} else {
		groups, order := groupChannels(channels, opts.GroupBy)
		for _, g := range order {
			lines = append(lines, fmt.Sprintf("#EXTGRP:%s", g))
			list := groups[g]
			sortChannels(list, opts.SortBy)
			for _, c := range list {
				lines = append(lines, buildExtinf(c, g, opts))
				lines = append(lines, channelURL(c, opts))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// WriteFile generates and writes to filepath.Join(opts.OutputDir, opts.Filename);
// returns the written absolute path.
func WriteFile(channels []types.Channel, opts Options) (string, error) {
	content, err := Generate(channels, opts)
	if err != nil {
		return "", err
	}
	if opts.OutputDir != "" {
		if err := util.EnsureDir(opts.OutputDir); err != nil {
			return "", err
		}
	}
	path := util.NormalizePathSeparator(filepath.Join(opts.OutputDir, opts.Filename))
	if err := util.WriteFileString(path, content); err != nil {
		return "", err
	}
	logger.L().Info("M3U written to %s (%d channels)", path, len(channels))
	return path, nil
}

// ────────────────────────────────────────────────────────────
// Multi-file output (base / qualified / single-stack parity with Python)
// ────────────────────────────────────────────────────────────

// MultiSets carries the pre-computed channel subsets for the multi-file output.
//   - Live      → written to the base file (Filename) and, when SeparateIPv4IPv6
//     is set, split into the IPv4 / IPv6 single-stack files.
//   - Qualified → written to the "qualified_" + Filename file.
type MultiSets struct {
	Live      []types.Channel
	Qualified []types.Channel
}

// WriteMultiFiles generates and writes the full set of playlist files that
// mirror Python's generate / generate_from_web_test output:
//   - base file          (opts.Filename)                  ← Live
//   - IPv4 single-stack  (opts.IPv4Filename)              ← Live ∩ IPv4
//   - IPv6 single-stack  (opts.IPv6Filename)              ← Live ∩ IPv6
//   - qualified file     ("qualified_" + opts.Filename)   ← Qualified
//
// Files are only written when their subset is non-empty (so an all-IPv4 source
// set does not produce an empty live-ipv6.m3u). Returns the list of written
// filenames (relative names, e.g. "live.m3u").
func WriteMultiFiles(sets MultiSets, opts Options) ([]string, error) {
	if opts.OutputDir != "" {
		if err := util.EnsureDir(opts.OutputDir); err != nil {
			return nil, err
		}
	}
	var files []string

	// 基础 / 全部有效文件
	if len(sets.Live) > 0 {
		if err := writeOne(sets.Live, opts.Filename, opts); err != nil {
			return files, err
		}
		files = append(files, opts.Filename)
	}

	// 单栈分文件（仅当显式开启且 Live 非空）
	if opts.SeparateIPv4IPv6 && len(sets.Live) > 0 {
		ipv4 := make([]types.Channel, 0, len(sets.Live))
		ipv6 := make([]types.Channel, 0, len(sets.Live))
		for _, c := range sets.Live {
			if isIPv6URL(c.URL) {
				ipv6 = append(ipv6, c)
			} else {
				ipv4 = append(ipv4, c)
			}
		}
		if len(ipv4) > 0 {
			if err := writeOne(ipv4, opts.IPv4Filename, opts); err != nil {
				return files, err
			}
			files = append(files, opts.IPv4Filename)
		}
		if len(ipv6) > 0 {
			if err := writeOne(ipv6, opts.IPv6Filename, opts); err != nil {
				return files, err
			}
			files = append(files, opts.IPv6Filename)
		}
	}

	// 合格（高级）文件
	if len(sets.Qualified) > 0 {
		qname := "qualified_" + opts.Filename
		if err := writeOne(sets.Qualified, qname, opts); err != nil {
			return files, err
		}
		files = append(files, qname)
	}

	return files, nil
}

// writeOne formats and atomically writes a single playlist file.
func writeOne(channels []types.Channel, filename string, opts Options) error {
	content, err := Format(channels, opts)
	if err != nil {
		return err
	}
	path := util.NormalizePathSeparator(filepath.Join(opts.OutputDir, filename))
	if err := util.WriteFileString(path, content); err != nil {
		return err
	}
	logger.L().Info("M3U written to %s (%d channels)", path, len(channels))
	return nil
}

// isIPv6URL reports whether rawURL points at an IPv6 literal — a host containing
// a colon (e.g. "http://[2001:db8::1]:80/stream" or "http://fe80::1/stream").
// Mirrors Python M3UGenerator.is_ipv6_url.
func isIPv6URL(rawURL string) bool {
	if strings.TrimSpace(rawURL) == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.Contains(u.Hostname(), ":")
}

// ResolutionBasedGrouping mirrors Python resolution_based_filtering: group by
// channel name + resolution (audio/radio grouped by name only as "audio_<name>")
// then keep the top N sources per group after sorting by
// (-download_speed, +response_time, -bitrate, +name). N = maxPerChannel.
func ResolutionBasedGrouping(channels []types.Channel, maxPerChannel int) []types.Channel {
	if maxPerChannel <= 0 {
		return channels
	}
	groups := map[string][]types.Channel{}
	var order []string
	for _, c := range channels {
		mt := strings.ToLower(strings.TrimSpace(c.MediaType))
		if mt == "radio" || mt == "audio" {
			key := "audio_" + c.Name
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], c)
			continue
		}
		res := strings.TrimSpace(c.Resolution)
		if res == "" {
			res = "unknown"
		}
		key := c.Name + "_" + res
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], c)
	}

	var out []types.Channel
	for _, key := range order {
		list := groups[key]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].DownloadSpeed != list[j].DownloadSpeed {
				return list[i].DownloadSpeed > list[j].DownloadSpeed
			}
			if list[i].ResponseTime != list[j].ResponseTime {
				return list[i].ResponseTime < list[j].ResponseTime
			}
			if list[i].Bitrate != list[j].Bitrate {
				return list[i].Bitrate > list[j].Bitrate
			}
			return list[i].Name < list[j].Name
		})
		if len(list) > maxPerChannel {
			list = list[:maxPerChannel]
		}
		out = append(out, list...)
	}
	return out
}

// FilterQualified keeps only channels passing the quality filter (passesFilter),
// mirroring Python condition_based_filtering. The input is expected to already
// be the base / success set, so no status check is performed here.
func FilterQualified(channels []types.Channel, filter map[string]any) []types.Channel {
	out := make([]types.Channel, 0, len(channels))
	for _, c := range channels {
		if passesFilter(c, filter) {
			out = append(out, c)
		}
	}
	return out
}

// ────────────────────────────────────────────────────────────
// Filtering
// ────────────────────────────────────────────────────────────

func filterChannels(channels []types.Channel, opts Options) []types.Channel {
	whitelistActive := opts.WhitelistForceKeep || len(opts.Whitelist) > 0
	var out []types.Channel
	for _, c := range channels {
		host := hostOf(c.URL)
		// Blacklist always drops, regardless of whitelist status.
		if listMatch(host, c.URL, opts.Blacklist) {
			continue
		}
		whitelisted := whitelistActive && listMatch(host, c.URL, opts.Whitelist)
		if !whitelisted {
			if opts.EnableFilter && !passesFilter(c, opts.Filter) {
				continue
			}
			if !opts.IncludeFailed && c.Status != "success" {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// passesFilter replicates M3UGenerator.enhanced_filter_sources quality checks.
func passesFilter(c types.Channel, filter map[string]any) bool {
	maxLatency := toInt(filter["max_latency"], 4000)
	minBitrate := toInt(filter["min_bitrate"], 80)
	mustHD := toBool(filter["must_hd"], false)
	must4k := toBool(filter["must_4k"], false)
	minSpeed := toInt(filter["min_speed"], 50)
	minRes := toStr(filter["min_resolution"], "360p")
	maxRes := toStr(filter["max_resolution"], "4k")
	resMode := toStr(filter["resolution_filter_mode"], "range")

	// Latency (spec: ResponseTime is in milliseconds).
	if c.ResponseTime > float64(maxLatency) {
		return false
	}

	// Resolution filter (range / min_only / max_only / exact).
	res := strings.TrimSpace(c.Resolution)
	if minRes != "" || maxRes != "" {
		switch resMode {
		case "min_only":
			if minRes != "" && !resolutionMeetsMin(res, minRes) {
				return false
			}
		case "max_only":
			if maxRes != "" && !resolutionMeetsMax(res, maxRes) {
				return false
			}
		case "exact":
			if minRes != "" && !strings.EqualFold(normalizeRes(res), normalizeRes(minRes)) {
				return false
			}
		default: // range
			if minRes != "" && !resolutionMeetsMin(res, minRes) {
				return false
			}
			if maxRes != "" && !resolutionMeetsMax(res, maxRes) {
				return false
			}
		}
	}

	// Bitrate (unknown/zero bitrate is allowed through).
	if c.Bitrate > 0 && c.Bitrate < minBitrate {
		return false
	}

	// Special resolution requirements derived from the Resolution string.
	if mustHD && !isResolutionHD(res) {
		return false
	}
	if must4k && !isResolution4K(res) {
		return false
	}

	// Speed (untested / zero speed is allowed through).
	if c.DownloadSpeed > 0 && c.DownloadSpeed < float64(minSpeed) {
		return false
	}

	return true
}

// ────────────────────────────────────────────────────────────
// Max-per-channel
// ────────────────────────────────────────────────────────────

func applyMaxPerChannel(channels []types.Channel, opts Options) []types.Channel {
	if opts.MaxPerChannel <= 0 {
		return channels
	}
	byName := map[string][]types.Channel{}
	var order []string
	for _, c := range channels {
		if _, ok := byName[c.Name]; !ok {
			order = append(order, c.Name)
		}
		byName[c.Name] = append(byName[c.Name], c)
	}
	var out []types.Channel
	for _, name := range order {
		list := byName[name]
		sortChannels(list, opts.SortBy)
		if len(list) > opts.MaxPerChannel {
			list = list[:opts.MaxPerChannel]
		}
		out = append(out, list...)
	}
	return out
}

// ────────────────────────────────────────────────────────────
// Grouping & sorting
// ────────────────────────────────────────────────────────────

func groupChannels(channels []types.Channel, groupBy string) (map[string][]types.Channel, []string) {
	groups := map[string][]types.Channel{}
	var order []string
	for _, c := range channels {
		g := groupKey(c, groupBy)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], c)
	}
	return groups, order
}

// mediaTypeOf 返回频道的归一化媒体类型（小写）。对齐 Python source['media_type']：
// 缺省 video；纯音频流经 refineMediaType 细分为 radio/audio。
func mediaTypeOf(c types.Channel) string {
	mt := strings.ToLower(strings.TrimSpace(c.MediaType))
	if mt == "" {
		return "video"
	}
	return mt
}

func groupKey(c types.Channel, groupBy string) string {
	switch groupBy {
	case "category":
		// 媒体类型专属分组（对齐 Python enhanced_group_and_sort_sources）：
		// 收音机/在线音频独立成组，视频走 content 维度分组。
		switch mediaTypeOf(c) {
		case "radio":
			return "收音机"
		case "audio":
			return "在线音频"
		}
		if c.Categories != nil {
			if v, ok := c.Categories["content"]; ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return "其他"
	case "country":
		// Group by country/region dimension (M11 fix: align with FieldOptions).
		if c.Categories != nil {
			if v, ok := c.Categories["country"]; ok && strings.TrimSpace(v) != "" {
				return v
			}
			if v, ok := c.Categories["region"]; ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return "其他"
	case "name":
		// Group by channel name (M11 fix: align with FieldOptions).
		name := strings.TrimSpace(c.Name)
		if name != "" {
			return name
		}
		return "Unknown"
	case "source":
		if strings.TrimSpace(c.Group) != "" {
			return c.Group
		}
		if strings.TrimSpace(c.FileName) != "" {
			return c.FileName
		}
		return "Unknown"
	default:
		return "All Channels"
	}
}

func sortChannels(chs []types.Channel, sortBy string) {
	switch sortBy {
	case "latency":
		sort.SliceStable(chs, func(i, j int) bool {
			return chs[i].ResponseTime < chs[j].ResponseTime
		})
	case "name":
		sort.SliceStable(chs, func(i, j int) bool {
			return chs[i].Name < chs[j].Name
		})
	default: // "speed" (descending) and any unknown value
		sort.SliceStable(chs, func(i, j int) bool {
			return chs[i].DownloadSpeed > chs[j].DownloadSpeed
		})
	}
}

// ────────────────────────────────────────────────────────────
// EXTINF building (ports build_enhanced_extinf at base level)
// ────────────────────────────────────────────────────────────

// buildHeader 构造 #EXTM3U 头。EPGURL 非空时注入 url-tvg / x-tvg-url，
// 双属性并写是为了兼容不同播放器（DIYP 认 x-tvg-url，Kodi/TiviMate 认 url-tvg）。
func buildHeader(opts Options) string {
	epgURL := strings.TrimSpace(opts.EPGURL)
	if epgURL == "" {
		return "#EXTM3U"
	}
	// 属性值内的引号会截断整行，直接剥掉。
	epgURL = strings.ReplaceAll(epgURL, `"`, "")
	return fmt.Sprintf(`#EXTM3U url-tvg="%s" x-tvg-url="%s"`, epgURL, epgURL)
}

// escapeAttr sanitizes a string for safe embedding inside a double-quoted
// M3U attribute value. Double quotes and newlines are stripped to prevent
// attribute injection (M6 fix).
func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func buildExtinf(c types.Channel, groupTitle string, opts Options) string {
	name := c.Name
	if name == "" {
		name = "Unknown"
	}
	parts := []string{"#EXTINF:-1"}
	// tvg-id 优先用 EPG 对齐结果，未命中才回落到按频道名生成的占位 id。
	tvgID := strings.ToLower(nonAlphaNum.ReplaceAllString(name, "_"))
	logo := c.Logo
	if info, ok := opts.TVGInfo[name]; ok {
		if info[0] != "" {
			tvgID = info[0]
		}
		if logo == "" && info[1] != "" {
			logo = info[1]
		}
	}
	parts = append(parts, fmt.Sprintf(`tvg-id="%s"`, escapeAttr(tvgID)))
	parts = append(parts, fmt.Sprintf(`tvg-name="%s"`, escapeAttr(name)))
	if logo != "" {
		parts = append(parts, fmt.Sprintf(`tvg-logo="%s"`, escapeAttr(logo)))
	}
	parts = append(parts, fmt.Sprintf(`group-title="%s"`, escapeAttr(groupTitle)))

	mediaType := c.MediaType
	if mediaType == "" {
		mediaType = "video"
	}
	parts = append(parts, fmt.Sprintf(`media-type="%s"`, escapeAttr(mediaType)))

	// 地区信息：对齐 Python tvg-country/region/province。Go 仅在 Categories
	// 携带 region(省级)维度时注入 tvg-region（其余维度 Python 端未稳定承载）。
	if c.Categories != nil {
		if region, ok := c.Categories["region"]; ok && strings.TrimSpace(region) != "" {
			parts = append(parts, fmt.Sprintf(`tvg-region="%s"`, escapeAttr(region)))
		}
	}

	if c.Resolution != "" {
		parts = append(parts, fmt.Sprintf(`resolution="%s"`, escapeAttr(c.Resolution)))
	}
	if c.Bitrate > 0 {
		parts = append(parts, fmt.Sprintf(`bitrate="%dkbps"`, c.Bitrate))
	}
	if c.Status != "" && c.Status != "success" {
		parts = append(parts, fmt.Sprintf(`status="%s"`, escapeAttr(c.Status)))
	}
	// UA injection at the EXTINF level (Python parity): only when enabled and
	// the channel actually carries a UA, and only for the extinf position.
	if opts.UAEnabled && c.UserAgent != "" && (opts.UAPosition == "" || opts.UAPosition == "extinf") {
		parts = append(parts, fmt.Sprintf(`user-agent="%s"`, escapeAttr(c.UserAgent)))
	}
	// Referer injection at the EXTINF level (Python lacks this; Go wires it so
	// generated playlists carry the Referer these sources require).
	if opts.RefererEnabled && c.Referrer != "" && (opts.RefererPosition == "" || opts.RefererPosition == "extinf") {
		parts = append(parts, fmt.Sprintf(`http-referer="%s"`, escapeAttr(c.Referrer)))
	}
	parts = append(parts, ","+name)
	return strings.Join(parts, " ")
}

// channelURL returns the stream URL, optionally appending the UA / Referer as a
// "|User-Agent=..." / "|Referer=..." suffix when the url position is selected
// (Python parity). The '|' separator inside the UA/Referer value is replaced
// to avoid breaking the pipe-delimited format (L19 fix).
func channelURL(c types.Channel, opts Options) string {
	u := c.URL
	if opts.UAEnabled && c.UserAgent != "" && opts.UAPosition == "url" {
		u = u + "|User-Agent=" + strings.ReplaceAll(c.UserAgent, "|", "")
	}
	if opts.RefererEnabled && c.Referrer != "" && opts.RefererPosition == "url" {
		u = u + "|Referer=" + strings.ReplaceAll(c.Referrer, "|", "")
	}
	return u
}

// ────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

// listMatch replicates M3UGenerator._matches_whitelist: an entry is a
// host substring (or a full-URL substring, case-insensitive).
func listMatch(host, rawURL string, list []string) bool {
	hl := strings.ToLower(host)
	ul := strings.ToLower(rawURL)
	for _, e := range list {
		el := strings.ToLower(strings.TrimSpace(e))
		if el == "" {
			continue
		}
		if hl != "" && strings.Contains(hl, el) {
			return true
		}
		if strings.Contains(ul, el) {
			return true
		}
	}
	return false
}

func parseResolution(res string) (int, int) {
	res = strings.TrimSpace(res)
	if res == "" {
		return 0, 0
	}
	// 高度令牌：4k / 2k 这类非 "WxH" / "Hp" 写法（对齐 Python is_resolution_4k）。
	// 默认 Filter.max_resolution="4k" 时若不能解析，resolutionMeetsMax 会把所有
	// 频道误判为超限而丢弃——必须先归一化为数值高度。
	switch strings.ToLower(res) {
	case "4k":
		return 3840, 2160
	case "2k":
		return 2560, 1440
	}
	if i := strings.Index(res, "x"); i >= 0 {
		parts := strings.SplitN(res, "x", 2)
		w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil {
			return w, h
		}
		return 0, 0
	}
	if strings.HasSuffix(res, "p") {
		h, err := strconv.Atoi(strings.TrimSpace(res[:len(res)-1]))
		if err == nil {
			return int(float64(h) * 16 / 9), h
		}
	}
	return 0, 0
}

func resolutionMeetsMin(res, minRes string) bool {
	if res == "" || minRes == "" {
		return true
	}
	rw, rh := parseResolution(res)
	mw, mh := parseResolution(minRes)
	return rw >= mw && rh >= mh
}

func resolutionMeetsMax(res, maxRes string) bool {
	if res == "" || maxRes == "" {
		return true
	}
	rw, rh := parseResolution(res)
	mw, mh := parseResolution(maxRes)
	return rw <= mw && rh <= mh
}

func normalizeRes(res string) string {
	return strings.ToLower(strings.TrimSpace(res))
}

func isResolutionHD(res string) bool {
	_, h := parseResolution(res)
	return h >= 720
}

func isResolution4K(res string) bool {
	if strings.Contains(strings.ToLower(res), "4k") {
		return true
	}
	_, h := parseResolution(res)
	return h >= 2160
}

func toInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n
		}
	}
	return def
}

func toBool(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "True" || x == "true" || x == "1"
	}
	return def
}

func toStr(v any, def string) string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return def
		}
		return x
	case nil:
		return def
	}
	return fmt.Sprint(v)
}
