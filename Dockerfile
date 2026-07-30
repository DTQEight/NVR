# 多阶段构建：编译 NVR（不内嵌 go2rtc，连接外部已部署的 go2rtc）
# 用法：docker build -t nvr:latest .
#       docker run -p 8080:8080 -v ./data:/app/data -e GO2RTC_API_BASE=http://192.168.1.10:1984 nvr:latest

# ---------- 阶段 1：编译 ----------
FROM golang:1.21-alpine AS builder

# GitHub Actions runner 在美国，用官方 proxy 更快；NAS 本地构建可改 goproxy.cn
ENV GOPROXY=https://proxy.golang.org,direct \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /build

# 先复制依赖文件（利用 Docker 层缓存，代码变动不重新下依赖）
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN go build -ldflags="-s -w" -o /out/nvr ./cmd/nvr

# ---------- 阶段 2：运行时 ----------
FROM alpine:3.19

# 时区与基本工具
RUN apk add --no-cache ca-certificates tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app

# 拷贝二进制与静态资源
COPY --from=builder /out/nvr /app/nvr
COPY web /app/web

# 数据目录（设备元数据、未来录像索引等）
# 通过 volume 持久化
RUN mkdir -p /app/data /app/recordings
VOLUME ["/app/data", "/app/recordings"]

# 默认配置（可通过环境变量覆盖，无需挂载 config.yaml）
ENV NVR_SERVER_PORT=8080 \
    NVR_GO2RTC_EXTERNAL=true \
    NVR_GO2RTC_API_BASE=http://127.0.0.1:1984 \
    NVR_GO2RTC_RTSP_BASE=rtsp://127.0.0.1:8554 \
    NVR_STORAGE_DATA_DIR=/app/data \
    NVR_STORAGE_RECORD_DIR=/app/recordings

EXPOSE 8080

# 健康检查（30 秒后开始，每 30 秒一次）
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

# 不指定 config 文件，让 NVR 走"默认值 + 环境变量"模式
ENTRYPOINT ["/app/nvr"]
CMD ["-config", "/app/config.yaml"]
