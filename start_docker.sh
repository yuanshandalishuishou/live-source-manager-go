#!/bin/bash
# =============================================================
# 直播源管理工具 (Go 版) Docker 启动脚本
# 功能: 目录准备 → 初始化数据库/默认值/分类词典/管理员 → 前台启动服务
# 用途: Docker 容器入口点（CMD ["/start_docker.sh"]）
# 设计要点（相较 Python 版的大幅简化）:
#   - 单二进制自包含，无 nginx / venv / uvicorn
#   - 二进制在启动时已幂等完成「建库 + 灌默认值 + 导入词典 + 建管理员」
#   - 仍显式跑一次 --init-db，保证首启数据就绪后再进入常驻服务
# =============================================================

set -euo pipefail

APP_DIR="${APP_DIR:-/app}"
BIN="${APP_DIR}/lsm"

echo "=== 直播源管理工具启动脚本 (Go 版 / SQLite) ==="

# 确保必要目录存在
mkdir -p "${APP_DIR}/data" "${APP_DIR}/www/output" "${APP_DIR}/log" \
         "${APP_DIR}/config/sources" "${APP_DIR}/config/online"

# 管理员密码（空则二进制默认 admin123456 并告警，请尽快修改）
export LSM_ADMIN_PASSWORD="${LSM_ADMIN_PASSWORD:-}"

# 可选端口 / 监听地址覆盖（通过环境变量传参给二进制）
ARGS=(--config-dir "${APP_DIR}")
if [ -n "${MANAGER_PORT:-}" ]; then
    ARGS+=(--manager-port "${MANAGER_PORT}")
fi
if [ -n "${FILESHARE_PORT:-}" ]; then
    ARGS+=(--fileshare-port "${FILESHARE_PORT}")
fi
if [ -n "${HOST:-}" ]; then
    ARGS+=(--host "${HOST}")
fi

echo "[init] 初始化数据库与默认值 (--init-db) ..."
"${BIN}" --config-dir "${APP_DIR}" --init-db || {
    echo "[init] 数据库初始化失败，启动中止"
    exit 1
}
echo "[init] 初始化完成"

echo "[run] 启动服务: ${BIN} ${ARGS[*]}"
echo "[run] 管理界面: http://<容器IP>:${MANAGER_PORT:-23456}/"
echo "[run] 文件发布: http://<容器IP>:${FILESHARE_PORT:-12345}/"

# exec 让 Go 进程直接接管 PID 1，便于接收 SIGTERM 优雅退出
exec "${BIN}" "${ARGS[@]}"
