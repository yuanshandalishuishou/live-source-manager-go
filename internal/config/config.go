package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/ini.v1"
)

// Config 聚合所有配置项
type Config struct {
	WebServer struct {
		Port             int      `ini:"port"`
		JWTSecret        string   `ini:"jwt_secret"`
		TokenExpireHours int      `ini:"token_expire_hours"`
		RateLimit        float64  `ini:"rate_limit"`
		RateBurst        int      `ini:"rate_burst"`
		AllowedOrigins   []string `ini:"-"` // 特殊处理
	} `ini:"WebServer"`
	Network struct {
		ProxyEnabled bool   `ini:"proxy_enabled"`
		ProxyURL     string `ini:"proxy_url"`
	} `ini:"Network"`
	Testing struct {
		Timeout           int    `ini:"timeout"`
		ConcurrentThreads int    `ini:"concurrent_threads"`
		BatchSize         int    `ini:"batch_size"`
		FlushInterval     int    `ini:"flush_interval"`
		FfmpegPath        string `ini:"ffmpeg_path"`
		RecheckInterval   int    `ini:"recheck_interval"` // 小时
		MaxTestBatch      int    `ini:"max_test_batch"`
	} `ini:"Testing"`
	Output struct {
		Directory string `ini:"directory"`
		Filename  string `ini:"filename"`
	} `ini:"Output"`
	Filter struct {
		MaxLatency       int    `ini:"max_latency"`
		MinBitrate       int    `ini:"min_bitrate"`
		MinResolution    string `ini:"min_resolution"`
		MaxResolution    string `ini:"max_resolution"`
		Location         string `ini:"location"`
		ISP              string `ini:"isp"`
		OriginTypePrefer string `ini:"origin_type_prefer"`
	} `ini:"Filter"`
	RTMP struct {
		OpenRTMP       bool   `ini:"open_rtmp"`
		NginxHTTPPort  int    `ini:"nginx_http_port"`
		NginxRTMPPort  int    `ini:"nginx_rtmp_port"`
		IdleTimeout    int    `ini:"idle_timeout"`
		MaxStreams     int    `ini:"max_streams"`
		TranscodeMode  string `ini:"transcode_mode"`
		RetryMax       int    `ini:"retry_max"`
		RetryBaseDelay int    `ini:"retry_base_delay"`
		FfmpegPath     string `ini:"ffmpeg_path"`
	} `ini:"RTMP"`
	EPG struct {
		UpdateInterval int  `ini:"update_interval"`
		RetentionDays  int  `ini:"retention_days"`
		IncludeEPGURL  bool `ini:"include_epg_url"`
	} `ini:"EPG"`
	System struct {
		AdminUsername string `ini:"admin_username"`
		LockFile      string `ini:"lock_file"`
	} `ini:"System"`
}

// LoadConfig 加载配置：先读取默认值，再覆盖 ini 文件，最后覆盖环境变量
func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	// 如果指定了配置文件，加载并映射
	if configPath != "" {
		if err := loadIniFile(cfg, configPath); err != nil {
			return nil, fmt.Errorf("加载配置文件失败: %w", err)
		}
	} else {
		// 尝试默认路径
		defaultPaths := []string{"/config/config.ini", "./config/config.ini", "config.ini"}
		for _, p := range defaultPaths {
			if _, err := os.Stat(p); err == nil {
				if err := loadIniFile(cfg, p); err == nil {
					break
				}
			}
		}
	}
	// 环境变量覆盖
	applyEnvOverrides(cfg)
	return cfg, nil
}

func setDefaults(cfg *Config) {
	cfg.WebServer.Port = 23456
	cfg.WebServer.TokenExpireHours = 24
	cfg.WebServer.RateLimit = 10
	cfg.WebServer.RateBurst = 20
	cfg.WebServer.AllowedOrigins = []string{"*"}
	cfg.Testing.Timeout = 10
	cfg.Testing.ConcurrentThreads = 30
	cfg.Testing.BatchSize = 50
	cfg.Testing.FlushInterval = 2
	cfg.Testing.FfmpegPath = "ffmpeg"
	cfg.Testing.RecheckInterval = 24
	cfg.Testing.MaxTestBatch = 2000
	cfg.Output.Directory = "/www/output"
	cfg.Output.Filename = "live.m3u"
	cfg.EPG.UpdateInterval = 12
	cfg.EPG.RetentionDays = 7
	cfg.RTMP.IdleTimeout = 300
	cfg.RTMP.MaxStreams = 5
	cfg.RTMP.RetryMax = 3
	cfg.RTMP.RetryBaseDelay = 5
	cfg.RTMP.TranscodeMode = "copy"
	// ... 其他默认值
}

func loadIniFile(cfg *Config, path string) error {
	iniFile, err := ini.Load(path)
	if err != nil {
		return err
	}
	// 映射到结构体
	if err := iniFile.MapTo(cfg); err != nil {
		return err
	}
	// 处理特殊字段：allowed_origins（逗号分隔字符串）
	origins := iniFile.Section("WebServer").Key("allowed_origins").String()
	if origins != "" {
		cfg.WebServer.AllowedOrigins = strings.Split(origins, ",")
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	// 覆盖 WebServer 配置
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.WebServer.JWTSecret = v
	}
	if v := os.Getenv("PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.WebServer.Port)
	}
	if v := os.Getenv("FFMPEG_PATH"); v != "" {
		cfg.Testing.FfmpegPath = v
		cfg.RTMP.FfmpegPath = v
	}
	// 其他环境变量覆盖...
}
