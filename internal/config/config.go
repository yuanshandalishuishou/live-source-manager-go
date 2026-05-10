// internal/config/config.go
// 配置文件的加载、解析与验证
package config

import (
	"fmt"
	"strings"

	"github.com/go-ini/ini"
)

// Config 应用全局配置
type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Collector CollectorConfig
	Scheduler SchedulerConfig
	Tester    TesterConfig    // 新增：测试器配置
	Filter    FilterConfig    // 新增：过滤器配置
	Generator GeneratorConfig
	RTMP      RTMPConfig
	Logging   LogConfig
	EPG       EPGConfig       // 新增：EPG 配置
}

type ServerConfig struct {
	Host string
	Port int
}

type DatabaseConfig struct {
	Path string
}

type CollectorConfig struct {
	SubscriptionURLs []string // 订阅源地址列表
	LocalSourcesDir  string   // 本地源目录
	EnableGitHub     bool     // 是否启用 GitHub 源
}

type SchedulerConfig struct {
	Enabled  bool
	CronExpr string
}

type TesterConfig struct {
	Concurrency int    // 并发测试数
	Timeout     int    // 单个源测试超时（秒）
	FfprobePath string // ffprobe 路径
	EnableSpeedTest bool // 是否启用速度测试
}

type FilterConfig struct {
	MinResolution string // 最低分辨率要求，如 "1920x1080"
	MaxLatency    int    // 最大延迟（毫秒）
	MinBitrate    int64  // 最低比特率 (bps)
}

type GeneratorConfig struct {
	OutputDir       string // 输出目录
	MaxSourcesPerChannel int // 每个频道最多保留源数量
}

type RTMPConfig struct {
	Enabled  bool
	Address  string
	AppName  string // RTMP 应用名称
}

type LogConfig struct {
	File  string
	Level string
}

type EPGConfig struct {
	Enabled      bool
	EPGURLs      []string // EPG 数据源地址列表
	UpdateInterval int    // 更新间隔（小时）
}

// Load 从 ini 文件加载配置并进行验证
func Load(path string) (*Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	c := &Config{}

	// Server 部分
	serverSec := cfg.Section("server")
	c.Server.Host = serverSec.Key("host").MustString("0.0.0.0")
	c.Server.Port = serverSec.Key("port").MustInt(8080)

	// Database 部分
	dbSec := cfg.Section("database")
	c.Database.Path = dbSec.Key("path").MustString("data/livesource.db")

	// Collector 部分 - 正确处理逗号分隔的 subscription_urls
	collectorSec := cfg.Section("collector")
	urlsStr := collectorSec.Key("subscription_urls").String()
	if urlsStr == "" {
		c.Collector.SubscriptionURLs = make([]string, 0) // 空切片
	} else {
		// 先按逗号分割，再去除每个 URL 周围空白，并过滤空字符串
		parts := strings.Split(urlsStr, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				c.Collector.SubscriptionURLs = append(c.Collector.SubscriptionURLs, trimmed)
			}
		}
	}
	c.Collector.LocalSourcesDir = collectorSec.Key("local_sources_dir").MustString("sources/")
	c.Collector.EnableGitHub = collectorSec.Key("enable_github").MustBool(false)

	// Scheduler 部分
	schedSec := cfg.Section("scheduler")
	c.Scheduler.Enabled = schedSec.Key("enabled").MustBool(false)
	c.Scheduler.CronExpr = schedSec.Key("cron").MustString("0 0 2 * * *")

	// Tester 部分
	testerSec := cfg.Section("tester")
	c.Tester.Concurrency = testerSec.Key("concurrency").MustInt(5)
	c.Tester.Timeout = testerSec.Key("timeout").MustInt(8)
	c.Tester.FfprobePath = testerSec.Key("ffprobe_path").MustString("ffprobe")
	c.Tester.EnableSpeedTest = testerSec.Key("enable_speed_test").MustBool(false)

	// Filter 部分
	filterSec := cfg.Section("filter")
	c.Filter.MinResolution = filterSec.Key("min_resolution").MustString("")
	c.Filter.MaxLatency = filterSec.Key("max_latency").MustInt(0)
	c.Filter.MinBitrate = filterSec.Key("min_bitrate").MustInt64(0)

	// Generator 部分
	genSec := cfg.Section("generator")
	c.Generator.OutputDir = genSec.Key("output_dir").MustString("www/output/")
	c.Generator.MaxSourcesPerChannel = genSec.Key("max_sources_per_channel").MustInt(3)

	// RTMP 部分
	rtmpSec := cfg.Section("rtmp")
	c.RTMP.Enabled = rtmpSec.Key("enabled").MustBool(false)
	c.RTMP.Address = rtmpSec.Key("address").MustString("rtmp://localhost/live")
	c.RTMP.AppName = rtmpSec.Key("app_name").MustString("live")

	// Logging 部分
	logSec := cfg.Section("logging")
	c.Logging.File = logSec.Key("file").MustString("logs/app.log")
	c.Logging.Level = logSec.Key("level").MustString("INFO")

	// EPG 部分
	epgSec := cfg.Section("epg")
	c.EPG.Enabled = epgSec.Key("enabled").MustBool(false)
	epgURLsStr := epgSec.Key("urls").String()
	if epgURLsStr != "" {
		for _, u := range strings.Split(epgURLsStr, ",") {
			trimmed := strings.TrimSpace(u)
			if trimmed != "" {
				c.EPG.EPGURLs = append(c.EPG.EPGURLs, trimmed)
			}
		}
	}
	c.EPG.UpdateInterval = epgSec.Key("update_interval").MustInt(12)

	// 配置验证
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return c, nil
}

// Validate 验证必要的配置项是否合法
func (c *Config) Validate() error {
	if c.Database.Path == "" {
		return fmt.Errorf("数据库路径不能为空")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("服务器端口无效: %d", c.Server.Port)
	}
	if c.Tester.Concurrency <= 0 {
		c.Tester.Concurrency = 5 // 设置默认值
	}
	if c.Tester.Timeout <= 0 {
		c.Tester.Timeout = 8
	}
	if c.Generator.OutputDir == "" {
		return fmt.Errorf("输出目录不能为空")
	}
	if c.Generator.MaxSourcesPerChannel <= 0 {
		c.Generator.MaxSourcesPerChannel = 3
	}
	if c.EPG.Enabled && len(c.EPG.EPGURLs) == 0 {
		return fmt.Errorf("EPG 已启用但未配置 EPG 源")
	}
	if c.EPG.UpdateInterval <= 0 {
		c.EPG.UpdateInterval = 12
	}
	// 如果启用 RTMP，检查必要配置
	if c.RTMP.Enabled && c.RTMP.Address == "" {
		return fmt.Errorf("RTMP 已启用但未配置服务器地址")
	}
	return nil
}
