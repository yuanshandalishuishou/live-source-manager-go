# Live Source Manager (Go 版)

> 将 [live-source-manager](https://github.com/yuanshandalishuishou/live-source-manager)（Python + FastAPI + Nginx + SQLite）完整重写为 **纯 Go 单二进制实现**，功能 1:1 对齐，部署更轻量。

本工具用于采集、测试、分类、聚合 IPTV 直播源，并生成可播放的 M3U / TXT 播放列表。

- **单二进制自包含**：无需 Python、venv、Nginx、uvicorn。一个 `lsm` 进程同时提供「管理界面（默认 23456）」与「文件发布（默认 12345）」。
- **纯 Go SQLite**：使用 `modernc.org/sqlite`，`CGO_ENABLED=0`，无 C 依赖，跨平台静态编译。
- **Web 管理界面**：登录 / 仪表盘 / 源采集 / 分类规则 / 配置中心 / 系统 / 实时测速 / 用户 / 日志 / 审计。
- **实时测速**：基于 `ffprobe`（可选）并发探测，WebSocket / SSE 推送进度。
- **定时自动执行**：内置调度器，支持「间隔」与「每日定时」两种模式（对应 Python 版的 cron）。
- **三平台部署**：Docker / docker-compose、Linux systemd、Windows 任务计划程序。

---

## 功能对照（与原 Python 版）

| 模块 | Python 版 | Go 版 |
|------|-----------|-------|
| 配置存储 | SQLite `app_config` | ✅ SQLite `app_config`（一致） |
| 分类词典 | `config/channel_rules.yml` | ✅ 同名 YAML 导入 |
| 源采集 | 本地 / 在线 / GitHub | ✅ 一致，并发 + 超时 + 取消 |
| M3U/TXT 解析 | ✅ | ✅ 含 URL 门禁（`is_static_safe`） |
| 分类规则引擎 | ✅ | ✅ 关键词规则 + 频道映射 |
| 流测试 | ffprobe/ffmpeg | ✅ 同（ffprobe 可选） |
| M3U 生成 | ✅ | ✅ 含媒体类型分组（收音机/在线音频）+ EPG 注入 |
| EPG 节目单 | ✅ | ✅ url-tvg + tvg-id/tvg-logo/tvg-region，XMLTV 导出 epg.xml.gz |
| Web UI | FastAPI + Jinja2 | ✅ `net/http` + `html/template` + `//go:embed` |
| 认证 | Session + CSRF | ✅ Session Cookie + `X-CSRF-Token` |
| 实时测速 | WebSocket | ✅ SSE + 轮询兜底 |
| 定时执行 | cron | ✅ 内置调度器（间隔/每日） |
| 部署 | Docker / Linux / Windows | ✅ 三平台一致 |

---

## 目录结构

```
live-source-manager-go/
├── main.go                  # 入口：开库/灌默认值/导入词典/建管理员/起服务
├── go.mod / go.sum
├── internal/                # 全部业务逻辑
│   ├── config/              # 配置读写（SQLite 单一事实来源）
│   ├── db/                  # SQLite 连接、建表、默认种子、用户/会话/审计
│   ├── security/            # URL 安全门禁 is_static_safe
│   ├── source/              # 源采集（本地/在线/GitHub）并发+超时
│   ├── streamtest/          # ffprobe 流测试
│   ├── m3u/                 # M3U/TXT 生成
│   ├── rules/               # 分类规则引擎 + 词典种子
│   ├── manager/             # 流水线编排 + UI 频道缓存 + 调度器
│   ├── auth/                # bcrypt / 会话
│   ├── web/                 # 路由、中间件、处理器、嵌入模板（internal/web/）
│   └── ...
├── internal/web/            # 前端模板与静态资源（被 //go:embed 打包进二进制）
├── config/
│   └── channel_rules.yml    # 分类受控词表（词典种子源）
├── deploy/                  # Linux systemd + Windows 自启脚本
├── Dockerfile / docker-compose.yml / .dockerignore
├── start_docker.sh / healthcheck.sh
└── .github/workflows/docker.yml   # GHCR 自动构建推送
```

---

## 快速开始（二进制）

### 1. 从源码构建

```bash
# 需要 Go 1.22+（推荐 1.23）
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o bin/lsm .
```

### 2. 初始化并运行

```bash
# 初始化数据库/默认值/词典/管理员（幂等，可单独执行）
./bin/lsm --config-dir . --init-db

# 常驻运行（默认 23456 管理、12345 发布）
./bin/lsm --config-dir .
```

首次启动若未设置 `LSM_ADMIN_PASSWORD`，默认管理员密码为 **`Admin@123`**（与历史 Python 部署习惯一致），并打印到启动日志（形如 `ADMIN_PASSWORD_INITIALIZED=Admin@123`）。**建议首次登录后即在「配置中心 → 密码管理」修改，或部署时直接设定 `LSM_ADMIN_PASSWORD`**。机制上 Go 版（`LSM_ADMIN_PASSWORD`）与 Python 版（`WEB_ADMIN_PASSWORD`）一致：环境变量优先、空则回退默认；仅初始默认值不同（Go=`Admin@123`，Python=随机生成）。

### 3. 访问

- 管理界面：<http://localhost:23456/>
- 文件发布：<http://localhost:12345/>

### 常用参数

| 参数 | 说明 | 默认 |
|------|------|------|
| `--config-dir` | 配置/数据根目录（含 `config/`、`data/`、`www/output`） | `.` |
| `--db` | SQLite 路径（默认 `<config-dir>/data/app.db`） | 自动 |
| `--host` | 监听地址 | `0.0.0.0` |
| `--manager-port` | 管理端口 | `23456` |
| `--fileshare-port` | 发布端口 | `12345` |
| `--init-db` | 仅初始化并退出 | 否 |
| `--log-file` | 日志文件路径 | 读配置 `Logging.file` |

环境变量：`LSM_ADMIN_PASSWORD`（管理员密码）。

---

## ffmpeg / ffprobe（实时测速依赖）

「实时测速」基于 `ffprobe`（可选，推荐安装）并发探测直播源可达性。**未安装时**：系统信息页会显示 ffprobe 不可用，触发实时测试会立即报错提示，但 Web / SQLite / 文件发布服务不受影响。

> ⚠️ **ffmpeg / ffprobe 二进制不入库**：`tools/ffmpeg/*.exe`（约 180MB）已被 `.gitignore` 忽略，仓库不携带。部署时需按下列方式获取。

### 获取方式（任选其一）

1. **同机已有 Python 版（最省事）**：程序会自动从兄弟目录 `../live-source-manager/tools/ffmpeg` 发现，无需额外操作。
2. **源码 / 二进制直接部署（Windows）**：把 `ffprobe.exe` + `ffmpeg.exe` 放入本项目的 `tools/ffmpeg/` 目录（程序启动时会优先扫描该目录）。
3. **Linux 源码部署**：`apt-get install -y ffmpeg`（进入 `PATH`，程序自动命中）。
4. **环境变量 / 配置显式指定**（适用于自定义安装路径）：
   - `LSM_FFPROBE_PATH` / `LSM_FFMPEG_PATH`：直接指向二进制完整路径
   - `LSM_FFMPEG_DIR`：指向含 ffprobe/ffmpeg 的目录
   - 配置项 `Tools.ffmpeg_dir`（Web「配置中心」→ 工具）
5. **Docker 部署**：镜像构建期自动下载 ffmpeg/ffprobe 到 `/usr/local/bin`（下载失败仅告警跳过），或运行时挂载宿主机的 ffmpeg。详见 [DOCKER_RUN.md 第七章](./DOCKER_RUN.md#七ffmpeg--ffprobe可选)。

### 验证

登录后在「系统」页查看 `ffprobe` 是否可用（显示路径即正常）。点击「实时测试 → 开始测试」前程序会校验 ffprobe 是否存在，缺失则给出明确中文提示。

---

## 配置中心

所有配置存于 SQLite `app_config` 表（单一事实来源），可在 Web「配置中心」修改并即时生效。关键分组：

- `HTTPServer`：`manager_port` / `fileshare_port` / `document_root`
- `Sources`：`local_dirs` / `online_urls` / `github_sources`
- `Testing`：`timeout` / `concurrent_threads` / `auto_scan_enabled` / `auto_scan_mode` / `auto_scan_interval_hours` / `auto_scan_daily_time`
- `Output`：`filename` / `output_dir`
- `Logging`：`level` / `file`

### 定时自动执行（调度器）

对应 Python 版 cron。在「配置中心 → 测试」中设置：

- `auto_scan_enabled`：开启后内置调度器按周期跑完整流水线（采集→测试→生成）。
- `auto_scan_mode`：`interval`（每 N 小时）或 `daily`（每日指定 HH:MM）。
- 调度器随进程启动，遵循 `context` 超时（单次运行 30 分钟上限），崩溃不影响主服务。

---

## 输出与 EPG（live.m3u / 媒体类型分组 / 去重）

Go 版与 Python 版在「生成播放列表」环节已逐轮对齐，确保 `live.m3u` 同时包含**检测有效的电视节目、收音机节目、其他节目**，并附带 EPG 节目单信息。

### 生成流水线

```
采集(本地/在线/GitHub) → 分类(规则引擎+频道映射) → [可选] ffprobe 测速 → 生成 live.m3u + epg.xml.gz
```

- 默认端口 12345 发布的 `www/output/live.m3u` 由上述流水线产出；开启「定时自动执行」后周期性刷新。
- 未开启测速时，`Generate()` 会跳过 ffprobe、直接产出全部已采集频道（无论可达性），便于快速生成全量列表。

### 媒体类型分组（收音机 / 在线音频）

对齐 Python `enhanced_group_and_sort_sources`：

- **电视节目（video）**：按 `content` 维度分组（如「央视」「卫视」「地方」等），空值回落「其他」。
- **收音机（radio）**：独立成组 `收音机`。
- **在线音频（audio）**：独立成组 `在线音频`。

判定逻辑（端口自 Python `classify_media_type` / `_refine_audio_type`）：

1. 以 **ffprobe 实测的 `has_video_stream`** 为准：纯音频流（无视频流）按频道名关键词细分 radio/audio；未测速的频道缺省视为有视频流 → `video`（与 Python 一致）。
2. 极低分辨率（宽或高 < 100）视频按音频细分。
3. 收音机关键词：`radio`/`广播`/`电台`/`fm`/`am`/`交通广播`/`音乐广播`/`新闻广播`/`经济广播`/`文艺广播`/`都市广播`/`农村广播`；其余音频关键词：`music`/`音乐`/`歌曲`/`mtv`/`演唱会`/`音乐会`/`有声`/`听书`/`相声`/`小品`/`朗诵`/`配音`/`音效`/`asmr`/`播客`；均未命中则默认 `audio`。
4. 因此**要先看到收音机/在线音频分组，需开启测速**（ffprobe 能识别出纯音频流）。仅采集不测速时一律归为 `video`。

> `#EXTINF` 行会写入 `media-type="video|audio|radio"` 便于播放器识别。

### EPG 节目单注入

对齐 Python `generate_enhanced_m3u` 的 EPG 注入：

- **`#EXTM3U` 头**：当 EPG 总开关与注入开关同时为 `True`、且能推导出外链地址时，注入 `url-tvg` 与 `x-tvg-url`（双属性兼容 DIYP/Kodi/TiviMate）。
- **每频道属性**：`tvg-id` / `tvg-logo` 优先使用 EPG 频道对齐结果（`channel_name_mapping`），未命中则按频道名生成占位 id；省级维度注入 `tvg-region`。
- **节目单文件**：`epg.Manager` 抓取并归一化对齐后原子导出 `epg.xml.gz`，外链地址由 `GetEPGURL` 推导。

### 去重（按原始 URL）

采集与实时测速**均按原始流地址（url）去重**，对齐 Python `dedup_sources_by_url`：

- 保留首次出现，同名不同源 / 同源不同名均不会被错误合并或误删。
- 实时测速前同样按 url 去重，仅对唯一地址发起一次探测（Web「实时测试」页会显示「剔除重复 N」）。

---

## 部署

### Docker / docker-compose

```bash
# 构建并后台启动
docker-compose up -d --build

# 仅服务（已构建镜像）
docker run -d --name lsm \
  -p 12345:12345 -p 23456:23456 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/output:/app/www/output" \
  -v "$PWD/log:/app/log" \
  -v "$PWD/sources:/app/config/sources:ro" \
  -e LSM_ADMIN_PASSWORD=your_password \
  lsm-go:latest
```

详见 [DOCKER_RUN.md](./DOCKER_RUN.md)。

### Linux (systemd)

```bash
sudo bash deploy/setup_linux.sh --project-dir /opt/live-source-manager
sudo systemctl status live-source-web
```

脚本会自动构建二进制、建目录、改属主、注册并启动 `live-source-web.service`。

### Windows (任务计划程序)

```powershell
# 以管理员运行：创建 SYSTEM 级开机自启任务
powershell -ExecutionPolicy Bypass -File deploy/windows/install-autostart.ps1
# 启动
Start-ScheduledTask -TaskName LiveSourceManagerWeb
```

非管理员运行时降级为「登录时」自启。

---

## 健康检查

- 管理界面：`GET /api/health` → `{"status":"ok"}`
- Docker：`/healthcheck.sh` 探测 23456 端口的 `/api/health`
- 文件发布：静态文件服务（M3U/TXT），无独立健康端点

---

## 与 Python 版的主要差异（部署视角）

1. **无 Nginx**：Go 二进制自带静态文件服务（12345），不再需要反向代理。
2. **无 venv / pip**：单二进制，镜像体积更小，启动更快。
3. **定时任务内置**：用 Go 调度器替代系统 cron，配置集中在 Web UI。
4. **词典位置**：`config/channel_rules.yml` 由二进制在启动时幂等导入 SQLite，容器镜像已内嵌一份。
5. **EPG / 去重 / 媒体类型分组已与 Python 对齐**：`live.m3u` 同样产出检测有效的电视/收音机/其他节目并附带 EPG（url-tvg + tvg-id/logo/region、XMLTV 导出）；采集与测速均按原始 URL 去重；收音机/在线音频独立分组由 ffprobe 的 `has_video_stream` 驱动（逻辑端口自 Python `classify_media_type`）。

---

## 许可证

同上游项目。
