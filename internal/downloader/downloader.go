package downloader

import (
	"io"
	"net/http"
	"os"
	"time"
)

// DownloadDB 尝试从 GitHub 下载数据库文件
func DownloadDB(destPath string) error {
	urls := []string{
		"https://github.com/your/repo/raw/main/live-source.db",
		"https://raw.githubusercontent.com/your/repo/main/live-source.db",
		"https://ghproxy.com/https://github.com/your/repo/raw/main/live-source.db",
		// 可添加更多带代理的尝试
	}
	client := http.Client{Timeout: 30 * time.Second}
	for _, u := range urls {
		resp, err := client.Get(u)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, resp.Body)
			if err == nil {
				return nil
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	return os.ErrNotExist
}
