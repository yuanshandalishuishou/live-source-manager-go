// Package config 负责加载、解析和管理系统配置。支持从 INI 文件读取，并与数据库 sys_config 表同步。
package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-ini/ini"
)

// Config 聚合所有配置节
type Config struct {
	Sources    SourcesConfig
	Network    NetworkConfig
	HTTPServer HTTPServerConfig
	GitHub     GitHubConfig
	Testing    TestingConfig
	Output     OutputConfig
	Logging    LoggingConfig
	Filter     FilterConfig
	EPG        EPGConfig
	Logo       LogoConfig
	Catchup    CatchupConfig
	WebServer  WebServerConfig
	RTMP       RTMPConfig
	Scan       ScanConfig
	System     SystemConfig
}

// SourcesConfig 源文件配置
type SourcesConfig struct {
	LocalDirs     []string
	OnlineURLs    []string
	GitHubSources []string
}

// NetworkConfig 网络配置
type NetworkConfig struct {
	ProxyEnabled        bool
	ProxyType           string
	ProxyHost           string
	ProxyPort           int
	ProxyUsername       string
	ProxyPassword       string
	IPv6Enabled         bool
	IPVersion           string
	GitHubMirrorEnabled bool
	GitHubMirrorURLs    []string
}

// HTTPServerConfig HTTP 文件服务配置
type HTTPServerConfig struct {
	Enabled      bool
	Host         string
	Port         int
	DocumentRoot string
}

// GitHubConfig GitHub API 配置
type GitHubConfig struct {
	APIURL    string
	APIToken  string
	RateLimit int
}

// TestingConfig 测试参数配置
type TestingConfig struct {
	Timeout           int
	ConcurrentThreads int
	CacheTTL          int
	EnableSpeedTest   bool
	SpeedTestDuration int
	MaxRetries        int
}

// OutputConfig 输出配置
type OutputConfig struct {
	Filename             string
	GroupBy              string
	IncludeFailed        bool
	MaxSourcesPerChannel int
	EnableFilter         bool
	OutputDir            string
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level       string
	File        string
	MaxSize     int
	BackupCount int
}

// FilterConfig 过滤配置
type FilterConfig struct {
	MaxLatency           int
	MinBitrate           int
	MustHD               bool
	Must4K               bool
	MinSpeed             int
	MinResolution        string
	MaxResolution        string
	ResolutionFilterMode string
	Location             string
	ISP                  string
	OriginTypePrefer     []string
}

// EPGConfig EPG 配置
type EPGConfig struct {
	UpdateInterval int
	IncludeEPGURL  bool
	EPGURL         string
	EPGSources     []string
}

// LogoConfig 台标配置
type LogoConfig struct {
	RemoteBaseURL string
	LocalDir      string
}

// CatchupConfig 回看配置
type CatchupConfig struct {
	Days     int
	Template string
}

// WebServerConfig Web 管理界面配置
type WebServerConfig struct {
	Port           int
	EnableAuth     bool
	SessionTimeout int
}

// RTMPConfig RTMP 推流配置
type RTMPConfig struct {
	OpenRTMP          bool
	NginxHTTPPort     int
	NginxRTMPPort     int
	RTMPIdleTimeout   int
	RTMPMaxStreams    int
	RTMPTranscodeMode string
}

// ScanConfig 扫描配置
type ScanConfig struct {
	HotelIPRanges       []string
	MulticastInterfaces []string
}

// SystemConfig 系统配置
type SystemConfig struct {
	AdminUsername     string
	AdminPasswordHash string
	CronExpression    string
}

// Load 从 INI 文件加载配置
func Load(path string) (*Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	c := &Config{}

	// Sources
	c.Sources.LocalDirs = splitTrim(cfg.Section("Sources").Key("local_dirs").String(), ",")
	c.Sources.OnlineURLs = parseMultiline(cfg.Section("Sources").Key("online_urls").String())
	c.Sources.GitHubSources = parseMultiline(cfg.Section("Sources").Key("github_sources").String())

	// Network
	c.Network.ProxyEnabled = cfg.Section("Network").Key("proxy_enabled").MustBool(false)
	c.Network.ProxyType = cfg.Section("Network").Key("proxy_type").String()
	c.Network.ProxyHost = cfg.Section("Network").Key("proxy_host").String()
	c.Network.ProxyPort = cfg.Section("Network").Key("proxy_port").MustInt(1080)
	c.Network.ProxyUsername = cfg.Section("Network").Key("proxy_username").String()
	c.Network.ProxyPassword = cfg.Section("Network").Key("proxy_password").String()
	c.Network.IPv6Enabled = cfg.Section("Network").Key("ipv6_enabled").MustBool(false)
	c.Network.IPVersion = cfg.Section("Network").Key("ip_version").In("all", []string{"ipv4", "ipv6", "all"})
	c.Network.GitHubMirrorEnabled = cfg.Section("Network").Key("github_mirror_enabled").MustBool(true)
	c.Network.GitHubMirrorURLs = splitTrim(cfg.Section("Network").Key("github_mirror_urls").String(), ",")

	// HTTPServer
	c.HTTPServer.Enabled = cfg.Section("HTTPServer").Key("enabled").MustBool(true)
	c.HTTPServer.Host = cfg.Section("HTTPServer").Key("host").String()
	c.HTTPServer.Port = cfg.Section("HTTPServer").Key("port").MustInt(12345)
	c.HTTPServer.DocumentRoot = cfg.Section("HTTPServer").Key("document_root").String()

	// GitHub
	c.GitHub.APIURL = cfg.Section("GitHub").Key("api_url").String()
	c.GitHub.APIToken = cfg.Section("GitHub").Key("api_token").String()
	c.GitHub.RateLimit = cfg.Section("GitHub").Key("rate_limit").MustInt(5000)

	// Testing
	c.Testing.Timeout = cfg.Section("Testing").Key("timeout").MustInt(10)
	c.Testing.ConcurrentThreads = cfg.Section("Testing").Key("concurrent_threads").MustInt(30)
	c.Testing.CacheTTL = cfg.Section("Testing").Key("cache_ttl").MustInt(120)
	c.Testing.EnableSpeedTest = cfg.Section("Testing").Key("enable_speed_test").MustBool(true)
	c.Testing.SpeedTestDuration = cfg.Section("Testing").Key("speed_test_duration").MustInt(6)
	c.Testing.MaxRetries = cfg.Section("Testing").Key("max_retries").MustInt(2)

	// Output
	c.Output.Filename = cfg.Section("Output").Key("filename").String()
	c.Output.GroupBy = cfg.Section("Output").Key("group_by").String()
	c.Output.IncludeFailed = cfg.Section("Output").Key("include_failed").MustBool(false)
	c.Output.MaxSourcesPerChannel = cfg.Section("Output").Key("max_sources_per_channel").MustInt(3)
	c.Output.EnableFilter = cfg.Section("Output").Key("enable_filter").MustBool(true)
	c.Output.OutputDir = cfg.Section("Output").Key("output_dir").String()

	// Logging
	c.Logging.Level = cfg.Section("Logging").Key("level").String()
	c.Logging.File = cfg.Section("Logging").Key("file").String()
	c.Logging.MaxSize = cfg.Section("Logging").Key("max_size").MustInt(10)
	c.Logging.BackupCount = cfg.Section("Logging").Key("backup_count").MustInt(5)

	// Filter
	c.Filter.MaxLatency = cfg.Section("Filter").Key("max_latency").MustInt(5000)
	c.Filter.MinBitrate = cfg.Section("Filter").Key("min_bitrate").MustInt(100)
	c.Filter.MustHD = cfg.Section("Filter").Key("must_hd").MustBool(false)
	c.Filter.Must4K = cfg.Section("Filter").Key("must_4k").MustBool(false)
	c.Filter.MinSpeed = cfg.Section("Filter").Key("min_speed").MustInt(40)
	c.Filter.MinResolution = cfg.Section("Filter").Key("min_resolution").String()
	c.Filter.MaxResolution = cfg.Section("Filter").Key("max_resolution").String()
	c.Filter.ResolutionFilterMode = cfg.Section("Filter").Key("resolution_filter_mode").String()
	c.Filter.Location = cfg.Section("Filter").Key("location").String()
	c.Filter.ISP = cfg.Section("Filter").Key("isp").String()
	c.Filter.OriginTypePrefer = splitTrim(cfg.Section("Filter").Key("origin_type_prefer").String(), ",")

	// EPG
	c.EPG.UpdateInterval = cfg.Section("EPG").Key("update_interval").MustInt(12)
	c.EPG.IncludeEPGURL = cfg.Section("EPG").Key("include_epg_url").MustBool(true)
	c.EPG.EPGURL = cfg.Section("EPG").Key("epg_url").String()
	epgSourcesJSON := cfg.Section("EPG").Key("epg_sources").String()
	if epgSourcesJSON != "" {
		json.Unmarshal([]byte(epgSourcesJSON), &c.EPG.EPGSources)
	}

	// Logo
	c.Logo.RemoteBaseURL = cfg.Section("Logo").Key("remote_base_url").String()
	c.Logo.LocalDir = cfg.Section("Logo").Key("local_dir").String()

	// Catchup
	c.Catchup.Days = cfg.Section("Catchup").Key("days").MustInt(7)
	c.Catchup.Template = cfg.Section("Catchup").Key("template").String()

	// WebServer
	c.WebServer.Port = cfg.Section("WebServer").Key("port").MustInt(23456)
	c.WebServer.EnableAuth = cfg.Section("WebServer").Key("enable_auth").MustBool(true)
	c.WebServer.SessionTimeout = cfg.Section("WebServer").Key("session_timeout").MustInt(1440)

	// RTMP
	c.RTMP.OpenRTMP = cfg.Section("RTMP").Key("open_rtmp").MustBool(true)
	c.RTMP.NginxHTTPPort = cfg.Section("RTMP").Key("nginx_http_port").MustInt(8080)
	c.RTMP.NginxRTMPPort = cfg.Section("RTMP").Key("nginx_rtmp_port").MustInt(1935)
	c.RTMP.RTMPIdleTimeout = cfg.Section("RTMP").Key("rtmp_idle_timeout").MustInt(300)
	c.RTMP.RTMPMaxStreams = cfg.Section("RTMP").Key("rtmp_max_streams").MustInt(10)
	c.RTMP.RTMPTranscodeMode = cfg.Section("RTMP").Key("rtmp_transcode_mode").String()

	// Scan
	hotelJSON := cfg.Section("Scan").Key("hotel_ip_ranges").String()
	if hotelJSON != "" {
		json.Unmarshal([]byte(hotelJSON), &c.Scan.HotelIPRanges)
	}
	multicastJSON := cfg.Section("Scan").Key("multicast_interfaces").String()
	if multicastJSON != "" {
		json.Unmarshal([]byte(multicastJSON), &c.Scan.MulticastInterfaces)
	}

	// System
	c.System.AdminUsername = cfg.Section("System").Key("admin_username").String()
	c.System.AdminPasswordHash = cfg.Section("System").Key("admin_password_hash").String()
	c.System.CronExpression = cfg.Section("System").Key("cron_expression").String()

	return c, nil
}

// LoadFromDB 从数据库加载配置并覆盖当前结构体
func (c *Config) LoadFromDB(db *sql.DB) error {
	rows, err := db.Query(`SELECT group_name, key, value FROM sys_config`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var group, key, value string
		if err := rows.Scan(&group, &key, &value); err != nil {
			continue
		}
		switch group {
		case "Network":
			switch key {
			case "proxy_enabled":
				c.Network.ProxyEnabled = value == "true"
			case "proxy_type":
				c.Network.ProxyType = value
			case "proxy_host":
				c.Network.ProxyHost = value
			case "proxy_port":
				c.Network.ProxyPort, _ = strconv.Atoi(value)
			case "ip_version":
				c.Network.IPVersion = value
			}
		case "Testing":
			switch key {
			case "timeout":
				c.Testing.Timeout, _ = strconv.Atoi(value)
			case "concurrent_threads":
				c.Testing.ConcurrentThreads, _ = strconv.Atoi(value)
			case "enable_speed_test":
				c.Testing.EnableSpeedTest = value == "true"
			}
		case "Output":
			switch key {
			case "filename":
				c.Output.Filename = value
			case "group_by":
				c.Output.GroupBy = value
			case "max_sources_per_channel":
				c.Output.MaxSourcesPerChannel, _ = strconv.Atoi(value)
			}
		case "Filter":
			switch key {
			case "max_latency":
				c.Filter.MaxLatency, _ = strconv.Atoi(value)
			case "min_bitrate":
				c.Filter.MinBitrate, _ = strconv.Atoi(value)
			case "location":
				c.Filter.Location = value
			case "isp":
				c.Filter.ISP = value
			}
		case "RTMP":
			switch key {
			case "open_rtmp":
				c.RTMP.OpenRTMP = value == "true"
			case "rtmp_max_streams":
				c.RTMP.RTMPMaxStreams, _ = strconv.Atoi(value)
			}
		case "WebServer":
			switch key {
			case "port":
				c.WebServer.Port, _ = strconv.Atoi(value)
			}
		case "System":
			switch key {
			case "cron_expression":
				c.System.CronExpression = value
			}
		}
	}
	return nil
}

// SaveToDB 将当前配置保存到数据库
func (c *Config) SaveToDB(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	updates := map[string]string{
		"Network.proxy_enabled":          fmt.Sprintf("%v", c.Network.ProxyEnabled),
		"Network.proxy_type":             c.Network.ProxyType,
		"Network.ip_version":             c.Network.IPVersion,
		"Testing.timeout":                strconv.Itoa(c.Testing.Timeout),
		"Testing.concurrent_threads":     strconv.Itoa(c.Testing.ConcurrentThreads),
		"Output.filename":                c.Output.Filename,
		"Output.max_sources_per_channel": strconv.Itoa(c.Output.MaxSourcesPerChannel),
		"Filter.max_latency":             strconv.Itoa(c.Filter.MaxLatency),
		"RTMP.open_rtmp":                 fmt.Sprintf("%v", c.RTMP.OpenRTMP),
		"WebServer.port":                 strconv.Itoa(c.WebServer.Port),
		"System.cron_expression":         c.System.CronExpression,
	}

	for key, value := range updates {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			continue
		}
		group, k := parts[0], parts[1]
		_, err := tx.Exec(`UPDATE sys_config SET value = ? WHERE group_name = ? AND key = ?`, value, group, k)
		if err != nil {
			_, _ = tx.Exec(`INSERT INTO sys_config (group_name, key, value) VALUES (?, ?, ?)`, group, k, value)
		}
	}
	return tx.Commit()
}

// GetLogLevel 返回日志级别常量
func (c *LoggingConfig) GetLogLevel() int {
	switch strings.ToUpper(c.Level) {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN":
		return 2
	case "ERROR":
		return 3
	default:
		return 1
	}
}

func parseMultiline(s string) []string {
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			result = append(result, line)
		}
	}
	return result
}

func splitTrim(s, sep string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, sep)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
