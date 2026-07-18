#!/usr/bin/env bash
# =============================================================================
# Live Source Manager (Go) — Linux 部署脚本
# =============================================================================
# 用法:
#   sudo bash deploy/setup_linux.sh [--project-dir /opt/live-source-manager] [--user www-data]
#
# 功能:
#   1. 构建二进制 bin/lsm（若 Go 工具链可用；否则要求已预编译好 bin/lsm）
#   2. 创建运行时目录（data / www/output / config/sources / config/online / log）
#   3. 将可写目录属主改为运行用户（默认 www-data）
#   4. 渲染 systemd 单元（替换 __PROJECT_DIR__）并 enable --now
#
# 相较 Python 版的大幅简化：无需创建 venv、无需装 nginx、无需 pip install。
# =============================================================================
set -euo pipefail

PROJECT_DIR=""
RUN_USER="www-data"

while [ $# -gt 0 ]; do
    case "$1" in
        --project-dir) PROJECT_DIR="$2"; shift 2 ;;
        --user)        RUN_USER="$2"; shift 2 ;;
        -h|--help)     sed -n '3,20p' "$0"; exit 0 ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

# 默认项目根：本脚本位于 <项目根>/deploy/，上一级即项目根
if [ -z "$PROJECT_DIR" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
fi

echo "=== Live Source Manager (Go) Linux 部署 ==="
echo "项目目录 : $PROJECT_DIR"
echo "运行用户 : $RUN_USER"

# ---- 1. 构建二进制 ----
BIN="$PROJECT_DIR/bin/lsm"
if [ -x "$BIN" ]; then
    echo "[build] 已存在二进制 $BIN，跳过构建"
else
    if command -v go >/dev/null 2>&1; then
        echo "[build] 使用 go 构建 linux/amd64 静态二进制 ..."
        mkdir -p "$PROJECT_DIR/bin"
        ( cd "$PROJECT_DIR" \
            && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
               go build -trimpath -ldflags '-s -w' -o bin/lsm . )
        if [ ! -x "$BIN" ]; then
            echo "[build] 构建失败：未生成 $BIN"; exit 1
        fi
        echo "[build] 构建完成: $BIN"
    else
        echo "[build] 未找到 go 工具链且 $BIN 不存在。"
        echo "        请先在本地执行: CGO_ENABLED=0 go build -o bin/lsm . 再拷入本项目，或安装 Go 后重试。"
        exit 1
    fi
fi

# ---- 2. 创建运行时目录 ----
echo "[dirs] 创建运行时目录 ..."
mkdir -p "$PROJECT_DIR/data" \
         "$PROJECT_DIR/www/output" \
         "$PROJECT_DIR/config/sources" \
         "$PROJECT_DIR/config/online" \
         "$PROJECT_DIR/log"

# ---- 3. 属主 ----
if id "$RUN_USER" >/dev/null 2>&1; then
    echo "[perms] 将可写目录属主设为 $RUN_USER ..."
    chown -R "$RUN_USER:$RUN_USER" \
        "$PROJECT_DIR/data" \
        "$PROJECT_DIR/www/output" \
        "$PROJECT_DIR/config/online" \
        "$PROJECT_DIR/log" 2>/dev/null || true
fi
chmod 755 "$BIN"

# ---- 4. 渲染并启用 systemd ----
UNIT_SRC="$PROJECT_DIR/deploy/live-source-web.service"
UNIT_DST="/etc/systemd/system/live-source-web.service"
if [ ! -f "$UNIT_SRC" ]; then
    echo "[systemd] 单元模板缺失: $UNIT_SRC"; exit 1
fi

echo "[systemd] 渲染单元 -> $UNIT_DST"
sed "s|__PROJECT_DIR__|$PROJECT_DIR|g" "$UNIT_SRC" > "$UNIT_DST"
chmod 644 "$UNIT_DST"

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
    systemctl enable --now live-source-web.service
    echo "[systemd] 已启用并启动 live-source-web.service"
    echo "[systemd] 查看状态: sudo systemctl status live-source-web"
    echo "[systemd] 查看日志: sudo journalctl -u live-source-web -f"
else
    echo "[systemd] 当前系统无 systemctl，跳过自启注册。"
    echo "        手动启动: sudo -u $RUN_USER $BIN --config-dir $PROJECT_DIR"
fi

echo "=== 部署完成 ==="
echo "管理界面: http://<本机IP>:23456/"
echo "文件发布: http://<本机IP>:12345/"
echo "默认管理员: admin（未设置 LSM_ADMIN_PASSWORD 时随机生成强密码，已打印到日志 ADMIN_PASSWORD_INITIALIZED=）"
echo "请尽快用该密码登录，或在 Web「用户管理」中修改；或部署前用环境变量 LSM_ADMIN_PASSWORD 指定。"
