// Package manager orchestrates the full live-source pipeline:
// collect sources -> classify channels -> (optionally) test streams -> generate M3U output.
// It also exposes a managed real-time test session with pause / resume / cancel for the web UI.
package manager

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"live-source-manager-go/internal/auth"
	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/m3u"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/internal/source"
	"live-source-manager-go/internal/streamtest"
	"live-source-manager-go/internal/types"
	"live-source-manager-go/internal/util"
)

// Manager wires the engine packages together.
type Manager struct {
	conn     *sql.DB
	cfg      *config.Config
	src      *source.Manager
	test     *streamtest.Tester
	rules    *rules.Engine
	mu       sync.Mutex
	sessions map[string]*RealtimeSession

	// channelCache bounds repeated UI channel collection (dashboard / sources).
	cacheMu     sync.RWMutex
	cacheCh     []types.Channel
	cacheReport *source.CollectReport
	cacheAt     time.Time
	cacheSig    string

	// schedCancel stops the auto-scan scheduler loop.
	schedCancel context.CancelFunc

	// collecting guards a single in-flight channel collection so the slow
	// network fetch never holds cacheMu (which would block PeekChannels readers,
	// e.g. the source-file list, for the whole collection duration).
	collecting  atomic.Bool
	collectWait chan struct{}
	collectWMu  sync.Mutex
}

// UI channel-collection bounds: cache lifetime, overall deadline, per-request timeout.
const (
	channelCacheTTL       = 60 * time.Second
	channelCollectTimeout = 20 * time.Second
	channelCollectPerReq  = 12 // seconds; per-request HTTP timeout for UI collection
)

// New constructs a Manager bound to the database and config.
func New(conn *sql.DB, cfg *config.Config) *Manager {
	return &Manager{
		conn:     conn,
		cfg:      cfg,
		src:      source.NewManager(conn, cfg),
		test:     streamtest.NewTester(conn, cfg),
		rules:    rules.NewEngine(conn),
		sessions: map[string]*RealtimeSession{},
	}
}

// RunOptions controls a single pipeline run.
type RunOptions struct {
	TestEnabled     bool
	GenerateEnabled bool
	CollectOpts     source.CollectOptions
}

// Report summarizes a pipeline run.
type Report struct {
	Collected  int    `json:"collected"`
	Classified int    `json:"classified"`
	Tested     int    `json:"tested"`
	Success    int    `json:"success"`
	Failed     int    `json:"failed"`
	OutputPath string `json:"output_path"`
	DurationMs int64  `json:"duration_ms"`
}

// Run executes collect -> classify -> (test) -> (generate).
func (m *Manager) Run(ctx context.Context, opts RunOptions) (*Report, error) {
	start := time.Now()
	rep := &Report{}

	coll, err := m.src.CollectAll(opts.CollectOpts)
	if err != nil {
		return nil, err
	}
	channels := coll.Channels
	rep.Collected = len(channels)
	logger.L().Info("采集完成：%d 个频道，%d 个源文件", rep.Collected, len(coll.SourceFiles))

	if err := m.rules.LoadRules(); err != nil {
		logger.L().Warning("加载分类规则失败：%v", err)
	}
	m.rules.Classify(channels)
	rep.Classified = len(channels)

	if opts.TestEnabled {
		params := m.buildTestParams()
		results := m.test.TestBatch(ctx, channels, params, func(p types.TestProgress) {
			logger.L().Debug("实时测试进度：%d/%d", p.Completed, p.Total)
		})
		applyTestResults(channels, results)
		for _, r := range results {
			if r.Status == "success" {
				rep.Success++
			} else {
				rep.Failed++
			}
		}
		rep.Tested = len(results)
		logger.L().Info("测试完成：成功 %d，失败 %d", rep.Success, rep.Failed)
	}

	if opts.GenerateEnabled {
		optsM3U := m.buildM3UOpts()
		if err := util.EnsureDir(optsM3U.OutputDir); err != nil {
			logger.L().Warning("创建输出目录失败：%v", err)
		}
		path, err := m3u.WriteFile(channels, optsM3U)
		if err != nil {
			return rep, err
		}
		rep.OutputPath = path
		logger.L().Info("已生成 M3U：%s", path)
	}

	rep.DurationMs = time.Since(start).Milliseconds()
	return rep, nil
}

// ── scheduler (auto-scan) ────────────────────────────────────────────────

// StartScheduler launches the background auto-scan loop when
// Testing.auto_scan_enabled is true. It mirrors the Python "定时执行模式":
// each tick runs the full pipeline (collect -> classify -> test -> generate).
//   - mode "interval": every auto_scan_interval_hours
//   - mode "daily":    at auto_scan_daily_time (HH:MM) every day
func (m *Manager) StartScheduler(ctx context.Context) {
	if !m.cfg.GetBool("Testing", "auto_scan_enabled", false) {
		logger.L().Info("定时采集未启用 (Testing.auto_scan_enabled=false)")
		return
	}
	sctx, cancel := context.WithCancel(ctx)
	m.schedCancel = cancel
	go m.runScheduler(sctx)
}

// StopScheduler cancels a running scheduler loop (for graceful shutdown).
func (m *Manager) StopScheduler() {
	if m.schedCancel != nil {
		m.schedCancel()
	}
}

func (m *Manager) runScheduler(ctx context.Context) {
	mode := m.cfg.Get("Testing", "auto_scan_mode", "interval")
	intervalHours := m.cfg.GetInt("Testing", "auto_scan_interval_hours", 24)
	dailyTime := m.cfg.Get("Testing", "auto_scan_daily_time", "03:00")
	wait := scheduleWait(mode, intervalHours, dailyTime)
	logger.L().Info("定时采集已启动: 模式=%s 间隔=%d小时 每日时刻=%s 下次执行 %v", mode, intervalHours, dailyTime, wait)
	var iteration int64
	for {
		select {
		case <-ctx.Done():
			logger.L().Info("定时采集已停止")
			return
		case <-time.After(wait):
		}
		iteration++
		logger.L().Info("📅 第 %d 轮定时采集开始", iteration)
		runCtx, rcancel := context.WithTimeout(ctx, 30*time.Minute)
		rep, err := m.Run(runCtx, RunOptions{
			TestEnabled:     m.cfg.GetBool("Testing", "enable_speed_test", true),
			GenerateEnabled: true,
			CollectOpts:     m.buildCollectOpts(),
		})
		rcancel()
		if err != nil {
			logger.L().Error("定时采集第 %d 轮失败: %v", iteration, err)
		} else {
			logger.L().Info("✅ 定时采集第 %d 轮完成: 采集 %d, 测试 %d(成功 %d), 输出 %s",
				iteration, rep.Collected, rep.Tested, rep.Success, rep.OutputPath)
		}
		wait = scheduleWait(mode, intervalHours, dailyTime)
	}
}

// scheduleWait computes the duration until the next scheduled tick.
func scheduleWait(mode string, intervalHours int, dailyTime string) time.Duration {
	if strings.EqualFold(mode, "daily") {
		if d, ok := parseDailyWait(dailyTime); ok {
			return d
		}
	}
	if intervalHours < 1 {
		intervalHours = 24
	}
	return time.Duration(intervalHours) * time.Hour
}

// parseDailyWait returns the duration from now until the next dailyTime (HH:MM).
func parseDailyWait(hhmm string) (time.Duration, bool) {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	min, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || min < 0 || min > 59 {
		return 0, false
	}
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), h, min, 0, 0, now.Location())
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return target.Sub(now), true
}

// DefaultTestParams returns config-derived stream-test parameters (for the web test trigger).
func (m *Manager) DefaultTestParams() streamtest.Params {
	return m.buildTestParams()
}

// collectionSignature is a cheap hash of the source config so the cache can
// auto-invalidate when local_dirs / online_urls / github_sources change.
func (m *Manager) collectionSignature() string {
	s := m.cfg.GetSources()
	return fmt.Sprintf("%v|%v|%v", s["local_dirs"], s["online_urls"], s["github_sources"])
}

// InvalidateChannels forces the next GetChannels call to re-collect.
func (m *Manager) InvalidateChannels() {
	m.cacheMu.Lock()
	m.cacheAt = time.Time{}
	m.cacheSig = ""
	m.cacheCh = nil
	m.cacheReport = nil
	m.cacheMu.Unlock()
}

// GetChannels returns classified channels for the sources browser.
// Results are cached for channelCacheTTL seconds (or until the source config
// changes), so repeated page loads do NOT re-fetch every remote source.
// A fresh collection is bounded by channelCollectTimeout and runs concurrently
// inside the source package, so the UI never hangs on a slow/hung URL.
func (m *Manager) GetChannels(ctx context.Context) ([]types.Channel, *source.CollectReport, error) {
	sig := m.collectionSignature()
	// fast path: return valid cache without blocking collectors.
	m.cacheMu.RLock()
	if !m.cacheAt.IsZero() && time.Since(m.cacheAt) < channelCacheTTL && m.cacheSig == sig && m.cacheCh != nil {
		ch, rep := m.cacheCh, m.cacheReport
		m.cacheMu.RUnlock()
		return ch, rep, nil
	}
	m.cacheMu.RUnlock()

	// Another goroutine already collecting? Wait for it, then serve the cache
	// (even if slightly stale) so callers never block on the slow network collect.
	if m.collecting.Load() {
		m.waitCollection()
		m.cacheMu.RLock()
		ch, rep := m.cacheCh, m.cacheReport
		m.cacheMu.RUnlock()
		if ch != nil {
			return ch, rep, nil
		}
		return m.GetChannels(ctx) // extremely rare: collection finished empty; retry once
	}

	// Claim the collector role, then RELEASE the lock before the slow work so
	// PeekChannels (RLock) readers are never blocked during collection.
	if !m.collecting.CompareAndSwap(false, true) {
		m.waitCollection()
		m.cacheMu.RLock()
		ch, rep := m.cacheCh, m.cacheReport
		m.cacheMu.RUnlock()
		if ch != nil {
			return ch, rep, nil
		}
		return m.GetChannels(ctx)
	}
	m.collectWMu.Lock()
	m.collectWait = make(chan struct{})
	m.collectWMu.Unlock()
	defer func() {
		m.collectWMu.Lock()
		close(m.collectWait)
		m.collectWMu.Unlock()
		m.collecting.Store(false)
	}()

	opts := m.buildCollectOpts()
	if opts.TimeoutSec > channelCollectPerReq || opts.TimeoutSec <= 0 {
		opts.TimeoutSec = channelCollectPerReq
	}
	collCtx, cancel := context.WithTimeout(ctx, channelCollectTimeout)
	defer cancel()
	coll, err := m.src.CollectAllContext(collCtx, opts)
	if err != nil {
		return nil, nil, err
	}
	if err := m.rules.LoadRules(); err != nil {
		logger.L().Warning("加载分类规则失败：%v", err)
	}
	m.rules.Classify(coll.Channels)

	// store under a brief write lock
	m.cacheMu.Lock()
	m.cacheCh = coll.Channels
	m.cacheReport = coll
	m.cacheAt = time.Now()
	m.cacheSig = sig
	m.cacheMu.Unlock()
	return coll.Channels, coll, nil
}

// waitCollection blocks until the in-flight collection signals completion.
func (m *Manager) waitCollection() {
	m.collectWMu.Lock()
	ch := m.collectWait
	m.collectWMu.Unlock()
	if ch != nil {
		<-ch
	}
}

// HasTestBinaries reports whether ffprobe/ffmpeg were located for real probing.
func (m *Manager) HasTestBinaries() bool {
	return m.test != nil && m.test.HasBinaries()
}

// ProbeBinaries returns the located ffprobe/ffmpeg paths (empty if absent).
func (m *Manager) ProbeBinaries() (ffprobe, ffmpeg string) {
	if m.test == nil {
		return "", ""
	}
	return m.test.FFprobePath(), m.test.FFmpegPath()
}

// GetChannelsRefresh invalidates the cache and returns freshly collected channels.
func (m *Manager) GetChannelsRefresh(ctx context.Context) ([]types.Channel, *source.CollectReport, error) {
	m.InvalidateChannels()
	return m.GetChannels(ctx)
}

// PeekChannels returns the current cached channels WITHOUT triggering a network
// collection. The boolean reports whether a valid (non-expired, signature-matched)
// cache exists. Dashboards use this so the first paint is instant even on a cold
// cache; collection is warmed in the background via WarmChannels.
func (m *Manager) PeekChannels() ([]types.Channel, *source.CollectReport, bool) {
	sig := m.collectionSignature()
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	if !m.cacheAt.IsZero() && time.Since(m.cacheAt) < channelCacheTTL && m.cacheSig == sig && m.cacheCh != nil {
		return m.cacheCh, m.cacheReport, true
	}
	return nil, nil, false
}

// WarmChannels triggers a background collection to populate the channel cache
// without blocking the caller. It reuses GetChannels (double-checked locking
// prevents duplicate concurrent collections), so it is safe to call from a
// request handler to prefetch data the dashboard will poll for.
func (m *Manager) WarmChannels() {
	go func() {
		_, _, _ = m.GetChannels(context.Background())
	}()
}

// ── real-time test session ────────────────────────────────────────────────

// RealtimeSession is a managed, interruptible test run.
type RealtimeSession struct {
	mu       sync.Mutex
	ID       string
	channels []types.Channel
	params   streamtest.Params
	report   *source.CollectReport // 采集报告（用于补全结果的所在源/源类型）
	Progress types.TestProgress
	Results  map[string]types.TestResult
	cancel   context.CancelFunc
	paused   atomic.Bool
}

// RealtimeTestMeta carries pre-run metadata (de-dup stats + source report)
// so the UI can show the true tested count and per-result source context.
type RealtimeTestMeta struct {
	Report           *source.CollectReport
	DedupRemoved     int
	TotalBeforeDedup int
}

// StartRealtimeTest launches a background test over the given channels using the given params.
func (m *Manager) StartRealtimeTest(channels []types.Channel, params streamtest.Params, meta RealtimeTestMeta) (string, error) {
	id := auth.GenerateSessionID()
	ctx, cancel := context.WithCancel(context.Background())
	s := &RealtimeSession{
		ID:       id,
		channels: channels,
		params:   params,
		report:   meta.Report,
		Results:  map[string]types.TestResult{},
		cancel:   cancel,
		Progress: types.TestProgress{
			Total:            len(channels),
			TotalBeforeDedup: meta.TotalBeforeDedup,
			DedupRemoved:     meta.DedupRemoved,
			Status:           "running",
			ErrorBreakdown:   map[string]int{},
		},
	}
	// 实时测试直接调用 Tester.Test（不经过 TestBatch），需手动按配置设置 ffprobe
	// 并发上限，否则会停留在 NewTester 的默认值（4），导致上万频道探测极慢。
	if params.MaxFFprobe > 0 {
		m.test.SetFFProbeConcurrency(params.MaxFFprobe)
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go m.runRealtime(ctx, s)
	return id, nil
}

// StartRealtimeTestWithConfig launches a realtime test using parameters from the config.
func (m *Manager) StartRealtimeTestWithConfig(channels []types.Channel) (string, error) {
	return m.StartRealtimeTest(channels, m.buildTestParams(), RealtimeTestMeta{TotalBeforeDedup: len(channels)})
}

// normalizeTestCategory maps an internal TestResult status to a stable
// failure category vocabulary (aligned with the Python 失败原因分布 panel).
func normalizeTestCategory(r types.TestResult) string {
	switch r.Status {
	case "success":
		return ""
	case "timeout":
		return "timeout"
	case "connection_failed":
		return "connection_failed"
	case "dns_error":
		return "dns_failed"
	case "blacklisted":
		return "global_blacklist"
	case "frozen":
		return "frozen"
	case "untested":
		return "no_probe_tool_available"
	case "interrupted":
		return "aborted"
	default:
		return "unknown"
	}
}

// StopRealtimeTest cancels a running session.
func (m *Manager) StopRealtimeTest(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	s.cancel()
	s.mu.Lock()
	s.Progress.Status = "canceling"
	s.mu.Unlock()
}

// PauseRealtimeTest pauses a running session.
func (m *Manager) PauseRealtimeTest(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	s.paused.Store(true)
	s.mu.Lock()
	s.Progress.Status = "paused"
	s.mu.Unlock()
}

// ResumeRealtimeTest resumes a paused session.
func (m *Manager) ResumeRealtimeTest(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	s.paused.Store(false)
	s.mu.Lock()
	s.Progress.Status = "running"
	s.mu.Unlock()
}

// GetRealtimeProgress returns a snapshot of progress + results for a session.
func (m *Manager) GetRealtimeProgress(id string) (*types.TestProgress, map[string]types.TestResult, bool) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make(map[string]types.TestResult, len(s.Results))
	for k, v := range s.Results {
		results[k] = v
	}
	p := s.Progress
	return &p, results, true
}

func (m *Manager) runRealtime(ctx context.Context, s *RealtimeSession) {
	concurrency := s.params.Concurrent
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var completed, success, failed int64
	total := len(s.channels)

	// fileID -> source type, used to complete each result's "所在源/源类型".
	srcTypeOf := map[string]string{}
	if s.report != nil {
		for _, sf := range s.report.SourceFiles {
			srcTypeOf[sf.ID] = sf.Type
		}
	}

	for _, ch := range s.channels {
		if ctx.Err() != nil {
			break
		}
		for s.paused.Load() {
			if ctx.Err() != nil {
				goto finish
			}
			time.Sleep(100 * time.Millisecond)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ch types.Channel) {
			defer wg.Done()
			defer func() { <-sem }()
			// 每个频道加硬上限，避免个别 URL 让 ffprobe/ffmpeg 长时间挂起导致进度卡死。
			chCtx, chCancel := context.WithTimeout(ctx, time.Duration(s.params.Timeout+10)*time.Second)
			r := m.test.Test(chCtx, ch, s.params)
			chCancel()
			// ── 补全 Python 版同维度字段 ──
			r.Name = ch.Name
			r.Source = ch.FileName
			r.SourceType = srcTypeOf[ch.FileID]
			r.Category = normalizeTestCategory(r)
			s.mu.Lock()
			s.Results[r.ID] = r
			completed++
			if r.Status == "success" {
				success++
			} else {
				failed++
				if r.Category != "" {
					s.Progress.ErrorBreakdown[r.Category]++
				}
			}
			s.Progress.Completed = int(completed)
			s.Progress.Success = int(success)
			s.Progress.Failed = int(failed)
			s.Progress.Running = len(sem)
			if total > 0 {
				s.Progress.Percent = int(completed * 100 / int64(total))
			}
			s.Progress.Status = "running"
			s.mu.Unlock()
		}(ch)
	}
	wg.Wait()
finish:
	s.mu.Lock()
	if ctx.Err() != nil {
		s.Progress.Status = "canceling"
	} else {
		s.Progress.Status = "done"
	}
	s.mu.Unlock()
}

// ── parameter builders ────────────────────────────────────────────────────

func (m *Manager) buildTestParams() streamtest.Params {
	tp := m.cfg.GetTestingParams()
	return streamtest.Params{
		Timeout:         toInt(tp["timeout"]),
		Concurrent:      toInt(tp["concurrent_threads"]),
		MaxFFprobe:      toInt(tp["max_concurrent_ffprobe"]),
		UseFFprobe:      true,
		SpeedTest:       toBool(tp["enable_speed_test"]),
		SpeedDuration:   toInt(tp["speed_test_duration"]),
		CacheTTL:        toInt(tp["cache_ttl"]),
		HostSpeedShare:  toBool(tp["enable_host_speed_share"]),
		SourceFreeze:    toBool(tp["enable_source_freeze"]),
		FreezeThreshold: toInt(tp["freeze_fail_threshold"]),
		FreezeBaseSec:   toInt(tp["freeze_base_seconds"]),
		FreezeMaxHours:  toInt(tp["freeze_max_hours"]),
		AdDetect:        toBool(tp["enable_ad_detect"]),
		AdKeywords:      splitCSV(toStr(tp["ad_keywords"])),
		AdMaxDuration:   toInt(tp["ad_max_duration"]),
		GlobalBlacklist: splitCSV(toStr(tp["global_blacklist"])),
		GlobalWhitelist: splitCSV(toStr(tp["global_whitelist"])),
		OutputSortBy:    toStr(tp["output_sort_by"]),
		MaxAttempts:     toInt(tp["max_test_attempts"]),
	}
}

func (m *Manager) buildCollectOpts() source.CollectOptions {
	n := m.cfg.GetNetworkConfig()
	return source.CollectOptions{
		Mirror:       toStr(n["github_mirror"]),
		APIURL:       m.cfg.Get("GitHub", "api_url", "https://api.github.com"),
		Token:        m.cfg.Get("GitHub", "api_token", ""),
		UserAgent:    "Mozilla/5.0 (live-source-manager)",
		ProxyEnabled: toBool(n["proxy_enabled"]),
		ProxyType:    toStr(n["proxy_type"]),
		ProxyHost:    toStr(n["proxy_host"]),
		ProxyPort:    toInt(n["proxy_port"]),
		TimeoutSec:   30,
	}
}

func (m *Manager) buildM3UOpts() m3u.Options {
	op := m.cfg.GetOutputParams()
	fp := m.cfg.GetFilterParams()
	return m3u.Options{
		Filename:           toStr(op["filename"]),
		OutputDir:          toStr(op["output_dir"]),
		GroupBy:            toStr(op["group_by"]),
		IncludeFailed:      toBool(op["include_failed"]),
		MaxPerChannel:      toInt(op["max_sources_per_channel"]),
		EnableFilter:       toBool(op["enable_filter"]),
		Filter:             fp,
		WhitelistForceKeep: toBool(op["whitelist_force_keep"]),
		SortBy:             m.cfg.Get("Testing", "output_sort_by", "speed"),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func applyTestResults(channels []types.Channel, results []types.TestResult) {
	byID := make(map[string]types.TestResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}
	for i := range channels {
		if r, ok := byID[channels[i].ID]; ok {
			channels[i].Status = r.Status
			channels[i].ResponseTime = r.ResponseTime
			channels[i].DownloadSpeed = r.DownloadSpeed
			channels[i].Resolution = r.Resolution
			channels[i].Bitrate = r.Bitrate
			channels[i].FPS = r.FPS
			channels[i].IsQualified = r.Status == "success"
		}
	}
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "True" || b == "true" || b == "1" || b == "yes" || b == "on"
	default:
		return false
	}
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}
