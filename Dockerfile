# 构建阶段
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o livesource-manager ./cmd/manager

# 运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata ffmpeg nginx nginx-mod-rtmp
RUN mkdir -p /var/www/hls /var/www/output /config /data /log

COPY --from=builder /app/livesource-manager /usr/local/bin/
COPY configs/ /config/
COPY web/ /app/web/
COPY scripts/start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 12345 23456 1935 8080

ENTRYPOINT ["/start.sh"]
