package tester

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/geo"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/progress"
)

// Tester 负责源的可达性与媒体信息测试
// 特点：
//   - 批量缓冲写入，减少数据库压力
//   - 通过 context 支持优雅取消
//   - 使用 ffprobe 参数数组防止命令注入
//   - 归属地识别集成
type Tester struct {
	mu            sync.Mutex
	cfg           *config.Config
	db            *db.DB
	geoResolver   *geo.Resolver
	progMgr       *progress.Manager
	httpClient    *http.Client

	batchBuffer   []*TestResult
	batchMu       sync.Mutex
	batchSize     int
	flushInterval time.Duration
}

// TestResult 封装单个源的测试结果
type TestResult struct {
	SourceID       int
	URL            string
	Success        bool
	ResponseTimeMs int
	StatusCode     int
	ErrorMsg       string
	Resolution     string
	Bitrate        int
	Location       string
	ISP            string
}

// NewTester 创建测试器实例
func NewTester(cfg *config.Config, database *db.DB, resolver *geo.Resolver, progMgr *progress.Manager) *Tester {
	bs := cfg.Testing.BatchSize
	if bs <= 0 {
		bs = 50
	}
	fi := cfg.Testing.FlushInterval
	if fi <= 0 {
		fi = 2
	}
	t := &Tester{
		cfg:           cfg,
		db:            database,
		geoResolver:   resolver,
		progMgr:       progMgr,
		httpClient: &http.Client{
			Timeout:   time.Duration(cfg.Testing.Timeout) * time.Second,
			Transport: &http.Transport{DisableKeepAlives: true}, // 避免连接复用导致误判
		},
		batchSize:     bs,
		flushInterval: time.Duration(fi) * time.Second,
		batchBuffer:   make([]*TestResult, 0, bs),
	}
	return t
}

// Start 执行一次完整的测试任务（从待测源列表读取，并发测试，批量写入）
// 返回错误仅表示无法启动，任务内部错误通过日志记录
func (t *Tester) Start(ctx context.Context) error {
	// ---------- 1. 互斥检查 ----------
	t.mu.Lock()
	// 这里可以检查全局标志，如果已有任务在运行则返回错误（略）
	t.mu.Unlock()

	// ---------- 2. 获取待测源 ----------
	sources, err := t.fetchSourcesToTest(ctx)
	if err != nil {
		return fmt.Errorf("获取待测源失败: %w", err)
	}
	if len(sources) == 0 {
		logger.Info("没有需要测试的源")
		return nil
	}

	// ---------- 3. 创建进度记录 ----------
	taskID := progress.RandomTaskID()
	t.progMgr.CreateTask(taskID, len(sources))

	// ---------- 4. 启动批量写入协程 ----------
	resultCh := make(chan *TestResult, t.batchSize)
	var writerWg sync.WaitGroup
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	writerWg.Add(1)
	go t.batchWriter(writerCtx, resultCh, taskID, &writerWg)

	// ---------- 5. 并发测试 ----------
	concurrency := t.cfg.Testing.ConcurrentThreads
	if concurrency <= 0 {
		concurrency = 30
	}
	sem := make(chan struct{}, concurrency)
	var testWg sync.WaitGroup

logger:
	Info("开始并发测试", "source_count", len(sources), "concurrency", concurrency)
	for _, src := range sources {
		select {
		case <-ctx.Done():
			// 上下文取消，不再发送新任务
			break
		default:
		}
		testWg.Add(1)
		sem <- struct{}{}
		go func(s models.URLSource) {
			defer func() {
				<-sem
				testWg.Done()
			}()
			// 每个测试独立 context，可设置超时
			testCtx, cancel := context.WithTimeout(ctx, time.Duration(t.cfg.Testing.Timeout)*time.Second)
			defer cancel()
			res := t.testSingle(testCtx, s)
			resultCh <- res
		}(src)
	}
	testWg.Wait()
	close(resultCh)
	writerWg.Wait() // 等待批量写入结束

	// ---------- 6. 完成进度 ----------
	t.progMgr.FinishTask(taskID)
	return nil
}

// fetchSourcesToTest 获取需要测试的源列表（从未测试或已过期且需要重新测试的 url_sources）
func (t *Tester) fetchSourcesToTest(ctx context.Context) ([]models.URLSource, error) {
	// 示例查询：选择未测试或 last_checked 早于指定间隔的源，限于配置的最大数量
	query := `
		SELECT us.id, us.url, us.name, us.tvg_id, us.group_title
		FROM url_sources us
		LEFT JOIN url_sources_passed pass ON pass.source_id = us.id
		WHERE pass.id IS NULL
		   OR pass.last_checked < datetime('now', ?)
		ORDER BY us.created_at DESC
		LIMIT ?
	`
	interval := fmt.Sprintf("-%d hours", t.cfg.Testing.RecheckInterval) // 如 24 小时
	limit := t.cfg.Testing.MaxTestBatch // 单次测试最大数量，默认 2000
	if limit <= 0 {
		limit = 2000
	}
	rows, err := t.db.QueryContext(ctx, query, interval, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.URLSource
	for rows.Next() {
		var s models.URLSource
		if err := rows.Scan(&s.ID, &s.URL, &s.Name, &s.TvgID, &s.GroupTitle); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// testSingle 测试单个源，返回完整结果
func (t *Tester) testSingle(ctx context.Context, src models.URLSource) *TestResult {
	res := &TestResult{
		SourceID: src.ID,
		URL:      src.URL,
	}
	start := time.Now()

	// -------- 1. HTTP 可达性测试 --------
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		res.ErrorMsg = "请求创建失败: " + err.Error()
		return res
	}
	// 设置模拟普通播放器的 User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		res.ErrorMsg = "请求失败: " + err.Error()
		return res
	}
	defer resp.Body.Close()
	res.StatusCode = resp.StatusCode
	res.ResponseTimeMs = int(time.Since(start).Milliseconds())
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		res.ErrorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	// 简易内容类型检测：如果是视频流，进一步探测
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "video/") && !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/") {
		// 可能只是重定向或网页，但也算可达，标记成功但不一定有媒体信息
		res.Success = true
		return res
	}
	res.Success = true

	// -------- 2. ffprobe 获取媒体信息（使用绝对路径/参数数组防注入）--------
	if info, err := t.probeMedia(ctx, src.URL); err == nil {
		res.Resolution = info.Resolution
		res.Bitrate = info.Bitrate
	} else {
		logger.Debug("ffprobe 分析失败", "url", src.URL, "error", err)
		// 不影响成功状态，仅没有额外信息
	}

	// -------- 3. 归属地识别 --------
	if t.geoResolver != nil {
		// 从 URL 提取主机名
		h := extractHost(resp.Request.URL) // 使用最终请求的 URL（处理重定向）
		if h != "" {
			loc, isp, err := t.geoResolver.Resolve(h)
			if err == nil {
				res.Location = loc
				res.ISP = isp
			}
		}
	}

	return res
}

// probeMedia 调用 ffprobe 获取流信息，参数数组模式防注入
// 返回媒体基本信息，失败则返回错误
func (t *Tester) probeMedia(ctx context.Context, url string) (*MediaInfo, error) {
	ffprobePath := t.cfg.Testing.FfmpegPath
	if ffprobePath == "" {
		ffprobePath = "ffprobe" // 默认从 PATH 查找（须确保安全）
	}
	// 参数数组，URL 作为最后一个参数
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-timeout", fmt.Sprintf("%d000000", t.cfg.Testing.Timeout), // 单位微秒
		"-i", url,
	}
	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe 执行失败: %w", err)
	}

	// 解析 JSON 输出
	var fprobe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &fprobe); err != nil {
		return nil, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}

	mi := &MediaInfo{}
	for _, s := range fprobe.Streams {
		if s.Width > 0 && s.Height > 0 {
			mi.Resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
			break
		}
	}
	if fprobe.Format.BitRate != "" {
		var br float64
		fmt.Sscanf(fprobe.Format.BitRate, "%f", &br)
		mi.Bitrate = int(br / 1000) // 转换为 kbps
	}
	return mi, nil
}

// MediaInfo 媒体信息
type MediaInfo struct {
	Resolution string
	Bitrate    int
}

// extractHost 从 URL 提取主机名（含端口）
func extractHost(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}

// batchWriter 协程：批量收集结果，定时或阈值触发写入
func (t *Tester) batchWriter(ctx context.Context, resCh <-chan *TestResult, taskID string, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]*TestResult, 0, t.batchSize)
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		toWrite := make([]*TestResult, len(buf))
		copy(toWrite, buf)
		buf = buf[:0]
		t.batchInsertResults(ctx, toWrite, taskID)
	}

	for {
		select {
		case res, ok := <-resCh:
			if !ok {
				flush()
				return
			}
			buf = append(buf, res)
			// 更新 WebSocket 进度（每个结果都更新，保证实时性）
			t.progMgr.Increment(taskID, res.Success)
			if len(buf) >= t.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

// batchInsertResults 将本批次结果通过事务批量写入数据库
func (t *Tester) batchInsertResults(ctx context.Context, results []*TestResult, taskID string) {
	if len(results) == 0 {
		return
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("开始事务失败", "error", err)
		return
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p) // 重新抛出
		}
		if err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit()
			if err != nil {
				logger.Error("提交事务失败", "error", err)
			}
		}
	}()

	upsertPassed, err := tx.PrepareContext(ctx, `
		INSERT INTO url_sources_passed 
			(source_id, status, response_time_ms, resolution, bitrate, last_checked, error_message, location, isp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			status = excluded.status,
			response_time_ms = excluded.response_time_ms,
			resolution = excluded.resolution,
			bitrate = excluded.bitrate,
			last_checked = excluded.last_checked,
			error_message = excluded.error_message,
			location = excluded.location,
			isp = excluded.isp
	`)
	if err != nil {
		logger.Error("准备 passed 语句失败", "error", err)
		return
	}
	defer upsertPassed.Close()

	insHistory, err := tx.PrepareContext(ctx, `
		INSERT INTO test_history 
			(source_id, success, response_time_ms, status_code, resolution, bitrate, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		logger.Error("准备 history 语句失败", "error", err)
		return
	}
	defer insHistory.Close()

	now := time.Now()
	for _, r := range results {
		status := "inactive"
		if r.Success {
			status = "active"
		}
		_, err = upsertPassed.ExecContext(ctx, r.SourceID, status, r.ResponseTimeMs,
			r.Resolution, r.Bitrate, now, r.ErrorMsg, r.Location, r.ISP)
		if err != nil {
			logger.Error("写入 url_sources_passed 失败", "source_id", r.SourceID, "error", err)
			err = fmt.Errorf("批量写入中断: %w", err)
			return
		}
		_, err = insHistory.ExecContext(ctx, r.SourceID, r.Success, r.ResponseTimeMs,
			r.StatusCode, r.Resolution, r.Bitrate, r.ErrorMsg)
		if err != nil {
			logger.Error("写入 test_history 失败", "source_id", r.SourceID, "error", err)
			err = fmt.Errorf("批量写入中断: %w", err)
			return
		}
	}
	// 更新任务进度最终状态（可选）
	_ = t.progMgr.UpdateTaskTotal(t.db, taskID, len(results)) // 实际上 progress 已实时更新，此处仅演示
}
