# 构建阶段
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /live-source-manager ./cmd/manager

# 运行阶段
FROM alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs ffmpeg

WORKDIR /app
COPY --from=builder /live-source-manager .
COPY web/static ./web/static
COPY config.ini.default ./config.ini

EXPOSE 23456

CMD ["./live-source-manager"]
