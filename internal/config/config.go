// internal/config/config.go
// 全局配置管理：从 INI 文件加载配置，支持环境变量覆盖和默认值回退。
// 配置结构体分为多个节 (section)，对应 ini 文件中的各配置块。
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
	SubScriber SubScriberConfig // 注意拼写与项目保持一致，或按需调整
	Classifier ClassifierConfig
	Generator  GeneratorConfig
	RTMP       RTMPConfig
	EPG        EPGConfig
	Output     OutputConfig
	Scheduler  SchedulerConfig
	Downloader DownloaderConfig
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
	SubscriptionURLs []string // 由多个 ini 键组合而成，Load 时手动解析
	IntervalMinutes  int      `ini:"interval_minutes"`
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

// Load 从指定路径加载 INI 配置文件，并覆盖环境变量中的同名配置。
// 未设置的字段将使用合理的默认值。
func Load(path string) (*Config, error) {
	cfg := &Config{}

	// 设置默认值
	setDefaults(cfg)

	// 如果文件不存在，则使用默认值（不影响）
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

	// 处理 Collector 中多个订阅源的特殊逻辑
	// 支持 subscription_url = url1,url2,url3 或 subscription_url_0, subscription_url_1 ...
	subURLs := iniFile.Section("collector").Key("subscription_url").String()
	if subURLs != "" {
		cfg.Collector.SubscriptionURLs = strings.Split(subURLs, ",")
	} else {
		// 备用：读取连续编号的键
		var urls []string
		for i := 0; ; i++ {
			key := fmt.Sprintf("subscription_url_%d", i)
			val := iniFile.Section("collector").Key(key).String()
			if val == "" {
				break
			}
			urls = append(urls, val)
		}
		if len(urls) > 0 {
			cfg.Collector.SubscriptionURLs = urls
		}
	}

	// 环境变量覆盖（优先级最高）
	applyEnvOverrides(cfg)

	return cfg, nil
}

// setDefaults 设置所有字段的默认值
func setDefaults(cfg *Config) {
	cfg.Server.Port = 8080
	cfg.Server.Debug = false
	cfg.Database.Path = "data.db"
	cfg.Collector.IntervalMinutes = 240
	cfg.Tester.Timeout = 30000 // 毫秒
	cfg.Tester.Concurrency = 10
	cfg.Tester.FfprobePath = "ffprobe"
	cfg.SubScriber.Enable = true
	cfg.Output.Directory = "output"
	cfg.Scheduler.Enabled = true
	cfg.Scheduler.Cron = "0 0 */2 * * *" // 每2小时
}

// applyEnvOverrides 用环境变量覆盖配置值（前缀为 LSM_）
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
	// 更多环境变量可按需添加
}
