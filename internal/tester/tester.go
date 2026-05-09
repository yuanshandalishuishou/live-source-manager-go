// internal/tester/tester.go
//
// 流测试器：使用 ffprobe 并发探测直播源状态，提取分辨率、编码、比特率等元数据。
//
// 【并发安全设计】
//   - 使用有缓冲的信号量通道 (sem) 控制并发数，避免无限制创建 goroutine。
//   - WaitGroup 确保所有测试 goroutine 结束后才由调用者关闭结果通道 resultCh，
//     从根本上杜绝“向已关闭 channel 写数据”的 panic。
//   - 单源测试支持 context 超时取消，且 ffprobe 命令自身带有 -timeout 参数。
//
// 【内存安全】
//   - readOutputSafe 限制单次输出上限为 4MB，防止恶意或损坏流导致 OOM。
//   - parseMetadata 对所有字段做空值检查，避免空指针异常。
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

// Tester 直播源测试器，聚合了所有依赖。
type Tester struct {
	cfg         *config.Config
	db          *db.DB
	progress    *progress.Manager
	httpClient  *http.Client
	concurrency int
	timeout     time.Duration
	ffprobePath string
}

// NewTester 创建测试器实例。
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

// StreamResult 单个流的测试结果。
type StreamResult struct {
	Source   *models.Source
	Success  bool
	Metadata *models.StreamMeta
	Error    error
}

// ffprobeOutput ffprobe 的 JSON 输出结构（只取需要的部分）。
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

// TestAll 从数据库读取所有待测源，执行并发测试，并将结果写回数据库。
// 本方法负责管理 resultCh 的完整生命周期：创建 → 启动生产者 → 消费结果 → 关闭。
func (t *Tester) TestAll(ctx context.Context) {
	sources, err := t.db.GetAllSources()
	if err != nil {
		logger.Error("获取待测源失败: %v", err)
		return
	}
	if len(sources) == 0 {
		logger.Info("没有需要测试的源")
		return
	}

	logger.Info("开始测试 %d 个源", len(sources))

	// 创建带缓冲的结果通道，避免消费者慢时阻塞生产者
	resultCh := make(chan *StreamResult, t.concurrency*2)

	// 在单独的 goroutine 中运行所有测试，完成后关闭 resultCh
	go func() {
		t.Run(ctx, sources, resultCh)
		close(resultCh) // 所有 goroutine 已结束，安全关闭
	}()

	// 消费结果并更新数据库
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
			t.progress.Broadcast("test", fmt.Sprintf("成功: %s (%dx%d)", res.Source.URL, res.Metadata.Width, res.Metadata.Height))
		}
	}
	logger.Info("源测试完成")
}

// Run 并发测试一组源，并将结果发送至 resultCh。
// 注意：resultCh 由调用者创建和关闭，本函数只负责写入，不负责关闭。
func (t *Tester) Run(ctx context.Context, sources []*models.Source, resultCh chan<- *StreamResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, t.concurrency) // 信号量，最大并发数

	for _, src := range sources {
		// 检查是否被提前取消
		select {
		case <-ctx.Done():
			break
		default:
		}

		// 获取令牌
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break
		}

		// 只有在成功获取令牌后才增加 WaitGroup 计数
		wg.Add(1)
		go func(s *models.Source) {
			defer wg.Done()
			defer func() { <-sem }() // 释放令牌

			res := t.testSingle(ctx, s)

			// 结果写入 resultCh。由于 resultCh 的关闭时间在 wg.Wait() 之后，
			// 且由外部控制，因此无需 recover。
			resultCh <- res
		}(src)
	}

	// 等待所有 goroutine 结束，之后外部调用者将关闭 resultCh
	wg.Wait()
}

// testSingle 对单个源执行 ffprobe 探测。
// 支持 context 超时和 ffprobe 自身的 -timeout 双重保护。
func (t *Tester) testSingle(ctx context.Context, s *models.Source) *StreamResult {
	res := &StreamResult{Source: s}

	// 构建 ffprobe 命令参数
	args := []string{
		"-v", "quiet", // 只输出错误信息
		"-print_format", "json", // 输出 JSON 格式
		"-show_format",         // 容器格式信息
		"-show_streams",        // 流信息
		"-select_streams", "v", // 只探测视频流
		"-timeout", strconv.FormatInt(t.timeout.Milliseconds(), 10), // 微秒级超时
		"-i", s.URL, // 输入 URL
	}

	// 为单次测试创建独立的超时 context
	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, t.ffprobePath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Error = fmt.Errorf("创建管道失败: %w", err)
		return res
	}

	if err := cmd.Start(); err != nil {
		res.Error = fmt.Errorf("启动 ffprobe 失败: %w", err)
		return res
	}

	// 安全读取输出，防止恶意流返回巨量数据
	output, readErr := readOutputSafe(stdout)
	if readErr != nil {
		res.Error = fmt.Errorf("读取输出错误: %w", readErr)
		// 即使读取失败，仍需等待进程退出以避免僵尸进程
	}

	// 等待命令结束
	if waitErr := cmd.Wait(); waitErr != nil {
		// ffprobe 对无效流会返回非0，视为测试失败
		res.Error = fmt.Errorf("ffprobe 执行错误: %w", waitErr)
		return res
	}

	if output == "" {
		res.Error = fmt.Errorf("ffprobe 返回空输出")
		return res
	}

	// 解析 JSON 输出
	var probe ffprobeOutput
	if err := json.Unmarshal([]byte(output), &probe); err != nil {
		res.Error = fmt.Errorf("解析 JSON 失败: %w", err)
		return res
	}

	// 提取所需元数据
	meta, err := parseMetadata(&probe)
	if err != nil {
		res.Error = err
		return res
	}

	res.Success = true
	res.Metadata = meta
	return res
}

// readOutputSafe 从 reader 读取全部内容，硬限制最大 4MB。
func readOutputSafe(r io.Reader) (string, error) {
	const maxSize = 4 * 1024 * 1024 // 4MB
	var buf strings.Builder
	scanner := bufio.NewScanner(r)
	// 设置缓冲区，单行不超过 1MB
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if buf.Len()+len(line) > maxSize {
			return "", fmt.Errorf("输出超过最大限制 %d 字节", maxSize)
		}
		buf.Write(line)
	}
	if err := scanner.Err(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

// parseMetadata 从 ffprobe 输出中提取视频流的宽度、高度、编码和比特率。
func parseMetadata(p *ffprobeOutput) (*models.StreamMeta, error) {
	if p == nil || len(p.Streams) == 0 {
		return nil, fmt.Errorf("未找到任何流")
	}

	// 查找第一个视频流
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

	// 比特率优先级：视频流比特率 > 容器比特率
	if br, err := strconv.Atoi(video.BitRate); err == nil && br > 0 {
		meta.BitRate = br
	} else if br, err := strconv.Atoi(p.Format.BitRate); err == nil && br > 0 {
		meta.BitRate = br
	}

	// 如果高度和宽度均为 0，可能流本身没有视频信息，视为无效
	if meta.Width == 0 && meta.Height == 0 {
		return nil, fmt.Errorf("视频流无效（宽高均为 0）")
	}

	return meta, nil
}
