package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"live-source-manager-go/internal/manager"
	"live-source-manager-go/internal/types"
)

func boolFromAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "True" || t == "true" || t == "1" || t == "yes" || t == "on"
	}
	return false
}

func (s *Server) hTestTrigger(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	channels, report := s.collectChannels()

	// ── 测试范围：全部频道 / 按在线源文件 ──────────────────
	scope := strField(m, "scope")
	fileID := strField(m, "file_id")
	if scope == "online" || fileID != "" {
		typeOf := map[string]string{}
		if report != nil {
			for _, sf := range report.SourceFiles {
				typeOf[sf.ID] = sf.Type
			}
		}
		filtered := make([]types.Channel, 0, len(channels))
		for _, ch := range channels {
			if fileID != "" {
				if ch.FileID == fileID {
					filtered = append(filtered, ch)
				}
			} else if typeOf[ch.FileID] == "online" {
				filtered = append(filtered, ch)
			}
		}
		channels = filtered
	}

	params := s.mgr.DefaultTestParams()
	if v, ok := m["timeout"]; ok {
		params.Timeout = atoiDefault(v, params.Timeout)
	}
	if v, ok := m["concurrent"]; ok {
		params.Concurrent = atoiDefault(v, params.Concurrent)
	}
	if v, ok := m["max_attempts"]; ok {
		params.MaxAttempts = atoiDefault(v, params.MaxAttempts)
	}
	if v, ok := m["speed_test"]; ok {
		params.SpeedTest = boolFromAny(v)
	}
	// ── 测试前去重：相同地址只测一次（对齐 Python dedup_sources_by_url） ──
	totalBefore := len(channels)
	seenURL := make(map[string]bool, totalBefore)
	deduped := make([]types.Channel, 0, totalBefore)
	for _, ch := range channels {
		u := strings.TrimSpace(ch.URL)
		if u == "" || !seenURL[u] {
			if u != "" {
				seenURL[u] = true
			}
			deduped = append(deduped, ch)
		}
	}
	dedupRemoved := totalBefore - len(deduped)
	channels = deduped

	if len(channels) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "没有可测试的频道，请先采集源"})
		return
	}
	// ffprobe/ffmpeg 可用性守卫：缺失时明确报错，避免“瞬间 100% 实则未测”。
	if !s.mgr.HasTestBinaries() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "未找到 ffprobe/ffmpeg，无法执行真实探测。请将 ffmpeg 工具放入 tools/ffmpeg/ 目录，或在配置 Tools.ffmpeg_dir / 环境变量 LSM_FFMPEG_DIR 中指定其所在目录。",
		})
		return
	}
	meta := manager.RealtimeTestMeta{
		Report:           report,
		DedupRemoved:     dedupRemoved,
		TotalBeforeDedup: totalBefore,
	}
	sid, err := s.mgr.StartRealtimeTest(channels, params, meta)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "test_trigger", sid, "触发实时测试，去重后 "+strconv.Itoa(len(channels))+" 个频道（剔除重复 "+strconv.Itoa(dedupRemoved)+"）")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"session_id":         sid,
		"ffprobe_available":  s.mgr.HasTestBinaries(),
		"total":              len(channels),
		"total_before_dedup": totalBefore,
		"dedup_removed":      dedupRemoved,
	})
}

func (s *Server) hTestPause(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	sid := strField(m, "session_id")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "session_id 不能为空"})
		return
	}
	s.mgr.PauseRealtimeTest(sid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已暂停"})
}

func (s *Server) hTestResume(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	sid := strField(m, "session_id")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "session_id 不能为空"})
		return
	}
	s.mgr.ResumeRealtimeTest(sid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已恢复"})
}

func (s *Server) hTestCancel(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	sid := strField(m, "session_id")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "session_id 不能为空"})
		return
	}
	s.mgr.StopRealtimeTest(sid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已取消"})
}

func (s *Server) hTestStatus(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "session_id 不能为空"})
		return
	}
	prog, results, ok := s.mgr.GetRealtimeProgress(sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "测试会话不存在或已结束"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "progress": prog, "results": results})
}

func (s *Server) hTestStream(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "session_id 不能为空"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		prog, results, ok := s.mgr.GetRealtimeProgress(sid)
		if !ok {
			fmt.Fprintf(w, "data: {\"status\":\"not_found\"}\n\n")
			fl.Flush()
			return
		}
		payload := map[string]any{"progress": prog, "results": results}
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
		if prog.Status == "done" || prog.Status == "canceling" {
			return
		}
	}
}
