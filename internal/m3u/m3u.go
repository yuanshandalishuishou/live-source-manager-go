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
}

// nonAlphaNum matches characters that are not safe inside a tvg-id attribute.
var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Generate returns the full M3U document as a string (does NOT write a file).
func Generate(channels []types.Channel, opts Options) (string, error) {
	kept := filterChannels(channels, opts)
	kept = applyMaxPerChannel(kept, opts)

	lines := []string{"#EXTM3U"}

	if opts.GroupBy == "" || opts.GroupBy == "none" {
		// Flat output: no #EXTGRP headers, empty group-title.
		sortChannels(kept, opts.SortBy)
		for _, c := range kept {
			lines = append(lines, buildExtinf(c, ""))
			lines = append(lines, c.URL)
		}
	} else {
		groups, order := groupChannels(kept, opts.GroupBy)
		for _, g := range order {
			lines = append(lines, fmt.Sprintf("#EXTGRP:%s", g))
			list := groups[g]
			sortChannels(list, opts.SortBy)
			for _, c := range list {
				lines = append(lines, buildExtinf(c, g))
				lines = append(lines, c.URL)
			}
		}
	}

	return strings.Join(lines, "\n"), nil
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

func groupKey(c types.Channel, groupBy string) string {
	switch groupBy {
	case "category":
		if c.Categories != nil {
			if v, ok := c.Categories["content"]; ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return "其他"
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

func buildExtinf(c types.Channel, groupTitle string) string {
	name := c.Name
	if name == "" {
		name = "Unknown"
	}
	parts := []string{"#EXTINF:-1"}
	tvgID := strings.ToLower(nonAlphaNum.ReplaceAllString(name, "_"))
	parts = append(parts, fmt.Sprintf(`tvg-id="%s"`, tvgID))
	parts = append(parts, fmt.Sprintf(`tvg-name="%s"`, name))
	if c.Logo != "" {
		parts = append(parts, fmt.Sprintf(`tvg-logo="%s"`, c.Logo))
	}
	parts = append(parts, fmt.Sprintf(`group-title="%s"`, groupTitle))

	mediaType := c.MediaType
	if mediaType == "" {
		mediaType = "video"
	}
	parts = append(parts, fmt.Sprintf(`media-type="%s"`, mediaType))

	if c.Resolution != "" {
		parts = append(parts, fmt.Sprintf(`resolution="%s"`, c.Resolution))
	}
	if c.Bitrate > 0 {
		parts = append(parts, fmt.Sprintf(`bitrate="%dkbps"`, c.Bitrate))
	}
	if c.Status != "" && c.Status != "success" {
		parts = append(parts, fmt.Sprintf(`status="%s"`, c.Status))
	}
	parts = append(parts, ","+name)
	return strings.Join(parts, " ")
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
