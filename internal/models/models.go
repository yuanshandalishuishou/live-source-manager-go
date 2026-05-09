// internal/models/models.go
//
// 项目所有数据模型定义，涵盖数据库表结构、API 交互对象和模块间交换的数据类型。
// 注意：部分字段（如 JSON 标签）需与前端及数据库列命名保持一致。
package models

import (
	"time"
)

// ---------- 系统配置 ----------

// SysConfig 系统配置持久化模型
type SysConfig struct {
	ID          int       `json:"id"`
	GroupName   string    `json:"group_name"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	ValueType   string    `json:"value_type"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}

// ---------- 用户与认证 ----------

// User 用户账户模型
type User struct {
	ID           int        `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`                      // 禁止 JSON 输出密码哈希
	IsAdmin      bool       `json:"is_admin"`
	IsActive     bool       `json:"is_active"`
	LastLogin    *time.Time `json:"last_login"`
}

// ---------- 直播源订阅 ----------

// LiveSource 订阅源文件/地址（外部 M3U/文本源）
type LiveSource struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	Location       string     `json:"location"`
	LocationType   string     `json:"location_type"`   // url, local_file
	Enable         bool       `json:"enable"`
	LastDownload   *time.Time `json:"last_download"`
	DownloadStatus string     `json:"download_status"`
	HTTPStatus     int        `json:"http_status"`
	RetryCount     int        `json:"retry_count"`
}

// ---------- 原始源条目 ----------

// URLSource 初步解析的源条目（来自订阅源解析后的单条记录）
type URLSource struct {
	ID            int       `json:"id"`
	LiveSourceID  int       `json:"live_source_id"`
	URL           string    `json:"url"`
	Name          string    `json:"name"`
	TvgID         string    `json:"tvg_id"`
	TvgLogo       string    `json:"tvg_logo"`
	GroupTitle    string    `json:"group_title"`
	Catchup       string    `json:"catchup"`
	CatchupDays   int       `json:"catchup_days"`
	UserAgent     string    `json:"user_agent"`
	RawAttributes string    `json:"raw_attributes"`
	SourceType    string    `json:"source_type"`
	CreatedAt     time.Time `json:"created_at"`
}

// ---------- 通过测试的有效源 ----------

// PassedSource 通过测试并可用于分发的有效源
type PassedSource struct {
	ID             int       `json:"id"`
	SourceID       int       `json:"source_id"`        // 关联 url_sources 的 id
	URL            string    `json:"url"`
	Name           string    `json:"name"`
	TvgID          string    `json:"tvg_id"`
	TvgLogo        string    `json:"tvg_logo"`
	GroupTitle     string    `json:"group_title"`
	Status         string    `json:"status"`           // active, failed, deprecated
	ResponseTimeMs int       `json:"response_time_ms"`
	Resolution     string    `json:"resolution"`       // 如 "1920x1080"
	Bitrate        int       `json:"bitrate"`
	LastChecked    time.Time `json:"last_checked"`
	Location       string    `json:"location"`
	ISP            string    `json:"isp"`
	CategoryIDs    []int     `json:"category_ids"`     // 多个分类 ID
}

// ---------- 展示规则 ----------

// DisplayRule 输出展示规则定义
type DisplayRule struct {
	ID                  int    `json:"id"`
	CategoryID          int    `json:"category_id"`
	CategoryName        string `json:"category_name"`
	GroupNameOverride   string `json:"group_name_override"`
	SortOrder           int    `json:"sort_order"`
	ItemSortOrder       string `json:"item_sort_order"` // "0" 降序, "1" 升序
	HideEmptyGroups     bool   `json:"hide_empty_groups"`
	MaxItemsPerCategory int    `json:"max_items_per_category"`
	Enable              bool   `json:"enable"`
}

// ---------- 黑白名单 ----------

// WhitelistRule 白名单规则
type WhitelistRule struct {
	ID         int    `json:"id"`
	Pattern    string `json:"pattern"`
	TargetType string `json:"target_type"`
	Priority   int    `json:"priority"`
	Enable     bool   `json:"enable"`
}

// BlacklistRule 黑名单规则
type BlacklistRule struct {
	ID         int    `json:"id"`
	Pattern    string `json:"pattern"`
	TargetType string `json:"target_type"`
	Enable     bool   `json:"enable"`
}

// ---------- RTMP 推流 ----------

// RTMPStream 当前推流记录
type RTMPStream struct {
	ID       int        `json:"id"`
	SourceID int        `json:"source_id"`
	Status   string     `json:"status"`
	PushURL  string     `json:"push_url"`
	HLSURL   string     `json:"hls_url"`
	LastPush *time.Time `json:"last_push"`
	IdleSec  int        `json:"idle_sec"`        // 空闲时间（秒）
}

// ---------- 历史与日志 ----------

// TestHistory 源测试历史
type TestHistory struct {
	ID             int       `json:"id"`
	SourceID       int       `json:"source_id"`
	TestTime       time.Time `json:"test_time"`
	Success        bool      `json:"success"`
	ResponseTimeMs int       `json:"response_time_ms"`
	StatusCode     int       `json:"status_code"`
	Resolution     string    `json:"resolution"`
	Bitrate        int       `json:"bitrate"`
	ErrorMessage   string    `json:"error_message"`
}

// ---------- EPG 节目单 ----------

// EPGProgram 电子节目单条目
type EPGProgram struct {
	ID          int       `json:"id"`
	EpgID       string    `json:"epg_id"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
}

// ---------- 测试器专用 ----------

// Source 测试器使用的通用源标识，仅包含测试所需的核心字段
type Source struct {
	ID   int    `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// StreamMeta 测试成功后提取的视频流元数据
type StreamMeta struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Codec   string `json:"codec"`
	BitRate int    `json:"bitrate"`
}

// ---------- 分类管理 ----------

// Category 频道分类模型，包含用于自动归类的关键字
type Category struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Keywords string `json:"keywords"` // 逗号分隔的关键词
}
