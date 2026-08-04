package types

// EPGSource 是一个外部 XMLTV 节目单来源。
type EPGSource struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	Enabled            bool   `json:"enabled"`
	Priority           int    `json:"priority"`
	RefreshMode        string `json:"refresh_mode"` // daily | interval | manual
	RefreshAt          string `json:"refresh_at"`   // HH:MM（daily 模式）
	RefreshMinutes     int    `json:"refresh_minutes"`
	Remark             string `json:"remark"`
	LastFetchAt        string `json:"last_fetch_at"`
	LastStatus         string `json:"last_status"` // success | failed | ""
	LastError          string `json:"last_error"`
	LastChannelCount   int    `json:"last_channel_count"`
	LastProgrammeCount int    `json:"last_programme_count"`
	LastDurationMs     int    `json:"last_duration_ms"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// EPGChannel 是某个来源里声明的一个频道（XMLTV <channel>）。
type EPGChannel struct {
	ID             int      `json:"id"`
	SourceID       int      `json:"source_id"`
	SourceName     string   `json:"source_name,omitempty"`
	TVGID          string   `json:"tvg_id"`
	DisplayName    string   `json:"display_name"`
	Icon           string   `json:"icon"`
	MatchedChannel string   `json:"matched_channel"`
	Aliases        []string `json:"aliases,omitempty"`
	UpdatedAt      string   `json:"updated_at"`
}

// EPGProgramme 是一条节目（XMLTV <programme>），时间统一以 UTC 字符串保存。
type EPGProgramme struct {
	ID          int    `json:"id"`
	SourceID    int    `json:"source_id"`
	TVGID       string `json:"tvg_id"`
	StartUTC    string `json:"start_utc"` // "2006-01-02 15:04:05"
	StopUTC     string `json:"stop_utc"`
	Title       string `json:"title"`
	SubTitle    string `json:"sub_title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Episode     string `json:"episode"`
	Icon        string `json:"icon"`
}

// EPGStats 是 EPG 概览统计。
type EPGStats struct {
	SourceCount     int    `json:"source_count"`
	EnabledSources  int    `json:"enabled_sources"`
	ChannelCount    int    `json:"channel_count"`
	ProgrammeCount  int    `json:"programme_count"`
	MatchedChannels int    `json:"matched_channels"`
	EarliestStart   string `json:"earliest_start"`
	LatestStop      string `json:"latest_stop"`
	LastRefreshAt   string `json:"last_refresh_at"`
}

// EPGRefreshResult 是单个来源刷新的结果。
type EPGRefreshResult struct {
	SourceID       int    `json:"source_id"`
	SourceName     string `json:"source_name"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	ChannelCount   int    `json:"channel_count"`
	ProgrammeCount int    `json:"programme_count"`
	DurationMs     int    `json:"duration_ms"`
}
