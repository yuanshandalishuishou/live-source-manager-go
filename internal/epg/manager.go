package epg

// EPG 管理器：抓取外部 XMLTV 源 → 入库 → 频道对齐 → 合并导出 epg.xml.gz → 提供 url-tvg 外链。
//
// 对齐 Python 版 app/epg.py，并针对其几处不足做了加固：
//   - 抓取走 context 超时 + 响应体大小上限，避免恶意巨型文件把内存吃穿；
//   - 单源失败不影响其他源（各自独立事务），失败原因完整写回 last_error；
//   - 导出临时文件 + 原子 rename，避免播放器读到写了一半的 epg.xml.gz。

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/security"
	"live-source-manager-go/internal/types"
)

// maxEPGBodyBytes 是单个节目单文件的下载上限（解压前），防御超大文件打爆磁盘/内存。
const maxEPGBodyBytes = 512 << 20 // 512 MiB

// Manager 负责全部 EPG 业务。并发安全。
type Manager struct {
	conn *sql.DB
	cfg  *config.Config

	// NamesProvider 可选：返回当前已采集到的本地频道名（用于 EPG 频道对齐）。
	// 未设置时回落到 channel_name_mapping 表里的已知频道名。
	NamesProvider func() []string

	mu        sync.Mutex
	running   bool
	lastState State
}

// State 是最近一次刷新的运行状态，供前端轮询。
type State struct {
	Running    bool                     `json:"running"`
	StartedAt  string                   `json:"started_at"`
	FinishedAt string                   `json:"finished_at"`
	Total      int                      `json:"total"`
	Done       int                      `json:"done"`
	Message    string                   `json:"message"`
	Results    []types.EPGRefreshResult `json:"results"`
}

// New 创建 EPG 管理器。
func New(conn *sql.DB, cfg *config.Config) *Manager {
	return &Manager{conn: conn, cfg: cfg}
}

// Enabled 返回 EPG 总开关。
func (m *Manager) Enabled() bool { return m.cfg.GetBool("EPG", "enabled", true) }

// InjectEnabled 返回是否向 m3u 注入 url-tvg（受总开关约束）。
func (m *Manager) InjectEnabled() bool {
	return m.Enabled() && m.cfg.GetBool("EPG", "inject_into_m3u", true)
}

// Location 返回配置的时区。
func (m *Manager) Location() *time.Location {
	return LoadLocation(m.cfg.Get("EPG", "timezone", "Asia/Shanghai"))
}

// State 返回当前刷新状态快照。
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.lastState
	st.Running = m.running
	return st
}

// RefreshModeDefault 返回全局刷新模式默认值（daily | interval | manual）。
func (m *Manager) RefreshModeDefault() string {
	return strings.ToLower(strings.TrimSpace(m.cfg.Get("EPG", "refresh_mode", "daily")))
}

// RefreshAtDefault 返回全局每日刷新时刻（HH:MM）。
func (m *Manager) RefreshAtDefault() string {
	return m.cfg.Get("EPG", "refresh_at", "03:30")
}

// RefreshMinutesDefault 返回全局间隔刷新分钟数。
func (m *Manager) RefreshMinutesDefault() int {
	return m.cfg.GetInt("EPG", "refresh_minutes", 360)
}

// ── 抓取 ──────────────────────────────────────────────────────────────────

// httpClient 构造带代理设置的 HTTP 客户端（复用 [Network] 段配置）。
func (m *Manager) httpClient(timeoutSec int) *http.Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		MaxIdleConns:        10,
		IdleConnTimeout:     60 * time.Second,
	}
	nc := m.cfg.GetNetworkConfig()
	if enabled, _ := nc["proxy_enabled"].(bool); enabled {
		host, _ := nc["proxy_host"].(string)
		port, _ := nc["proxy_port"].(int)
		ptype, _ := nc["proxy_type"].(string)
		if strings.TrimSpace(host) != "" && port > 0 {
			scheme := "http"
			if strings.EqualFold(ptype, "socks5") {
				scheme = "socks5"
			}
			raw := fmt.Sprintf("%s://%s:%d", scheme, host, port)
			if u, err := url.Parse(raw); err == nil {
				if user, _ := nc["proxy_username"].(string); user != "" {
					pass, _ := nc["proxy_password"].(string)
					u.User = url.UserPassword(user, pass)
				}
				tr.Proxy = http.ProxyURL(u)
			} else {
				logger.L().Warning("EPG 代理地址解析失败，改用直连: %v", err)
			}
		}
	}
	return &http.Client{Timeout: time.Duration(timeoutSec) * time.Second, Transport: tr}
}

// fetch 打开一个节目单源，返回可读流。支持 http/https 与本地 file:// 或裸路径。
func (m *Manager) fetch(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return nil, fmt.Errorf("节目单地址为空")
	}
	// 本地文件：file:// 或直接写路径，方便离线导入。
	if strings.HasPrefix(strings.ToLower(raw), "file://") || looksLikeLocalPath(raw) {
		path := raw
		if strings.HasPrefix(strings.ToLower(raw), "file://") {
			if u, err := url.Parse(raw); err == nil {
				path = u.Path
				if len(path) > 2 && path[0] == '/' && path[2] == ':' { // Windows /C:/...
					path = path[1:]
				}
			}
		}
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("打开本地节目单失败: %w", err)
		}
		return f, nil
	}
	// 远程：走解析阶段窄门禁（协议白名单 + SSRF），不做 DNS/黑名单拦截。
	if ok, reason, _ := security.IsStaticSafe(raw); !ok {
		return nil, fmt.Errorf("地址被安全策略拒绝: %s", reason)
	}
	timeout := m.cfg.GetInt("EPG", "fetch_timeout", 60)
	client := m.httpClient(timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LiveSourceManager/1.0; +EPG)")
	req.Header.Set("Accept", "application/xml,text/xml,application/gzip,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return &limitedReadCloser{r: io.LimitReader(resp.Body, maxEPGBodyBytes), c: resp.Body}, nil
}

// looksLikeLocalPath 判断是否为本地绝对路径（Windows 盘符或 Unix 根路径）。
func looksLikeLocalPath(s string) bool {
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	return strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//")
}

type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }

// ── 刷新 ──────────────────────────────────────────────────────────────────

// RefreshSource 抓取并入库单个来源。
func (m *Manager) RefreshSource(ctx context.Context, src types.EPGSource) types.EPGRefreshResult {
	start := time.Now()
	res := types.EPGRefreshResult{SourceID: src.ID, SourceName: src.Name}

	loc := m.Location()
	pastHours := m.cfg.GetInt("EPG", "past_hours", 6)
	keepDays := m.cfg.GetInt("EPG", "keep_days", 7)
	now := time.Now()
	windowStart := now.Add(-time.Duration(pastHours) * time.Hour)
	windowStop := now.Add(time.Duration(keepDays) * 24 * time.Hour)

	body, err := m.fetch(ctx, src.URL)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	defer body.Close()

	parsed, err := ParseStream(body, loc, windowStart, windowStop)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	if len(parsed.Channels) == 0 && len(parsed.Programmes) == 0 {
		res.Error = "解析结果为空（可能不是有效的 XMLTV 文档）"
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	if err := db.ReplaceEPGData(m.conn, src.ID, parsed.Channels, parsed.Programmes); err != nil {
		res.Error = "写入数据库失败: " + err.Error()
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	res.Success = true
	res.ChannelCount = len(parsed.Channels)
	res.ProgrammeCount = len(parsed.Programmes)
	res.DurationMs = int(time.Since(start).Milliseconds())
	logger.L().Info("EPG 源[%s]刷新完成: %d 频道 / %d 节目 / %dms（窗口外丢弃 %d）",
		src.Name, res.ChannelCount, res.ProgrammeCount, res.DurationMs, parsed.Skipped)
	return res
}

// RefreshAll 并发刷新给定来源（ids 为空表示全部启用的来源），刷新后自动对齐频道、
// 清理过期节目并重新导出 epg.xml.gz。返回每个来源的结果。
func (m *Manager) RefreshAll(ctx context.Context, ids []int) ([]types.EPGRefreshResult, error) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil, fmt.Errorf("已有刷新任务在进行中")
	}
	m.running = true
	m.lastState = State{Running: true, StartedAt: time.Now().Format("2006-01-02 15:04:05"), Message: "正在抓取节目单…"}
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.lastState.Running = false
		m.lastState.FinishedAt = time.Now().Format("2006-01-02 15:04:05")
		m.mu.Unlock()
	}()

	all, err := db.ListEPGSources(m.conn, false)
	if err != nil {
		return nil, err
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	targets := []types.EPGSource{}
	for _, s := range all {
		if len(want) > 0 {
			if want[s.ID] {
				targets = append(targets, s)
			}
			continue
		}
		if s.Enabled {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		m.mu.Lock()
		m.lastState.Message = "没有启用的节目单源"
		m.mu.Unlock()
		return []types.EPGRefreshResult{}, nil
	}

	maxConc := m.cfg.GetInt("EPG", "max_concurrent", 3)
	if maxConc < 1 {
		maxConc = 1
	}
	if maxConc > 8 {
		maxConc = 8
	}

	m.mu.Lock()
	m.lastState.Total = len(targets)
	m.mu.Unlock()

	sem := make(chan struct{}, maxConc)
	results := make([]types.EPGRefreshResult, len(targets))
	var wg sync.WaitGroup
	for i, src := range targets {
		wg.Add(1)
		go func(idx int, s types.EPGSource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := m.RefreshSource(ctx, s)
			if err := db.UpdateEPGSourceStatus(m.conn, r); err != nil {
				logger.L().Warning("写回 EPG 源状态失败[%s]: %v", s.Name, err)
			}
			results[idx] = r
			m.mu.Lock()
			m.lastState.Done++
			m.lastState.Message = fmt.Sprintf("已完成 %d/%d", m.lastState.Done, m.lastState.Total)
			m.mu.Unlock()
		}(i, src)
	}
	wg.Wait()

	// 后处理：清过期 → 频道对齐 → 导出。任一步失败只告警，不影响已入库的数据。
	if n, err := m.Cleanup(); err != nil {
		logger.L().Warning("清理过期节目失败: %v", err)
	} else if n > 0 {
		logger.L().Info("已清理过期节目 %d 条", n)
	}
	if n, err := m.MatchChannels(); err != nil {
		logger.L().Warning("EPG 频道对齐失败: %v", err)
	} else {
		logger.L().Info("EPG 频道对齐完成: %d 个频道已关联", n)
	}
	if path, err := m.GenerateXMLTV(); err != nil {
		logger.L().Warning("生成 XMLTV 失败: %v", err)
	} else {
		logger.L().Info("XMLTV 已生成: %s", path)
	}

	m.mu.Lock()
	m.lastState.Results = results
	ok := 0
	for _, r := range results {
		if r.Success {
			ok++
		}
	}
	m.lastState.Message = fmt.Sprintf("刷新完成：成功 %d / 共 %d", ok, len(results))
	m.mu.Unlock()
	return results, nil
}

// Cleanup 删除已经结束超过保留窗口的节目。
func (m *Manager) Cleanup() (int64, error) {
	pastHours := m.cfg.GetInt("EPG", "past_hours", 6)
	before := ToUTCStr(time.Now().Add(-time.Duration(pastHours) * time.Hour))
	return db.CleanupEPGProgrammes(m.conn, before)
}

// ── 频道对齐 ──────────────────────────────────────────────────────────────

// MatchChannels 把 EPG 频道与本地频道映射表按归一化名称对齐，
// 并把 tvg_id / tvg_logo 回写到 channel_name_mapping，供 m3u 生成使用。
// 返回成功对齐的频道数。
func (m *Manager) MatchChannels() (int, error) {
	epgChannels, err := db.ListAllEPGChannels(m.conn)
	if err != nil {
		return 0, err
	}
	if len(epgChannels) == 0 {
		return 0, nil
	}
	var localNames []string
	if m.NamesProvider != nil {
		localNames = m.NamesProvider()
	}
	if len(localNames) == 0 {
		localNames, err = db.ListAllChannelNames(m.conn)
		if err != nil {
			return 0, err
		}
	}
	if len(localNames) == 0 {
		return 0, nil
	}
	// 归一化名 → 本地频道原名
	index := make(map[string]string, len(localNames))
	for _, name := range localNames {
		key := NormalizeChannelName(name)
		if key == "" {
			continue
		}
		if _, exists := index[key]; !exists {
			index[key] = name
		}
	}
	matched := 0
	for _, ec := range epgChannels {
		candidates := append([]string{ec.DisplayName, ec.TVGID}, ec.Aliases...)
		var localName string
		for _, cand := range candidates {
			key := NormalizeChannelName(cand)
			if key == "" {
				continue
			}
			if n, ok := index[key]; ok {
				localName = n
				break
			}
		}
		if localName == "" {
			continue
		}
		if ec.MatchedChannel != localName {
			if err := db.SetEPGChannelMatch(m.conn, ec.ID, localName); err != nil {
				logger.L().Warning("回写 EPG 频道匹配失败[%s]: %v", ec.TVGID, err)
				continue
			}
		}
		if err := db.SetChannelTVGInfo(m.conn, localName, ec.TVGID, ec.Icon); err != nil {
			logger.L().Warning("回写频道 tvg 信息失败[%s]: %v", localName, err)
			continue
		}
		matched++
	}
	return matched, nil
}

// ── 导出 ──────────────────────────────────────────────────────────────────

// OutputPath 返回 XMLTV 导出文件的绝对路径（发布目录 + 配置文件名）。
func (m *Manager) OutputPath() string {
	hs := m.cfg.GetHTTPServerConfig()
	root, _ := hs["document_root"].(string)
	if strings.TrimSpace(root) == "" {
		root = "./www/output"
	}
	name := strings.TrimSpace(m.cfg.Get("EPG", "output_filename", "epg.xml.gz"))
	if name == "" {
		name = "epg.xml.gz"
	}
	return filepath.Join(root, name)
}

// GenerateXMLTV 合并全部启用来源的数据，导出为 XMLTV 文件（按 .gz 后缀决定是否压缩）。
// 写临时文件后原子 rename，避免播放器读到半截文件。
func (m *Manager) GenerateXMLTV() (string, error) {
	out := m.OutputPath()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	channels, err := db.ListAllEPGChannels(m.conn)
	if err != nil {
		return "", err
	}
	programmes, err := db.ListAllEPGProgrammes(m.conn)
	if err != nil {
		return "", err
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	writeErr := func() error {
		defer f.Close()
		var w io.Writer = f
		if strings.HasSuffix(strings.ToLower(out), ".gz") {
			gz := gzip.NewWriter(f)
			defer gz.Close()
			w = gz
		}
		return WriteXMLTV(w, channels, programmes, m.Location())
	}()
	if writeErr != nil {
		_ = os.Remove(tmp)
		return "", writeErr
	}
	// Windows 下 rename 到已存在文件会失败，先删旧文件。
	_ = os.Remove(out)
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

// GetEPGURL 返回可供播放器使用的节目单外链。
// 优先使用配置的 web_base_url，否则用局域网 IP + 发布端口拼装。
func (m *Manager) GetEPGURL() string {
	if !m.Enabled() {
		return ""
	}
	name := strings.TrimSpace(m.cfg.Get("EPG", "output_filename", "epg.xml.gz"))
	if name == "" {
		name = "epg.xml.gz"
	}
	if base := strings.TrimSpace(m.cfg.Get("EPG", "web_base_url", "")); base != "" {
		return strings.TrimRight(base, "/") + "/" + name
	}
	hs := m.cfg.GetHTTPServerConfig()
	port, _ := hs["fileshare_port"].(int)
	if port <= 0 {
		port = 12345
	}
	host := guessLANIP()
	if host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d/%s", host, port, name)
}

// guessLANIP 猜测本机局域网 IP（不发包，仅查网卡）。
func guessLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			if ip4.IsPrivate() {
				return ip4.String()
			}
			if fallback == "" {
				fallback = ip4.String()
			}
		}
	}
	return fallback
}

// ── 查询（供前端展示） ────────────────────────────────────────────────────

// GridRow 是节目单网格的一行（一个频道 + 其时段节目）。
type GridRow struct {
	TVGID       string               `json:"tvg_id"`
	DisplayName string               `json:"display_name"`
	Icon        string               `json:"icon"`
	Matched     string               `json:"matched_channel"`
	Programmes  []types.EPGProgramme `json:"programmes"`
}

// GetGrid 返回指定日期（本地时区）的节目单网格。dayOffset=0 表示今天。
func (m *Manager) GetGrid(query string, dayOffset, limit int) ([]GridRow, error) {
	loc := m.Location()
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, dayOffset)
	dayStop := dayStart.AddDate(0, 0, 1)

	channels, err := db.ListEPGChannels(m.conn, 0, query, limit, 0)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return []GridRow{}, nil
	}
	ids := make([]string, 0, len(channels))
	for _, c := range channels {
		ids = append(ids, c.TVGID)
	}
	progs, err := db.QueryEPGProgrammes(m.conn, ids, ToUTCStr(dayStart), ToUTCStr(dayStop))
	if err != nil {
		return nil, err
	}
	byID := map[string][]types.EPGProgramme{}
	for _, p := range progs {
		byID[p.TVGID] = append(byID[p.TVGID], p)
	}
	rows := make([]GridRow, 0, len(channels))
	for _, c := range channels {
		list := byID[c.TVGID]
		sort.Slice(list, func(i, j int) bool { return list[i].StartUTC < list[j].StartUTC })
		rows = append(rows, GridRow{
			TVGID: c.TVGID, DisplayName: c.DisplayName, Icon: c.Icon,
			Matched: c.MatchedChannel, Programmes: list,
		})
	}
	return rows, nil
}

// NowNext 是某频道的当前 + 下一个节目。
type NowNext struct {
	TVGID       string              `json:"tvg_id"`
	DisplayName string              `json:"display_name"`
	Now         *types.EPGProgramme `json:"now"`
	Next        *types.EPGProgramme `json:"next"`
}

// GetNowNext 返回若干频道的「正在播 / 下一个」。
func (m *Manager) GetNowNext(query string, limit int) ([]NowNext, error) {
	if limit <= 0 {
		limit = 50
	}
	channels, err := db.ListEPGChannels(m.conn, 0, query, limit, 0)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return []NowNext{}, nil
	}
	ids := make([]string, 0, len(channels))
	for _, c := range channels {
		ids = append(ids, c.TVGID)
	}
	now := time.Now().UTC()
	progs, err := db.QueryEPGProgrammes(m.conn, ids, ToUTCStr(now.Add(-time.Minute)), ToUTCStr(now.Add(12*time.Hour)))
	if err != nil {
		return nil, err
	}
	byID := map[string][]types.EPGProgramme{}
	for _, p := range progs {
		byID[p.TVGID] = append(byID[p.TVGID], p)
	}
	nowStr := ToUTCStr(now)
	out := make([]NowNext, 0, len(channels))
	for _, c := range channels {
		list := byID[c.TVGID]
		sort.Slice(list, func(i, j int) bool { return list[i].StartUTC < list[j].StartUTC })
		nn := NowNext{TVGID: c.TVGID, DisplayName: c.DisplayName}
		for i := range list {
			p := list[i]
			if p.StartUTC <= nowStr && p.StopUTC > nowStr && nn.Now == nil {
				nn.Now = &list[i]
				continue
			}
			if p.StartUTC > nowStr && nn.Next == nil {
				nn.Next = &list[i]
			}
		}
		out = append(out, nn)
	}
	return out, nil
}
