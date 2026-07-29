# =============================================================
# Dockerfile — Live Source Manager (Go 版)
# 多阶段构建 · 纯 Go (CGO_ENABLED=0) · 单二进制自包含
# 与 Python 版不同：本镜像无需 nginx / venv / uvicorn，
# 单一 Go 二进制同时承载「管理界面(23456)」与「文件发布(12345)」。
# =============================================================
# 本地构建:
#   docker build -t lsm-go:latest .
# 国内加速（构建参数覆盖 Go 代理）:
#   docker build --build-arg GOPROXY=https://goproxy.cn,direct -t lsm-go:latest .
# =============================================================

ARG GO_IMAGE=golang:1.23-bookworm

# ===== Stage 1: 编译（CGO 关闭，产出静态二进制）=====
FROM ${GO_IMAGE} AS builder

# 默认走国内代理；CI(GitHub Runner) 通过 --build-arg 改回官方源
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOPROXY=${GOPROXY} \
    GOSUMDB=off

WORKDIR /src

# 依赖层缓存
COPY go.mod go.sum ./
RUN go mod download

# 源码（含 web 模板 //go:embed 所需文件）
COPY . .

# 编译：去调试符号、内联版本信息
RUN go build -trimpath -ldflags '-s -w' -o /out/lsm .

# ===== Stage 2: 运行环境 =====
FROM debian:bookworm-slim

ENV TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    APP_DIR=/app \
    MANAGER_PORT=23456 \
    FILESHARE_PORT=12345 \
    HOST=0.0.0.0 \
    LSM_ADMIN_PASSWORD=""

LABEL maintainer="Live Source Manager <admin@example.com>" \
      description="Live Source Manager (Go) - self-contained IPTV source manager (SQLite + embedded web)" \
      version="1.0"

# 运行时依赖：CA 证书(HTTPS 采集/ GitHub API)、curl(健康检查)、tzdata
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        tzdata \
        procps \
        ffmpeg \
    && ln -snf /usr/share/zoneinfo/${TZ} /etc/localtime \
    && echo ${TZ} > /etc/timezone \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /app/data /app/www/output /app/log /app/config/sources /app/config/online

# FFmpeg 改用 Debian 官方仓库安装（已在上方 apt-get install 一并装好，路径 /usr/bin，在 PATH 内）
# 不再从 GitHub 下载静态包：构建稳定，且安装失败会直接令构建失败而非静默跳过。
# 双保险：软链 /app/tools/ffmpeg/{ffmpeg,ffprobe} -> /usr/bin
# 兼容程序「项目内 tools/ffmpeg 目录」查找逻辑，也方便用户挂载宿主二进制到该目录
RUN mkdir -p /app/tools/ffmpeg \
    && ln -sf /usr/bin/ffmpeg /app/tools/ffmpeg/ffmpeg \
    && ln -sf /usr/bin/ffprobe /app/tools/ffmpeg/ffprobe \
    && echo "FFmpeg ready: $(ffmpeg -version | head -1)"

# 二进制 + 分类词典 + 启动脚本
COPY --from=builder /out/lsm /app/lsm
COPY config/channel_rules.yml /app/config/channel_rules.yml
COPY start_docker.sh /start_docker.sh
COPY healthcheck.sh /healthcheck.sh

RUN chmod +x /app/lsm /start_docker.sh /healthcheck.sh \
    && echo "healthy" > /app/www/output/health \
    && chmod 644 /app/www/output/health

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD /healthcheck.sh

# 12345 = 文件发布端口 (HTTPServer.fileshare_port)
# 23456 = 管理界面端口 (HTTPServer.manager_port)
EXPOSE 12345 23456

WORKDIR /app
CMD ["/start_docker.sh"]
