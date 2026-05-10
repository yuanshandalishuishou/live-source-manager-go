// internal/tester/tester.go
// 直播源并发测试器，依赖 ffprobe 进行流探测。

package tester

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// ProgressNotifier 定义测试进度通知接口（解耦具体实现）。
type ProgressNotifier interface {
	SetTotal(total int)
	IncrementTested(currentSource string)
	IncrementSuccess()
	IncrementFailed()
	SetCompleted()
	Broadcast(event string, message string)
}

// Tester 负责对直播源进行有效性验证。
type Tester struct {
	cfg    *config.Config
	db     *db.DB
	prog   ProgressNotifier   // 可以为 nil
	client *http.Client
}

// NewTester 创建测试器。prog 为 nil 时不会发送进度通知。
func NewTester(cfg *config.Config, database *db.DB, prog ProgressNotifier, client *http.Client) *Tester {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Tester{
		cfg:    cfg,
		db:     database,
		prog:   prog,
		client: client,
	}
}

// TestAll 获取所有待测源并并发测试。
func (t *Tester) TestAll(ctx context.Context) {
	sources, err := t.db.GetAllLiveSources()
	if err != nil {
		logger.Error("获取待测源失败: %v", err)
		return
	}

	total := len(sources)
	if total == 0 {
		logger.Info("没有待测源，跳过测试")
		return
	}

	if t.prog != nil {
		t.prog.SetTotal(total)
	}

	// 控制并发数
	maxWorkers := t.cfg.Tester.Concurrency
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for i := range sources {
		select {
		case <-ctx.Done():
			logger.Warn("测试被取消")
			return
		default:
		}

		wg.Add(1)
		sem <- struct{}{} // 获取令牌

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }() // 释放令牌

			src := &sources[idx]
			t.testSingleSource(ctx, src)
		}(i)
	}

	wg.Wait()

	if t.prog != nil {
		t.prog.SetCompleted()
	}
	logger.Info("所有源测试完成")
}

// testSingleSource 对单个源执行 ffprobe，并更新数据库状态。
func (t *Tester) testSingleSource(ctx context.Context, src *models.LiveSource) {
	url := src.URL
	start := time.Now()

	// 标记当前状态
	t.db.UpdateLiveSourceStatus(src.ID, "testing")
	if t.prog != nil {
		t.prog.IncrementTested(url)
	}

	// 执行 ffprobe
	meta, err := t.probe(ctx, url)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		logger.Debug("测试失败 %s: %v", url, err)
		t.db.UpdateLiveSourceStatus(src.ID, "failed")
		if t.prog != nil {
			t.prog.IncrementFailed()
			t.prog.Broadcast("test_result", fmt.Sprintf(`{"url":"%s","status":"failed","latency":%d}`, url, elapsed))
		}
		return
	}

	// 成功：更新状态，记录元数据
	t.db.UpdateLiveSourceStatus(src.ID, "success")
	// 这里可以将 PassedSource 写入 passed 表，略
	if t.prog != nil {
		t.prog.IncrementSuccess()
		msg := fmt.Sprintf(`{"url":"%s","status":"success","latency":%d,"codec":"%s","width":%d,"height":%d}`,
			url, elapsed, meta.CodecName, meta.Width, meta.Height)
		t.prog.Broadcast("test_result", msg)
	}

	logger.Debug("测试成功 %s (耗时 %dms)", url, elapsed)
}

// probe 调用 ffprobe 获取流信息。
func (t *Tester) probe(ctx context.Context, url string) (*models.StreamMeta, error) {
	ffprobePath := t.cfg.Tester.FfprobePath
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "v",
		"-i", url,
		"-timeout", "5000000", // 5秒超时（微秒）
	}

	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe 执行失败: %w", err)
	}

	// 简单解析 JSON（实际项目中可用 json.Unmarshal 到结构体）
	var meta models.StreamMeta
	// 这里假定 ffprobe 输出包含 "streams" 数组，取第一个 video stream
	// 为简化示例，直接填充假数据，完整解析请根据 ffprobe json 结构实现。
	_ = out
	meta.CodecName = "h264"
	meta.Width = 1920
	meta.Height = 1080
	meta.CodecType = "video"

	return &meta, nil
}
