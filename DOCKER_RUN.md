# Docker 部署指南（Live Source Manager · Go 版）

本镜像为**单二进制自包含**：一个 `lsm` 进程同时提供管理界面（23456）与文件发布（12345），无需 Nginx / venv / uvicorn。

---

## 变更记录

> 仅记录影响部署 / 行为的关键变更，完整提交见 `git log`。

- **2026-08-16** — 16 项安全审计修复（commit `4cfe76d`）。
  CSRF 中间件保护、敏感字段掩码、代理认证、响应体大小限制(50MB/2MB)、M3U 属性转义、Cookie Secure 标志、logout CSRF 清理、EPG daily 补刷、配置默认值扩展至 84 键等。不影响部署流程，但提升了运行时安全性。
- **2026-07-18** — 默认管理员初始密码改为 `Admin@123`（commit `2f93ff0`）。
  `ensureAdmin` 在未设置 `WEB_ADMIN_PASSWORD` / `LSM_ADMIN_PASSWORD` 时，**不再随机生成**，改用固定默认 `Admin@123`（与历史 Python 部署习惯一致）；环境变量仍可覆盖，启动时仍打印 `ADMIN_PASSWORD_INITIALIZED=Admin@123`。
  **仅影响全新部署**（库内无用户时）。已存在用户不受影响，本地库已手动重置为 `Admin@123`。
- **2026-07-18** — 修复实时测试「有效频道恒为 0」（commit `f7ee063`）。
  `findBinary` 改用 `os.Executable()` 推导 ffmpeg/ffprobe 资源路径（不再依赖启动目录，避免服务态探测被拦截）；`probeFFprobe` 由 `-v quiet` 改 `-v error` 并新增 `bad_source` 错误分类；触发响应 `ffprobe_available` 改为真实探测值。实测有效频道 >0，失败原因分布正确归类。

---

## 一、镜像构建

### 本地构建

```bash
# 默认使用国内 Go 代理（goproxy.cn）
docker build -t lsm-go:latest .

# 指定代理
docker build --build-arg GOPROXY=https://goproxy.cn,direct -t lsm-go:latest .
```

### GitHub Actions 自动构建（GHCR）

推送到 `master` / `main` 分支（任意文件变更即触发，无 paths 过滤）或手动 `workflow_dispatch` 会触发
`.github/workflows/docker.yml`，自动构建并推送至 GHCR：

```
ghcr.io/yuanshandalishuishou/live-source-manager-go:latest
ghcr.io/yuanshandalishuishou/live-source-manager-go:master
ghcr.io/yuanshandalishuishou/live-source-manager-go:sha-<commit>
```

推送成功后自动将包设为 **public**（他人无需登录即可 `docker pull`）。
GitHub Runner 在境外，工作流会自动将构建期 Go 代理改回官方源
（`GOPROXY=https://proxy.golang.org,direct`）。

```bash
# 拉取（public）
docker pull ghcr.io/yuanshandalishuishou/live-source-manager-go:latest
```

---

## 二、目录与卷映射

容器内固定布局（`--config-dir /app`）：

| 容器内路径 | 用途 | 建议挂载 |
|------------|------|----------|
| `/app/data` | SQLite 数据库（配置/用户/会话/审计） | 必挂（持久化） |
| `/app/www/output` | M3U/TXT 播放列表输出 | 必挂 |
| `/app/log` | 日志（按 `Logging.max_size` MB / `Logging.backup_count` 自动轮转，不无限增长） | 建议挂 |
| `/app/config/sources` | 本地直播源文件（只读扫描） | 可选 ro |
| `/app/config/online` | 在线源下载目录（运行时写） | 可选 rw |
| `/app/config/channel_rules.yml` | 分类词典（镜像内已内嵌一份） | 可选覆盖 |

> 注意：镜像在 `/app/config/channel_rules.yml` 已内嵌一份词典。若挂载整个 `/app/config`
> 覆盖目录，请自行提供 `channel_rules.yml`，否则首启仅告警、不影响启动。

---

## 三、docker-compose 快速启动

```bash
# 1) 准备目录（首次）
mkdir -p data output logs sources online

# 2) 启动（可选：先建 .env 设置 LSM_ADMIN_PASSWORD 等）
docker-compose up -d --build

# 3) 查看日志
docker-compose logs -f

# 4) 停止
docker-compose down
```

`.env` 示例：

```dotenv
FILESHARE_PORT=12345
MANAGER_PORT=23456
HOST=0.0.0.0
LSM_ADMIN_PASSWORD=your_strong_password_here
GOPROXY=https://goproxy.cn,direct

DATA_DIR=./data
OUTPUT_DIR=./output
LOG_DIR=./logs
SOURCES_DIR=./sources
ONLINE_DIR=./online
```

访问：

- 管理界面：<http://localhost:23456/>
- 文件发布：<http://localhost:12345/>

---

## 四、纯 docker run

```bash
docker run -d --name lsm-go \
  -p 12345:12345 -p 23456:23456 \
  -e LSM_ADMIN_PASSWORD=your_password \
  -e TZ=Asia/Shanghai \
  -v "$PWD/data:/app/data" \
  -v "$PWD/output:/app/www/output" \
  -v "$PWD/log:/app/log" \
  -v "$PWD/sources:/app/config/sources:ro" \
  --restart unless-stopped \
  lsm-go:latest
```

---

## 五、健康检查与运维

| 操作 | 命令 |
|------|------|
| 容器健康检查 | 内置 `HEALTHCHECK` 探测 `http://localhost:23456/api/health` |
| 手动健康探测 | `docker exec lsm-go /healthcheck.sh` |
| 查看日志 | `docker logs -f lsm-go` |
| 进入容器 | `docker exec -it lsm-go sh` |
| 重启 | `docker restart lsm-go` |

`docker ps` 中 STATUS 显示 `healthy` 即服务正常。

---

## 六、端口说明

| 端口 | 服务 | 对应配置 |
|------|------|----------|
| 23456 | 管理 Web 界面（登录/仪表盘/配置/…） | `HTTPServer.manager_port` |
| 12345 | 文件发布（M3U/TXT 静态下载） | `HTTPServer.fileshare_port` |

可通过环境变量 `MANAGER_PORT` / `FILESHARE_PORT`（compose）或 `--manager-port` / `--fileshare-port`（二进制）覆盖。

---

## 七、ffmpeg / ffprobe（可选）

镜像通过 Debian 官方仓库 `apt-get install ffmpeg` 安装（见 `Dockerfile` 运行阶段 `apt-get install ... ffmpeg`），
安装失败将**直接令镜像构建失败**（无静默跳过），因此生产镜像必定自带 ffmpeg/ffprobe
（位于 `/usr/bin`，已在 `PATH` 内）。为兼容程序「项目内 `tools/ffmpeg` 目录」查找逻辑，
并方便挂载宿主二进制，镜像内额外软链 `/app/tools/ffmpeg/{ffmpeg,ffprobe} -> /usr/bin`。
（与早期「从 GitHub 下载静态包、失败仅告警跳过」的方案不同：构建期更稳定，且不再有「探测功能缺失」的坑。）

宿主机已装 ffmpeg 时，也可在 `docker run` 时挂载复用：

```bash
docker run -d --name lsm-go \
  -p 12345:12345 -p 23456:23456 \
  -v "$PWD/data:/app/data" -v "$PWD/output:/app/www/output" \
  -v /usr/local/bin/ffprobe:/usr/local/bin/ffprobe:ro \
  -v /usr/local/bin/ffmpeg:/usr/local/bin/ffmpeg:ro \
  lsm-go:latest
```

---

## 八、首次登录

默认管理员：`admin`，密码在**首次启动且未设置 `LSM_ADMIN_PASSWORD` 时默认为 `Admin@123`**（与历史 Python 部署习惯一致），并打印到容器/进程启动日志（形如 `ADMIN_PASSWORD_INITIALIZED=Admin@123`）。部署时建议通过 `LSM_ADMIN_PASSWORD` 覆盖，或首次登录后在「配置中心 → 密码管理」修改。

若设置了 `LSM_ADMIN_PASSWORD`，则使用该密码（向后兼容旧变量 `LSM_ADMIN_PASSWORD`；Python 版变量名为 `WEB_ADMIN_PASSWORD`，二者等价）。

**强烈建议**通过环境变量 `LSM_ADMIN_PASSWORD` 指定强密码（部署时即设定，避免随机密码丢失），或在 Web「用户管理」中修改。
