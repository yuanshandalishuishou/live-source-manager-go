package collector

import (
	"bufio"
	"database/sql"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
)

// CollectFromURL 下载网络文件并解析存入 url_sources
func CollectFromURL(db *sql.DB, liveSourceID int64, url string, cfg *config.Config) {
	// 尝试多种下载方式（类似 DownloadDB 逻辑）
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		logDownloadError(db, liveSourceID, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logDownloadError(db, liveSourceID, url, nil)
		return
	}
	parseAndInsert(db, liveSourceID, resp.Body)
}

// CollectFromFile 解析本地文件
func CollectFromFile(db *sql.DB, liveSourceID int64, filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		logDownloadError(db, liveSourceID, filePath, err)
		return
	}
	defer f.Close()
	parseAndInsert(db, liveSourceID, f)
}

// parseAndInsert 解析 M3U/TXT 并插入 url_sources
func parseAndInsert(db *sql.DB, liveSourceID int64, r io.Reader) {
	scanner := bufio.NewScanner(r)
	var name string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#EXTINF:") {
			// 提取名称
			parts := strings.SplitN(line, ",", 2)
			if len(parts) == 2 {
				name = strings.TrimSpace(parts[1])
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			// 是 URL
			url := line
			// 插入数据库，忽略冲突
			_, err := db.Exec(`
                INSERT OR IGNORE INTO url_sources (live_source_id, url, name, source_type, created_at)
                VALUES (?, ?, ?, ?, ?)
            `, liveSourceID, url, name, "video", time.Now())
			if err != nil {
				// 记录错误
			}
			name = "" // 重置
		}
	}
}

func logDownloadError(db *sql.DB, liveSourceID int64, path string, err error) {
	// 可插入 system_log
}
