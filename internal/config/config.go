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
	Generator GeneratorConfig
	RTMP      RTMPConfig
	Logging   LogConfig
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
}

type SchedulerConfig struct {
	Enabled  bool
	CronExpr string
}

type GeneratorConfig struct {
	OutputDir string
}

type RTMPConfig struct {
	Enabled bool
	Address string
}

type LogConfig struct {
	File  string
	Level string
}

// Load 从 ini 文件加载配置
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
		// 先按逗号分割，再去除每个 URL 周围空白，并过滤空串
		parts := strings.Split(urlsStr, ",")
		urls := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				urls = append(urls, p)
			}
		}
		c.Collector.SubscriptionURLs = urls
	}

	// Scheduler 部分
	schedSec := cfg.Section("scheduler")
	c.Scheduler.Enabled = schedSec.Key("enabled").MustBool(false)
	c.Scheduler.CronExpr = schedSec.Key("cron").MustString("@every 6h")

	// Generator 部分
	genSec := cfg.Section("generator")
	c.Generator.OutputDir = genSec.Key("output_dir").MustString("output")

	// RTMP 部分
	rtmpSec := cfg.Section("rtmp")
	c.RTMP.Enabled = rtmpSec.Key("enabled").MustBool(false)
	c.RTMP.Address = rtmpSec.Key("address").MustString("rtmp://localhost/live")

	// Logging 部分
	logSec := cfg.Section("logging")
	c.Logging.File = logSec.Key("file").MustString("logs/app.log")
	c.Logging.Level = logSec.Key("level").MustString("info")

	return c, nil
}
