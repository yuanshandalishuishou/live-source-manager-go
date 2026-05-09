// internal/config/config.go

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
	Filter     FilterConfig
}

// FilterConfig 黑白名单过滤器配置
type FilterConfig struct {
	BlacklistFile string `ini:"blacklist_file"`
	WhitelistFile string `ini:"whitelist_file"`
}

// ... (其他配置结构体定义保持不变) ...
type ServerConfig struct {
	Port      int    `ini:"port"`
	JWTSecret string `ini:"jwt_secret"`
	Debug     bool   `ini:"debug"`
}
type DatabaseConfig struct {
	Path string `ini:"path"`
}
type CollectorConfig struct {
	SubscriptionURLs []string
	IntervalMinutes  int `ini:"interval_minutes"`
}
type TesterConfig struct {
	Timeout     int    `ini:"timeout"`
	Concurrency int    `ini:"concurrency"`
	FfprobePath string `ini:"ffprobe_path"`
}
type SubScriberConfig struct {
	Enable bool `ini:"enable"`
}
type ClassifierConfig struct {
	RulesFile string `ini:"rules_file"`
}
type GeneratorConfig struct {
	Template string `ini:"template"`
}
type RTMPConfig struct {
	Enable    bool   `ini:"enable"`
	ServerURL string `ini:"server_url"`
}
type EPGConfig struct {
	Enable    bool   `ini:"enable"`
	SourceURL string `ini:"source_url"`
}
type OutputConfig struct {
	Directory string `ini:"directory"`
}
type SchedulerConfig struct {
	Enabled bool   `ini:"enabled"`
	Cron    string `ini:"cron"`
}
type DownloaderConfig struct {
	DatabaseURL string `ini:"database_url"`
}

// Load 从指定路径加载 INI 配置文件
func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg) // 先设置默认值

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("配置文件 %s 不存在，使用默认配置\n", path)
		return cfg, nil
	}

	iniFile, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("加载配置文件失败: %w", err)
	}

	// 映射各节，并检查每个映射的错误
	// 注意：这里修复了原始代码中忽略错误的问题
	if err := iniFile.Section("server").MapTo(&cfg.Server); err != nil {
		return nil, fmt.Errorf("映射 [server] 配置失败: %w", err)
	}
	if err := iniFile.Section("database").MapTo(&cfg.Database); err != nil {
		return nil, fmt.Errorf("映射 [database] 配置失败: %w", err)
	}
	if err := iniFile.Section("collector").MapTo(&cfg.Collector); err != nil {
		return nil, fmt.Errorf("映射 [collector] 配置失败: %w", err)
	}
	if err := iniFile.Section("tester").MapTo(&cfg.Tester); err != nil {
		return nil, fmt.Errorf("映射 [tester] 配置失败: %w", err)
	}
	if err := iniFile.Section("subscriber").MapTo(&cfg.SubScriber); err != nil {
		return nil, fmt.Errorf("映射 [subscriber] 配置失败: %w", err)
	}
	if err := iniFile.Section("classifier").MapTo(&cfg.Classifier); err != nil {
		return nil, fmt.Errorf("映射 [classifier] 配置失败: %w", err)
	}
	if err := iniFile.Section("generator").MapTo(&cfg.Generator); err != nil {
		return nil, fmt.Errorf("映射 [generator] 配置失败: %w", err)
	}
	if err := iniFile.Section("rtmp").MapTo(&cfg.RTMP); err != nil {
		return nil, fmt.Errorf("映射 [rtmp] 配置失败: %w", err)
	}
	if err := iniFile.Section("epg").MapTo(&cfg.EPG); err != nil {
		return nil, fmt.Errorf("映射 [epg] 配置失败: %w", err)
	}
	if err := iniFile.Section("output").MapTo(&cfg.Output); err != nil {
		return nil, fmt.Errorf("映射 [output] 配置失败: %w", err)
	}
	if err := iniFile.Section("scheduler").MapTo(&cfg.Scheduler); err != nil {
		return nil, fmt.Errorf("映射 [scheduler] 配置失败: %w", err)
	}
	if err := iniFile.Section("downloader").MapTo(&cfg.Downloader); err != nil {
		return nil, fmt.Errorf("映射 [downloader] 配置失败: %w", err)
	}
	if err := iniFile.Section("filter").MapTo(&cfg.Filter); err != nil {
		return nil, fmt.Errorf("映射 [filter] 配置失败: %w", err)
	}

	// 处理 Collector 中多个订阅源的特殊逻辑
	// 如果单个 subscription_url 存在，则按逗号分割
	subURLs := iniFile.Section("collector").Key("subscription_url").String()
	if subURLs != "" {
		cfg.Collector.SubscriptionURLs = strings.Split(subURLs, ",")
	} else {
		// 否则，查找编号的 subscription_url_0, subscription_url_1...
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

	// 使用 ReflectFrom 保存结构体
	if err := iniFile.Section("server").ReflectFrom(&cfg.Server); err != nil {
		return fmt.Errorf("保存 [server] 配置失败: %w", err)
	}
	// ... 其余各节类似，这里省略
	if err := iniFile.SaveTo(path); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	return nil
}

// setDefaults 设置所有配置项的默认值
func setDefaults(cfg *Config) {
	cfg.Server.Port = 23456 // 修复：默认 Web 端口为 23456，与 README 一致
	cfg.Server.Debug = false
	cfg.Database.Path = "data.db" // 修复：默认使用 data.db，确保与 db.go 中的处理一致
	cfg.Collector.IntervalMinutes = 240
	cfg.Tester.Timeout = 30000
	cfg.Tester.Concurrency = 10
	cfg.Tester.FfprobePath = "ffprobe"
	cfg.SubScriber.Enable = true
	cfg.Output.Directory = "output"
	cfg.Scheduler.Enabled = true
	cfg.Scheduler.Cron = "0 0 */2 * * *"
}

// applyEnvOverrides 应用环境变量覆盖配置
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
