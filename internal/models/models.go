package models

import (
	"time"
)

// SysConfig 系统配置
type SysConfig struct {
	ID          int
	GroupName   string
	Key         string
	Value       string
	ValueType   string
	Description string
	Version     int
}

// User 用户
type User struct {
	ID           int
	Username     string
	PasswordHash string
	IsAdmin      bool
	IsActive     bool
	LastLogin    *time.Time
}

// LiveSource 订阅源文件/地址
type LiveSource struct {
	ID             int
	Name           string
	Location       string
	LocationType   string // url, local_file
	Enable         bool
	LastDownload   *time.Time
	DownloadStatus string
	HTTPStatus     int
	RetryCount     int
}

// URLSource 初步分解的源条目
type URLSource struct {
	ID            int
	LiveSourceID  int
	URL           string
	Name          string
	TvgID         string
	TvgLogo       string
	GroupTitle    string
	Catchup       string
	CatchupDays   int
	UserAgent     string
	RawAttributes string
	SourceType    string
	CreatedAt     time.Time
}

// PassedSource 通过测试的有效源（结合 url_sources_passed 和 url_sources）
type PassedSource struct {
	ID             int
	SourceID       int // url_sources 的 id
	URL            string
	Name           string
	TvgID          string
	TvgLogo        string
	GroupTitle     string
	Status         string
	ResponseTimeMs int
	Resolution     string
	Bitrate        int
	LastChecked    time.Time
	Location       string
	ISP            string
	CategoryIDs    []int
}

// DisplayRule 显示规则
type DisplayRule struct {
	ID                  int
	CategoryID          int
	CategoryName        string
	GroupNameOverride   string
	SortOrder           int
	ItemSortOrder       string // "0"降序,"1"升序
	HideEmptyGroups     bool
	MaxItemsPerCategory int
	Enable              bool
}

// WhitelistRule / BlacklistRule
type WhitelistRule struct {
	ID         int
	Pattern    string
	TargetType string
	Priority   int
	Enable     bool
}
type BlacklistRule struct {
	ID         int
	Pattern    string
	TargetType string
	Enable     bool
}

// RTMPStream 推流记录
type RTMPStream struct {
	ID       int
	SourceID int
	Status   string
	PushURL  string
	HLSURL   string
	LastPush *time.Time
	IdleSec  int
}

// TestHistory 测试历史
type TestHistory struct {
	ID             int
	SourceID       int
	TestTime       time.Time
	Success        bool
	ResponseTimeMs int
	StatusCode     int
	Resolution     string
	Bitrate        int
	ErrorMessage   string
}

// EPGProgram 节目
type EPGProgram struct {
	ID          int
	EpgID       string
	StartTime   time.Time
	EndTime     time.Time
	Title       string
	Description string
	Category    string
}

// HotelScanConfig / MulticastConfig 等根据需要定义
