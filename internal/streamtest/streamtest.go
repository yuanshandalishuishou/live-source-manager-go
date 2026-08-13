// Package streamtest implements IPTV live-source connectivity/quality testing.
//
// It ports the behavior of the Python StreamTester (app/stream_tester.py):
//   - ffprobe is preferred for detailed metadata (resolution, bitrate, fps)
//   - ffmpeg is used as a fallback for connectivity, speed and ad/loop probing
//   - results are cached per-URL (honoring a TTL)
//   - same-host speed results are shared across channels
//   - repeatedly failing hosts are frozen with exponential backoff
//   - global black/whitelist and ad/loop detection are honored
//   - batch testing runs concurrently and reports live progress
package streamtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
	"live-source-manager-go/internal/util"
)

// Params controls a test pass.
type Params struct {
	Timeout         int
	Concurrent      int
	MaxFFprobe      int
	UseFFprobe      bool
	SpeedTest       bool
	SpeedDuration   int
	CacheTTL        int
	HostSpeedShare  bool
	SourceFreeze    bool
	FreezeThreshold int
	FreezeBaseSec   int
	FreezeMaxHours  int
	AdDetect        bool
	AdKeywords      []string
	AdMaxDuration   int
	GlobalBlacklist []string
	GlobalWhitelist []string
	OutputSortBy    string // "speed" | "latency" | "name"
	MaxAttempts     int
}

// Tester holds shared state for a stream-testing session.
type Tester struct {
	conn        *sql.DB
	cfg         *config.Config
	ffprobePath string
	ffmpegPath  string

	cache   map[string]cacheEntry
	cacheMu sync.Mutex

	hostCache map[string]hostEntry
	hostMu    sync.Mutex

	frozen   map[string]frozenEntry
	frozenMu sync.Mutex

	ffSem chan struct{}
}

type cacheEntry struct {
	result types.TestResult
	ts     time.Time
	ttl    int
}

type hostEntry struct {
	result types.TestResult
	ts     time.Time
	ttl    int
}

type frozenEntry struct {
	failCount   int
	frozenUntil int64 // unix seconds; 0 == not frozen
}

var (
	reSpeed    = regexp.MustCompile(`(?i)speed=\s*([0-9]+(?:\.[0-9]+)?)x`)
	reBitrate  = regexp.MustCompile(`(?i)bitrate[:=]?\s*([0-9]+(?:\.[0-9]+)?)\s*k`)
	reDuration = regexp.MustCompile(`Duration:\s*([0-9]+:[0-9]+:[0-9]+(?:\.[0-9]+)?)`)
)

// NewTester creates a Tester bound to a database and config, locating the
// ffprobe/ffmpeg binaries up-front.
func NewTester(conn *sql.DB, cfg *config.Config) *Tester {
	t := &Tester{
		conn:      conn,
		cfg:       cfg,
		cache:     make(map[string]cacheEntry),
		hostCache: make(map[string]hostEntry),
		frozen:    make(map[string]frozenEntry),
		ffSem:     make(chan struct{}, 4),
	}
	t.ffprobePath, t.ffmpegPath = t.LocateBinaries()
	return t
}

// fileExists reports whether a path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LocateBinaries finds ffprobe then ffmpeg. Search order:
//  1. env LSM_FFPROBE_PATH / LSM_FFMPEG_PATH
//  2. env LSM_FFMPEG_DIR / config Tools.ffmpeg_dir (directory containing the binaries)
//  3. ./tools/ffmpeg/ffprobe[.exe] & ffmpeg[.exe] relative to cwd
//  4. sibling ../live-source-manager/tools/ffmpeg (co-located Python project)
//  5. PATH
//
// Each path is returned independently; ("", "") is returned only when both
// binaries are absent.
func (t *Tester) LocateBinaries() (ffprobe, ffmpeg string) {
	return t.findBinary("ffprobe"), t.findBinary("ffmpeg")
}

// HasBinaries reports whether at least one of ffprobe/ffmpeg was located.
func (t *Tester) HasBinaries() bool {
	return t.ffprobePath != "" || t.ffmpegPath != ""
}

// FFprobePath returns the located ffprobe path ("" if absent).
func (t *Tester) FFprobePath() string { return t.ffprobePath }

// FFmpegPath returns the located ffmpeg path ("" if absent).
func (t *Tester) FFmpegPath() string { return t.ffmpegPath }

// SetFFProbeConcurrency adjusts how many ffprobe/ffmpeg processes may run at once.
func (t *Tester) SetFFProbeConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	t.ffSem = make(chan struct{}, n)
}

func (t *Tester) findBinary(name string) string {
	// 1) explicit binary path env (LSM_FFPROBE_PATH / LSM_FFMPEG_PATH)
	envKey := "LSM_" + strings.ToUpper(name) + "_PATH"
	if env := os.Getenv(envKey); env != "" && fileExists(env) {
		return env
	}
	// 2) explicit directory env / config (Tools.ffmpeg_dir)
	dirs := []string{}
	if d := os.Getenv("LSM_FFMPEG_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if t.cfg != nil {
		if d := t.cfg.Get("Tools", "ffmpeg_dir", ""); d != "" {
			dirs = append(dirs, d)
		}
	}
	cwd, _ := os.Getwd()
	candidates := []string{}
	// expand appends both the flat layout (<dir>/ffprobe[.exe]) and the official
	// release layout (<dir>/bin/ffprobe[.exe]). BtbN / gyan.dev Windows zips and
	// johnvansickle tarballs ship binaries under bin/, so a user who unpacks the
	// archive verbatim into tools/ffmpeg/ would otherwise be reported as
	// "ffprobe not found" even though the binary is right there.
	expand := func(base string) {
		candidates = append(candidates,
			filepath.Join(base, name+".exe"),
			filepath.Join(base, name),
			filepath.Join(base, "bin", name+".exe"),
			filepath.Join(base, "bin", name),
		)
	}
	for _, d := range dirs {
		expand(d)
	}
	// 2.5) derive project root from the executable path — robust against CWD.
	// Mirrors Python's `Path(__file__).resolve().parent` resource discovery, so
	// the bundled tools/ffmpeg is always found regardless of the launch directory
	// (Windows service / systemd / Docker may start with an unrelated CWD).
	if exe, err := os.Executable(); err == nil {
		if ec, e2 := filepath.EvalSymlinks(exe); e2 == nil {
			exe = ec
		}
		root := filepath.Dir(filepath.Dir(exe)) // .../bin/lsm.exe -> project root
		expand(filepath.Join(root, "tools", "ffmpeg"))
	}
	// 3) project-local tools/ffmpeg (cwd + sibling Python project) then bare name
	expand(filepath.Join(cwd, "tools", "ffmpeg"))
	expand(filepath.Join(cwd, "..", "live-source-manager", "tools", "ffmpeg"))
	expand("tools/ffmpeg")
	candidates = append(candidates,
		name+".exe",
		name,
	)
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if p, err := exec.LookPath(name + ".exe"); err == nil {
		return p
	}
	return ""
}

// GetCache returns a previously cached result for url (within the entry's TTL).
func (t *Tester) GetCache(url string) (types.TestResult, bool) {
	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()
	e, ok := t.cache[url]
	if !ok {
		return types.TestResult{}, false
	}
	if e.ttl > 0 && time.Since(e.ts).Seconds() > float64(e.ttl) {
		delete(t.cache, url)
		return types.TestResult{}, false
	}
	return e.result, true
}

func (t *Tester) setCache(u string, r types.TestResult, ttl int) {
	t.cacheMu.Lock()
	t.cache[u] = cacheEntry{result: r, ts: time.Now(), ttl: ttl}
	t.cacheMu.Unlock()
}

func (t *Tester) getHostCache(host string) (types.TestResult, bool) {
	t.hostMu.Lock()
	defer t.hostMu.Unlock()
	e, ok := t.hostCache[host]
	if !ok {
		return types.TestResult{}, false
	}
	if e.ttl > 0 && time.Since(e.ts).Seconds() > float64(e.ttl) {
		delete(t.hostCache, host)
		return types.TestResult{}, false
	}
	return e.result, true
}

func (t *Tester) setHostCache(host string, r types.TestResult, ttl int) {
	if r.Status != "success" || host == "" {
		return
	}
	t.hostMu.Lock()
	t.hostCache[host] = hostEntry{result: r, ts: time.Now(), ttl: ttl}
	t.hostMu.Unlock()
}

// checkFrozen reports whether a host is currently within its freeze window.
func (t *Tester) checkFrozen(host string) bool {
	t.frozenMu.Lock()
	defer t.frozenMu.Unlock()
	fr, ok := t.frozen[host]
	if !ok {
		return false
	}
	now := time.Now().Unix()
	if fr.frozenUntil > 0 {
		if fr.frozenUntil > now {
			return true
		}
		delete(t.frozen, host)
	}
	return false
}

func (t *Tester) recordFailure(host string, p Params) {
	t.frozenMu.Lock()
	defer t.frozenMu.Unlock()
	fr := t.frozen[host]
	fr.failCount++
	if fr.failCount >= p.FreezeThreshold {
		delay := p.FreezeBaseSec * pow2(fr.failCount-p.FreezeThreshold)
		cap := p.FreezeMaxHours * 3600
		if cap <= 0 {
			cap = 1
		}
		if delay > cap {
			delay = cap
		}
		if delay < 1 {
			delay = 1
		}
		fr.frozenUntil = time.Now().Unix() + int64(delay)
		logger.L().Info("source frozen for %ds (host=%s, fails=%d)", delay, host, fr.failCount)
	}
	t.frozen[host] = fr
}

func (t *Tester) recordSuccess(host string) {
	t.frozenMu.Lock()
	defer t.frozenMu.Unlock()
	delete(t.frozen, host)
}

func pow2(n int) int {
	r := 1
	for i := 0; i < n; i++ {
		r *= 2
	}
	return r
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// extractHost returns the lower-cased host (with port) of a URL.
func extractHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.ToLower(u.Host)
}

// inList mirrors Python's _url_in_list: host exact match or URL substring
// (case-insensitive).
func inList(rawURL string, entries []string) bool {
	if len(entries) == 0 {
		return false
	}
	u := strings.ToLower(rawURL)
	host := extractHost(rawURL)
	for _, e := range entries {
		el := strings.ToLower(strings.TrimSpace(e))
		if el == "" {
			continue
		}
		if el == host || strings.Contains(u, el) {
			return true
		}
	}
	return false
}

// Test probes a single channel and returns a TestResult.
func (t *Tester) Test(ctx context.Context, ch types.Channel, p Params) types.TestResult {
	id := util.ChannelID(ch.Name, ch.URL)
	base := types.TestResult{ID: id, URL: ch.URL}
	url := ch.URL
	host := extractHost(url)

	// ---- global white/black list (tested first, mirrors Python) ----
	if len(p.GlobalWhitelist) > 0 && inList(url, p.GlobalWhitelist) {
		// whitelisted: do not skip, test normally
	} else if len(p.GlobalBlacklist) > 0 && inList(url, p.GlobalBlacklist) {
		base.Status = "blacklisted"
		base.Error = "url in global blacklist"
		return base
	}

	// ---- source freeze (skip hosts cooling down) ----
	if p.SourceFreeze && t.checkFrozen(host) {
		base.Status = "frozen"
		base.Message = "source frozen (exponential backoff)"
		return base
	}

	// ---- URL cache ----
	if c, ok := t.GetCache(url); ok {
		c.ID = id
		c.URL = url
		return c
	}

	// ---- host speed share ----
	if p.HostSpeedShare {
		if h, ok := t.getHostCache(host); ok {
			h.ID = id
			h.URL = url
			return h
		}
	}

	// ---- binaries absent ----
	if t.ffprobePath == "" && t.ffmpegPath == "" {
		base.Status = "untested"
		base.Error = "ffprobe/ffmpeg not found"
		return base
	}

	if ctx.Err() != nil {
		base.Status = "interrupted"
		base.Error = "context canceled"
		return base
	}

	// ---- probe ----
	start := time.Now()
	status, meta := t.probe(ctx, url, ch.UserAgent, ch.Referrer, p)
	base.ResponseTime = time.Since(start).Seconds()
	base.Resolution = meta.Resolution
	base.Bitrate = meta.Bitrate
	base.FPS = meta.FPS
	base.DownloadSpeed = meta.DownloadSpeed

	if status != "success" {
		base.Status = status
		if meta.Message != "" {
			base.Message = meta.Message
		}
		if base.Error == "" {
			base.Error = status
		}
		if p.SourceFreeze {
			t.recordFailure(host, p)
		}
		t.setCache(url, base, p.CacheTTL)
		return base
	}

	base.Status = "success"

	// ---- ad / loop detection (HTTP playlist probe) ----
	if p.AdDetect && t.detectAdPlaylist(url, ch.UserAgent, ch.Referrer, p) {
		base.Status = "failed"
		base.Message = "ad/loop detected"
		t.setCache(url, base, p.CacheTTL)
		return base
	}

	// ---- download speed test ----
	if p.SpeedTest {
		if sp := t.speedTest(ctx, url, ch.UserAgent, ch.Referrer, p); sp > 0 {
			base.DownloadSpeed = sp
		}
	}

	t.setCache(url, base, p.CacheTTL)
	if p.HostSpeedShare {
		t.setHostCache(host, base, p.CacheTTL)
	}
	if p.SourceFreeze {
		t.recordSuccess(host)
	}
	return base
}

// probe runs ffprobe (preferred) and falls back to ffmpeg on failure.
func (t *Tester) probe(ctx context.Context, u, ua, referer string, p Params) (string, types.TestResult) {
	t.acquireFF()
	defer t.releaseFF()

	if t.ffprobePath != "" {
		status, meta := t.probeFFprobe(ctx, u, ua, referer, t.ffprobePath, p)
		if status == "success" || status == "timeout" {
			return status, meta
		}
		// ffprobe failed (not a timeout): fall back to ffmpeg if present.
		if t.ffmpegPath != "" {
			return t.probeFFmpeg(ctx, u, ua, referer, t.ffmpegPath, p, true)
		}
		return status, meta
	}
	if t.ffmpegPath != "" {
		return t.probeFFmpeg(ctx, u, ua, referer, t.ffmpegPath, p, true)
	}
	return "failed", types.TestResult{}
}

func (t *Tester) acquireFF() {
	if t.ffSem != nil {
		t.ffSem <- struct{}{}
	}
}

func (t *Tester) releaseFF() {
	if t.ffSem != nil {
		<-t.ffSem
	}
}

type ffprobeOut struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func (t *Tester) probeFFprobe(ctx context.Context, u, ua, referer string, bin string, p Params) (string, types.TestResult) {
	timeout := p.Timeout + 2
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// -timeout expects microseconds; convert the configured seconds.
	// NOTE: use `-v error` (not `quiet`) so ffprobe still reports the real
	// failure reason (403 / connection refused / invalid data) on stderr.
	// With `-v quiet` every failure is silent and classifyError() can only
	// return the generic "no output" bucket, defeating the 失败原因分布 panel.
	args := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams",
		"-timeout", strconv.Itoa(p.Timeout * 1_000_000), u}
	if ua != "" {
		args = append(args, "-headers", "User-Agent: "+ua)
	}
	if referer != "" {
		args = append(args, "-headers", "Referer: "+referer)
	}
	cmd := exec.CommandContext(cctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if cctx.Err() == context.DeadlineExceeded {
		return "timeout", types.TestResult{}
	}
	if err != nil {
		raw := stderr.String()
		if raw == "" {
			raw = stdout.String()
		}
		st, msg := classifyError(raw)
		return st, types.TestResult{Message: msg}
	}

	var out ffprobeOut
	if jsonErr := json.Unmarshal(stdout.Bytes(), &out); jsonErr != nil {
		return "failed", types.TestResult{}
	}
	if len(out.Streams) == 0 {
		return "failed", types.TestResult{}
	}

	res := types.TestResult{}
	hasVideo := false
	for _, s := range out.Streams {
		if s.CodecType == "video" {
			hasVideo = true
			if s.Width > 0 && s.Height > 0 {
				res.Resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
			}
			if s.AvgFrameRate != "" {
				if fps, ok := parseFrac(s.AvgFrameRate); ok {
					res.FPS = fps
				}
			}
			break
		}
	}
	// 对齐 Python has_video_stream：ffprobe 命中视频流为 true，纯音频(有流但无视频)为 false。
	res.HasVideoStream = hasVideo
	if out.Format.BitRate != "" {
		if b, e := strconv.Atoi(out.Format.BitRate); e == nil {
			res.Bitrate = b / 1000
		}
	}
	return "success", res
}

func (t *Tester) probeFFmpeg(ctx context.Context, u, ua, referer string, bin string, p Params, adCheck bool) (string, types.TestResult) {
	timeout := p.Timeout + p.SpeedDuration + 5
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := []string{"-i", u, "-t", strconv.Itoa(p.SpeedDuration), "-f", "null", "-"}
	if ua != "" {
		args = append(args, "-user_agent", ua)
	}
	if referer != "" {
		args = append(args, "-headers", "Referer: "+referer)
	}
	cmd := exec.CommandContext(cctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	raw := stderr.String()

	if cctx.Err() == context.DeadlineExceeded {
		return "timeout", types.TestResult{}
	}
	if err != nil {
		st, msg := classifyError(raw)
		return st, types.TestResult{Message: msg}
	}

	res := types.TestResult{}
	if m := reBitrate.FindStringSubmatch(raw); m != nil {
		if b, e := strconv.ParseFloat(m[1], 64); e == nil {
			res.Bitrate = int(b)
		}
	}
	if m := reSpeed.FindStringSubmatch(raw); m != nil {
		if sp, e := strconv.ParseFloat(m[1], 64); e == nil && res.Bitrate > 0 {
			// throughput (KB/s) ~= bitrate(kbps)/8 * speed_factor
			res.DownloadSpeed = float64(res.Bitrate)/8.0*sp + res.DownloadSpeed
		}
	}
	if adCheck && p.AdDetect {
		if m := reDuration.FindStringSubmatch(raw); m != nil {
			if d, ok := parseDuration(m[1]); ok && d > 0 && d <= float64(p.AdMaxDuration) {
				res.Message = "ad/loop detected (finite duration)"
				return "failed", res
			}
		}
	}
	return "success", res
}

func (t *Tester) speedTest(ctx context.Context, u, ua, referer string, p Params) float64 {
	if t.ffmpegPath == "" {
		return 0
	}
	t.acquireFF()
	defer t.releaseFF()
	_, meta := t.probeFFmpeg(ctx, u, ua, referer, t.ffmpegPath, p, false)
	return meta.DownloadSpeed
}

// detectAdPlaylist mirrors Python's _detect_ad_playlist: fetch the HLS playlist
// head and look for ad keywords or a short VOD (ENDLIST) loop.
func (t *Tester) detectAdPlaylist(u, ua, referer string, p Params) bool {
	lu := strings.ToLower(u)
	if !strings.Contains(u, ".m3u8") && !strings.Contains(lu, "m3u") {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return false
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	raw := string(buf[:n])
	if raw == "" {
		return false
	}
	lowered := strings.ToLower(raw)
	for _, kw := range p.AdKeywords {
		if kw != "" && strings.Contains(lowered, strings.ToLower(kw)) {
			return true
		}
	}
	if strings.Contains(lowered, "#ext-x-endlist") {
		total := 0.0
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "#EXTINF") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			durPart := strings.SplitN(parts[1], ",", 2)[0]
			if f, e := strconv.ParseFloat(strings.TrimSpace(durPart), 64); e == nil {
				total += f
			}
		}
		if total > 0 && total <= float64(p.AdMaxDuration) {
			return true
		}
	}
	return false
}

// classifyError maps ffprobe/ffmpeg stderr text to a (status, humanMessage) pair.
// It mirrors Python's _classify_stream_error, adding the auth_blocked / not_found
// diagnostic categories so the realtime test surfaces readable, actionable reasons
// instead of a generic "failed". The machine status stays stable for the UI/filter,
// while Message carries a localized explanation (notably flagging "server unreachable
// from this host but likely viewable by the end user").
func classifyError(msg string) (string, string) {
	if strings.TrimSpace(msg) == "" {
		return "failed", "ffprobe 无输出（进程可能被中断）"
	}
	t := strings.ToLower(msg)
	if strings.Contains(t, "timed out") {
		return "timeout", "测试超时（源响应过慢或网络拥塞）"
	}
	connKw := []string{
		"connection refused", "error number -111", "connection timed out",
		"error number -138", "network unreachable", "no route", "error number -101",
		"host is down", "error number -64", "connection failed", "could not connect",
		"failed: connection",
	}
	for _, k := range connKw {
		if strings.Contains(t, k) {
			return "connection_failed", "连接失败/超时（服务器不可达；本机可能连不上，但用户侧仍可观看）"
		}
	}
	dnsKw := []string{
		"name or service not known", "could not resolve", "nodename nor servname",
		"getaddrinfo", "resolve", "dns error",
	}
	for _, k := range dnsKw {
		if strings.Contains(t, k) {
			return "dns_error", "DNS 解析失败（域名无法解析）"
		}
	}
	authKw := []string{"403", "401", "forbidden", "unauthorized", "expired", "txsecret", "txtime"}
	for _, k := range authKw {
		if strings.Contains(t, k) {
			return "auth_blocked", "鉴权失败（403/401，可能是防盗链或 token 过期）"
		}
	}
	if strings.Contains(t, "404") || strings.Contains(t, "not found") || strings.Contains(t, "no such") {
		return "not_found", "资源不存在（404）"
	}
	// invalid/corrupt source or an explicit server rejection (not auth-specific).
	badSrcKw := []string{"invalid data", "server returned", "method not allowed", "operation not permitted"}
	for _, k := range badSrcKw {
		if strings.Contains(t, k) {
			return "bad_source", "源数据无效/服务器拒绝（流可能已失效或触发防盗链）"
		}
	}
	return "failed", "ffprobe 执行失败（未知错误）"
}

func parseFrac(s string) (float64, bool) {
	if !strings.Contains(s, "/") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	parts := strings.SplitN(s, "/", 2)
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0, false
	}
	return num / den, true
}

func parseDuration(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return float64(h)*3600 + float64(m)*60 + sec, true
}

// progTracker maintains live progress for a batch run.
type progTracker struct {
	mu sync.Mutex
	tp types.TestProgress
}

func (p *progTracker) snapshot() types.TestProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tp
}

func (p *progTracker) startRunning(u string) {
	p.mu.Lock()
	p.tp.Running++
	p.tp.CurrentURL = u
	p.mu.Unlock()
}

func (p *progTracker) finish(success bool) {
	p.mu.Lock()
	p.tp.Running--
	p.tp.Completed++
	if success {
		p.tp.Success++
	} else {
		p.tp.Failed++
	}
	if p.tp.Total > 0 {
		p.tp.Percent = p.tp.Completed * 100 / p.tp.Total
	}
	p.mu.Unlock()
}

// TestBatch runs tests concurrently, invoking onProgress with a progress
// snapshot, and returns one result per input channel in input order.
func (t *Tester) TestBatch(ctx context.Context, channels []types.Channel, p Params, onProgress func(types.TestProgress)) []types.TestResult {
	total := len(channels)
	results := make([]types.TestResult, total)

	concurrent := maxInt(1, p.Concurrent)
	maxFF := maxInt(1, p.MaxFFprobe)
	t.ffSem = make(chan struct{}, maxFF)

	prog := &progTracker{tp: types.TestProgress{
		Total:     total,
		Status:    "running",
		StartedAt: time.Now().Format(time.RFC3339),
	}}
	if onProgress != nil {
		onProgress(prog.snapshot())
	}

	sem := make(chan struct{}, concurrent)
	var wg sync.WaitGroup

	for i, ch := range channels {
		// Do not start new work once the context is canceled.
		if ctx.Err() != nil {
			results[i] = types.TestResult{
				ID:     util.ChannelID(ch.Name, ch.URL),
				URL:    ch.URL,
				Status: "interrupted",
				Error:  "context canceled",
			}
			prog.finish(false)
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // acquire a worker slot
		go func(idx int, c types.Channel) {
			defer wg.Done()
			defer func() { <-sem }()

			// Re-check cancellation right before testing.
			if ctx.Err() != nil {
				results[idx] = types.TestResult{
					ID:     util.ChannelID(c.Name, c.URL),
					URL:    c.URL,
					Status: "interrupted",
					Error:  "context canceled",
				}
				prog.finish(false)
				return
			}

			prog.startRunning(c.URL)
			if onProgress != nil {
				onProgress(prog.snapshot())
			}

			r := t.Test(ctx, c, p)
			results[idx] = r

			prog.finish(r.Status == "success")
			if onProgress != nil {
				onProgress(prog.snapshot())
			}
		}(i, ch)
	}

	wg.Wait()
	prog.mu.Lock()
	prog.tp.Status = "done"
	prog.tp.Running = 0
	prog.mu.Unlock()
	if onProgress != nil {
		onProgress(prog.snapshot())
	}
	return results
}
