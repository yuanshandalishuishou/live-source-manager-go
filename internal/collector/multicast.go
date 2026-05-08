package collector

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// MulticastScanner 组播源扫描器
type MulticastScanner struct {
	db *db.DB
}

// NewMulticastScanner 创建组播扫描器
func NewMulticastScanner(database *db.DB) *MulticastScanner {
	return &MulticastScanner{db: database}
}

// Scan 执行一次组播扫描
func (m *MulticastScanner) Scan(ctx context.Context) error {
	configs, err := m.db.GetMulticastConfigs()
	if err != nil {
		return fmt.Errorf("获取组播配置失败: %w", err)
	}
	if len(configs) == 0 {
		return nil
	}

	for _, cfg := range configs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		logger.Info("扫描组播", "interface", cfg.Interface, "address", cfg.Address)
		entries, err := m.listenAndParse(ctx, cfg)
		if err != nil {
			logger.Error("组播扫描失败", "address", cfg.Address, "error", err)
			continue
		}
		if len(entries) > 0 {
			for i := range entries {
				entries[i].GroupTitle = "组播源"
			}
			_, err := m.db.BatchInsertURLSources(0, entries)
			if err != nil {
				logger.Warn("插入组播源失败", "error", err)
			}
		}
		m.db.UpdateMulticastScanStats(cfg.ID)
	}
	return nil
}

// listenAndParse 加入组播组监听数据包，对接收的流使用 ffprobe 分析
func (m *MulticastScanner) listenAndParse(ctx context.Context, cfg models.MulticastConfig) ([]models.URLSource, error) {
	// 解析组播地址
	addr, err := net.ResolveUDPAddr("udp", cfg.Address)
	if err != nil {
		return nil, err
	}

	// 绑定到指定网卡（需要获取接口地址）
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenMulticastUDP("udp", iface, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) // 监听 10 秒

	buf := make([]byte, 188*7) // TS 包
	var packets [][]byte
	for {
		select {
		case <-ctx.Done():
			break
		default:
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		packet := make([]byte, n)
		copy(packet, buf[:n])
		packets = append(packets, packet)
	}

	if len(packets) == 0 {
		return nil, nil
	}

	// 如果收到包，则尝试用 ffprobe 分析（这里简化：直接构造 UDP URL）
	sourceURL := fmt.Sprintf("udp://@%s", cfg.Address)
	// 实际应当分析 RTP/TS 流中的节目信息，这里仅生成一个条目假设
	entry := models.URLSource{
		URL:        sourceURL,
		Name:       fmt.Sprintf("组播-%s", cfg.Address),
		SourceType: "multicast",
	}
	return []models.URLSource{entry}, nil
}
