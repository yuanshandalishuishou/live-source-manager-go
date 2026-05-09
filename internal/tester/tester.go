// internal/tester/tester.go
// 流测试器 —— 使用信号量控制并发数量，通过 ffprobe 检测流有效性并提取元数据。
// 已修复上下文取消时的 channel 写入安全问题，所有 goroutine 结束后才关闭结果通道。
package tester

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// StreamResult 表示单个流的测试结果
type StreamResult struct {
	Source   *models.Source     // 被测试的源对象
	Success  bool               // 测试是否通过
	Metadata *models.StreamMeta // ffprobe 提取的元数据
	Error    error              // 出现的错误（如果有）
}

// Tester 流测试器结构体
type Tester struct {
	concurrency int           // 最大并发测试数
	timeout     time.Duration // 单个测试超时时间
	ffprobePath string        // ffprobe 可执行文件路径
}

// NewTester 创建新的测试器实例
func NewTester(concurrency, timeout int, ffprobePath string) *Tester {
	return &Tester{
		concurrency: concurrency,
		timeout:     time.Duration(timeout) * time.Second,
		ffprobePath: ffprobePath,
	}
}

// ffprobeOutput 对应 ffprobe JSON 输出的顶层结构
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	CodecName string `json:"codec_name"`
	CodecType string `json:"codec_type"`
	BitRate   string `json:"bit_rate"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

// Run 并发测试一组源，并将结果通过 resultCh 发送。
// 使用信号量控制并发数，所有源测试完毕或上下文取消后关闭 resultCh。
func (t *Tester) Run(ctx context.Context, sources []*models.Source, resultCh chan<- *StreamResult) {
	var wg sync.WaitGroup
	// 信号量通道，容量为并发数
	sem := make(chan struct{}, t.concurrency)

	for _, src := range sources {
		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			goto waitAndClose
		default:
		}

		// 获取信号量令牌
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			goto waitAndClose
		}

		// 成功获取令牌后才增加 WaitGroup
		wg.Add(1)
		go func(s *models.Source) {
			defer wg.Done()
			defer func() { <-sem }() // 释放令牌

			res := t.testSingle(s)

			// 非阻塞写入结果，防止通道关闭或上下文取消
			select {
			case resultCh <- res:
			case <-ctx.Done():
			}
		}(src)
	}

waitAndClose:
	// 等待所有已启动的 goroutine 结束
	wg.Wait()
	close(resultCh)
}

// testSingle 对单个源执行 ffprobe 探测
func (t *Tester) testSingle(s *models.Source) *StreamResult {
	result := &StreamResult{Source: s}

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-select_streams", "v",
		"-timeout", strconv.FormatInt(t.timeout.Milliseconds(), 10),
		"-i", s.URL,
	}

	// 创建带超时的子上下文
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, t.ffprobePath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("创建管道失败: %w", err)
		return result
	}

	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("启动 ffprobe 失败: %w", err)
		return result
	}

	// 读取全部输出
	var output strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		output.WriteString(scanner.Text())
	}
	if scanErr := scanner.Err(); scanErr != nil {
		result.Error = fmt.Errorf("读取输出错误: %w", scanErr)
		// 即使读取失败也尝试处理已获取的数据
	}

	// 等待命令结束
	if err := cmd.Wait(); err != nil {
		// ffprobe 碰到无效流通常会返回非 0，视为测试失败
		result.Error = fmt.Errorf("ffprobe 执行失败: %w", err)
		return result
	}

	// 解析 JSON 输出
	var probe ffprobeOutput
	if err := json.Unmarshal([]byte(output.String()), &probe); err != nil {
		result.Error = fmt.Errorf("解析 ffprobe 输出失败: %w", err)
		return result
	}

	meta, err := parseMetadata(&probe)
	if err != nil {
		result.Error = err
		return result
	}

	result.Success = true
	result.Metadata = meta
	return result
}

// parseMetadata 从 ffprobe 输出中提取视频流元数据
func parseMetadata(p *ffprobeOutput) (*models.StreamMeta, error) {
	if len(p.Streams) == 0 {
		return nil, fmt.Errorf("未找到视频流")
	}

	// 只关心视频流
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

	// 比特率解析优先级：视频流比特率 > 封装格式比特率
	if video.BitRate != "" {
		if br, err := strconv.Atoi(video.BitRate); err == nil {
			meta.BitRate = br
		}
	}
	if meta.BitRate == 0 && p.Format.BitRate != "" {
		if br, err := strconv.Atoi(p.Format.BitRate); err == nil {
			meta.BitRate = br
		}
	}

	return meta, nil
}
