package collector

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/source"
)

// HotelScanner 酒店源扫描器
type HotelScanner struct {
	db         *db.DB
	parser     *source.Parser
	httpClient *http.Client
}

// NewHotelScanner 创建酒店源扫描器
func NewHotelScanner(database *db.DB, parser *source.Parser) *HotelScanner {
	return &HotelScanner{
		db:     database,
		parser: parser,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Scan 执行一次酒店源扫描任务
func (h *HotelScanner) Scan(ctx context.Context) error {
	configs, err := h.db.GetHotelScanConfigs()
	if err != nil {
		return fmt.Errorf("获取扫描配置失败: %w", err)
	}
	if len(configs) == 0 {
		logger.Info("没有启用的酒店扫描配置")
		return nil
	}

	for _, cfg := range configs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		logger.Info("开始扫描 IP 段", "range", cfg.IPRange, "port", cfg.Port, "path", cfg.Path)
		found, err := h.scanRange(ctx, cfg)
		if err != nil {
			logger.Error("扫描 IP 段失败", "range", cfg.IPRange, "error", err)
			continue
		}
		h.db.UpdateHotelScanStats(cfg.ID, found)
		logger.Info("扫描完成", "range", cfg.IPRange, "found", found)
	}
	return nil
}

// scanRange 扫描一个 CIDR 范围，发现并解析 M3U
func (h *HotelScanner) scanRange(ctx context.Context, cfg models.HotelScanConfig) (int, error) {
	ips, err := expandCIDR(cfg.IPRange)
	if err != nil {
		return 0, err
	}

	// 多路径探测（默认至少 3 个常见路径）
	paths := strings.Split(cfg.Path, ",")
	if len(paths) == 0 {
		paths = []string{"/iptv.m3u", "/tv.m3u", "/live.m3u"}
	}

	const maxConcurrency = 50
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	resultCh := make(chan []models.URLSource, len(ips))

	for _, ip := range ips {
		select {
		case <-ctx.Done():
			break
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer func() {
				<-sem
				wg.Done()
			}()
			entries := h.probeIP(ctx, ip, cfg.Port, paths)
			if len(entries) > 0 {
				resultCh <- entries
			}
		}(ip)
	}
	wg.Wait()
	close(resultCh)

	totalFound := 0
	for entries := range resultCh {
		// 标记来源为 "hotel_scan"
		for i := range entries {
			entries[i].LiveSourceID = 0 // 或特殊标记
			entries[i].GroupTitle = "酒店源"
		}
		_, err := h.db.BatchInsertURLSources(0, entries) // 需要修改 BatchInsertURLSources 支持 live_source_id=0 或创建虚拟订阅源
		if err != nil {
			logger.Warn("插入酒店源条目失败", "error", err)
		} else {
			totalFound += len(entries)
		}
	}
	return totalFound, nil
}

// probeIP 对单个 IP+端口探测所有路径，返回发现的源条目
func (h *HotelScanner) probeIP(ctx context.Context, ip string, port int, paths []string) []models.URLSource {
	addr := fmt.Sprintf("%s:%d", ip, port)

	// TCP 连通性测试
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil
	}
	conn.Close()

	var allEntries []models.URLSource
	for _, p := range paths {
		url := fmt.Sprintf("http://%s%s", addr, p)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := h.httpClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		data, err := readAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		// 快速检查是否为 M3U 格式
		if strings.HasPrefix(string(data), "#EXTM3U") {
			entries, err := h.parser.Parse(data)
			if err == nil {
				allEntries = append(allEntries, entries...)
			}
		}
	}
	return allEntries
}

// expandCIDR 展开 CIDR
func expandCIDR(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	var ips []string
	for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}
	return ips, nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func readAll(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, 2*1024*1024))
	return buf.Bytes(), err
}
