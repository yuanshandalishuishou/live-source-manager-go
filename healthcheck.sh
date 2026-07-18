#!/bin/sh
# =============================================================
# 健康检查脚本 — Live Source Manager (Go 版)
# 探测管理界面 /api/health 端点（返回 {"status":"ok"} 即健康）
# 与 Python 版不同：无 Nginx，直接探测 Go 二进制自带的健康端点。
# =============================================================
PORT="${MANAGER_PORT:-23456}"

if ! curl -sf "http://localhost:${PORT}/api/health" > /dev/null 2>&1; then
    echo "management service not healthy on port ${PORT}"
    exit 1
fi

echo "healthy"
exit 0
