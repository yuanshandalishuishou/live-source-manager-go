# live-source-manager-go

说明
依赖：代码使用了 github.com/mattn/go-sqlite3，需要先 go mod tidy。

完整性：部分函数（如 downloader.DownloadDB 中的 URL）需要您替换为实际的 GitHub 仓库地址。

配置传递：collector.CollectFromURL 和 tester.TestAllPending 需要传入 *config.Config，在 main.go 中调用时需传入 cfg。上面代码中有些地方传入了 nil，请自行修正。

解析逻辑：collector.parseAndInsert 仅处理了简单的 M3U 格式，实际可能需要更复杂的解析（如 #EXTINF 中的属性）。

去重：deduplicateURLSources 使用了简单的 DELETE，您可能需要更完善的策略（如保留最新等）。

定时任务：startScheduler 中的 cfg.TestInterval 需要从数据库加载，示例中已加载。

默认密码检查：仅演示了从 sys_config 读取 admin_password 项，实际需要在初始化时插入默认数据。

Web 管理界面：提供了极简的 HTML，您可以根据需要扩展，使用模板和静态文件。
