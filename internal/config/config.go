// internal/config/config.go
// 补全了 Filter 配置字段，并添加了 config.Save 函数以支持 Web 端配置持久化。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

// Config 汇总所有配置节
type Config struct {
	Server     ServerConfig
	Database   DatabaseConfig
	Collector  CollectorConfig
	Tester     TesterConfig
	SubScriber SubScriberConfig
	Classifier ClassifierConfig
	Generator  GeneratorConfig
	RTMP       RTMPConfig
	EPG        EPGConfig
	Output     OutputConfig
	Scheduler  SchedulerConfig
	Downloader DownloaderConfig
	Filter     FilterConfig // 新增：过滤器配置
}

// FilterConfig 黑白名单过滤器配置
type FilterConfig struct {
	BlacklistFile string `ini:"blacklist_file"`
	WhitelistFile string `ini:"whitelist_file"`
}

// ServerConfig Web 服务配置
type ServerConfig struct {
	Port      int    `ini:"port"`
	JWTSecret string `ini:"jwt_secret"`
	Debug     bool   `ini:"debug"`
}

// DatabaseConfig 数据库连接配置
type DatabaseConfig struct {
	Path string `ini:"path"`
}

// CollectorConfig 数据采集器配置
type CollectorConfig struct {
	SubscriptionURLs []string
	IntervalMinutes  int `ini:"interval_minutes"`
}

// TesterConfig 流测试器配置
type TesterConfig struct {
	Timeout     int    `ini:"timeout"`
	Concurrency int    `ini:"concurrency"`
	FfprobePath string `ini:"ffprobe_path"`
}

// SubScriberConfig 订阅管理器配置
type SubScriberConfig struct {
	Enable bool `ini:"enable"`
}

// ClassifierConfig 分类器配置
type ClassifierConfig struct {
	RulesFile string `ini:"rules_file"`
}

// GeneratorConfig M3U 生成器配置
type GeneratorConfig struct {
	Template string `ini:"template"`
}

// RTMPConfig RTMP 推流配置
type RTMPConfig struct {
	Enable    bool   `ini:"enable"`
	ServerURL string `ini:"server_url"`
}

// EPGConfig EPG 电子节目单配置
type EPGConfig struct {
	Enable    bool   `ini:"enable"`
	SourceURL string `ini:"source_url"`
}

// OutputConfig 输出路径配置
type OutputConfig struct {
	Directory string `ini:"directory"`
}

// SchedulerConfig 调度器配置
type SchedulerConfig struct {
	Enabled bool   `ini:"enabled"`
	Cron    string `ini:"cron"`
}

// DownloaderConfig 数据库下载器配置
type DownloaderConfig struct {
	DatabaseURL string `ini:"database_url"`
}

// Load 从指定路径加载 INI 配置文件
func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("配置文件 %s 不存在，使用默认配置\n", path)
		return cfg, nil
	}

	iniFile, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("加载配置文件失败: %w", err)
	}

	// 映射各节
	if err := iniFile.Section("server").MapTo(&cfg.Server); err != nil {
		return nil, err
	}
	if err := iniFile.Section("database").MapTo(&cfg.Database); err != nil {
		return nil, err
	}
	if err := iniFile.Section("collector").MapTo(&cfg.Collector); err != nil {
		return nil, err
	}
	if err := iniFile.Section("tester").MapTo(&cfg.Tester); err != nil {
		return nil, err
	}
	if err := iniFile.Section("subscriber").MapTo(&cfg.SubScriber); err != nil {
		return nil, err
	}
	if err := iniFile.Section("classifier").MapTo(&cfg.Classifier); err != nil {
		return nil, err
	}
	if err := iniFile.Section("generator").MapTo(&cfg.Generator); err != nil {
		return nil, err
	}
	if err := iniFile.Section("rtmp").MapTo(&cfg.RTMP); err != nil {
		return nil, err
	}
	if err := iniFile.Section("epg").MapTo(&cfg.EPG); err != nil {
		return nil, err
	}
	if err := iniFile.Section("output").MapTo(&cfg.Output); err != nil {
		return nil, err
	}
	if err := iniFile.Section("scheduler").MapTo(&cfg.Scheduler); err != nil {
		return nil, err
	}
	if err := iniFile.Section("downloader").MapTo(&cfg.Downloader); err != nil {
		return nil, err
	}
	if err := iniFile.Section("filter").MapTo(&cfg.Filter); err != nil {
		return nil, err
	}

	// 处理 Collector 中多个订阅源的特殊逻辑
	subURLs := iniFile.Section("collector").Key("subscription_url").String()
	if subURLs != "" {
		cfg.Collector.SubscriptionURLs = strings.Split(subURLs, ",")
	} else {
		var urls []string
		for i := 0; ; i++ {
			val := iniFile.Section("collector").Key(fmt.Sprintf("subscription_url_%d", i)).String()
			if val == "" {
				break
			}
			urls = append(urls, val)
		}
		if len(urls) > 0 {
			cfg.Collector.SubscriptionURLs = urls
		}
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

// Save 将当前配置持久化到指定路径的 INI 文件
func Save(path string, cfg *Config) error {
	iniFile := ini.Empty()

	if err := iniFile.Section("server").ReflectFrom(&cfg.Server); err != nil {
		return err
	}
	if err := iniFile.Section("database").ReflectFrom(&cfg.Database); err != nil {
		return err
	}
	if err := iniFile.Section("collector").ReflectFrom(&cfg.Collector); err != nil {
		return err
	}
	if err := iniFile.Section("tester").ReflectFrom(&cfg.Tester); err != nil {
		return err
	}
	if err := iniFile.Section("subscriber").ReflectFrom(&cfg.SubScriber); err != nil {
		return err
	}
	if err := iniFile.Section("classifier").ReflectFrom(&cfg.Classifier); err != nil {
		return err
	}
	if err := iniFile.Section("generator").ReflectFrom(&cfg.Generator); err != nil {
		return err
	}
	if err := iniFile.Section("rtmp").ReflectFrom(&cfg.RTMP); err != nil {
		return err
	}
	if err := iniFile.Section("epg").ReflectFrom(&cfg.EPG); err != nil {
		return err
	}
	if err := iniFile.Section("output").ReflectFrom(&cfg.Output); err != nil {
		return err
	}
	if err := iniFile.Section("scheduler").ReflectFrom(&cfg.Scheduler); err != nil {
		return err
	}
	if err := iniFile.Section("downloader").ReflectFrom(&cfg.Downloader); err != nil {
		return err
	}
	if err := iniFile.Section("filter").ReflectFrom(&cfg.Filter); err != nil {
		return err
	}

	return iniFile.SaveTo(path)
}

func setDefaults(cfg *Config) {
	cfg.Server.Port = 8080
	cfg.Server.Debug = false
	cfg.Database.Path = "data.db"
	cfg.Collector.IntervalMinutes = 240
	cfg.Tester.Timeout = 30000
	cfg.Tester.Concurrency = 10
	cfg.Tester.FfprobePath = "ffprobe"
	cfg.SubScriber.Enable = true
	cfg.Output.Directory = "output"
	cfg.Scheduler.Enabled = true
	cfg.Scheduler.Cron = "0 0 */2 * * *"
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LSM_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := os.Getenv("LSM_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("LSM_TESTER_CONCURRENCY"); v != "" {
		if c, err := strconv.Atoi(v); err == nil {
			cfg.Tester.Concurrency = c
		}
	}
	if v := os.Getenv("LSM_OUTPUT_DIR"); v != "" {
		cfg.Output.Directory = v
	}
	if v := os.Getenv("LSM_SCHEDULER_CRON"); v != "" {
		cfg.Scheduler.Cron = v
	}
}
