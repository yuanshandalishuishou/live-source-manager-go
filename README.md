# Live Source Manager (Go 版)

https://zread.ai/yuanshandalishuishou/live-source-manager-go

**Live Source Manager** 是一个用 Go 语言编写的高性能 IPTV 直播源自动聚合、测速、筛选与管理工具。它可以自动从订阅源、本地文件中拉取直播源，通过 ffprobe 检测可用性与质量，智能分类、去重，最终生成高质量的 M3U/TXT 播放列表。同时内置 Web 管理界面，支持可视化管理源、订阅、分类、配置和日志。

> 本项目参考了 [Guovin/iptv-api](https://github.com/Guovin/iptv-api) 的优秀设计，并在此基础上增加了 Web 管理界面、SQLite 持久化、用户认证等功能，采用 Go 语言实现，具有更低的资源占用和更简单的部署方式。

---

## ✨ 主要功能

- **多源聚合**：支持本地 M3U/TXT 文件、在线订阅源、GitHub 源，自动下载并解析。
- **智能测试**：使用 ffprobe 检测流可用性，获取分辨率、码率、编码格式等信息。
- **质量筛选**：可配置延迟、分辨率、比特率等过滤条件，自动剔除无效和低质量源。
- **频道分类**：基于关键词和正则表达式的智能分类引擎，支持自定义分类规则和频道别名。
- **归属地优选**：集成纯真 IP 数据库，识别源服务器归属地和运营商，优先选择同城/同运营商源。
- **RTMP 推流**：支持将直播源推送到本地 Nginx-RTMP 服务器，生成 HLS 流，提升弱网环境下的播放体验。
- **EPG 支持**：自动下载 XMLTV 格式的电子节目单，生成 EPG XML 文件，支持频道自动关联。
- **黑白名单**：支持 URL/频道名的正则黑白名单过滤。
- **Web 管理界面**：内置 Web 后台，支持源管理、订阅管理、分类配置、显示规则、系统配置、日志查看和播放列表预览。
- **用户认证**：JWT 认证，支持多用户和权限控制。
- **定时任务**：支持 cron 表达式定时自动更新，无需人工干预。
- **跨平台**：纯 Go 实现，支持 Linux、Windows、macOS 等主流操作系统。

---

## 🚀 快速开始

### 方式一：下载预编译二进制（推荐）

从 [Releases](https://github.com/yuanshandalishuishou/live-source-manager-go/releases) 页面下载对应平台的二进制文件。

```bash
# Linux (amd64)
wget https://github.com/yuanshandalishuishou/live-source-manager-go/releases/latest/download/live-source-manager_linux_amd64_$(date +%Y%m%d)
chmod +x live-source-manager_linux_amd64_*
./live-source-manager_linux_amd64_* -config ./configs/config.ini

# Windows (amd64)
# 下载 live-source-manager_windows_amd64_*.exe 后双击运行
方式二：Docker 部署
bash
# 克隆仓库
git clone https://github.com/yuanshandalishuishou/live-source-manager-go.git
cd live-source-manager-go

# 构建并运行
docker build -t live-source-manager .
docker run -d \
  --name live-source-manager \
  -p 12345:12345 \   # 播放列表 HTTP 服务
  -p 23456:23456 \   # Web 管理界面
  -p 1935:1935 \     # RTMP 服务
  -p 8080:8080 \     # HLS 服务
  -v $(pwd)/config:/config \
  -v $(pwd)/data:/data \
  -v $(pwd)/output:/www/output \
  live-source-manager
方式三：从源码编译
前置要求：Go 1.21+、ffmpeg（含 ffprobe）

bash
git clone https://github.com/yuanshandalishuishou/live-source-manager-go.git
cd live-source-manager-go

# 下载依赖
go mod tidy

# 编译
make build

# 运行
./bin/livesource-manager -config ./configs/config.ini
📦 项目结构
text
live-source-manager-go/
├── cmd/manager/          # 主程序入口
├── internal/
│   ├── config/           # 配置管理
│   ├── db/               # 数据库初始化
│   ├── models/           # 数据模型定义
│   ├── rules/            # 频道分类规则引擎
│   ├── source/           # 源下载与解析
│   ├── geo/              # IP 归属地识别
│   ├── tester/           # 流测试器
│   ├── filter/           # 筛选与黑白名单
│   ├── generator/        # M3U/TXT 生成器
│   ├── rtmp/             # RTMP 推流管理
│   ├── epg/              # EPG 管理
│   └── web/              # Web 服务
├── pkg/
│   ├── logger/           # 日志封装
│   └── utils/            # 通用工具
├── configs/              # 默认配置文件
├── web/
│   ├── templates/        # HTML 模板
│   └── static/           # 静态资源
├── scripts/              # 辅助脚本
├── Dockerfile
├── Makefile
├── go.mod
└── README.md
⚙️ 配置说明
主配置文件位于 /config/config.ini，主要配置项包括：

配置节	说明
[Sources]	本地源目录、在线订阅 URL、GitHub 源
[Network]	代理设置、IPv6 支持、IP 版本偏好
[Testing]	测试超时、并发数、速度测试参数
[Output]	输出文件名、分组方式、每频道最大源数
[Filter]	延迟/分辨率/码率过滤条件、归属地/运营商偏好
[EPG]	EPG 更新间隔、EPG 源列表
[RTMP]	RTMP 推流开关、端口、空闲超时
[WebServer]	Web 管理界面端口、认证开关
[System]	定时任务 cron 表达式
首次运行时会自动在 ./db/ 目录创建 SQLite 数据库，并在 /config/ 目录生成默认配置文件。

🌐 Web 管理界面
启动后访问 http://localhost:23456 即可打开 Web 管理界面。

默认账号：admin / admin@1234（首次登录后建议修改）

主要功能页面：

仪表盘：查看系统统计信息，手动触发更新，查看测试进度

源管理：查看所有已测试的直播源，支持搜索、筛选、启用/禁用

订阅管理：管理在线订阅源，支持增删改查

分类管理：自定义频道分类，配置关键词/正则匹配规则

显示规则：配置播放列表的分组、排序、数量限制

系统配置：Web 界面编辑所有配置项

日志查看：实时查看系统日志，支持按级别筛选

预览：预览生成的 M3U/TXT 播放列表内容

🔧 主要模块说明
源管理器 (internal/source)
负责从配置的订阅 URL 和本地目录下载 M3U/TXT 文件，解析 #EXTINF 标签提取频道名、台标、分组等信息，并应用正则别名进行频道名标准化。

流测试器 (internal/tester)
使用 ffprobe 并发测试每个源的可用性，获取分辨率、码率、编码格式等元数据。测试进度通过 WebSocket 实时推送到 Web 界面。

归属地识别 (internal/geo)
集成纯真 IP 数据库，解析源服务器 IP 的地理位置（省份、城市）和运营商信息，供筛选器进行智能优选。

筛选器 (internal/filter)
执行黑白名单过滤、质量指标筛选（延迟、分辨率、码率），并根据归属地/运营商偏好对同一频道的多个源进行排序，最终保留质量最好的若干源。

生成器 (internal/generator)
根据显示规则将筛选后的源按分类分组、排序，生成标准的 M3U 和 TXT 播放列表文件，并添加 EPG URL、台标、回放等扩展标签。

RTMP 管理器 (internal/rtmp)
管理 FFmpeg 推流进程，将选定的直播源推送到 Nginx-RTMP 服务器，生成 HLS 播放地址，并支持空闲检测自动停止推流。

EPG 管理器 (internal/epg)
从配置的 XMLTV 源下载电子节目单，解析后存入数据库，并生成标准 epg.xml 文件供播放器使用，同时自动关联频道与 EPG ID。

🔄 自动编译与发布
本项目使用 GitHub Actions 实现代码推送后自动编译和发布。工作流配置文件位于 .github/workflows/build.yml，使用 GoReleaser 进行多平台交叉编译。

版本号采用当日日期（格式 YYYYMMDD），每次推送到 main 分支都会自动构建 Linux/Windows 的 amd64/arm64 版本，并发布到 Releases 页面。

📋 依赖项
Go 1.21+：编译运行

SQLite 3：数据存储（已内嵌）

FFmpeg（含 ffprobe）：流测试和 RTMP 推流

Nginx（含 RTMP 模块）：HTTP 文件服务和 RTMP 推流（Docker 版内置）

🤝 贡献
欢迎提交 Issue 和 Pull Request！

Fork 本仓库

创建特性分支 (git checkout -b feature/AmazingFeature)

提交更改 (git commit -m 'Add some AmazingFeature')

推送到分支 (git push origin feature/AmazingFeature)

开启 Pull Request

📄 许可证
本项目采用 AGPL-3.0 许可证。请注意，本项目的部分设计思路参考了 Guovin/iptv-api（同样采用 AGPL-3.0），使用时请遵守相关协议要求。

🙏 致谢
Guovin/iptv-api - 提供了优秀的设计思路和功能参考

纯真 IP 数据库 - 提供 IP 归属地数据

Gin - Go Web 框架

go-ini - INI 配置解析
