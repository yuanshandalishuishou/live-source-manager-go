// internal/tester/tester.go
// 流测试器 —— 增加了输出缓冲安全边界、JSON 解码空值校验与 goroutine 防泄漏。
package tester

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// 常量定义
const (
	maxLineSize  = 16 * 1024 // 单行最大 16KB
	maxOutputSize = 4 * 1024 * 1024 // 总输出限制 4MB
)

type Tester struct {
	concurrency int
	timeout     time.Duration
	ffprobePath string
}

func NewTester(concurrency, timeout int, ffprobePath string) *Tester {
	return &Tester{
		concurrency: concurrency,
		timeout:     time.Duration(timeout) * time.Second,
		ffprobePath: ffprobePath,
	}
}

type StreamResult struct {
	Source   *models.Source
	Success  bool
	Metadata *models.StreamMeta
	Error    error
}

// 安全的输出读取，防止大文件撑爆内存
func readOutputSafe(reader io.Reader) (string, error) {
	var b strings.Builder
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	bytesRead := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		bytesRead += len(line)
		if bytesRead > maxOutputSize {
			return "", fmt.Errorf("ffprobe 输出超过 %d 字节，已截断", maxOutputSize)
		}
		b.Write(line)
	}
	if err := scanner.Err(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

func (t *Tester) Run(ctx context.Context, sources []*models.Source, resultCh chan<- *StreamResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, t.concurrency)

	for _, src := range sources {
		select {
		case <-ctx.Done():
			goto waitAndClose
		default:
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			goto waitAndClose
		}
		wg.Add(1)
		go func(s *models.Source) {
			defer wg.Done()
			defer func() { <-sem }()
			res := t.testSingle(ctx, s)
			select {
			case resultCh <- res:
			case <-ctx.Done():
			}
		}(src)
	}
waitAndClose:
	wg.Wait()
	close(resultCh)
}

func (t *Tester) testSingle(ctx context.Context, s *models.Source) *StreamResult {
	res := &StreamResult{Source: s}
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format", "-show_streams",
		"-select_streams", "v",
		"-timeout", strconv.FormatInt(t.timeout.Milliseconds(), 10),
		"-i", s.URL,
	}
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

	output, err := readOutputSafe(stdout)
	if err != nil {
		res.Error = fmt.Errorf("读取输出错误: %w", err)
		// 即使读取失败也尝试解析已有数据
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		res.Error = fmt.Errorf("ffprobe 执行错误: %w", waitErr)
		return res
	}

	// 仅当无错误且输出不为空时才解析
	if output == "" {
		res.Error = fmt.Errorf("ffprobe 返回空输出")
		return res
	}

	var probe ffprobeOutput
	if err := json.Unmarshal([]byte(output), &probe); err != nil {
		res.Error = fmt.Errorf("解析 JSON 失败: %w", err)
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

// 安全解析元数据，防止空指针
func parseMetadata(p *ffprobeOutput) (*models.StreamMeta, error) {
	if p == nil || len(p.Streams) == 0 {
		return nil, fmt.Errorf("未找到视频流")
	}
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
	intRate, _ := strconv.Atoi(video.BitRate)
	pkgRate, _ := strconv.Atoi(p.Format.BitRate)
	if intRate > 0 {
		meta.BitRate = intRate
	} else if pkgRate > 0 {
		meta.BitRate = pkgRate
	}
	return meta, nil
}

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
	BitRate  string `json:"bit_rate"`
}
