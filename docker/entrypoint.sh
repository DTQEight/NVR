#!/bin/sh
# entrypoint.sh - 启动 go2rtc 子进程，再启动 NVR 主进程
# go2rtc 会被 NVR 通过子进程方式管理（生成 config、健康检查、重启）
# 这里只是确保 go2rtc 二进制可执行，并启动 NVR

set -e

# 验证 go2rtc 二进制可用
if [ -x /usr/local/bin/go2rtc ]; then
    echo "[entrypoint] go2rtc 二进制就绪: $(/usr/local/bin/go2rtc -v 2>&1 | head -1 || echo 'unknown version')"
else
    echo "[entrypoint] 警告: go2rtc 二进制不存在或不可执行"
fi

# 验证 ffmpeg 可用（录像用）
if command -v ffmpeg >/dev/null 2>&1; then
    echo "[entrypoint] ffmpeg 就绪: $(ffmpeg -version 2>&1 | head -1)"
else
    echo "[entrypoint] 警告: ffmpeg 未安装"
fi

echo "[entrypoint] 启动 NVR 主进程..."
exec /app/nvr -config /app/config.yaml
