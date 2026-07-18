// Package types defines the shared domain types used across the live-source-manager-go project.
package types

// Channel represents a single parsed live-stream entry (#EXTINF) from an M3U/TXT source file.
type Channel struct {
	ID            string            `json:"id"` // md5(name|url)[:12]
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	URLOriginal   string            `json:"url_original"`
	Logo          string            `json:"logo"`
	Group         string            `json:"group"`
	FileID        string            `json:"file_id"`
	FileName      string            `json:"file_name"`
	UserAgent     string            `json:"user_agent"`
	UAPosition    string            `json:"ua_position"`
	Referrer      string            `json:"referrer"`
	ReferrerPosition string         `json:"referrer_position"`
	Categories    map[string]string `json:"categories"`
	Status        string            `json:"status"`         // success | failed | timeout | connection_failed | ...
	ResponseTime  float64           `json:"response_time"`  // seconds
	DownloadSpeed float64           `json:"download_speed"` // KB/s
	Resolution    string            `json:"resolution"`
	Bitrate       int               `json:"bitrate"`
	FPS           float64           `json:"fps"`
	MediaType     string            `json:"media_type"`
	IsQualified   bool              `json:"is_qualified"`
}

// SourceFile represents a source file (local dir file, downloaded online file, or github file).
type SourceFile struct {
	ID           string `json:"id"` // md5(path)
	Name         string `json:"name"`
	Path         string `json:"path"`
	Type         string `json:"type"` // local | online | github
	ChannelCount int    `json:"channel_count"`
	Size         int64  `json:"size"`
	UpdatedAt    string `json:"updated_at"`
}

// TestResult is the outcome of testing a single channel URL.
// Fields Name/Source/SourceType/Category/RetryInfo mirror the Python
// livetest.html dimensions so the Go test page shows the same columns.
type TestResult struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Name          string  `json:"name,omitempty"`        // 频道名（来自源文件）
	Source        string  `json:"source,omitempty"`      // 所在源文件（文件名）
	SourceType    string  `json:"source_type,omitempty"` // local | online | github
	Status        string  `json:"status"`                // success | failed | timeout | connection_failed | dns_error | blacklisted | ...
	Category      string  `json:"category,omitempty"`    // 归一化错误类别（失败原因分布用）
	ResponseTime  float64 `json:"response_time"`
	DownloadSpeed float64 `json:"download_speed"`
	Resolution    string  `json:"resolution"`
	Bitrate       int     `json:"bitrate"`
	FPS           float64 `json:"fps"`
	Error         string  `json:"error,omitempty"`
	Message       string  `json:"message,omitempty"`
	RetryInfo     string  `json:"retry_info,omitempty"`
}

// TestProgress describes the live status of a running/paused test pass.
// TotalBeforeDedup/DedupRemoved expose the de-duplication that happens
// before a test run; ErrorBreakdown aggregates failure reasons by category
// (matches Python's 失败原因分布 panel).
type TestProgress struct {
	Total            int            `json:"total"`              // 去重后的测试数
	TotalBeforeDedup int            `json:"total_before_dedup"` // 去重前
	DedupRemoved     int            `json:"dedup_removed"`      // 因网址重复被剔除的数量
	Completed        int            `json:"completed"`
	Success          int            `json:"success"`
	Failed           int            `json:"failed"`
	Running          int            `json:"running"`
	Status           string         `json:"status"` // idle | running | paused | done | canceling
	Percent          int            `json:"percent"`
	CurrentURL       string         `json:"current_url,omitempty"`
	StartedAt        string         `json:"started_at,omitempty"`
	ErrorBreakdown   map[string]int `json:"error_breakdown,omitempty"`
}

// ClassificationRule is a keyword-based auto-classification rule.
type ClassificationRule struct {
	ID        int      `json:"id"`
	RuleType  string   `json:"rule_type"` // category | channel_type
	Name      string   `json:"name"`
	Keywords  []string `json:"keywords"`
	Priority  int      `json:"priority"`
	SortOrder int      `json:"sort_order"`
	IsActive  bool     `json:"is_active"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// ClassificationDimension is a classification axis (content/region/language/quality/media_type/genre).
type ClassificationDimension struct {
	DimKey    string `json:"dim_key"`
	DimName   string `json:"dim_name"`
	SortOrder int    `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
}

// ProvinceExclusion maps a province keyword to an excluded keyword (avoid mis-classifying).
type ProvinceExclusion struct {
	ID              int    `json:"id"`
	ProvinceKeyword string `json:"province_keyword"`
	ExcludedKeyword string `json:"excluded_keyword"`
	Note            string `json:"note"`
	CreatedAt       string `json:"created_at"`
}

// ChannelMapping is a manual per-channel-name multi-dimension override.
type ChannelMapping struct {
	ChannelName string            `json:"channel_name"`
	Content     string            `json:"content"`
	Region      string            `json:"region"`
	Language    string            `json:"language"`
	Quality     string            `json:"quality"`
	MediaType   string            `json:"media_type"`
	Genre       string            `json:"genre"`
	IsManual    bool              `json:"is_manual"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
	Categories  map[string]string `json:"categories,omitempty"`
}

// CategoryDictValue is one allowed value within a dimension's controlled vocabulary.
type CategoryDictValue struct {
	ID        int    `json:"id"`
	Dimension string `json:"dimension"`
	Value     string `json:"value"`
	Label     string `json:"label"`
	SortOrder int    `json:"sort_order"`
}

// User is a web-management account.
type User struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | viewer
	DisplayName  string `json:"display_name"`
	IsActive     bool   `json:"is_active"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// AuditLogEntry is one row of the audit trail.
type AuditLogEntry struct {
	ID        int    `json:"id"`
	UserID    int    `json:"user_id"`
	Username  string `json:"username"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	IPAddress string `json:"ip_address"`
	CreatedAt string `json:"created_at"`
}

// GitHubCacheEntry records a downloaded github file.
type GitHubCacheEntry struct {
	RepoKey      string `json:"repo_key"`
	Filename     string `json:"filename"`
	FileSize     int64  `json:"file_size"`
	DownloadedAt string `json:"downloaded_at"`
}
