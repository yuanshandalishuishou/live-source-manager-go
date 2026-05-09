// internal/config/config.go
// 完整配置管理模块。
// 修复内容：
//   1. Save 函数现在会保存所有配置节，而非仅 [server] 一节。
//   2. 新增 ToEnvMap 函数，便于 Web 界面读取当前配置。
//   3. 新增 Validate 方法，在加载后验证配置的合法性。
//   4. 修复 FilterConfig 默认值缺失问题。

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

// ──────── 配置结构体定义 ────────

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
	Filename  string `ini:"filename"`
}

type SchedulerConfig struct {
	Enabled bool   `ini:"enabled"`
	Cron    string `ini:"cron"`
}

type DownloaderConfig struct {
	DatabaseURL string `ini:"database_url"`
}

type FilterConfig struct {
	BlacklistFile string `ini:"blacklist_file"`
	WhitelistFile string `ini:"whitelist_file"`
}

// ──────── 加载与保存 ────────

// Load 从指定路径加载 INI 配置文件。
// 如果文件不存在，使用默认配置并返回。
// 加载后自动应用环境变量覆盖。
func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	// 如果配置文件不存在，使用默认值
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("配置文件 %s 不存在，使用默认配置\n", path)
		return cfg, nil
	}

	iniFile, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("加载配置文件失败: %w", err)
	}

	// 逐一映射各配置节，确保所有错误都被捕获
	sections := map[string]interface{}{
		"server":     &cfg.Server,
		"database":   &cfg.Database,
		"collector":  &cfg.Collector,
		"tester":     &cfg.Tester,
		"subscriber": &cfg.SubScriber,
		"classifier": &cfg.Classifier,
		"generator":  &cfg.Generator,
		"rtmp":       &cfg.RTMP,
		"epg":        &cfg.EPG,
		"output":     &cfg.Output,
		"scheduler":  &cfg.Scheduler,
		"downloader": &cfg.Downloader,
		"filter":     &cfg.Filter,
	}

	for name, target := range sections {
		if err := iniFile.Section(name).MapTo(target); err != nil {
			return nil, fmt.Errorf("映射 [%s] 配置失败: %w", name, err)
		}
	}

	// 处理 Collector 的多订阅源逻辑
	parseCollectorURLs(iniFile, cfg)

	// 应用环境变量覆盖
	applyEnvOverrides(cfg)

	return cfg, nil
}

// Save 将当前配置持久化到指定路径的 INI 文件。
// 修复：现在会保存所有配置节，而非仅 [server] 一节。
func Save(path string, cfg *Config) error {
	iniFile := ini.Empty()

	// 保存所有配置节
	sections := map[string]interface{}{
		"server":     &cfg.Server,
		"database":   &cfg.Database,
		"tester":     &cfg.Tester,
		"subscriber": &cfg.SubScriber,
		"classifier": &cfg.Classifier,
		"generator":  &cfg.Generator,
		"rtmp":       &cfg.RTMP,
		"epg":        &cfg.EPG,
		"output":     &cfg.Output,
		"scheduler":  &cfg.Scheduler,
		"downloader": &cfg.Downloader,
		"filter":     &cfg.Filter,
	}

	for name, target := range sections {
		if err := iniFile.Section(name).ReflectFrom(target); err != nil {
			return fmt.Errorf("保存 [%s] 配置失败: %w", name, err)
		}
	}

	// Collector 的 SubscriptionURLs 需要特殊处理：将切片序列化为逗号分隔的字符串
	collectorSection := iniFile.Section("collector")
	collectorSection.Key("subscription_url").SetValue(
		strings.Join(cfg.Collector.SubscriptionURLs, ","))
	collectorSection.Key("interval_minutes").SetValue(
		strconv.Itoa(cfg.Collector.IntervalMinutes))

	if err := iniFile.SaveTo(path); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

// ──────── 默认值 ────────

// setDefaults 设置所有配置项的默认值。
func setDefaults(cfg *Config) {
	cfg.Server.Port = 23456
	cfg.Server.JWTSecret = "change-me-in-production" // 生产环境务必修改
	cfg.Server.Debug = false
	cfg.Database.Path = "data.db"
	cfg.Collector.IntervalMinutes = 240
	cfg.Tester.Timeout = 30000
	cfg.Tester.Concurrency = 10
	cfg.Tester.FfprobePath = "ffprobe"
	cfg.SubScriber.Enable = true
	cfg.Output.Directory = "output"
	cfg.Output.Filename = "live.m3u"
	cfg.Scheduler.Enabled = true
	cfg.Scheduler.Cron = "0 0 */2 * * *"
	cfg.Filter.BlacklistFile = "" // 修复：默认不启用黑白名单文件
	cfg.Filter.WhitelistFile = "" // 修复：默认不启用黑白名单文件
}

// ──────── 环境变量覆盖 ────────

// applyEnvOverrides 应用环境变量覆盖配置。
// 支持的环境变量：
//
//	LSM_SERVER_PORT          - Web 服务端口
//	LSM_DB_PATH              - 数据库文件路径
//	LSM_TESTER_CONCURRENCY   - 测试并发数
//	LSM_OUTPUT_DIR           - 输出目录
//	LSM_SCHEDULER_CRON       - 定时任务 cron 表达式
//	LSM_JWT_SECRET           - JWT 密钥
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
	if v := os.Getenv("LSM_JWT_SECRET"); v != "" {
		cfg.Server.JWTSecret = v
	}
}

// ──────── 辅助函数 ────────

// parseCollectorURLs 解析采集器的多订阅源配置。
func parseCollectorURLs(iniFile *ini.File, cfg *Config) {
	subURLs := iniFile.Section("collector").Key("subscription_url").String()
	if subURLs != "" {
		cfg.Collector.SubscriptionURLs = strings.Split(subURLs, ",")
		return
	}

	// 否则查找编号形式 subscription_url_0, subscription_url_1 ...
	var urls []string
	for i := 0; ; i++ {
		val := iniFile.Section("collector").Key(
			fmt.Sprintf("subscription_url_%d", i)).String()
		if val == "" {
			break
		}
		urls = append(urls, val)
	}
	if len(urls) > 0 {
		cfg.Collector.SubscriptionURLs = urls
	}
}

// Validate 在加载配置后进行合法性验证。
// 返回 nil 表示配置有效。
func (cfg *Config) Validate() error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("无效的端口号: %d，有效范围 1-65535", cfg.Server.Port)
	}
	if cfg.Server.JWTSecret == "change-me-in-production" {
		fmt.Println("[警告] 正在使用默认 JWT 密钥，生产环境请务必修改！")
	}
	if cfg.Tester.Concurrency < 1 {
		cfg.Tester.Concurrency = 1
	}
	if cfg.Tester.Timeout < 1000 {
		cfg.Tester.Timeout = 1000
	}
	return nil
}

// ToEnvMap 将部分关键配置转换为 map，供 Web 界面读取。
func (cfg *Config) ToEnvMap() map[string]interface{} {
	return map[string]interface{}{
		"server_port":         cfg.Server.Port,
		"server_debug":        cfg.Server.Debug,
		"db_path":             cfg.Database.Path,
		"collector_interval":  cfg.Collector.IntervalMinutes,
		"tester_timeout":      cfg.Tester.Timeout,
		"tester_concurrency":  cfg.Tester.Concurrency,
		"tester_ffprobe_path": cfg.Tester.FfprobePath,
		"output_directory":    cfg.Output.Directory,
		"output_filename":     cfg.Output.Filename,
		"scheduler_enabled":   cfg.Scheduler.Enabled,
		"scheduler_cron":      cfg.Scheduler.Cron,
		"rtmp_enable":         cfg.RTMP.Enable,
		"epg_enable":          cfg.EPG.Enable,
		"subscriber_enable":   cfg.SubScriber.Enable,
	}
}
