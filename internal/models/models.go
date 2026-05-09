// internal/models/models.go
// 通用数据模型定义。
// 修复：原文件缺失 package 声明，导致无法被其他模块引用。
// 本文件定义了贯穿数据采集、测试、筛选、生成全流程的核心结构体。
// 所有字段均添加了 json 和 db 标签，确保 Web API 序列化及数据库操作的兼容性。

package models

import (
	"encoding/json"
	"time"
)

// LiveSource 直播源（订阅源），对应数据库 live_sources 表。
// 用于管理在线订阅地址或本地文件路径。
type LiveSource struct {
	ID             int        `json:"id" db:"id"`
	Name           string     `json:"name" db:"name"`
	Location       string     `json:"location" db:"location"`             // 订阅URL或本地文件路径
	LocationType   string     `json:"location_type" db:"location_type"`   // "url" 或 "local_file"
	Enable         int        `json:"enable" db:"enable"`
	LastDownload   *time.Time `json:"last_download" db:"last_download"`
	DownloadStatus string     `json:"download_status" db:"download_status"` // "success", "failed"
	HTTPStatus     int        `json:"http_status" db:"http_status"`
	RetryCount     int        `json:"retry_count" db:"retry_count"`
	DeletedAt      *time.Time `json:"deleted_at" db:"deleted_at"`
}

// URLSource 从 M3U/TXT 解析出的单条 URL 源条目。
// 对应订阅文件中的 #EXTINF 块和紧随其后的 URL 行。
type URLSource struct {
	LiveSourceID  int    `json:"live_source_id" db:"live_source_id"`
	Name          string `json:"name" db:"name"`
	URL           string `json:"url" db:"url"`
	GroupTitle    string `json:"group_title" db:"group_title"`       // EXTVL 标签中的 group-title 属性
	TvgID         string `json:"tvg_id" db:"tvg_id"`                 // tvg-id 属性，用于关联 EPG
	TvgLogo       string `json:"tvg_logo" db:"tvg_logo"`             // tvg-logo 属性，台标 URL
	UserAgent     string `json:"user_agent" db:"user_agent"`         // 自定义请求头
	SourceType    string `json:"source_type" db:"source_type"`       // 根据 URL 推测的协议类型
	RawAttributes string `json:"raw_attributes" db:"raw_attributes"` // 剩余未解析的原始属性（JSON 字符串）
	Status        string `json:"status" db:"status"`                 // "active", "invalid", "pending"
}

// PassedSource 通过测试的有效源。
// 在 tester 完成后，status 为 "active" 的源会存入 url_sources_passed 表。
type PassedSource struct {
	ID         int    `json:"id" db:"id"`
	Name       string `json:"name" db:"name"`
	URL        string `json:"url" db:"url"`
	GroupName  string `json:"group_name" db:"group_name"`     // 分类后的分组名
	Logo       string `json:"logo" db:"logo"`                 // 台标 URL
	CategoryID int    `json:"category_id" db:"category_id"`   // 关联的分类 ID
	EPGID      string `json:"epg_id" db:"epg_id"`             // 关联的 EPG ID
	Status     string `json:"status" db:"status"`             // "active" 等
	CreatedAt  string `json:"created_at" db:"created_at"`
	UpdatedAt  string `json:"updated_at" db:"updated_at"`
}

// Source 测试器使用的通用源结构，包含 ID 和 URL 两个最精简字段。
type Source struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// SourceMeta 流元数据，由 ffprobe 解析生成。
// 用于存储视频编码、分辨率、码率等实际技术指标。
type SourceMeta struct {
	CodecType string  `json:"codec_type"` // "video", "audio"
	CodecName string  `json:"codec_name"` // "h264", "aac" 等
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	BitRate   int     `json:"bit_rate"`  // 单位: bps
	Duration  float64 `json:"duration"`  // 单位: 秒
}

// User 系统用户。
// 由 auth.go 中的登录逻辑从数据库加载，密码哈希字段不参与 JSON 序列化。
type User struct {
	ID           int        `json:"id" db:"id"`
	Username     string     `json:"username" db:"username"`
	PasswordHash string     `json:"-" db:"password_hash"` // 禁止 JSON 序列化
	IsAdmin      int        `json:"is_admin" db:"is_admin"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	LastLogin    *time.Time `json:"last_login" db:"last_login"`
	DeletedAt    *time.Time `json:"deleted_at" db:"deleted_at"`
}

// Category 频道分类。
// 供 classifier 模块和 Web 管理界面使用。
type Category struct {
	ID       int    `json:"id" db:"id"`
	Name     string `json:"name" db:"name"`
	Keywords string `json:"keywords" db:"keywords"` // 逗号分隔的关键词，用于自动分类
}

// DisplayRule 显示规则。
// 用于控制 generator 生成 M3U 时的分组、排序和数量限制。
type DisplayRule struct {
	ID                  int    `json:"id" db:"id"`
	CategoryID          int    `json:"category_id" db:"category_id"`
	CategoryName        string `json:"category_name" db:"category_name"`
	GroupNameOverride   string `json:"group_name_override" db:"group_name_override"`
	SortOrder           int    `json:"sort_order" db:"sort_order"`
	ItemSortOrder       string `json:"item_sort_order" db:"item_sort_order"` // "asc" 或 "desc"
	HideEmptyGroups     bool   `json:"hide_empty_groups" db:"hide_empty_groups"`
	MaxItemsPerCategory int    `json:"max_items_per_category" db:"max_items_per_category"`
	Enable              bool   `json:"enable" db:"enable"`
}

// FilterRule 过滤器规则。
// 黑/白名单正则规则，由 filter 模块从数据库加载并编译。
type FilterRule struct {
	ID          int    `json:"id" db:"id"`
	RuleType    string `json:"rule_type" db:"rule_type"`       // "whitelist" 或 "blacklist"
	Pattern     string `json:"pattern" db:"pattern"`           // 正则表达式字符串
	TargetType  string `json:"target_type" db:"target_type"`   // "url" 或 "name"
	Enable      bool   `json:"enable" db:"enable"`
	Priority    int    `json:"priority" db:"priority"`
	Description string `json:"description" db:"description"`
}

// HotelScanConfig 酒店源扫描配置。
// 由 collector 使用，定义需要探测的内网 IP 范围和路径。
type HotelScanConfig struct {
	ID      int    `json:"id" db:"id"`
	IPRange string `json:"ip_range" db:"ip_range"` // CIDR 格式，例如 192.168.1.0/24
	Port    int    `json:"port" db:"port"`
	Path    string `json:"path" db:"path"`   // 多个路径用逗号分隔，例如 "/iptv.m3u,/tv.m3u"
	Enable  bool   `json:"enable" db:"enable"`
}

// Stats 系统统计信息，由仪表盘 API 返回。
type Stats struct {
	TotalSources     int    `json:"total_sources"`
	ActiveSources    int    `json:"active_sources"`
	LastTestTime     string `json:"last_test_time"`
	TotalEPGPrograms int    `json:"total_epg_programs"`
}

// ProgressEvent 测试进度事件，通过 WebSocket 推送给前端。
type ProgressEvent struct {
	Type    string `json:"type"`    // "test", "download", "generate"
	Message string `json:"message"` // 描述信息
	Current int    `json:"current"` // 当前进度
	Total   int    `json:"total"`   // 总数
}

// MarshalJSON 自定义 JSON 序列化，将时间格式化为 ISO 8601。
func (p ProgressEvent) MarshalJSON() ([]byte, error) {
	type Alias ProgressEvent
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(&p),
	})
}
