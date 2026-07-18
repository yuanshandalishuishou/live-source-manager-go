// Package config provides the application configuration backed by the SQLite app_config table.
//
// All 64 default keys are defined here (matching the Python project's Config._DEFAULT_VALUES)
// and are used both as the seed source for the database and as fallbacks when a key is missing.
package config

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"live-source-manager-go/internal/db"
)

// DefaultValues is the authoritative map of "Section.key" -> default string value.
func DefaultValues() map[string]string {
	return map[string]string{
		// [Sources]
		"Sources.local_dirs": "./config/sources",
		"Sources.online_urls": strings.Join([]string{
			"https://live.zbds.org/tv/iptv4.m3u",
			"https://myernestlu.github.io/zby.txt",
			"https://raw.githubusercontent.com/Rivens7/Livelist/main/CCTV.m3u",
			"https://raw.githubusercontent.com/Rivens7/Livelist/main/CNTV.m3u",
			"https://raw.githubusercontent.com/Rivens7/Livelist/main/IPTV.m3u",
			"https://raw.githubusercontent.com/Guovin/iptv-api/gd/output/ipv4/result.m3u",
			"https://raw.githubusercontent.com/suxuang/myIPTV/refs/heads/main/ipv4.m3u",
			"https://raw.githubusercontent.com/hujingguang/ChinaIPTV/main/cnTV_AutoUpdate.m3u8",
			"https://raw.githubusercontent.com/zwc456baby/iptv_alive/refs/heads/master/live.m3u",
			"https://raw.githubusercontent.com/zbefine/iptv/main/iptv.m3u",
			"https://raw.githubusercontent.com/vamoschuck/TV/main/M3U",
			"https://raw.githubusercontent.com/BigBigGrandG/IPTV-URL/release/Gather.m3u",
			"https://raw.githubusercontent.com/Kimentanm/aptv/master/m3u/iptv.m3u",
			"https://raw.githubusercontent.com/YanG-1989/m3u/main/Gather.m3u",
			"https://raw.githubusercontent.com/huang770101/my-iptv/main/IPTV-ipv4.m3u",
			"https://raw.githubusercontent.com/fanmingming/live/main/tv/m3u/ipv6.m3u",
			"https://live.fanmingming.cn/tv/m3u/ipv6.m3u",
			"https://raw.githubusercontent.com/YueChan/Live/main/IPTV.m3u",
			"https://iptv-org.github.io/iptv/countries/tw.m3u",
			"https://iptv-org.github.io/iptv/index.m3u",
		}, "\n"),
		"Sources.github_sources": strings.Join([]string{
			"wcb1969/iptv/main",
			"joevess/IPTV/main",
			"suxuang/myIPTV/main",
			"YueChan/Live",
			"YanG-1989/m3u",
			"qwerttvv/Beijing-IPTV",
			"joevess/IPTV",
			"cymz6/AutoIPTV-Hotel",
			"Rivens7/Livelist",
		}, "\n"),
		"Sources.github_source_settings":  "{}",
		"Sources.source_file_ua_settings": "{}",
		"Sources.channel_ua_overrides":    "{}",
		"Sources.source_file_referer_settings": "{}",
		"Sources.channel_referer_overrides":    "{}",
		// [Network]
		"Network.proxy_enabled":  "False",
		"Network.proxy_type":     "socks5",
		"Network.proxy_host":     "192.168.1.46",
		"Network.proxy_port":     "1800",
		"Network.proxy_username": "",
		"Network.proxy_password": "",
		"Network.github_mirror":  "https://ghproxy.com/",
		"Network.ipv6_enabled":   "True",
		// [HTTPServer]
		"HTTPServer.enabled":        "True",
		"HTTPServer.host":           "0.0.0.0",
		"HTTPServer.fileshare_port": "12345",
		"HTTPServer.manager_port":   "23456",
		"HTTPServer.document_root":  "./www/output",
		// [GitHub]
		"GitHub.api_url":    "https://api.github.com",
		"GitHub.api_token":  "",
		"GitHub.rate_limit": "5000",
		// [Testing]
		"Testing.timeout":                  "10",
		"Testing.concurrent_threads":       "40",
		"Testing.max_concurrent_ffprobe":   "16",
		"Testing.cache_ttl":                "120",
		"Testing.enable_speed_test":        "True",
		"Testing.speed_test_duration":      "6",
		"Testing.auto_scan_enabled":        "False",
		"Testing.auto_scan_mode":           "interval",
		"Testing.auto_scan_interval_hours": "24",
		"Testing.auto_scan_daily_time":     "03:00",
		"Testing.enable_host_speed_share":  "True",
		"Testing.enable_source_freeze":     "True",
		"Testing.freeze_fail_threshold":    "3",
		"Testing.freeze_base_seconds":      "60",
		"Testing.freeze_max_hours":         "24",
		"Testing.enable_ad_detect":         "True",
		"Testing.ad_keywords":              "no_signal,/ad/,advertisement,测试卡,无信号,test_pattern,colorbar,broadcast_test,signal_lost",
		"Testing.ad_max_duration":          "90",
		"Testing.global_blacklist":         "",
		"Testing.global_whitelist":         "",
		"Testing.output_sort_by":           "speed",
		"Testing.max_test_attempts":        "1",
		// [Tools]
		"Tools.ffmpeg_dir": "",
		// [Output]
		"Output.filename":                "live.m3u",
		"Output.group_by":                "category",
		"Output.include_failed":          "False",
		"Output.max_sources_per_channel": "8",
		"Output.enable_filter":           "False",
		"Output.whitelist_force_keep":    "False",
		// [Logging]
		"Logging.level":        "INFO",
		"Logging.file":         "./log/app.log",
		"Logging.max_size":     "10",
		"Logging.backup_count": "5",
		// [Filter]
		"Filter.max_latency":            "4000",
		"Filter.min_bitrate":            "80",
		"Filter.must_hd":                "False",
		"Filter.must_4k":                "False",
		"Filter.min_speed":              "50",
		"Filter.min_resolution":         "360p",
		"Filter.max_resolution":         "4k",
		"Filter.resolution_filter_mode": "range",
		// [UserAgents]
		"UserAgents.ua_position": "extinf",
		"UserAgents.ua_enabled":  "False",
	}
}

// Config reads/writes configuration from the SQLite app_config table.
type Config struct {
	conn *sql.DB
}

// New creates a Config bound to the given database handle.
func New(conn *sql.DB) *Config {
	return &Config{conn: conn}
}

// Get returns the value for section.key, or def if absent.
func (c *Config) Get(section, key, def string) string {
	v, err := db.GetAppConfig(c.conn, section+"."+key)
	if err != nil || v == "" {
		if def != "" {
			return def
		}
		return c.defaultValue(section, key)
	}
	return v
}

// GetRaw returns the raw stored value (no default fallback to empty string).
func (c *Config) GetRaw(section, key string) string {
	v, _ := db.GetAppConfig(c.conn, section+"."+key)
	return v
}

// Set writes section.key = value.
func (c *Config) Set(section, key, value string) {
	_ = db.SetAppConfig(c.conn, section+"."+key, value)
}

// GetInt / GetBool / GetFloat convenience accessors.
func (c *Config) GetInt(section, key string, def int) int {
	v := c.Get(section, key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (c *Config) GetBool(section, key string, def bool) bool {
	v := c.Get(section, key, "")
	if v == "" {
		return def
	}
	return v == "True" || v == "true" || v == "1" || v == "yes" || v == "on"
}

func (c *Config) GetFloat(section, key string, def float64) float64 {
	v := c.Get(section, key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func (c *Config) defaultValue(section, key string) string {
	return DefaultValues()[section+"."+key]
}

// GetAll returns the full configuration as section -> key -> value.
func (c *Config) GetAll() map[string]map[string]string {
	return db.GetAllConfig(c.conn)
}

// NetworkConfig bundles network settings.
func (c *Config) GetNetworkConfig() map[string]any {
	return map[string]any{
		"proxy_enabled":  c.GetBool("Network", "proxy_enabled", false),
		"proxy_type":     c.Get("Network", "proxy_type", "socks5"),
		"proxy_host":     c.Get("Network", "proxy_host", "192.168.1.46"),
		"proxy_port":     c.GetInt("Network", "proxy_port", 1800),
		"proxy_username": c.Get("Network", "proxy_username", ""),
		"proxy_password": c.Get("Network", "proxy_password", ""),
		"github_mirror":  c.Get("Network", "github_mirror", "https://ghproxy.com/"),
		"ipv6_enabled":   c.GetBool("Network", "ipv6_enabled", true),
	}
}

// TestingParams bundles testing settings.
func (c *Config) GetTestingParams() map[string]any {
	return map[string]any{
		"timeout":                 c.GetInt("Testing", "timeout", 10),
		"concurrent_threads":      c.GetInt("Testing", "concurrent_threads", 40),
		"max_concurrent_ffprobe":  c.GetInt("Testing", "max_concurrent_ffprobe", 16),
		"cache_ttl":               c.GetInt("Testing", "cache_ttl", 120),
		"enable_speed_test":       c.GetBool("Testing", "enable_speed_test", true),
		"speed_test_duration":     c.GetInt("Testing", "speed_test_duration", 6),
		"enable_host_speed_share": c.GetBool("Testing", "enable_host_speed_share", true),
		"enable_source_freeze":    c.GetBool("Testing", "enable_source_freeze", true),
		"freeze_fail_threshold":   c.GetInt("Testing", "freeze_fail_threshold", 3),
		"freeze_base_seconds":     c.GetInt("Testing", "freeze_base_seconds", 60),
		"freeze_max_hours":        c.GetInt("Testing", "freeze_max_hours", 24),
		"enable_ad_detect":        c.GetBool("Testing", "enable_ad_detect", true),
		"ad_keywords":             c.Get("Testing", "ad_keywords", ""),
		"ad_max_duration":         c.GetInt("Testing", "ad_max_duration", 90),
		"global_blacklist":        c.Get("Testing", "global_blacklist", ""),
		"global_whitelist":        c.Get("Testing", "global_whitelist", ""),
		"output_sort_by":          c.Get("Testing", "output_sort_by", "speed"),
		"max_workers":             50,
	}
}

// FilterParams bundles filtering settings.
func (c *Config) GetFilterParams() map[string]any {
	return map[string]any{
		"max_latency":            c.GetInt("Filter", "max_latency", 4000),
		"min_bitrate":            c.GetInt("Filter", "min_bitrate", 80),
		"must_hd":                c.GetBool("Filter", "must_hd", false),
		"must_4k":                c.GetBool("Filter", "must_4k", false),
		"min_speed":              c.GetInt("Filter", "min_speed", 50),
		"min_resolution":         c.Get("Filter", "min_resolution", "360p"),
		"max_resolution":         c.Get("Filter", "max_resolution", "4k"),
		"resolution_filter_mode": c.Get("Filter", "resolution_filter_mode", "range"),
	}
}

// OutputParams bundles output settings.
func (c *Config) GetOutputParams() map[string]any {
	outputDir := c.Get("Output", "output_dir", "./www/output")
	return map[string]any{
		"filename":                c.Get("Output", "filename", "live.m3u"),
		"group_by":                c.Get("Output", "group_by", "category"),
		"include_failed":          c.GetBool("Output", "include_failed", false),
		"max_sources_per_channel": c.GetInt("Output", "max_sources_per_channel", 8),
		"enable_filter":           c.GetBool("Output", "enable_filter", false),
		"whitelist_force_keep":    c.GetBool("Output", "whitelist_force_keep", false),
		"output_dir":              outputDir,
	}
}

// HTTPServerConfig bundles file-share / manager server settings.
func (c *Config) GetHTTPServerConfig() map[string]any {
	return map[string]any{
		"enabled":        c.GetBool("HTTPServer", "enabled", true),
		"host":           c.Get("HTTPServer", "host", "0.0.0.0"),
		"fileshare_port": c.GetInt("HTTPServer", "fileshare_port", 12345),
		"manager_port":   c.GetInt("HTTPServer", "manager_port", 23456),
		"document_root":  c.Get("HTTPServer", "document_root", "./www/output"),
	}
}

// GetSources returns local_dirs / online_urls / github_sources splits.
func (c *Config) GetSources() map[string]any {
	local := c.Get("Sources", "local_dirs", "./config/sources")
	online := c.Get("Sources", "online_urls", "")
	github := c.Get("Sources", "github_sources", "")
	return map[string]any{
		"local_dirs":     strings.Split(local, ","),
		"online_urls":    splitLines(online),
		"github_sources": splitLines(github),
	}
}

// GetUserAgents returns the UA map (everything under UserAgents except ua_position/ua_enabled).
func (c *Config) GetUserAgents() map[string]string {
	all := c.GetAll()
	section, ok := all["UserAgents"]
	if !ok {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range section {
		if k == "ua_position" || k == "ua_enabled" {
			continue
		}
		out[k] = v
	}
	return out
}

// GetSourceFileUASettings parses Sources.source_file_ua_settings JSON.
func (c *Config) GetSourceFileUASettings() map[string]any {
	raw := c.Get("Sources", "source_file_ua_settings", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// GetChannelUAOverrides parses Sources.channel_ua_overrides JSON.
func (c *Config) GetChannelUAOverrides() map[string]any {
	raw := c.Get("Sources", "channel_ua_overrides", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// GetUAEnabled / GetUAPosition expose UA toggles.
func (c *Config) GetUAEnabled() bool    { return c.GetBool("UserAgents", "ua_enabled", false) }
func (c *Config) GetUAPosition() string { return c.Get("UserAgents", "ua_position", "extinf") }

// GetSourceFileRefererSettings / GetChannelRefererOverrides mirror the UA
// settings but for the Referer header. Python lacks these entirely (it parses
// http_referrer but never consumes it); Go wires them end-to-end so refs that
// carry a Referer actually reach ffprobe and the generated playlist.
func (c *Config) GetSourceFileRefererSettings() map[string]any {
	raw := c.Get("Sources", "source_file_referer_settings", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func (c *Config) GetChannelRefererOverrides() map[string]any {
	raw := c.Get("Sources", "channel_referer_overrides", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// GetReferrerEnabled / GetReferrerPosition gate referer injection into the
// generated M3U output (extinf attribute vs |Referer= url suffix).
func (c *Config) GetReferrerEnabled() bool    { return c.GetBool("Referrers", "referer_enabled", false) }
func (c *Config) GetReferrerPosition() string { return c.Get("Referrers", "referer_position", "extinf") }

func splitLines(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
