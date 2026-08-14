package main

// epg_scheduler 是 EPG 定时刷新调度（常驻后台 goroutine）。
//
// 对齐 Python 版 web/routes/epg.py 的 epg_scheduler：
//   - 每个启用的源可单独配置 refresh_mode / refresh_at / refresh_minutes（覆盖全局默认值）；
//   - 每分钟巡检一次，到点即触发增量刷新；
//   - 用状态文件 data/status/epg_fetch_state.json 持久化每源最近刷新时间，避免跨分钟/重启重复触发。
//
// 刷新本身走 epg.Manager.RefreshAll（并发抓取 → 清过期 → 频道对齐 → 生成 epg.xml.gz），
// 与手动「全量刷新」「单源刷新」共用同一管理器，running 标志天然互斥，不会并发打架。

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/logger"
)

// parseHHMM 解析 "HH:MM" 为时/分，越界返回 false。
func parseHHMM(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func atoiSafe(s string) (int, error) { return strconv.Atoi(s) }
func itoa(n int) string              { return strconv.Itoa(n) }

type epgScheduler struct {
	mgr     *epg.Manager
	conn    *sql.DB
	state   string // 状态文件目录
	mu      sync.Mutex
	lastRun map[int]time.Time // 内存去重：每源上次刷新时间
	stop    chan struct{}
}

func newEPGScheduler(mgr *epg.Manager, conn *sql.DB, stateDir string) *epgScheduler {
	return &epgScheduler{
		mgr:     mgr,
		conn:    conn,
		state:   stateDir,
		lastRun: map[int]time.Time{},
		stop:    make(chan struct{}),
	}
}

// run 是调度主循环，阻塞直到 ctx 取消。
func (s *epgScheduler) run(ctx context.Context) {
	// 启动后稍候，待频道采集预热、配置生效。
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *epgScheduler) tick(ctx context.Context) {
	if !s.mgr.Enabled() {
		return
	}
	globalMode := strings.ToLower(strings.TrimSpace(s.mgr.RefreshModeDefault()))
	globalAt := s.mgr.RefreshAtDefault()
	globalMinutes := s.mgr.RefreshMinutesDefault()

	now := time.Now()
	st := s.loadState()
	perSource := st.PerSourceLast

	due := []int{}
	sources, err := db.ListEPGSources(s.conn, true)
	if err != nil {
		logger.L().Warning("[EPG-SCHED] 读取启用源失败: %v", err)
		return
	}
	for _, src := range sources {
		mode := strings.ToLower(strings.TrimSpace(src.RefreshMode))
		if mode == "" {
			mode = globalMode
		}
		lastRaw := perSource[src.ID]
		var lastDt *time.Time
		if !lastRaw.IsZero() {
			lastDt = &lastRaw
		}
		if mode == "interval" {
			minutes := src.RefreshMinutes
			if minutes <= 0 {
				minutes = globalMinutes
			}
			if minutes < 5 {
				minutes = 5
			}
			if lastDt == nil || now.Sub(*lastDt) >= time.Duration(minutes)*time.Minute {
				due = append(due, src.ID)
			}
		} else { // daily
			h, m := 3, 30
			if at := strings.TrimSpace(src.RefreshAt); at != "" {
				if ph, pm, ok := parseHHMM(at); ok {
					h, m = ph, pm
				}
			} else if gh, gm, ok := parseHHMM(globalAt); ok {
				h, m = gh, gm
			}
			if now.Hour() == h && now.Minute() == m &&
				(lastDt == nil || lastDt.Day() != now.Day() || lastDt.Year() != now.Year() || lastDt.Month() != now.Month()) {
				due = append(due, src.ID)
			}
		}
	}
	if len(due) == 0 {
		return
	}
	logger.L().Info("[EPG-SCHED] 到点触发刷新 %d 个源", len(due))
	if _, err := s.mgr.RefreshAll(ctx, due); err != nil {
		if strings.Contains(err.Error(), "进行中") {
			logger.L().Info("[EPG-SCHED] 刷新已被其他任务占用，跳过本次触发")
			return
		}
		logger.L().Warning("[EPG-SCHED] 刷新失败: %v", err)
		return
	}
	// 刷新成功：更新去重时间戳（内存 + 状态文件）。
	s.mu.Lock()
	for _, id := range due {
		s.lastRun[id] = now
		st.PerSourceLast[id] = now
	}
	s.mu.Unlock()
	s.saveState(st)
}

// ── 状态文件（与 Python epg_fetch_state.json 兼容结构） ──────────────────

type epgState struct {
	PerSourceLast map[int]time.Time `json:"per_source_last"`
	LastRefresh   time.Time         `json:"last_refresh"`
}

func (s *epgScheduler) statePath() string {
	return filepath.Join(s.state, "epg_fetch_state.json")
}

func (s *epgScheduler) loadState() *epgState {
	st := &epgState{PerSourceLast: map[int]time.Time{}}
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		return st
	}
	var raw struct {
		PerSourceLast map[string]time.Time `json:"per_source_last"`
		LastRefresh   time.Time            `json:"last_refresh"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return st
	}
	for k, v := range raw.PerSourceLast {
		if n, err := atoiSafe(k); err == nil {
			st.PerSourceLast[n] = v
			s.lastRun[n] = v
		}
	}
	st.LastRefresh = raw.LastRefresh
	return st
}

func (s *epgScheduler) saveState(st *epgState) {
	_ = os.MkdirAll(s.state, 0o755)
	raw := struct {
		PerSourceLast map[string]time.Time `json:"per_source_last"`
		LastRefresh   time.Time            `json:"last_refresh"`
	}{
		PerSourceLast: map[string]time.Time{},
		LastRefresh:   time.Now(),
	}
	for k, v := range st.PerSourceLast {
		raw.PerSourceLast[itoa(k)] = v
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.statePath(), b, 0o644)
}
