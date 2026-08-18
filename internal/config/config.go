// Package config provides the application configuration backed by the SQLite app_config table.
//
// All default config keys are defined here (matching the Python project's Config._DEFAULT_VALUES)
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
			// iptv-org.github.io 已被墙且路径已重组为 streams/，改用实测可达的 raw 源：
			"https://raw.githubusercontent.com/iptv-org/iptv/master/streams/tw.m3u",
			"https://raw.githubusercontent.com/iptv-org/iptv/master/streams/hk.m3u",
		}, "\n"),
		"Sources.github_sources": strings.Join([]string{
			// 注意：wcb1969/iptv 已被 GitHub 全局 451 封锁（区域无关的硬封锁），
			// 代理/镜像均无法恢复，故从默认源移除；收音机/电影/MTV 请走 Sources.local_dirs 自备。
			"joevess/IPTV/main",
			"suxuang/myIPTV/main",
			"YueChan/Live",
			"YanG-1989/m3u",
			"qwerttvv/Beijing-IPTV",
			"joevess/IPTV",
			"cymz6/AutoIPTV-Hotel",
			"Rivens7/Livelist",
		}, "\n"),
		"Sources.github_source_settings":       "{}",
		"Sources.source_file_ua_settings":      "{}",
		"Sources.channel_ua_overrides":         "{}",
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
		"Output.output_dir":              "./www/output",
		// [Logging]
		"Logging.level":        "INFO",
		"Logging.file":         "./log/app.log",
		"Logging.max_size":     "10",
		"Logging.backup_count": "5",
		// [Session] 会话过期（秒）。首启即播种，使配置中心可见可改（对齐 Python 完整播种）。
		"Session.idle_timeout": "1800",
		"Session.session_ttl":  "28800",
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
		"UserAgents.ua_enabled":  "True",
		// [Referrers]
		"Referrers.referer_enabled":  "True",
		"Referrers.referer_position": "extinf",
		// [EPG] 电子节目单。默认全开：总开关与注入开关同时为 True，
		// 避免出现「抓了节目单却不注入」或「注入了 url-tvg 却是死链」的割裂状态。
		"EPG.enabled":         "True",
		"EPG.inject_into_m3u": "True",
		"EPG.output_filename": "epg.xml.gz",
		"EPG.refresh_mode":    "daily",
		"EPG.refresh_at":      "03:30",
		"EPG.refresh_minutes": "360",
		"EPG.timezone":        "Asia/Shanghai",
		"EPG.keep_days":       "7",
		"EPG.past_hours":      "6",
		"EPG.fetch_timeout":   "60",
		"EPG.max_concurrent":  "3",
		"EPG.web_base_url":    "",
	}
}

// FieldOptions lists fixed choices for config keys that should render as a
// <select> in the UI instead of a free-text input.
func FieldOptions() map[string][]string {
	return map[string][]string{
		"Network.proxy_type":            {"socks5", "http"},
		"Testing.auto_scan_mode":        {"interval", "daily"},
		"Testing.output_sort_by":        {"speed", "resolution", "name"},
		"Output.group_by":               {"category", "source", "country", "name"},
		"Filter.resolution_filter_mode": {"range", "min", "max"},
		"Filter.min_resolution":         {"240p", "360p", "480p", "720p", "1080p", "1440p", "4k"},
		"Filter.max_resolution":         {"240p", "360p", "480p", "720p", "1080p", "1440p", "4k"},
		"UserAgents.ua_position":        {"extinf", "url"},
		"Referrers.referer_position":    {"extinf", "url"},
		"Logging.level":                 {"DEBUG", "INFO", "WARNING", "ERROR"},
		"EPG.refresh_mode":              {"daily", "interval", "manual"},
	}
}

// SecretKeys are config keys whose values must be masked in the UI and skipped
// when left empty on save (password-like fields).
func SecretKeys() map[string]bool {
	return map[string]bool{
		"Network.proxy_password": true,
		"Network.proxy_username": true,
		"GitHub.api_token":       true,
	}
}

// FieldInfo carries the Chinese display metadata for a single config key.
// Label is the human-readable field name shown in the UI; Description is a
// short hint explaining the field's purpose.
type FieldInfo struct {
	Label       string
	Description string
}

// SectionTitles maps each config section to its Chinese title.
func SectionTitles() map[string]string {
	return map[string]string{
		"Sources":    "源管理",
		"Network":    "网络代理",
		"HTTPServer": "HTTP 服务",
		"GitHub":     "GitHub 源",
		"Testing":    "探测测试",
		"Tools":      "工具路径",
		"Output":     "输出设置",
		"Filter":     "过滤规则",
		"Logging":    "日志",
		"Session":    "会话安全",
		"UserAgents": "User-Agent",
		"Referrers":  "Referer",
		"EPG":        "电子节目单（EPG）",
	}
}

// SectionOrder returns the logical display order of config sections.
func SectionOrder() []string {
	return []string{
		"Sources",
		"Network",
		"HTTPServer",
		"GitHub",
		"Testing",
		"Tools",
		"Output",
		"Filter",
		"Logging",
		"Session",
		"UserAgents",
		"Referrers",
		"EPG",
	}
}

// FieldMeta maps "Section.key" to its Chinese label and description.
func FieldMeta() map[string]FieldInfo {
	return map[string]FieldInfo{
		// [Sources]
		"Sources.local_dirs":            {Label: "本地源目录", Description: "本地存放 M3U/TXT 源文件的目录，多个用逗号分隔。"},
		"Sources.online_urls":           {Label: "在线源地址", Description: "在线 M3U 源地址列表，每行一个。"},
		"Sources.github_sources":        {Label: "GitHub 源仓库", Description: "从 GitHub 拉取的源仓库，格式为 owner/repo 或 owner/repo/branch。"},
		"Sources.github_source_settings": {Label: "GitHub 源高级设置", Description: "针对各 GitHub 源的覆盖设置（JSON 格式）。"},
		"Sources.source_file_ua_settings": {Label: "源文件 UA 设置", Description: "为不同源文件指定 User-Agent（JSON 格式）。"},
		"Sources.channel_ua_overrides":  {Label: "频道 UA 覆盖", Description: "为指定频道覆盖 User-Agent（JSON 格式）。"},
		"Sources.source_file_referer_settings": {Label: "源文件 Referer 设置", Description: "为不同源文件指定 Referer（JSON 格式）。"},
		"Sources.channel_referer_overrides": {Label: "频道 Referer 覆盖", Description: "为指定频道覆盖 Referer（JSON 格式）。"},
		// [Network]
		"Network.proxy_enabled":  {Label: "启用代理", Description: "开启后所有对外请求（GitHub、在线源）走代理。"},
		"Network.proxy_type":     {Label: "代理类型", Description: "代理协议类型。"},
		"Network.proxy_host":     {Label: "代理主机", Description: "代理服务器地址。"},
		"Network.proxy_port":     {Label: "代理端口", Description: "代理服务器端口。"},
		"Network.proxy_username": {Label: "代理用户名", Description: "代理认证用户名（留空表示无需认证）。"},
		"Network.proxy_password": {Label: "代理密码", Description: "代理认证密码（留空表示无需认证）。"},
		"Network.github_mirror":  {Label: "GitHub 镜像", Description: "加速 GitHub 访问的反向代理地址。"},
		"Network.ipv6_enabled":   {Label: "启用 IPv6", Description: "允许探测与输出 IPv6 源。"},
		// [HTTPServer]
		"HTTPServer.enabled":        {Label: "启用内置服务器", Description: "启动内置的文件发布与管理 Web 服务。"},
		"HTTPServer.host":           {Label: "监听地址", Description: "服务绑定的网络地址，0.0.0.0 表示监听所有网卡。"},
		"HTTPServer.fileshare_port": {Label: "文件发布端口", Description: "对外提供 M3U/EPG 文件下载的端口。"},
		"HTTPServer.manager_port":   {Label: "管理端口", Description: "后台管理界面的访问端口。"},
		"HTTPServer.document_root":  {Label: "文件根目录", Description: "文件发布服务对外暴露的根目录。"},
		// [GitHub]
		"GitHub.api_url":    {Label: "API 地址", Description: "GitHub REST API 基地址。"},
		"GitHub.api_token":  {Label: "访问令牌", Description: "带 repo 权限的个人访问令牌（PAT），用于鉴权与提升限额。"},
		"GitHub.rate_limit": {Label: "速率限制", Description: "GitHub API 每小时请求上限。"},
		// [Testing]
		"Testing.timeout":                  {Label: "探测超时", Description: "单次探测的最长等待时间（秒）。"},
		"Testing.concurrent_threads":       {Label: "并发线程数", Description: "同时探测的源数量。"},
		"Testing.max_concurrent_ffprobe":   {Label: "最大 ffprobe 并发", Description: "ffprobe 进程的最大并发数。"},
		"Testing.cache_ttl":                {Label: "缓存有效期", Description: "探测结果缓存时间（秒）。"},
		"Testing.enable_speed_test":        {Label: "启用测速", Description: "探测时测量源的实际速度。"},
		"Testing.speed_test_duration":      {Label: "测速时长", Description: "每个源测速采样的持续时间（秒）。"},
		"Testing.auto_scan_enabled":        {Label: "启用自动探测", Description: "按计划周期性自动执行源探测。"},
		"Testing.auto_scan_mode":           {Label: "自动探测模式", Description: "按固定间隔还是每日固定时间触发。"},
		"Testing.auto_scan_interval_hours": {Label: "间隔小时数", Description: "间隔模式下两次探测的间隔（小时）。"},
		"Testing.auto_scan_daily_time":     {Label: "每日探测时间", Description: "每日模式下触发探测的本地时间（HH:MM）。"},
		"Testing.enable_host_speed_share":  {Label: "启用主机测速共享", Description: "向社区共享本机测速结果以加速其他用户探测。"},
		"Testing.enable_source_freeze":     {Label: "启用源冻结", Description: "连续失败的源将被临时冻结，避免反复无效探测。"},
		"Testing.freeze_fail_threshold":    {Label: "冻结失败阈值", Description: "达到该失败次数后冻结源。"},
		"Testing.freeze_base_seconds":      {Label: "冻结基准秒数", Description: "冻结的基础时长（秒）。"},
		"Testing.freeze_max_hours":         {Label: "冻结最大小时", Description: "冻结的最长持续时间（小时）。"},
		"Testing.enable_ad_detect":         {Label: "启用广告检测", Description: "根据关键词识别并剔除广告/测试卡频道。"},
		"Testing.ad_keywords":              {Label: "广告关键词", Description: "用于广告识别的关键词，逗号分隔。"},
		"Testing.ad_max_duration":          {Label: "广告最大时长", Description: "判定为广告片段的最大时长（秒）。"},
		"Testing.global_blacklist":         {Label: "全局黑名单", Description: "全局强制排除的频道/源关键词，逗号分隔。"},
		"Testing.global_whitelist":         {Label: "全局白名单", Description: "全局强制保留的频道/源关键词，逗号分隔。"},
		"Testing.output_sort_by":           {Label: "输出排序方式", Description: "生成 M3U 时频道内源的排序依据。"},
		"Testing.max_test_attempts":        {Label: "最大探测次数", Description: "单个源失败前的重试次数。"},
		// [Tools]
		"Tools.ffmpeg_dir": {Label: "ffmpeg 目录", Description: "ffmpeg/ffprobe 可执行文件所在目录（留空则使用系统 PATH）。"},
		// [Output]
		"Output.filename":                {Label: "输出文件名", Description: "生成的 M3U 播放列表文件名。"},
		"Output.group_by":                {Label: "分组方式", Description: "频道在播放列表中的分组维度。"},
		"Output.include_failed":          {Label: "包含失败源", Description: "即使探测失败也保留该源。"},
		"Output.max_sources_per_channel": {Label: "每频道最大源数", Description: "单个频道最多保留的源数量。"},
		"Output.enable_filter":           {Label: "启用过滤", Description: "按过滤规则剔除不达标源。"},
		"Output.whitelist_force_keep":    {Label: "白名单强制保留", Description: "白名单中的频道即便不达标也强制保留。"},
		"Output.output_dir":              {Label: "输出目录", Description: "生成文件（M3U/EPG）的写入目录。"},
		// [Filter]
		"Filter.max_latency":            {Label: "最大延迟", Description: "超过该延迟（毫秒）的源将被过滤。"},
		"Filter.min_bitrate":            {Label: "最小码率", Description: "低于该码率（Kbps）的源将被过滤。"},
		"Filter.must_hd":                {Label: "必须高清", Description: "仅保留高清及以上分辨率的源。"},
		"Filter.must_4k":                {Label: "必须 4K", Description: "仅保留 4K 分辨率的源。"},
		"Filter.min_speed":              {Label: "最小速度", Description: "低于该速度（KB/s）的源将被过滤。"},
		"Filter.min_resolution":         {Label: "最小分辨率", Description: "低于该分辨率的源将被过滤。"},
		"Filter.max_resolution":         {Label: "最大分辨率", Description: "高于该分辨率的源将被过滤。"},
		"Filter.resolution_filter_mode": {Label: "分辨率过滤模式", Description: "区间/最小/最大三种过滤策略。"},
		// [Logging]
		"Logging.level":        {Label: "日志级别", Description: "记录日志的最低级别。"},
		"Logging.file":         {Label: "日志文件", Description: "日志写入的文件路径。"},
		"Logging.max_size":     {Label: "单文件最大体积", Description: "单个日志文件达到该体积（MB）后滚动。"},
		"Logging.backup_count": {Label: "备份数量", Description: "保留的历史日志文件份数。"},
		// [Session]
		"Session.idle_timeout": {Label: "空闲超时", Description: "无操作多少秒后会话失效。"},
		"Session.session_ttl":  {Label: "会话有效期", Description: "会话从登录起的最长存活时间（秒）。"},
		// [UserAgents]
		"UserAgents.ua_position": {Label: "UA 注入位置", Description: "User-Agent 注入到 EXTINF 属性还是 URL 后缀。"},
		"UserAgents.ua_enabled":  {Label: "启用 UA 注入", Description: "为源请求附加 User-Agent 头。"},
		// [Referrers]
		"Referrers.referer_enabled":  {Label: "启用 Referer 注入", Description: "为源请求附加 Referer 头。"},
		"Referrers.referer_position": {Label: "Referer 注入位置", Description: "Referer 注入到 EXTINF 属性还是 URL 后缀。"},
		// [EPG]
		"EPG.enabled":         {Label: "启用 EPG", Description: "抓取并管理电子节目单。"},
		"EPG.inject_into_m3u": {Label: "注入到 M3U", Description: "将 EPG 地址写入生成的播放列表。"},
		"EPG.output_filename": {Label: "输出文件名", Description: "生成的 EPG 文件名。"},
		"EPG.refresh_mode":    {Label: "刷新模式", Description: "EPG 的更新触发方式。"},
		"EPG.refresh_at":      {Label: "每日刷新时间", Description: "每日模式下刷新 EPG 的本地时间（HH:MM）。"},
		"EPG.refresh_minutes": {Label: "间隔刷新分钟", Description: "间隔模式下两次刷新的间隔（分钟）。"},
		"EPG.timezone":        {Label: "时区", Description: "EPG 节目时间使用的时区。"},
		"EPG.keep_days":       {Label: "保留天数", Description: "EPG 数据保留的天数。"},
		"EPG.past_hours":      {Label: "过去小时数", Description: "一并保留的过去节目小时数。"},
		"EPG.fetch_timeout":   {Label: "抓取超时", Description: "单次 EPG 抓取的最长等待时间（秒）。"},
		"EPG.max_concurrent":  {Label: "最大并发", Description: "EPG 抓取的最大并发数。"},
		"EPG.web_base_url":    {Label: "Web 基地址", Description: "对外暴露 EPG 的 Web 基地址（留空则自动推断）。"},
	}
}

// OptionLabels maps "Section.key" to a value->Chinese-label map for the fixed
// choices rendered as <select> in the UI.
func OptionLabels() map[string]map[string]string {
	return map[string]map[string]string{
		"Network.proxy_type": {
			"socks5": "SOCKS5",
			"http":   "HTTP",
		},
		"Testing.auto_scan_mode": {
			"interval": "固定间隔",
			"daily":    "每日定时",
		},
		"Testing.output_sort_by": {
			"speed":      "速度",
			"resolution": "分辨率",
			"name":       "名称",
		},
		"Output.group_by": {
			"category": "分类",
			"source":   "来源",
			"country":  "国家",
			"name":     "名称",
		},
		"Filter.resolution_filter_mode": {
			"range": "区间",
			"min":   "最小值",
			"max":   "最大值",
		},
		"Filter.min_resolution": {
			"240p":  "240p（标清）",
			"360p":  "360p",
			"480p":  "480p（标清）",
			"720p":  "720p（高清）",
			"1080p": "1080p（全高清）",
			"1440p": "1440p（2K）",
			"4k":    "4K（超高清）",
		},
		"Filter.max_resolution": {
			"240p":  "240p（标清）",
			"360p":  "360p",
			"480p":  "480p（标清）",
			"720p":  "720p（高清）",
			"1080p": "1080p（全高清）",
			"1440p": "1440p（2K）",
			"4k":    "4K（超高清）",
		},
		"UserAgents.ua_position": {
			"extinf": "EXTINF 属性",
			"url":    "URL 后缀",
		},
		"Referrers.referer_position": {
			"extinf": "EXTINF 属性",
			"url":    "URL 后缀",
		},
		"Logging.level": {
			"DEBUG":   "调试（DEBUG）",
			"INFO":    "信息（INFO）",
			"WARNING": "警告（WARNING）",
			"ERROR":   "错误（ERROR）",
		},
		"EPG.refresh_mode": {
			"daily":   "每日定时",
			"interval": "固定间隔",
			"manual":  "手动",
		},
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
func (c *Config) GetUAEnabled() bool    { return c.GetBool("UserAgents", "ua_enabled", true) }
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
func (c *Config) GetReferrerEnabled() bool { return c.GetBool("Referrers", "referer_enabled", true) }
func (c *Config) GetReferrerPosition() string {
	return c.Get("Referrers", "referer_position", "extinf")
}

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
