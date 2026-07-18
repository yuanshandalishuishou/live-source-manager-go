package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/source"
	"live-source-manager-go/internal/types"
	"live-source-manager-go/internal/util"
)

func (s *Server) collectChannels() ([]types.Channel, *source.CollectReport) {
	channels, report, err := s.mgr.GetChannels(context.Background())
	if err != nil {
		loggerWarn("采集频道失败: %v", err)
		return nil, nil
	}
	return channels, report
}

// collectChannelsRefresh forces a fresh collection (bypasses the TTL cache).
func (s *Server) collectChannelsRefresh() ([]types.Channel, *source.CollectReport) {
	channels, report, err := s.mgr.GetChannelsRefresh(context.Background())
	if err != nil {
		loggerWarn("采集频道失败: %v", err)
		return nil, nil
	}
	return channels, report
}

func (s *Server) hListSources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fileID := q.Get("file_id")
	search := strings.ToLower(q.Get("q"))
	status := q.Get("status")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	channels, _ := s.collectChannels()
	total := len(channels)
	var filtered []types.Channel
	for _, ch := range channels {
		if fileID != "" && ch.FileID != fileID {
			continue
		}
		if status != "" && ch.Status != status {
			continue
		}
		if search != "" && !(strings.Contains(strings.ToLower(ch.Name), search) ||
			strings.Contains(strings.ToLower(ch.URL), search) ||
			strings.Contains(strings.ToLower(ch.Group), search)) {
			continue
		}
		filtered = append(filtered, ch)
	}
	total = len(filtered)
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[offset:end]

	groupByFile := q.Get("group") == "1" || q.Get("group_by_file") == "1"
	if groupByFile {
		files := map[string]*fileGroup{}
		for _, ch := range filtered {
			g, ok := files[ch.FileID]
			if !ok {
				g = &fileGroup{FileID: ch.FileID, FileName: ch.FileName}
				files[ch.FileID] = g
			}
			g.Channels = append(g.Channels, ch)
		}
		var groups []*fileGroup
		for _, g := range files {
			groups = append(groups, g)
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].FileName < groups[j].FileName })
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": groups, "total": total})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channels": page, "total": total, "limit": limit, "offset": offset})
}

type fileGroup struct {
	FileID   string          `json:"file_id"`
	FileName string          `json:"file_name"`
	Channels []types.Channel `json:"channels"`
}

func (s *Server) findChannel(id string) (*types.Channel, []types.Channel) {
	channels, _ := s.collectChannels()
	for i := range channels {
		if channels[i].ID == id {
			return &channels[i], channels
		}
	}
	return nil, channels
}

func (s *Server) hGetSource(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "source_id")
	ch, _ := s.findChannel(id)
	if ch == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "源不存在"})
		return
	}
	cats, _ := db.GetSourceCategories(s.conn, id)
	out := map[string]any{"ok": true, "source": ch, "categories": cats}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) hUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "source_id")
	m := decodeBody(r)
	if catsRaw, ok := m["categories"].(map[string]any); ok {
		cats := map[string]string{}
		for k, v := range catsRaw {
			if s, ok := v.(string); ok {
				cats[k] = s
			}
		}
		if err := db.SaveSourceCategories(s.conn, id, cats); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "保存分类失败"})
			return
		}
		s.audit(r, "source_update", id, "更新频道分类维度")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "分类已更新"})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无需更新的字段"})
}

func (s *Server) hDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "source_id")
	ch, _ := s.findChannel(id)
	if err := db.DeleteSourceCategories(s.conn, id); err != nil {
		loggerWarn("删除频道分类失败: %v", err)
	}
	if ch != nil && ch.URL != "" {
		s.removeOnlineURL(ch.URL)
	}
	s.audit(r, "source_delete", id, "删除频道记录")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已删除"})
}

func (s *Server) removeOnlineURL(url string) {
	urls := s.cfg.Get("Sources", "online_urls", "")
	if urls == "" {
		return
	}
	var kept []string
	for _, u := range strings.Split(urls, "\n") {
		u = strings.TrimSpace(u)
		if u == "" || u == url {
			continue
		}
		kept = append(kept, u)
	}
	s.cfg.Set("Sources", "online_urls", strings.Join(kept, "\n"))
}

func (s *Server) hCreateSource(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	url := strField(m, "url")
	name := strField(m, "name")
	if url == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "URL 不能为空"})
		return
	}
	if name == "" {
		name = url
	}
	existing := s.cfg.Get("Sources", "online_urls", "")
	for _, u := range strings.Split(existing, "\n") {
		if strings.TrimSpace(u) == url {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "exists", "name": name, "url": url, "message": "该 URL 已存在"})
			return
		}
	}
	kept := []string{}
	for _, u := range strings.Split(existing, "\n") {
		if t := strings.TrimSpace(u); t != "" {
			kept = append(kept, t)
		}
	}
	kept = append(kept, url)
	s.cfg.Set("Sources", "online_urls", strings.Join(kept, "\n"))
	s.audit(r, "source_add", name, "添加在线源："+url)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "created", "name": name, "url": url, "message": "源已添加"})
}

func (s *Server) hCollectSources(w http.ResponseWriter, r *http.Request) {
	channels, report := s.collectChannelsRefresh()
	out := map[string]any{
		"ok":       true,
		"channels": channels,
		"total":    len(channels),
	}
	if report != nil {
		out["files"] = report.SourceFiles
		out["errors"] = report.Errors
		out["exclusions"] = len(report.Exclusions)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) hGetSourceCategories(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "source_id")
	cats, err := db.GetSourceCategories(s.conn, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "categories": cats})
}

func (s *Server) hUpdateSourceCategory(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "source_id")
	dim := routeParam(r, "dim_key")
	m := decodeBody(r)
	val := strField(m, "value")
	if val == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "value 不能为空"})
		return
	}
	if err := db.UpdateSourceCategory(s.conn, id, dim, val); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "source_category_update", id, dim+"="+val)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已更新"})
}

// ── source files ──────────────────────────────────────────────────────────

// peekChannels returns the cached channels/report WITHOUT triggering a blocking
// real-time collection. Mirrors the dashboard warm/cold pattern so the sources
// page never blocks on a ~20s cold cache (the root cause of the slow login).
func (s *Server) peekChannels() ([]types.Channel, *source.CollectReport, bool) {
	return s.mgr.PeekChannels()
}

func fileStatusLabel(f map[string]any) (string, string) {
	cc, _ := f["channel_count"].(int)
	t, _ := f["type"].(string)
	switch t {
	case "online", "github":
		if cc > 0 {
			return "已采集", "ok"
		}
		return "未采集", "warn"
	default: // local
		if cc > 0 {
			return "存在", "ok"
		}
		return "空", "warn"
	}
}

func (s *Server) githubSettingsKeyForURL(fileURL string) string {
	if fileURL == "" {
		return ""
	}
	raw := s.cfg.Get("Sources", "github_source_settings", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return ""
	}
	for key := range m {
		if key != "" && strings.Contains(fileURL, key) {
			return key
		}
	}
	return ""
}

func (s *Server) downloadMethodOf(fileURL, t string) string {
	if t != "github" || fileURL == "" {
		return ""
	}
	key := s.githubSettingsKeyForURL(fileURL)
	if key == "" {
		return "raw"
	}
	raw := s.cfg.Get("Sources", "github_source_settings", "{}")
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		if v, ok := m[key].(map[string]any); ok {
			if dm, ok := v["download_method"].(string); ok && dm != "" {
				return dm
			}
		}
	}
	return "raw"
}

func (s *Server) repoForFileID(id string) string {
	gh := s.cfg.Get("Sources", "github_sources", "")
	for _, repo := range linesOf(gh) {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		parts := strings.Split(repo, "/")
		if len(parts) < 2 {
			continue
		}
		owner, name := parts[0], parts[1]
		branch := "main"
		if len(parts) >= 3 {
			branch = parts[2]
		}
		mirror := s.cfg.Get("Network", "github_mirror", "")
		raw := "https://raw.githubusercontent.com/" + owner + "/" + name + "/" + branch
		if mirror != "" {
			raw = strings.TrimRight(mirror, "/") + "/" + raw
		}
		if util.FileID(raw) == id {
			return repo
		}
	}
	return ""
}

func (s *Server) fileURLForID(id string) string {
	_, report, ok := s.peekChannels()
	if !ok {
		return ""
	}
	if report != nil {
		for _, sf := range report.SourceFiles {
			if sf.ID == id {
				return sf.Path
			}
		}
	}
	return ""
}

func (s *Server) setGithubDownloadMethod(repo, dm string) {
	raw := s.cfg.Get("Sources", "github_source_settings", "{}")
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil || settings == nil {
		settings = map[string]any{}
	}
	entry := map[string]any{}
	if cur, ok := settings[repo].(map[string]any); ok {
		entry = cur
	}
	entry["download_method"] = dm
	settings[repo] = entry
	if b, err := json.Marshal(settings); err == nil {
		s.cfg.Set("Sources", "github_source_settings", string(b))
	}
}

func containsLine(s, line string) bool {
	line = strings.TrimSpace(line)
	for _, l := range linesOf(s) {
		if l == line {
			return true
		}
	}
	return false
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range linesOf(s) {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func linesOf(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, ",") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func containsItem(list []string, item string) bool {
	for _, l := range list {
		if l == item {
			return true
		}
	}
	return false
}

func (s *Server) hListSourceFiles(w http.ResponseWriter, r *http.Request) {
	_, report, ok := s.peekChannels()
	// 始终基于配置即时构建源文件列表（与 Python 一致——首屏即列出全部已配置源，
	// 不再整块返回空导致页面空白/轮询阻塞）。缓存预热后用采集报告回填频道数。
	files := s.listSourceFilesImmediate()
	warming := false
	if ok && report != nil {
		byID := make(map[string]types.SourceFile, len(report.SourceFiles))
		for _, sf := range report.SourceFiles {
			byID[sf.ID] = sf
		}
		for _, f := range files {
			if id, _ := f["id"].(string); id != "" {
				if sf, found := byID[id]; found {
					f["channel_count"] = sf.ChannelCount
					f["size"] = sf.Size
					f["file_status"], f["file_status_class"] = fileStatusLabel(f)
				}
			}
		}
	} else {
		// 冷缓存：后台预热，下次请求即可回填频道数。本次仍即时返回配置列表。
		warming = true
		s.mgr.WarmChannels()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files, "total": len(files), "warming": warming})
}

// fileEntry 构造一个源文件列表项（冷缓存即时列表与预热后列表共用同一形状）。
func (s *Server) fileEntry(id, name, path, typ string, channelCount int, size int64, downloadMethod string, uaSettings map[string]any) map[string]any {
	entry := map[string]any{
		"id":              id,
		"name":            name,
		"path":            path,
		"url_or_path":     path,
		"type":            typ,
		"channel_count":   channelCount,
		"size":            size,
		"updated_at":      "",
		"download_method": downloadMethod,
		"ua_settings":     uaSettings[id],
	}
	entry["file_status"], entry["file_status_class"] = fileStatusLabel(entry)
	return entry
}

// listSourceFilesImmediate 在缓存未预热时，从配置即时构建源文件列表，
// 其 ID 推导与真实采集完全一致（online: FileID(url)；local: FileID(文件路径)；
// github: 以仓库 base raw URL 推导占位 ID），保证页面初始值与 Python 一致。
func (s *Server) listSourceFilesImmediate() []map[string]any {
	src := s.cfg.GetSources()
	uaSettings := s.cfg.GetSourceFileUASettings()
	localDirs, _ := src["local_dirs"].([]string)
	onlineURLs, _ := src["online_urls"].([]string)
	githubRepos, _ := src["github_sources"].([]string)
	files := []map[string]any{}

	for _, u := range onlineURLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		files = append(files, s.fileEntry(util.FileID(u), util.URLToFilename(u), u, "online", 0, 0, "", uaSettings))
	}

	for _, dir := range localDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fn := strings.ToLower(e.Name())
			if !strings.HasSuffix(fn, ".m3u") && !strings.HasSuffix(fn, ".m3u8") && !strings.HasSuffix(fn, ".txt") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			files = append(files, s.fileEntry(util.FileID(p), e.Name(), p, "local", 0, 0, "", uaSettings))
		}
	}

	for _, repo := range githubRepos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		base := s.githubBaseRawURL(repo)
		files = append(files, s.fileEntry(util.FileID(base), repo, repo, "github", 0, 0, s.downloadMethodOf(base, "github"), uaSettings))
	}

	return files
}

// githubBaseRawURL 返回仓库的 base raw URL（与 repoForFileID 推导一致），
// 用于冷缓存阶段给 GitHub 仓库生成确定性的占位 ID / 下载方式。
func (s *Server) githubBaseRawURL(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[0], parts[1]
	branch := "main"
	if len(parts) >= 3 {
		branch = parts[2]
	}
	mirror := s.cfg.Get("Network", "github_mirror", "")
	raw := "https://raw.githubusercontent.com/" + owner + "/" + name + "/" + branch
	if mirror != "" {
		raw = strings.TrimRight(mirror, "/") + "/" + raw
	}
	return raw
}

func (s *Server) hGetSourceFile(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	_, report := s.collectChannels()
	if report != nil {
		for _, sf := range report.SourceFiles {
			if sf.ID == id {
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": sf})
				return
			}
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "源文件不存在"})
}

func (s *Server) hCreateSourceFile(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	typ := strField(m, "type")
	value := strings.TrimSpace(strField(m, "value"))
	if value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "地址不能为空"})
		return
	}
	switch typ {
	case "github":
		existing := s.cfg.Get("Sources", "github_sources", "")
		if containsLine(existing, value) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "exists", "message": "该 GitHub 源已存在"})
			return
		}
		kept := nonEmptyLines(existing)
		kept = append(kept, value)
		s.cfg.Set("Sources", "github_sources", strings.Join(kept, "\n"))
		if dm := strField(m, "download_method"); dm != "" {
			s.setGithubDownloadMethod(value, dm)
		}
		s.audit(r, "source_file_add", value, "添加 GitHub 源")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "created", "message": "GitHub 源已添加，请点击\"采集所有源\""})
	case "local":
		existing := s.cfg.Get("Sources", "local_dirs", "")
		if containsItem(splitComma(existing), value) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "exists", "message": "该本地路径已存在"})
			return
		}
		dirs := splitComma(existing)
		dirs = append(dirs, value)
		s.cfg.Set("Sources", "local_dirs", strings.Join(dirs, ","))
		s.audit(r, "source_file_add", value, "添加本地源")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "created", "message": "本地源已添加"})
	default: // online
		existing := s.cfg.Get("Sources", "online_urls", "")
		if containsLine(existing, value) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "exists", "message": "该 URL 已存在"})
			return
		}
		kept := nonEmptyLines(existing)
		kept = append(kept, value)
		s.cfg.Set("Sources", "online_urls", strings.Join(kept, "\n"))
		s.audit(r, "source_file_add", value, "添加在线源")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "created", "message": "在线源已添加"})
	}
}

func (s *Server) hUpdateSourceFile(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	m := decodeBody(r)
	if dm := strField(m, "download_method"); dm != "" {
		fileURL := s.fileURLForID(id)
		key := s.githubSettingsKeyForURL(fileURL)
		if key == "" {
			key = s.repoForFileID(id)
		}
		if key == "" {
			key = id
		}
		s.setGithubDownloadMethod(key, dm)
		s.audit(r, "source_file_update", id, "更新下载方式: "+dm)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "下载方式已更新"})
		return
	}
	s.setUAConfig(id, m)
	s.audit(r, "source_file_update", id, "更新源文件 UA 设置")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已更新"})
}

// hDeleteSourceFile 删除一个源文件条目（online URL / github 仓库 / local 磁盘文件），
// 并清理其配套的 UA / Referer 源文件级设置与频道级覆盖。
// 注意：之前该接口只清空了 UA 设置却未移除源本身，导致列表里源删不掉——现已修正。
func (s *Server) hDeleteSourceFile(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "file_id 不能为空"})
		return
	}

	removed := s.deleteSourceFileByID(id, r)

	// 配套清理（无论源是否在配置中匹配到，都清掉这些设置，避免脏数据残留）
	s.delUAConfig(id)
	s.delRefererConfig(id)
	s.delChannelOverridesForFileID(id)

	if !removed {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": false, "message": "未在源配置中找到该条目，已清理相关设置"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": true, "message": "源文件已删除"})
}

// deleteSourceFileByID 从配置中移除匹配 file_id 的源文件条目。
//   - online: 从 Sources.online_urls 移除对应行（在线源不落盘，无需删文件）
//   - github: 从 Sources.github_sources 移除对应仓库，并清其下载方式设置
//   - local:  删除磁盘上对应的 .m3u/.m3u8/.txt 文件
//
// 返回是否实际从配置/磁盘移除了源本身。
func (s *Server) deleteSourceFileByID(id string, r *http.Request) bool {
	// ── online ──
	if urls := s.cfg.Get("Sources", "online_urls", ""); strings.TrimSpace(urls) != "" {
		anyRemoved := false
		kept := []string{}
		for _, u := range linesOf(urls) {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if util.FileID(u) == id {
				anyRemoved = true
				s.audit(r, "source_file_delete", u, "删除在线源")
				continue
			}
			kept = append(kept, u)
		}
		if anyRemoved {
			s.cfg.Set("Sources", "online_urls", strings.Join(kept, "\n"))
			return true
		}
	}

	// ── github ──
	if repos := s.cfg.Get("Sources", "github_sources", ""); strings.TrimSpace(repos) != "" {
		anyRemoved := false
		kept := []string{}
		for _, repo := range linesOf(repos) {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				continue
			}
			base := s.githubBaseRawURL(repo)
			if base != "" && util.FileID(base) == id {
				anyRemoved = true
				s.clearGithubDownloadMethod(repo)
				s.audit(r, "source_file_delete", repo, "删除 GitHub 源")
				continue
			}
			kept = append(kept, repo)
		}
		if anyRemoved {
			s.cfg.Set("Sources", "github_sources", strings.Join(kept, "\n"))
			return true
		}
	}

	// ── local ── 删除目录下匹配的具体文件
	dirs := splitComma(s.cfg.Get("Sources", "local_dirs", ""))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fn := strings.ToLower(e.Name())
			if !strings.HasSuffix(fn, ".m3u") && !strings.HasSuffix(fn, ".m3u8") && !strings.HasSuffix(fn, ".txt") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if util.FileID(p) == id {
				if err := os.Remove(p); err == nil {
					s.audit(r, "source_file_delete", p, "删除本地源文件")
					return true
				}
			}
		}
	}

	return false
}

// clearGithubDownloadMethod 清除仓库的 GitHub 下载方式设置。
func (s *Server) clearGithubDownloadMethod(repo string) {
	raw := s.cfg.Get("Sources", "github_source_settings", "{}")
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil || settings == nil {
		return
	}
	if _, ok := settings[repo]; !ok {
		return
	}
	delete(settings, repo)
	if b, err := json.Marshal(settings); err == nil {
		s.cfg.Set("Sources", "github_source_settings", string(b))
	}
}

// delRefererConfig 删除源文件级 Referer 设置（与 delUAConfig 对称）。
func (s *Server) delRefererConfig(id string) {
	settings := s.cfg.GetSourceFileRefererSettings()
	delete(settings, id)
	raw, _ := json.Marshal(settings)
	s.cfg.Set("Sources", "source_file_referer_settings", string(raw))
}

// delChannelOverridesForFileID 删除该源文件下所有频道的 UA / Referer 频道级覆盖。
func (s *Server) delChannelOverridesForFileID(id string) {
	chans, _, ok := s.peekChannels()
	if !ok {
		return
	}
	urls := map[string]bool{}
	names := map[string]bool{}
	for _, ch := range chans {
		if ch.FileID == id {
			urls[ch.URL] = true
			names[ch.Name] = true
		}
	}
	if len(urls) == 0 && len(names) == 0 {
		return
	}
	for _, key := range []string{"channel_ua_overrides", "channel_referer_overrides"} {
		raw := s.cfg.Get("Sources", key, "{}")
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
			continue
		}
		changed := false
		for k := range m {
			if urls[k] || names[k] {
				delete(m, k)
				changed = true
			}
		}
		if changed {
			if b, err := json.Marshal(m); err == nil {
				s.cfg.Set("Sources", key, string(b))
			}
		}
	}
}

func (s *Server) hGetSourceFileChannels(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	channels, _, ok := s.peekChannels()
	if !ok {
		s.mgr.WarmChannels()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channels": []any{}, "total": 0, "warming": true})
		return
	}
	q := r.URL.Query()
	search := strings.ToLower(q.Get("search"))
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 100
	}

	var filtered []types.Channel
	for _, ch := range channels {
		if ch.FileID != id {
			continue
		}
		if search != "" && !(strings.Contains(strings.ToLower(ch.Name), search) ||
			strings.Contains(strings.ToLower(ch.URL), search) ||
			strings.Contains(strings.ToLower(ch.Group), search)) {
			continue
		}
		filtered = append(filtered, ch)
	}
	total := len(filtered)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	pageCh := filtered[start:end]

	overrides := s.cfg.GetChannelUAOverrides()
	out := make([]map[string]any, 0, len(pageCh))
	for _, ch := range pageCh {
		m := map[string]any{
			"id":          ch.ID,
			"name":        ch.Name,
			"url":         ch.URL,
			"group":       ch.Group,
			"user_agent":  ch.UserAgent,
			"ua_position": ch.UAPosition,
			"category":    ch.Categories["content"],
			"status":      ch.Status,
		}
		if mp, err := db.GetChannelMapping(s.conn, ch.Name); err == nil && mp != nil {
			m["existing_mapping"] = map[string]any{
				"content":    mp.Content,
				"region":     mp.Region,
				"language":   mp.Language,
				"quality":    mp.Quality,
				"media_type": mp.MediaType,
				"genre":      mp.Genre,
				"is_manual":  1,
			}
		}
		key := ch.Name
		if key == "" {
			key = ch.URL
		}
		if ov, ok := overrides[key].(map[string]any); ok {
			m["ua_override"] = true
			if v, ok := ov["ua_value"].(string); ok && v != "" {
				m["user_agent"] = v
			}
			if v, ok := ov["ua_position"].(string); ok && v != "" {
				m["ua_position"] = v
			}
		} else {
			m["ua_override"] = false
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "channels": out, "total": total,
		"unfiltered_total": total, "page": page,
	})
}

func (s *Server) hSetSourceFileUA(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	m := decodeBody(r)
	s.setUAConfig(id, m)
	s.audit(r, "source_file_ua", id, "设置源文件 UA")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "UA 已设置"})
}

func (s *Server) hDeleteSourceFileUA(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	s.delUAConfig(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "UA 已删除"})
}

func (s *Server) hSetChannelUA(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	m := decodeBody(r)
	key := strField(m, "channel_name")
	if key == "" {
		key = strField(m, "url")
	}
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "channel_name 或 url 不能为空"})
		return
	}
	overrides := s.cfg.GetChannelUAOverrides()
	overrides[key] = map[string]any{
		"ua_value":    strField(m, "ua_value"),
		"ua_position": strField(m, "ua_position"),
	}
	raw, _ := json.Marshal(overrides)
	s.cfg.Set("Sources", "channel_ua_overrides", string(raw))
	s.audit(r, "channel_ua", id, "设置频道 UA："+key)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "频道 UA 已设置"})
}

func (s *Server) hDeleteChannelUA(w http.ResponseWriter, r *http.Request) {
	id := routeParam(r, "file_id")
	m := decodeBody(r)
	key := strField(m, "channel_name")
	if key == "" {
		key = strField(m, "url")
	}
	overrides := s.cfg.GetChannelUAOverrides()
	delete(overrides, key)
	raw, _ := json.Marshal(overrides)
	s.cfg.Set("Sources", "channel_ua_overrides", string(raw))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "频道 UA 已删除", "file_id": id})
}

func (s *Server) setUAConfig(id string, m map[string]any) {
	settings := s.cfg.GetSourceFileUASettings()
	entry := map[string]any{}
	if cur, ok := settings[id].(map[string]any); ok {
		entry = cur
	}
	enabled := strField(m, "enabled")
	uaValue := strField(m, "ua_value")
	uaPosition := strField(m, "ua_position")
	switch enabled {
	case "true", "1", "on":
		entry["enabled"] = true
	case "false", "0", "off":
		entry["enabled"] = false
	}
	if uaValue != "" {
		entry["ua_value"] = uaValue
	}
	if uaPosition != "" {
		entry["ua_position"] = uaPosition
	}
	settings[id] = entry
	raw, _ := json.Marshal(settings)
	s.cfg.Set("Sources", "source_file_ua_settings", string(raw))
}

func (s *Server) delUAConfig(id string) {
	settings := s.cfg.GetSourceFileUASettings()
	delete(settings, id)
	raw, _ := json.Marshal(settings)
	s.cfg.Set("Sources", "source_file_ua_settings", string(raw))
}

// ensure util import is referenced (ChannelID used elsewhere).
var _ = util.ChannelID
