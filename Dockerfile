# 多阶段构建：编译 NVR + 打包 go2rtc（单容器集成部署）
# 用法：docker build -t nvr:latest .
#       docker run -p 8080:8080 -v ./data:/app/data nvr:latest

# ---------- 阶段 1：编译 NVR ----------
FROM golang:1.21-alpine AS builder

ARG TARGETARCH
ENV GOPROXY=https://proxy.golang.org,direct \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /build

# 先复制 go.mod 利用缓存下载依赖
COPY go.mod* ./
RUN go mod download || true

# 复制全部源码后生成 go.sum 并编译
COPY . .
RUN go mod tidy && go build -ldflags="-s -w" -o /out/nvr ./cmd/nvr

# ---------- 阶段 2：下载 go2rtc 二进制 ----------
FROM alpine:3.19 AS go2rtc
ARG TARGETARCH
# go2rtc releases: https://github.com/AlexxIT/go2rtc/releases
RUN apk add --no-cache curl && \
    case "$TARGETARCH" in \
      amd64) GO2RTC_ARCH="amd64" ;; \
      arm64) GO2RTC_ARCH="arm64" ;; \
      arm)   GO2RTC_ARCH="armv7" ;; \
      *) echo "unsupported arch: $TARGETARCH" && exit 1 ;; \
    esac && \
    curl -L -o /usr/local/bin/go2rtc \
      "https://github.com/AlexxIT/go2rtc/releases/latest/download/go2rtc_linux_${GO2RTC_ARCH}" && \
    chmod +x /usr/local/bin/go2rtc

# ---------- 阶段 3：运行时 ----------
FROM alpine:3.19

# 时区 + FFmpeg（录像用）+ 基本工具
RUN apk add --no-cache ca-certificates tzdata ffmpeg wget && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app

# 拷贝 NVR 二进制 + 静态资源
COPY --from=builder /out/nvr /app/nvr
COPY web /app/web

# 拷贝 go2rtc 二进制
COPY --from=go2rtc /usr/local/bin/go2rtc /usr/local/bin/go2rtc

# 启动脚本：先起 go2rtc，再起 NVR
COPY docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# 数据目录
RUN mkdir -p /app/data /app/recordings /app/config
VOLUME ["/app/data", "/app/recordings", "/app/config"]

# 默认配置：集成模式（容器内 go2rtc）
ENV NVR_SERVER_PORT=8080 \
    NVR_GO2RTC_EXTERNAL=false \
    NVR_GO2RTC_BINARY_PATH=/usr/local/bin/go2rtc \
    NVR_GO2RTC_CONFIG_PATH=/app/config/go2rtc.yaml \
    NVR_GO2RTC_API_PORT=1984 \
    NVR_GO2RTC_RTSP_PORT=8554 \
    NVR_GO2RTC_API_BASE=http://127.0.0.1:1984 \
    NVR_GO2RTC_RTSP_BASE=rtsp://127.0.0.1:8554 \
    NVR_STORAGE_DATA_DIR=/app/data \
    NVR_STORAGE_RECORD_DIR=/app/recordings

# 对外暴露：NVR Web(8080) + go2rtc WebUI(1984, 可选) + go2rtc RTSP(8554, 可选)
EXPOSE 8080 1984 8554

ENTRYPOINT ["/app/entrypoint.sh"]
