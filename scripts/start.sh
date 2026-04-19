#!/bin/sh
set -e

# 确保目录存在
mkdir -p /config/online /data /www/output /log /var/www/hls

# 复制默认配置文件（如果不存在）
[ ! -f /config/config.ini ] && cp /app/configs/config.ini /config/
[ ! -f /config/channel_rules.yml ] && cp /app/configs/channel_rules.yml /config/

# 配置 Nginx（如果存在 nginx.conf）
if [ -f /app/configs/nginx.conf ]; then
    cp /app/configs/nginx.conf /etc/nginx/nginx.conf
fi

# 启动 Nginx
nginx -g "daemon off;" &

# 等待 Nginx 启动
sleep 2

# 启动主程序
exec /usr/local/bin/livesource-manager -config /config/config.ini
