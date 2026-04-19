package tester

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/geo"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/pkg/logger"
)

// Tester 流测试器
type Tester struct {
	cfg      *config.Config
	log      *logger.Logger
	db       *sql.DB
	geo      *geo.Resolver
	rulesMgr *rules.Manager
	progress *ProgressManager
	mu       sync.Mutex
	running  bool
}

// NewTester 创建测试器实例
func NewTester(cfg *config.Config, log *logger.Logger, db *sql.DB, geo *geo.Resolver, rulesMgr *rules.Manager) *Tester {
	return &Tester{
		cfg:      cfg,
		log:      log,
		db:       db,
		geo:      geo,
		rulesMgr: rulesMgr,
		running:  false,
	}
}

// IsRunning 检查测试是否正在运行
func (t *Tester) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// Start 启动测试任务（非阻塞，内部启动 goroutine）
func (t *Tester) Start() (string, error) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return "", fmt.Errorf("测试任务已在运行中")
	}
	t.running = true
	t.mu.Unlock()

	// 创建进度管理器
	t.progress = NewProgressManager(t.db, t.log)
	taskID := t.progress.TaskID()

	go func() {
		defer func() {
			t.mu.Lock()
			t.running = false
			t.mu.Unlock()
			t.progress.SetCompleted()
		}()
		t.runTests()
	}()

	return taskID, nil
}

// runTests 执行实际的测试流程
func (t *Tester) runTests() {
	// 1. 从 url_sources 获取未测试或需要重测的源
	sources, err := t.fetchPendingSources()
	if err != nil {
		t.log.Error("获取待测源失败: %v", err)
		t.progress.SetFailed(err.Error())
		return
	}
	if len(sources) == 0 {
		t.log.Info("没有待测试的源")
		t.progress.SetTotal(0)
		return
	}

	t.log.Info("开始测试 %d 个源", len(sources))
	t.progress.SetTotal(len(sources))

	// 2. 并发测试
	sem := make(chan struct{}, t.cfg.Testing.ConcurrentThreads)
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(s models.URLSource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			t.testSingle(s)
		}(src)
	}
	wg.Wait()
}

// fetchPendingSources 获取待测试的源
func (t *Tester) fetchPendingSources() ([]models.URLSource, error) {
	rows, err := t.db.Query(`SELECT id, url, name, tvg_id, tvg_logo, group_title, 
		catchup, catchup_days, user_agent, raw_attributes, source_type, live_source_id
		FROM url_sources WHERE id NOT IN (SELECT source_id FROM url_sources_passed) 
		OR id IN (SELECT id FROM url_sources WHERE updated_at < datetime('now', '-1 day'))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []models.URLSource
	for rows.Next() {
		var src models.URLSource
		err := rows.Scan(&src.ID, &src.URL, &src.Name, &src.TvgID, &src.TvgLogo, &src.GroupTitle,
			&src.Catchup, &src.CatchupDays, &src.UserAgent, &src.RawAttributes, &src.SourceType, &src.LiveSourceID)
		if err != nil {
			t.log.Warn("扫描 url_sources 记录失败: %v", err)
			continue
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// testSingle 测试单个源并入库
func (t *Tester) testSingle(src models.URLSource) {
	t.progress.IncrementTested(src.Name)

	// 检查 IP 版本兼容性
	if !t.checkNetworkCompatible(src.URL) {
		t.recordFailure(src, "network_incompatible", nil)
		t.progress.IncrementFailed()
		return
	}

	// 执行 ffprobe 测试
	result := t.testWithFFprobe(src.URL, src.UserAgent.String)
	result.Name = src.Name
	result.URL = src.URL

	if result.Status != "success" {
		t.recordFailure(src, result.ErrorReason, nil)
		t.progress.IncrementFailed()
		return
	}
	t.progress.IncrementSuccess()

	// 归属地识别
	location, isp := "", ""
	if t.geo != nil {
		location = t.geo.GetLocation(src.URL)
		isp = t.geo.GetISP(src.URL)
	}

	// 分类匹配
	category := t.rulesMgr.DetermineCategory(src.Name)

	// 保存到 url_sources_passed
	passedID, err := t.savePassedSource(src, result, location, isp, category)
	if err != nil {
		t.log.Error("保存通过的源失败: %v", err)
		return
	}

	// 记录测试历史
	t.saveTestHistory(passedID, result)
}

// FFProbeResult 测试结果
type FFProbeResult struct {
	Status        string  `json:"status"`
	ResponseTime  int64   `json:"response_time"`
	Resolution    string  `json:"resolution"`
	Bitrate       int     `json:"bitrate"`
	VideoCodec    string  `json:"video_codec"`
	AudioCodec    string  `json:"audio_codec"`
	FrameRate     float64 `json:"frame_rate"`
	HasVideo      bool    `json:"has_video"`
	HasAudio      bool    `json:"has_audio"`
	DownloadSpeed float64 `json:"download_speed"`
	ErrorReason   string  `json:"error_reason,omitempty"`
	Name          string  `json:"-"`
	URL           string  `json:"-"`
}

func (t *Tester) testWithFFprobe(urlStr, userAgent string) FFProbeResult {
	result := FFProbeResult{Status: "failed"}

	timeout := time.Duration(t.cfg.Testing.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+2*time.Second)
	defer cancel()

	args := []string{"-v", "quiet", "-print_format", "json", "-show_streams", "-show_format"}
	if userAgent != "" {
		args = append(args, "-headers", "User-Agent: "+userAgent)
	}
	args = append(args, urlStr)

	start := time.Now()
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	output, err := cmd.Output()
	result.ResponseTime = time.Since(start).Milliseconds()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.ErrorReason = "timeout"
		} else {
			result.ErrorReason = fmt.Sprintf("ffprobe_error: %v", err)
		}
		return result
	}

	var data FFProbeOutput
	if err := json.Unmarshal(output, &data); err != nil {
		result.ErrorReason = "json_parse_error"
		return result
	}

	t.extractMetadata(&data, &result)
	if len(data.Streams) > 0 {
		result.Status = "success"
	} else {
		result.ErrorReason = "no_valid_streams"
	}
	return result
}

type FFProbeOutput struct {
	Streams []FFProbeStream `json:"streams"`
	Format  FFProbeFormat   `json:"format"`
}
type FFProbeStream struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	BitRate      string `json:"bit_rate"`
}
type FFProbeFormat struct {
	BitRate string `json:"bit_rate"`
}

func (t *Tester) extractMetadata(data *FFProbeOutput, result *FFProbeResult) {
	if data.Format.BitRate != "" {
		br, _ := strconv.Atoi(data.Format.BitRate)
		result.Bitrate = br / 1000
	}
	for _, stream := range data.Streams {
		switch stream.CodecType {
		case "video":
			result.HasVideo = true
			if result.Resolution == "" && stream.Width > 0 && stream.Height > 0 {
				result.Resolution = fmt.Sprintf("%dx%d", stream.Width, stream.Height)
			}
			result.VideoCodec = stream.CodecName
			if result.FrameRate == 0 && stream.AvgFrameRate != "" {
				parts := strings.Split(stream.AvgFrameRate, "/")
				if len(parts) == 2 {
					num, _ := strconv.ParseFloat(parts[0], 64)
					den, _ := strconv.ParseFloat(parts[1], 64)
					if den > 0 {
						result.FrameRate = num / den
					}
				}
			}
		case "audio":
			result.HasAudio = true
			result.AudioCodec = stream.CodecName
		}
	}
}

func (t *Tester) checkNetworkCompatible(urlStr string) bool {
	// 简单检查 IPv6
	if strings.Contains(urlStr, "[") && strings.Contains(urlStr, "]") {
		if !t.cfg.Network.IPv6Enabled {
			return false
		}
	}
	return true
}

func (t *Tester) savePassedSource(src models.URLSource, result FFProbeResult, location, isp, category string) (int64, error) {
	res, err := t.db.Exec(`INSERT OR REPLACE INTO url_sources_passed 
		(url, name, tvg_id, tvg_logo, group_title, catchup, catchup_days, user_agent, source_type, raw_attributes, live_source_id,
		status, response_time_ms, resolution, bitrate, video_codec, audio_codec, frame_rate, last_checked, test_status,
		location, isp, extra_attrs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		src.URL, src.Name, src.TvgID, src.TvgLogo, src.GroupTitle, src.Catchup, src.CatchupDays, src.UserAgent,
		src.SourceType, src.RawAttributes, src.LiveSourceID,
		"active", result.ResponseTime, result.Resolution, result.Bitrate, result.VideoCodec, result.AudioCodec,
		result.FrameRate, time.Now(), result.Status, location, isp, "{}")
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	// 关联分类
	if category != "" {
		var catID int64
		t.db.QueryRow(`SELECT id FROM categories WHERE name = ?`, category).Scan(&catID)
		if catID > 0 {
			t.db.Exec(`INSERT OR IGNORE INTO source_categories (source_id, category_id) VALUES (?, ?)`, id, catID)
		}
	}
	return id, nil
}

func (t *Tester) recordFailure(src models.URLSource, reason string, result *FFProbeResult) {
	t.db.Exec(`UPDATE url_sources SET updated_at = ? WHERE id = ?`, time.Now(), src.ID)
	// 可扩展记录到 test_history
}

func (t *Tester) saveTestHistory(sourceID int64, result FFProbeResult) {
	t.db.Exec(`INSERT INTO test_history (source_id, test_time, success, response_time_ms, resolution, bitrate, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sourceID, time.Now(), result.Status == "success", result.ResponseTime, result.Resolution, result.Bitrate, result.ErrorReason)
}

// GetCurrentProgress 返回当前进度管理器
func (t *Tester) GetCurrentProgress() *ProgressManager {
	return t.progress
}
