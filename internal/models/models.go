// internal/models/models.go
// 数据模型定义。补充 FilterRule 和 User 模型，这些在 filter.go、handlers.go 中被引用。

package models

import (
	"database/sql"
	"time"
)

// LiveSource 直播源（订阅源）
type LiveSource struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	Location       string         `json:"location"`
	LocationType   string         `json:"location_type"`
	Enable         int            `json:"enable"`
	LastDownload   sql.NullString `json:"last_download"`
	DownloadStatus sql.NullString `json:"download_status"`
	HTTPStatus     sql.NullInt64  `json:"http_status"`
	RetryCount     int            `json:"retry_count"`
}

// PassedSource 通过测试的有效源
type PassedSource struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	GroupName  string `json:"group_name"`
	Logo       string `json:"logo"`
	CategoryID int    `json:"category_id"`
	EPGID      string `json:"epg_id"`
	Status     string `json:"status"`
}

// Source 通用源结构（用于测试器）
type Source struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// StreamMeta 流元数据（ffprobe 解析结果）
type StreamMeta struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BitRate   int    `json:"bit_rate"`
	Duration  float64 `json:"duration"`
}

// FilterRule 过滤器规则
type FilterRule struct {
	ID          int    `json:"id"`
	RuleType    string `json:"rule_type"`    // "whitelist" 或 "blacklist"
	Pattern     string `json:"pattern"`      // 正则表达式
	TargetType  string `json:"target_type"`  // "url" 或 "name"
	Enable      bool   `json:"enable"`
	Priority    int    `json:"priority"`
	Description string `json:"description"`
}

// User 系统用户
type User struct {
	ID           int            `json:"id"`
	Username     string         `json:"username"`
	PasswordHash string         `json:"-"`
	IsAdmin      int            `json:"is_admin"`
	IsActive     bool           `json:"is_active"`
	LastLogin    sql.NullString `json:"last_login"`
}

// Category 频道分类
type Category struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Keywords string `json:"keywords"`
}

// HotelScanConfig 酒店源扫描配置
type HotelScanConfig struct {
	ID      int    `json:"id"`
	IPRange string `json:"ip_range"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
	Enable  bool   `json:"enable"`
}

// URLSource URL 源条目
type URLSource struct {
	LiveSourceID int    `json:"live_source_id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	GroupTitle   string `json:"group_title"`
	Logo         string `json:"logo"`
	TvgID        string `json:"tvg_id"`
	TvgLogo      string `json:"tvg_logo"`
}

// DisplayRule 显示规则
type DisplayRule struct {
	ID                  int    `json:"id"`
	CategoryID          int    `json:"category_id"`
	CategoryName        string `json:"category_name"`
	GroupNameOverride   string `json:"group_name_override"`
	SortOrder           int    `json:"sort_order"`
	ItemSortOrder       string `json:"item_sort_order"`
	HideEmptyGroups     bool   `json:"hide_empty_groups"`
	MaxItemsPerCategory int    `json:"max_items_per_category"`
	Enable              bool   `json:"enable"`
}

// Stats 系统统计信息
type Stats struct {
	TotalSources     int       `json:"total_sources"`
	ActiveSources    int       `json:"active_sources"`
	LastTestTime     time.Time `json:"last_test_time"`
	TotalEPGPrograms int       `json:"total_epg_programs"`
}
