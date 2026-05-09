// internal/tester/tester.go
//
// 流测试器：使用 ffprobe 并发检测直播源的状态、分辨率、编码格式等元数据。
// 支持超时控制、并发限制、结果回调，并已修复上下文取消时的 channel panic 风险。
package tester

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/internal/progress"
	"live-source-manager-go/pkg/logger"
)

// Tester 直播源测试器
type Tester struct {
	cfg         *config.Config
	db          *db.DB
	progress    *progress.Manager
	httpClient  *http.Client
	concurrency int
	timeout     time.Duration
	ffprobePath string
}

// NewTester 创建测试器实例
func NewTester(cfg *config.Config, database *db.DB, prog *progress.Manager, client *http.Client) *Tester {
	return &Tester{
		cfg:         cfg,
		db:          database,
		progress:    prog,
		httpClient:  client,
		concurrency: cfg.Tester.Concurrency,
		timeout:     time.Duration(cfg.Tester.Timeout) * time.Millisecond,
		ffprobePath: cfg.Tester.FfprobePath,
	}
}

// StreamResult 单个流的测试结果
type StreamResult struct {
	Source   *models.Source
	Success  bool
	Metadata *models.StreamMeta
	Error    error
}

// ffprobe 输出结构体
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}
type ffprobeStream struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BitRate   string `json:"bit_rate"`
}
type ffprobeFormat struct {
	BitRate string `json:"bit_rate"`
}

// TestAll 从数据库读取所有待测源并执行并发测试，结果自动写入数据库
func (t *Tester) TestAll(ctx context.Context) {
	sources, err := t.db.GetAllSources() // 假设返回 []*models.Source
	if err != nil {
		logger.Error("获取待测源失败: %v", err)
		return
	}
	if len(sources) == 0 {
		logger.Info("没有需要测试的源")
		return
	}

	logger.Info("开始测试 %d 个源", len(sources))
	resultCh := make(chan *StreamResult, 100)
	go t.Run(ctx, sources, resultCh)

	// 处理结果：更新数据库、进度通知
	for res := range resultCh {
		if res.Error != nil {
			logger.Error("源 %s 测试失败: %v", res.Source.URL, res.Error)
			t.db.UpdateSourceStatus(res.Source.ID, "failed")
			t.progress.Broadcast("test", fmt.Sprintf("失败: %s", res.Source.URL))
			continue
		}
		if res.Success {
			t.db.UpdateSourceStatus(res.Source.ID, "active")
			if res.Metadata != nil {
				t.db.UpdateSourceMeta(res.Source.ID, res.Metadata)
			}
			t.progress.Broadcast("test", fmt.Sprintf("成功: %s", res.Source.URL))
		}
	}
	logger.Info("源测试完成")
}

// Run 并发测试一组源，将结果发送至 resultCh。
// 所有 goroutine 结束后关闭 resultCh。
func (t *Tester) Run(ctx context.Context, sources []*models.Source, resultCh chan<- *StreamResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, t.concurrency) // 信号量控制并发数

	for _, src := range sources {
		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			goto finish
		default:
		}

		// 获取信号量
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			goto finish
		}

		wg.Add(1)
		go func(s *models.Source) {
			defer wg.Done()
			defer func() { <-sem }() // 释放令牌

			res := t.testSingle(ctx, s)

			// 安全写入：如果外部已经关闭 resultCh，recover 防止 panic
			defer func() {
				if r := recover(); r != nil {
					logger.Error("写入测试结果时发生 panic（可能 resultCh 已关闭）: %v", r)
				}
			}()
			select {
			case resultCh <- res:
			case <-ctx.Done():
			}
		}(src)
	}

finish:
	wg.Wait()
	// 安全关闭 resultCh
	defer func() {
		if r := recover(); r != nil {
			logger.Error("关闭 resultCh 时发生 panic: %v", r)
		}
	}()
	close(resultCh)
}

// testSingle 对单个源执行 ffprobe 探测
func (t *Tester) testSingle(ctx context.Context, s *models.Source) *StreamResult {
	res := &StreamResult{Source: s}

	// 构建 ffprobe 命令参数
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-select_streams", "v",
		"-timeout", strconv.FormatInt(t.timeout.Milliseconds(), 10),
		"-i", s.URL,
	}

	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, t.ffprobePath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Error = fmt.Errorf("创建管道: %w", err)
		return res
	}

	if err := cmd.Start(); err != nil {
		res.Error = fmt.Errorf("启动 ffprobe: %w", err)
		return res
	}

	// 安全读取输出
	output, readErr := readOutput(stdout)
	if readErr != nil {
		res.Error = fmt.Errorf("读取输出: %w", readErr)
		// 即使读取错误也尝试等待进程结束，避免僵尸进程
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		// ffprobe 在无法打开流时返回非0，这是预期行为
		res.Error = fmt.Errorf("ffprobe: %w", waitErr)
		return res
	}

	if output == "" {
		res.Error = fmt.Errorf("ffprobe 返回空输出")
		return res
	}

	// 解析 JSON
	var probe ffprobeOutput
	if err := json.Unmarshal([]byte(output), &probe); err != nil {
		res.Error = fmt.Errorf("解析 JSON: %w", err)
		return res
	}

	meta, err := parseMetadata(&probe)
	if err != nil {
		res.Error = err
		return res
	}

	res.Success = true
	res.Metadata = meta
	return res
}

// readOutput 安全地读取所有输出，限制最大 4MB
func readOutput(r io.Reader) (string, error) {
	const maxSize = 4 * 1024 * 1024 // 4MB
	var buf strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 每行最大 1MB
	for scanner.Scan() {
		line := scanner.Bytes()
		if buf.Len()+len(line) > maxSize {
			return "", fmt.Errorf("输出超过最大限制 %d 字节", maxSize)
		}
		buf.Write(line)
	}
	return buf.String(), scanner.Err()
}

// parseMetadata 从 ffprobe 输出中提取视频流元数据
func parseMetadata(p *ffprobeOutput) (*models.StreamMeta, error) {
	if len(p.Streams) == 0 {
		return nil, fmt.Errorf("未找到流")
	}

	// 找到第一个视频流
	var video *ffprobeStream
	for i := range p.Streams {
		if p.Streams[i].CodecType == "video" {
			video = &p.Streams[i]
			break
		}
	}
	if video == nil {
		return nil, fmt.Errorf("未找到视频流")
	}

	meta := &models.StreamMeta{
		Width:  video.Width,
		Height: video.Height,
		Codec:  video.CodecName,
	}

	// 比特率：优先视频流比特率，其次包装格式比特率
	if br, err := strconv.Atoi(video.BitRate); err == nil && br > 0 {
		meta.BitRate = br
	} else if br, err := strconv.Atoi(p.Format.BitRate); err == nil && br > 0 {
		meta.BitRate = br
	}

	return meta, nil
}
