// internal/tester/tester.go
// 流测试器（安全版）—— 修复 Run 方法在上下文取消时的并发写入问题。
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
	"github.com/yuanshandalishuishou/live-source-manager-go/pkg/logger"
)

// Tester 负责对流进行 ffprobe 探测
type Tester struct {
	concurrency int
	timeout     time.Duration
	ffprobePath string
}

// NewTester 创建测试器实例
func NewTester(concurrency, timeout int, ffprobePath string) *Tester {
	return &Tester{
		concurrency: concurrency,
		timeout:     time.Duration(timeout) * time.Second,
		ffprobePath: ffprobePath,
	}
}

// StreamResult 封装单个流的测试结果
type StreamResult struct {
	Source   *models.Source
	Success  bool
	Metadata *models.StreamMeta
	Error    error
}

// ffprobeOutput 定义 ffprobe 输出结构
type ffprobeOutput struct {
	Streams []struct {
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		CodecName string `json:"codec_name"`
		BitRate   string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// Run 并发测试一组流，并通过 resultCh 发送结果。
// [修复] 使用 WaitGroup 确保所有 goroutine 结束后再关闭 resultCh，
// 避免在 context 取消时仍有 goroutine 尝试写入已关闭的 channel。
func (t *Tester) Run(ctx context.Context, sources []*models.Source, resultCh chan<- *StreamResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, t.concurrency) // 信号量控制并发

	for _, s := range sources {
		select {
		case <-ctx.Done():
			// 上下文已取消，不再启动新的测试
			goto waitAndClose
		default:
		}
		sem <- struct{}{} // 获取令牌
		wg.Add(1)
		go func(src *models.Source) {
			defer wg.Done()
			defer func() { <-sem }() // 释放令牌

			res := t.testSingle(src)
			// 使用 select 非阻塞写入，防止 ctx 取消后阻塞
			select {
			case resultCh <- res:
			case <-ctx.Done():
				return
			}
		}(s)
	}

waitAndClose:
	wg.Wait()
	close(resultCh)
}

// testSingle 对单个流执行 ffprobe 检测
func (t *Tester) testSingle(s *models.Source) *StreamResult {
	result := &StreamResult{Source: s}

	// 构建 ffprobe 命令
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-select_streams", "v",
		"-timeout", strconv.FormatInt(t.timeout.Milliseconds(), 10),
		"-i", s.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, t.ffprobePath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("create pipe: %w", err)
		return result
	}

	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("start ffprobe: %w", err)
		return result
	}

	scanner := bufio.NewScanner(stdout)
	var output strings.Builder
	for scanner.Scan() {
		output.WriteString(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		result.Error = fmt.Errorf("read output: %w", err)
		// 继续处理，可能仍有部分结果
	}

	if err := cmd.Wait(); err != nil {
		// ffprobe 在流超时时会返回非 0 退出码，视为测试失败
		result.Error = fmt.Errorf("ffprobe exit error: %w", err)
		return result
	}

	// 解析结果
	var probe ffprobeOutput
	if err := json.Unmarshal([]byte(output.String()), &probe); err != nil {
		result.Error = fmt.Errorf("unmarshal ffprobe output: %w", err)
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

// parseMetadata 从 ffprobeOutput 中提取流元数据
func parseMetadata(p *ffprobeOutput) (*models.StreamMeta, error) {
	meta := &models.StreamMeta{}
	if len(p.Streams) == 0 {
		return nil, fmt.Errorf("no video stream found")
	}
	stream := p.Streams[0]
	meta.Width = stream.Width
	meta.Height = stream.Height
	meta.Codec = stream.CodecName

	// 优先取视频流的比特率
	if stream.BitRate != "" {
		if br, err := strconv.Atoi(stream.BitRate); err == nil {
			meta.BitRate = br
		}
	}
	// 回退到包层的比特率
	if meta.BitRate == 0 && p.Format.BitRate != "" {
		if br, err := strconv.Atoi(p.Format.BitRate); err == nil {
			meta.BitRate = br
		}
	}
	return meta, nil
}
