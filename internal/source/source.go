// Package source collects IPTV live sources from local files, online URLs and
// GitHub repositories, parsing them into Channel records. It is a faithful Go
// port of the Python app/source_manager.py SourceManager.
//
// Parse-stage URL safety gate:
//
//	Every extracted stream URL is passed through security.IsStaticSafe (the
//	narrow SSRF + scheme gate). Unsafe URLs are NOT silently dropped: they are
//	recorded in CollectReport.Exclusions[url] = reason and the channel is skipped.
//	This is a hard requirement.
package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/security"
	"live-source-manager-go/internal/types"
	"live-source-manager-go/internal/util"
)

// CollectOptions controls how sources are collected (network behavior).
type CollectOptions struct {
	Mirror       string // github mirror prefix, e.g. https://ghproxy.com/
	APIURL       string // github api base
	Token        string // github token (optional)
	UserAgent    string
	ProxyEnabled bool
	ProxyType    string // socks5/http
	ProxyHost    string
	ProxyPort    int
	TimeoutSec   int
	Concurrency  int // max concurrent HTTP fetches (online/github); 0 => default (8)
}

// CollectReport is the aggregated result of a collection pass.
type CollectReport struct {
	Channels    []types.Channel
	SourceFiles []types.SourceFile
	Errors      []string          // top-level collection errors
	FileErrors  map[string]string // path/url -> error message
	Exclusions  map[string]string // excluded URL -> reason (parse-stage IsStaticSafe failures)
}

// Manager collects and parses live sources.
type Manager struct {
	conn *sql.DB
	cfg  *config.Config
}

// NewManager builds a source Manager bound to a database and config.
func NewManager(conn *sql.DB, cfg *config.Config) *Manager {
	return &Manager{conn: conn, cfg: cfg}
}

func newReport() *CollectReport {
	return &CollectReport{
		Channels:    nil,
		SourceFiles: nil,
		Errors:      nil,
		FileErrors:  map[string]string{},
		Exclusions:  map[string]string{},
	}
}

// ── Collect Local ──────────────────────────────────────────────────────────

// CollectLocal reads local paths that are either directories (walked for
// .m3u/.m3u8/.txt) or individual files (parsed directly).
func (m *Manager) CollectLocal(paths []string, opts CollectOptions) (*CollectReport, error) {
	report := newReport()
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			report.FileErrors[p] = err.Error()
			logger.L().Warning("本地源路径不存在，跳过: %s (%v)", p, err)
			continue
		}
		if info.IsDir() {
			m.walkLocalDir(p, report)
		} else {
			m.parseLocalFile(p, report)
		}
	}
	logger.L().Info("本地采集完成: %d 个文件, %d 个频道", len(report.SourceFiles), len(report.Channels))
	return report, nil
}

func (m *Manager) walkLocalDir(dir string, report *CollectReport) {
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".m3u" && ext != ".m3u8" && ext != ".txt" {
			return nil
		}
		info, ierr := d.Info()
		size := int64(0)
		if ierr == nil {
			size = info.Size()
		}
		if cerr := m.parseLocalFileWithSize(path, size, report); cerr != nil {
			report.FileErrors[path] = cerr.Error()
		}
		return nil
	})
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("遍历本地目录失败 %s: %v", dir, err))
	}
}

func (m *Manager) parseLocalFile(path string, report *CollectReport) {
	info, err := os.Stat(path)
	size := int64(0)
	if err == nil {
		size = info.Size()
	}
	if err := m.parseLocalFileWithSize(path, size, report); err != nil {
		report.FileErrors[path] = err.Error()
	}
}

func (m *Manager) parseLocalFileWithSize(path string, size int64, report *CollectReport) error {
	content, err := util.ReadFileString(path)
	if err != nil {
		return err
	}
	fileID := util.FileID(path)
	fileName := filepath.Base(path)
	m.parseContentInto(content, fileID, fileName, path, "local", size, report)
	return nil
}

// ── Collect Online ─────────────────────────────────────────────────────────

// CollectOnline fetches each URL over HTTP and parses the response.
// Fetches run concurrently (bounded by opts.Concurrency) and honour ctx so a
// single hung URL cannot block the whole collection.
func (m *Manager) CollectOnline(ctx context.Context, urls []string, opts CollectOptions) (*CollectReport, error) {
	report := newReport()
	client := m.httpClient(opts)
	ua := m.userAgent(opts)
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 8
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	logger.L().Info("开始在线采集: %d 个 URL (并发 %d)", len(urls), concurrency)
	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()
			content, err := m.httpGetString(client, ctx, u, ua, opts.Token)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if ctx.Err() != nil {
					logger.L().Info("在线源采集被取消(超时): %s", u)
					return
				}
				report.FileErrors[u] = err.Error()
				logger.L().Warning("在线源下载失败 %s: %v", u, err)
				return
			}
			fileID := util.FileID(u)
			fileName := util.URLToFilename(u)
			m.parseContentInto(content, fileID, fileName, u, "online", int64(len(content)), report)
		}(u)
	}
	wg.Wait()
	logger.L().Info("在线采集完成: %d 个文件, %d 个频道", len(report.SourceFiles), len(report.Channels))
	return report, nil
}

// ── Collect GitHub ─────────────────────────────────────────────────────────

// CollectGitHub discovers and downloads .m3u/.m3u8/.txt files from GitHub
// repositories. Each repo entry is like "owner/name", "owner/name/branch", or
// "owner/name/branch/path". Fetches run concurrently and honour ctx.
func (m *Manager) CollectGitHub(ctx context.Context, repos []string, opts CollectOptions) (*CollectReport, error) {
	report := newReport()
	client := m.httpClient(opts)
	ua := m.userAgent(opts)
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 8
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	logger.L().Info("开始 GitHub 采集: %d 个仓库 (并发 %d)", len(repos), concurrency)
	for _, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		// per-repo download_method 覆盖下载通道：
		//   raw    -> 强制直连 raw.githubusercontent.com，忽略全局 github_mirror
		//   mirror -> 走全局 github_mirror（若未配置则回退为空=直连）
		//   缺省/其他 -> 沿用全局 opts（与现有行为一致）
		repoOpts := opts
		if dm := m.githubDownloadMethodOf(repo); dm != "" {
			switch dm {
			case "raw":
				repoOpts.Mirror = ""
			case "mirror":
				repoOpts.Mirror = opts.Mirror
			}
		}
		urls, err := m.discoverGitHubFiles(client, ctx, repo, repoOpts)
		if err != nil {
			mu.Lock()
			report.Errors = append(report.Errors, fmt.Sprintf("github %s: %v", repo, err))
			mu.Unlock()
			logger.L().Warning("GitHub 仓库发现失败 %s: %v", repo, err)
			continue
		}
		logger.L().Info("GitHub 仓库 %s 发现 %d 个源文件", repo, len(urls))
		for _, rawURL := range urls {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(rawURL string) {
				defer wg.Done()
				defer func() { <-sem }()
				content, derr := m.httpGetString(client, ctx, rawURL, ua, opts.Token)
				mu.Lock()
				defer mu.Unlock()
				if derr != nil {
					if ctx.Err() != nil {
						return
					}
					report.FileErrors[rawURL] = derr.Error()
					logger.L().Warning("GitHub 文件下载失败 %s: %v", rawURL, derr)
					return
				}
				fileID := util.FileID(rawURL)
				fileName := util.URLToFilename(rawURL)
				m.parseContentInto(content, fileID, fileName, rawURL, "github", int64(len(content)), report)
			}(rawURL)
		}
	}
	wg.Wait()
	logger.L().Info("GitHub 采集完成: %d 个文件, %d 个频道", len(report.SourceFiles), len(report.Channels))
	return report, nil
}

// discoverGitHubFiles resolves a repo entry into a list of downloadable raw URLs.
func (m *Manager) discoverGitHubFiles(client *http.Client, ctx context.Context, repo string, opts CollectOptions) ([]string, error) {
	parts := strings.Split(repo, "/")
	var clean []string
	for _, p := range parts {
		if p != "" {
			clean = append(clean, strings.TrimSpace(p))
		}
	}
	if len(clean) < 2 {
		return nil, fmt.Errorf("无效的 GitHub 仓库条目格式: %s", repo)
	}
	owner, name := clean[0], clean[1]

	// owner/name/branch/path -> direct raw URL
	if len(clean) >= 4 {
		branch := clean[2]
		filePath := strings.Join(clean[3:], "/")
		return []string{buildGitHubRawURL(owner, name, branch, filePath, opts.Mirror)}, nil
	}

	branch := ""
	if len(clean) >= 3 {
		branch = clean[2]
	}
	return m.githubAPIDiscover(client, ctx, owner, name, branch, opts)
}

// githubAPIDiscover uses the GitHub API to list source files in a repo tree.
func (m *Manager) githubAPIDiscover(client *http.Client, ctx context.Context, owner, name, branch string, opts CollectOptions) ([]string, error) {
	apiURL := opts.APIURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	apiURL = strings.TrimRight(apiURL, "/")
	headers := map[string]string{}
	if opts.Token != "" {
		headers["Authorization"] = "Bearer " + opts.Token
	}

	if branch == "" {
		repoURL := fmt.Sprintf("%s/repos/%s/%s", apiURL, owner, name)
		data, err := m.httpGetJSON(client, ctx, repoURL, headers)
		if err != nil {
			return nil, err
		}
		if b, ok := data["default_branch"].(string); ok && b != "" {
			branch = b
		} else {
			branch = "main"
		}
	}

	treeURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", apiURL, owner, name, branch)
	data, err := m.httpGetJSON(client, ctx, treeURL, headers)
	if err != nil {
		return nil, err
	}
	tree, _ := data["tree"].([]any)
	excluded := map[string]bool{"readme": true, "license": true, "changelog": true, "contributing": true}
	var urls []string
	for _, item := range tree {
		it, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if it["type"] != "blob" {
			continue
		}
		p, _ := it["path"].(string)
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		if !strings.HasSuffix(lower, ".m3u") && !strings.HasSuffix(lower, ".m3u8") && !strings.HasSuffix(lower, ".txt") {
			continue
		}
		base := lower
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if excluded[base] {
			continue
		}
		urls = append(urls, buildGitHubRawURL(owner, name, branch, p, opts.Mirror))
	}
	return urls, nil
}

func buildGitHubRawURL(owner, repo, branch, filePath, mirror string) string {
	raw := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, filePath)
	if mirror != "" {
		return strings.TrimRight(mirror, "/") + "/" + raw
	}
	return raw
}

// githubDownloadMethodOf 读取仓库的 per-repo 下载方式设置（raw/mirror）。
// 与 web 包 downloadMethodOf 读取同一份 Sources.github_source_settings，保证
// 列表展示、采集实际行为一致。返回空串表示未设置（沿用全局默认）。
func (m *Manager) githubDownloadMethodOf(repo string) string {
	if m.cfg == nil {
		return ""
	}
	raw := m.cfg.Get("Sources", "github_source_settings", "{}")
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil || settings == nil {
		return ""
	}
	if v, ok := settings[repo].(map[string]any); ok {
		if dm, ok := v["download_method"].(string); ok && dm != "" {
			return dm
		}
	}
	return ""
}

// ── Collect All ────────────────────────────────────────────────────────────

// CollectAll gathers local_dirs / online_urls / github_sources from config,
// runs all three collectors and merges the reports, de-duplicating channels by ID.
// It is not bounded by a context; callers wanting a deadline should use
// CollectAllContext.
func (m *Manager) CollectAll(opts CollectOptions) (*CollectReport, error) {
	return m.CollectAllContext(context.Background(), opts)
}

// CollectAllContext is CollectAll with context cancellation / deadline support.
// A cancelled ctx stops launching new network fetches (online/github) promptly.
func (m *Manager) CollectAllContext(ctx context.Context, opts CollectOptions) (*CollectReport, error) {
	if m.cfg == nil {
		return nil, fmt.Errorf("config 未初始化")
	}
	sources := m.cfg.GetSources()
	localDirs, _ := sources["local_dirs"].([]string)
	onlineURLs, _ := sources["online_urls"].([]string)
	githubSources, _ := sources["github_sources"].([]string)

	final := newReport()
	logger.L().Info("CollectAll: 本地 %d, 在线 %d, GitHub %d", len(localDirs), len(onlineURLs), len(githubSources))

	if ctx.Err() != nil {
		return final, ctx.Err()
	}
	local, lerr := m.CollectLocal(localDirs, opts)
	if lerr != nil {
		final.Errors = append(final.Errors, "local: "+lerr.Error())
	}
	online, oerr := m.CollectOnline(ctx, onlineURLs, opts)
	if oerr != nil {
		final.Errors = append(final.Errors, "online: "+oerr.Error())
	}
	github, gerr := m.CollectGitHub(ctx, githubSources, opts)
	if gerr != nil {
		final.Errors = append(final.Errors, "github: "+gerr.Error())
	}

	mergeReport(final, local)
	mergeReport(final, online)
	mergeReport(final, github)

	// Dedup channels by ID (keep first occurrence).
	seen := map[string]bool{}
	var deduped []types.Channel
	for _, ch := range final.Channels {
		if seen[ch.ID] {
			continue
		}
		seen[ch.ID] = true
		deduped = append(deduped, ch)
	}
	final.Channels = deduped

	logger.L().Info("CollectAll 完成: %d 个源文件, %d 个频道(去重后), %d 个排除项",
		len(final.SourceFiles), len(final.Channels), len(final.Exclusions))
	return final, nil
}

func mergeReport(dst, src *CollectReport) {
	if src == nil {
		return
	}
	dst.Channels = append(dst.Channels, src.Channels...)
	dst.SourceFiles = append(dst.SourceFiles, src.SourceFiles...)
	dst.Errors = append(dst.Errors, src.Errors...)
	for k, v := range src.FileErrors {
		dst.FileErrors[k] = v
	}
	for k, v := range src.Exclusions {
		dst.Exclusions[k] = v
	}
}

// ── Shared parsing entry ───────────────────────────────────────────────────

func (m *Manager) parseContentInto(content, fileID, fileName, sourcePath, fileType string, size int64, report *CollectReport) {
	var channels []types.Channel
	if strings.EqualFold(filepath.Ext(fileName), ".txt") {
		channels = ParseTXT(content, fileID, fileName)
	} else {
		channels = ParseM3U(content, fileID, fileName)
	}
	// Layer file-level + channel-level UA configs on top of the UA the parser
	// already extracted from each source (Python parity: file_ua + overrides).
	applyChannelUA(m.cfg, fileID, sourcePath, fileName, channels)
	sf := types.SourceFile{
		ID:           fileID,
		Name:         fileName,
		Path:         sourcePath,
		Type:         fileType,
		ChannelCount: len(channels),
		Size:         size,
		UpdatedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}
	report.SourceFiles = append(report.SourceFiles, sf)
	report.Channels = append(report.Channels, channels...)
}

// ── Low-level parsers (also used by manager/web) ───────────────────────────

// ParseM3U parses M3U/M3U8 playlist content. fileID and fileName are stamped
// onto every returned channel. Unsafe stream URLs are skipped (see package doc).
func ParseM3U(content, fileID, fileName string) []types.Channel {
	return parseM3U(content, fileID, fileName, nil)
}

// ParseTXT parses a plain-text source list (one URL per non-empty line).
func ParseTXT(content, fileID, fileName string) []types.Channel {
	return parseTXT(content, fileID, fileName, nil)
}

func parseM3U(content, fileID, fileName string, exclusions map[string]string) []types.Channel {
	var channels []types.Channel
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#EXTM3U") {
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			extinf := line
			i++ // consume the #EXTINF line

		// Skip #EXTVLCOPT: and other #EXT* directives until the URL line.
		// Capture per-source UA / Referer from #EXTVLCOPT:http-user-agent= /
		// #EXTVLCOPT:http-referrer= (Python parity).
		var extvlcUA, extvlcReferer string
		for i < len(lines) {
			peek := strings.TrimSpace(lines[i])
			if strings.HasPrefix(peek, "#EXTVLCOPT:") {
				opt := strings.TrimSpace(peek[len("#EXTVLCOPT:"):])
				if k, v, ok := splitKV(opt); ok {
					switch {
					case strings.EqualFold(k, "http-user-agent"):
						extvlcUA = v
					case strings.EqualFold(k, "http-referrer"), strings.EqualFold(k, "http-referer"):
						extvlcReferer = v
					}
				}
				i++
				continue
			}
				if strings.HasPrefix(peek, "#") && !strings.HasPrefix(peek, "#EXTINF:") {
					i++
					continue
				}
				break
			}
			if i >= len(lines) {
				break
			}
			urlLine := strings.TrimSpace(lines[i])
			if urlLine == "" || strings.HasPrefix(urlLine, "#") {
				continue
			}
		cleanURL, inlineUA, inlineReferer := splitCleanURLAndUA(urlLine)
		streamURL := cleanURL
			ok, reason, _ := security.IsStaticSafe(streamURL)
			if !ok {
				if exclusions != nil {
					exclusions[streamURL] = reason
				}
				logger.L().Info("解析跳过(窄门禁): %s - %s", streamURL, reason)
				continue
			}
			name := extractM3UName(extinf)
			// UA priority (Python parity): inline(|User-Agent=) > #EXTVLCOPT:http-user-agent
			// > EXTINF http-user-agent attribute. File-level / channel-level config is
			// layered on top later in applyChannelUA.
		ua := pickFirstNonEmpty(inlineUA, extvlcUA, extractAttr(extinf, "http-user-agent"))
		// Referer priority (Python parity, but actually consumed here):
		// inline(|Referer=) > #EXTVLCOPT:http-referrer > EXTINF http-referrer attr.
		referer := pickFirstNonEmpty(inlineReferer, extvlcReferer, extractAttr(extinf, "http-referrer"), extractAttr(extinf, "http-referer"))
		ch := types.Channel{
			ID:          util.ChannelID(name, streamURL),
			Name:        name,
			URL:         streamURL,
			URLOriginal: streamURL,
			Logo:        extractAttr(extinf, "tvg-logo"),
			Group:       extractAttr(extinf, "group-title"),
			FileID:      fileID,
			FileName:    fileName,
			UserAgent:   ua,
			Referrer:    referer,
			Categories:  nil,
			Status:      "",
		}
			channels = append(channels, ch)
			continue
		}

		// Plain URL line (no #EXTINF).
		if line != "" && !strings.HasPrefix(line, "#") {
			cleanURL, inlineUA, inlineReferer := splitCleanURLAndUA(line)
			streamURL := cleanURL
			ok, reason, _ := security.IsStaticSafe(streamURL)
			if !ok {
				if exclusions != nil {
					exclusions[streamURL] = reason
				}
				logger.L().Info("解析跳过(窄门禁): %s - %s", streamURL, reason)
				continue
			}
			name := deriveTXTName(streamURL)
			ch := types.Channel{
				ID:          util.ChannelID(name, streamURL),
				Name:        name,
				URL:         streamURL,
				URLOriginal: streamURL,
				FileID:      fileID,
				FileName:    fileName,
				UserAgent:   inlineUA,
				Referrer:    inlineReferer,
				Group:       "",
				Categories:  nil,
				Status:      "",
			}
			channels = append(channels, ch)
		}
	}
	return channels
}

func parseTXT(content, fileID, fileName string, exclusions map[string]string) []types.Channel {
	var channels []types.Channel
	for _, line := range util.SplitLines(content) {
		cleanURL, inlineUA, inlineReferer := splitCleanURLAndUA(line)
		streamURL := cleanURL
		ok, reason, _ := security.IsStaticSafe(streamURL)
		if !ok {
			if exclusions != nil {
				exclusions[streamURL] = reason
			}
			logger.L().Info("解析跳过(窄门禁): %s - %s", streamURL, reason)
			continue
		}
		name := deriveTXTName(streamURL)
		ch := types.Channel{
			ID:          util.ChannelID(name, streamURL),
			Name:        name,
			URL:         streamURL,
			URLOriginal: streamURL,
			FileID:      fileID,
			FileName:    fileName,
			UserAgent:   inlineUA,
			Referrer:    inlineReferer,
			Group:       "",
			Categories:  nil,
			Status:      "",
		}
		channels = append(channels, ch)
	}
	return channels
}

// ── Helpers ────────────────────────────────────────────────────────────────

// applyChannelUA resolves the final UA for every channel of one source file,
// matching the Python source_manager priority:
//
//	channel override (channel_ua_overrides)  >  embedded (inline/EXTVLCOPT/EXTINF)
//	                                       >  file-level (source_file_ua_settings / UserAgents section)
//
// ch.UserAgent already holds the embedded UA (set by the parser); this only
// upgrades it when a configured file-level or channel-level UA exists.
// applyChannelUA resolves the final UA and Referer for every channel of one
// source file, matching the Python source_manager priority:
//
//	channel override  >  embedded (inline/EXTVLCOPT/EXTINF)  >  file-level config
//
// ch.UserAgent / ch.Referrer already hold the embedded values (set by the
// parser); this only upgrades them when a configured file-level or
// channel-level value exists.
func applyChannelUA(cfg *config.Config, fileID, sourcePath, fileName string, channels []types.Channel) {
	if cfg == nil || len(channels) == 0 {
		return
	}
	// File-level UA from the Web UI setting (Sources.source_file_ua_settings[fileID]).
	var fileUA, fileUAPos string
	if settings := cfg.GetSourceFileUASettings(); len(settings) > 0 {
		if entry, ok := settings[fileID].(map[string]any); ok {
			if en, _ := entry["enabled"].(bool); en {
				if v, _ := entry["ua_value"].(string); v != "" {
					fileUA = v
				}
			}
			if v, _ := entry["ua_position"].(string); v != "" {
				fileUAPos = v
			}
		}
	}
	// File-level Referer (Sources.source_file_referer_settings[fileID]).
	var fileRef, fileRefPos string
	if settings := cfg.GetSourceFileRefererSettings(); len(settings) > 0 {
		if entry, ok := settings[fileID].(map[string]any); ok {
			if en, _ := entry["enabled"].(bool); en {
				if v, _ := entry["referer_value"].(string); v != "" {
					fileRef = v
				}
			}
			if v, _ := entry["referer_position"].(string); v != "" {
				fileRefPos = v
			}
		}
	}
	// Legacy fallback: UserAgents config section keyed by source path/name.
	if fileUA == "" {
		if uas := cfg.GetUserAgents(); len(uas) > 0 {
			for _, key := range []string{sourcePath, fileName} {
				if v, ok := uas[key]; ok && v != "" {
					fileUA = v
					break
				}
			}
		}
	}
	uaOverrides := cfg.GetChannelUAOverrides()
	refOverrides := cfg.GetChannelRefererOverrides()
	for i := range channels {
		ch := &channels[i]
		resolved := fileUA
		if ch.UserAgent != "" {
			resolved = ch.UserAgent // embedded UA beats file-level
		}
		key := ch.Name
		if key == "" {
			key = ch.URL
		}
		if ov, ok := uaOverrides[key].(map[string]any); ok {
			if v, _ := ov["ua_value"].(string); v != "" {
				resolved = v // channel override wins over everything
			}
			if v, _ := ov["ua_position"].(string); v != "" {
				ch.UAPosition = v
			}
		}
		if ch.UAPosition == "" {
			ch.UAPosition = fileUAPos
		}
		ch.UserAgent = resolved

		// Referer resolution (same precedence as UA).
		resolvedRef := fileRef
		if ch.Referrer != "" {
			resolvedRef = ch.Referrer
		}
		if ov, ok := refOverrides[key].(map[string]any); ok {
			if v, _ := ov["referer_value"].(string); v != "" {
				resolvedRef = v
			}
			if v, _ := ov["referrer_position"].(string); v != "" {
				ch.ReferrerPosition = v
			}
		}
		if ch.ReferrerPosition == "" {
			ch.ReferrerPosition = fileRefPos
		}
		ch.Referrer = resolvedRef
	}
}

// splitCleanURLAndUA splits a raw playlist URL line into the clean stream URL
// (everything before the first '|') and any inline "User-Agent=..." /
// "Referer=..." values carried after the '|' (Python parity: url_parts[1]).
// The '|' suffix must be stripped before the parse-stage safety gate, but the
// headers are preserved so they reach the realtime test and the m3u output.
func splitCleanURLAndUA(line string) (clean, inlineUA, inlineReferer string) {
	if idx := strings.Index(line, "|"); idx >= 0 {
		clean = line[:idx]
		rest := line[idx+1:]
		for _, part := range strings.Split(rest, "|") {
			low := strings.ToLower(part)
			if i := strings.Index(low, "user-agent="); i >= 0 {
				inlineUA = part[i+len("user-agent="):]
			}
			if i := strings.Index(low, "referer="); i >= 0 {
				inlineReferer = part[i+len("referer="):]
			}
		}
		return clean, inlineUA, inlineReferer
	}
	return line, "", ""
}

// splitKV parses a "key=value" option string (e.g. from #EXTVLCOPT:).
func splitKV(s string) (k, v string, ok bool) {
	if i := strings.Index(s, "="); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
	}
	return "", "", false
}

// pickFirstNonEmpty returns the first non-empty string (UA precedence helper).
func pickFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func extractM3UName(extinf string) string {
	if name := extractAttr(extinf, "tvg-name"); name != "" {
		return name
	}
	if idx := strings.LastIndex(extinf, ","); idx >= 0 {
		t := strings.TrimSpace(extinf[idx+1:])
		if t != "" {
			return t
		}
	}
	return "Unknown Channel"
}

var attrRe = regexp.MustCompile(`([\w-]+)="([^"]*)"`)

func extractAttr(line, key string) string {
	matches := attrRe.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		if strings.EqualFold(m[1], key) {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

// deriveTXTName derives a channel name from the URL: last path segment without
// extension, falling back to the host.
func deriveTXTName(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		host := u.Hostname()
		p := u.Path
		if idx := strings.LastIndex(p, "/"); idx >= 0 {
			p = p[idx+1:]
		}
		if idx := strings.LastIndex(p, "."); idx >= 0 {
			p = p[:idx]
		}
		if p != "" {
			return p
		}
		if host != "" {
			return host
		}
	}
	s := rawURL
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		s = rawURL
	}
	return s
}

// ── HTTP ───────────────────────────────────────────────────────────────────

func (m *Manager) httpClient(opts CollectOptions) *http.Client {
	timeout := opts.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	transport := &http.Transport{}
	if opts.ProxyEnabled && opts.ProxyHost != "" {
		scheme := "http"
		if strings.EqualFold(opts.ProxyType, "socks5") {
			scheme = "socks5"
		}
		proxyURL, err := url.Parse(fmt.Sprintf("%s://%s:%d", scheme, opts.ProxyHost, opts.ProxyPort))
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			logger.L().Warning("代理 URL 解析失败，使用直连: %v", err)
		}
	}
	return &http.Client{Timeout: time.Duration(timeout) * time.Second, Transport: transport}
}

func (m *Manager) userAgent(opts CollectOptions) string {
	if opts.UserAgent != "" {
		return opts.UserAgent
	}
	if m.cfg != nil {
		if uas := m.cfg.GetUserAgents(); len(uas) > 0 {
			for _, v := range uas {
				if v != "" {
					return v
				}
			}
		}
	}
	return "Mozilla/5.0 (compatible; LiveSourceManager/1.0)"
}

func (m *Manager) httpGetString(client *http.Client, ctx context.Context, u, ua, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (m *Manager) httpGetJSON(client *http.Client, ctx context.Context, u string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LiveSourceManager/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API HTTP %d: %s", resp.StatusCode, u)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("GitHub API 响应解析失败: %w", err)
	}
	return out, nil
}
