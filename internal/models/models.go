// internal/models/models.go
// 统一数据模型定义，被所有模块引用。

package models

import (
	"database/sql"
	"time"
)

// LiveSource 一个待测直播源。
type LiveSource struct {
	ID         int       `json:"id"`
	URL        string    `json:"url"`
	Name       string    `json:"name"`
	GroupTitle string    `json:"group_title"`
	Status     string    `json:"status"` // pending, testing, success, failed
	SourceID   int       `json:"source_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// PassedSource 测试通过的有效源。
type PassedSource struct {
	ID         int       `json:"id"`
	URL        string    `json:"url"`
	Name       string    `json:"name"`
	GroupName  string    `json:"group_name"`
	Resolution string    `json:"resolution"`
	Bitrate    int64     `json:"bitrate"`
	Speed      float64   `json:"speed"`
	Latency    float64   `json:"latency"`
	CheckedAt  time.Time `json:"checked_at"`
}

// URLSource 从 M3U/TXT 解析出的原始条目。
type URLSource struct {
	URL        string `json:"url"`
	Name       string `json:"name"`
	GroupTitle string `json:"group_title"`
}

// HotelScanConfig 酒店源扫描配置。
type HotelScanConfig struct {
	ID      int    `json:"id"`
	IPRange string `json:"ip_range"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// StreamMeta ffprobe 返回的码流信息。
type StreamMeta struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BitRate   int64  `json:"bit_rate"`
}

// TestProgress 测试进度快照。
type TestProgress struct {
	TaskID        string         `json:"task_id"`
	TotalSources  int            `json:"total_sources"`
	TestedSources int            `json:"tested_sources"`
	SuccessCount  int            `json:"success_count"`
	FailedCount   int            `json:"failed_count"`
	CurrentSource sql.NullString `json:"current_source"`
	Status        string         `json:"status"` // running, completed, failed
	StartedAt     time.Time      `json:"started_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// FilterRule 黑白名单规则。
type FilterRule struct {
	ID         int    `json:"id"`
	Pattern    string `json:"pattern"`
	TargetType string `json:"target_type"` // url / name
	Enable     bool   `json:"enable"`
	Priority   int    `json:"priority"`
	RuleType   string `json:"rule_type"`   // whitelist / blacklist
}
