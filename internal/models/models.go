// Package models 定义与数据库表对应的结构体，用于数据传递和 ORM 操作。
package models

import (
	"database/sql"
	"time"
)

// SysConfig 系统配置表
type SysConfig struct {
	ID          int64     `json:"id"`
	GroupName   string    `json:"group_name"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	ValueType   string    `json:"value_type"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// User 用户表
type User struct {
	ID           int64        `json:"id"`
	Username     string       `json:"username"`
	PasswordHash string       `json:"-"`
	IsAdmin      bool         `json:"is_admin"`
	CreatedAt    time.Time    `json:"created_at"`
	LastLogin    sql.NullTime `json:"last_login"`
	IsActive     bool         `json:"is_active"`
}

// LiveSource 直播源文件表
type LiveSource struct {
	ID             int64         `json:"id"`
	Name           string        `json:"name"`
	Location       string        `json:"location"`
	LocationType   string        `json:"location_type"`
	Enable         bool          `json:"enable"`
	LastDownload   sql.NullTime  `json:"last_download"`
	DownloadStatus string        `json:"download_status"`
	HTTPStatus     sql.NullInt32 `json:"http_status"`
	RetryCount     int           `json:"retry_count"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// URLSource 原始直播源条目表
type URLSource struct {
	ID            int64          `json:"id"`
	LiveSourceID  int64          `json:"live_source_id"`
	URL           string         `json:"url"`
	Name          string         `json:"name"`
	TvgID         sql.NullString `json:"tvg_id"`
	TvgLogo       sql.NullString `json:"tvg_logo"`
	GroupTitle    sql.NullString `json:"group_title"`
	Catchup       sql.NullString `json:"catchup"`
	CatchupDays   sql.NullInt32  `json:"catchup_days"`
	UserAgent     sql.NullString `json:"user_agent"`
	RawAttributes sql.NullString `json:"raw_attributes"`
	SourceType    string         `json:"source_type"`
	CreatedAt     time.Time      `json:"created_at"`
}

// Category 分类表
type Category struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	ParentID     sql.NullInt64  `json:"parent_id"`
	Priority     int            `json:"priority"`
	KeywordRules sql.NullString `json:"keyword_rules"` // JSON 格式：{"type":"regex|keyword","patterns":[...]}
	SortOrder    int            `json:"sort_order"`
	Description  sql.NullString `json:"description"`
	CreatedAt    time.Time      `json:"created_at"`
}

// ChannelAlias 频道别名表（正则匹配）
type ChannelAlias struct {
	ID          int64          `json:"id"`
	Pattern     string         `json:"pattern"`
	TargetName  string         `json:"target_name"`
	Priority    int            `json:"priority"`
	Enable      bool           `json:"enable"`
	Description sql.NullString `json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
}

// SourceCategory 源-分类关联表
type SourceCategory struct {
	SourceID   int64 `json:"source_id"`
	CategoryID int64 `json:"category_id"`
}

// URLSourcePassed 验证通过的直播源表
type URLSourcePassed struct {
	ID             int64           `json:"id"`
	URL            string          `json:"url"`
	Name           string          `json:"name"`
	TvgID          sql.NullString  `json:"tvg_id"`
	TvgLogo        sql.NullString  `json:"tvg_logo"`
	GroupTitle     sql.NullString  `json:"group_title"`
	Catchup        sql.NullString  `json:"catchup"`
	CatchupDays    sql.NullInt32   `json:"catchup_days"`
	UserAgent      sql.NullString  `json:"user_agent"`
	SourceType     string          `json:"source_type"`
	RawAttributes  sql.NullString  `json:"raw_attributes"`
	LiveSourceID   sql.NullInt64   `json:"live_source_id"`
	EPGID          sql.NullString  `json:"epg_id"`
	EPGName        sql.NullString  `json:"epg_name"`
	EPGLogo        sql.NullString  `json:"epg_logo"`
	Status         string          `json:"status"` // active/inactive/unknown
	ResponseTimeMs sql.NullInt32   `json:"response_time_ms"`
	Resolution     sql.NullString  `json:"resolution"`
	Bitrate        sql.NullInt32   `json:"bitrate"`
	VideoCodec     sql.NullString  `json:"video_codec"`
	AudioCodec     sql.NullString  `json:"audio_codec"`
	FrameRate      sql.NullFloat64 `json:"frame_rate"`
	DownloadSpeed  sql.NullFloat64 `json:"download_speed"`
	LastChecked    sql.NullTime    `json:"last_checked"`
	FailCount      int             `json:"fail_count"`
	TestStatus     sql.NullString  `json:"test_status"`
	ErrorMessage   sql.NullString  `json:"error_message"`
	Location       sql.NullString  `json:"location"` // 归属地
	ISP            sql.NullString  `json:"isp"`      // 运营商
	ExtraAttrs     sql.NullString  `json:"extra_attrs"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// DisplayRule 显示规则表
type DisplayRule struct {
	ID                  int64          `json:"id"`
	CategoryID          int64          `json:"category_id"`
	GroupNameOverride   sql.NullString `json:"group_name_override"`
	SortOrder           int            `json:"sort_order"`
	ItemSortOrder       string         `json:"item_sort_order"`
	HideEmptyGroups     bool           `json:"hide_empty_groups"`
	MaxItemsPerCategory int            `json:"max_items_per_category"`
	FilterResolutionMin sql.NullString `json:"filter_resolution_min"`
	Enable              bool           `json:"enable"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

// Whitelist 白名单表
type Whitelist struct {
	ID          int64          `json:"id"`
	Pattern     string         `json:"pattern"`
	TargetType  string         `json:"target_type"` // url/channel_name/tvg_id
	Enable      bool           `json:"enable"`
	Priority    int            `json:"priority"`
	Description sql.NullString `json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Blacklist 黑名单表
type Blacklist struct {
	ID          int64          `json:"id"`
	Pattern     string         `json:"pattern"`
	TargetType  string         `json:"target_type"`
	Enable      bool           `json:"enable"`
	Description sql.NullString `json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
}

// RTMPStream RTMP推流管理表
type RTMPStream struct {
	ID           int64          `json:"id"`
	SourceID     int64          `json:"source_id"`
	StreamStatus string         `json:"stream_status"` // stopped/pushing/error
	PushURL      sql.NullString `json:"push_url"`
	HLSURL       sql.NullString `json:"hls_url"`
	LastPushTime sql.NullTime   `json:"last_push_time"`
	IdleSeconds  int            `json:"idle_seconds"`
	ErrorMessage sql.NullString `json:"error_message"`
	CreatedAt    time.Time      `json:"created_at"`
}

// TestProgress 测试进度表
type TestProgress struct {
	ID            int64          `json:"id"`
	TaskID        string         `json:"task_id"`
	TotalSources  int            `json:"total_sources"`
	TestedSources int            `json:"tested_sources"`
	SuccessCount  int            `json:"success_count"`
	FailedCount   int            `json:"failed_count"`
	CurrentSource sql.NullString `json:"current_source"`
	Status        string         `json:"status"` // running/completed/failed
	StartedAt     time.Time      `json:"started_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// EPGProgram EPG节目表
type EPGProgram struct {
	ID          int64          `json:"id"`
	EPGID       string         `json:"epg_id"`
	StartTime   time.Time      `json:"start_time"`
	EndTime     time.Time      `json:"end_time"`
	Title       string         `json:"title"`
	Description sql.NullString `json:"description"`
	Category    sql.NullString `json:"category"`
	Language    sql.NullString `json:"language"`
	Icon        sql.NullString `json:"icon"`
	CreatedAt   time.Time      `json:"created_at"`
}

// TestHistory 测试历史表
type TestHistory struct {
	ID             int64          `json:"id"`
	SourceID       int64          `json:"source_id"`
	TestTime       time.Time      `json:"test_time"`
	Success        bool           `json:"success"`
	ResponseTimeMs sql.NullInt32  `json:"response_time_ms"`
	StatusCode     sql.NullInt32  `json:"status_code"`
	Resolution     sql.NullString `json:"resolution"`
	Bitrate        sql.NullInt32  `json:"bitrate"`
	ErrorMessage   sql.NullString `json:"error_message"`
}

// SystemLog 系统日志表
type SystemLog struct {
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Module    sql.NullString `json:"module"`
	Message   string         `json:"message"`
	Details   sql.NullString `json:"details"`
	CreatedAt time.Time      `json:"created_at"`
}

// Subscription 订阅管理表
type Subscription struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	URL            string       `json:"url"`
	UpdateInterval int          `json:"update_interval"`
	LastUpdate     sql.NullTime `json:"last_update"`
	Enable         bool         `json:"enable"`
	CreatedAt      time.Time    `json:"created_at"`
}

// HotelScanConfig 酒店源扫描配置表
type HotelScanConfig struct {
	ID         int64        `json:"id"`
	IPRange    string       `json:"ip_range"`
	Port       int          `json:"port"`
	Path       string       `json:"path"`
	Enable     bool         `json:"enable"`
	LastScan   sql.NullTime `json:"last_scan"`
	FoundCount int          `json:"found_count"`
}

// MulticastConfig 组播源配置表
type MulticastConfig struct {
	ID        int64        `json:"id"`
	Interface string       `json:"interface"`
	Address   string       `json:"address"`
	Enable    bool         `json:"enable"`
	LastScan  sql.NullTime `json:"last_scan"`
}
