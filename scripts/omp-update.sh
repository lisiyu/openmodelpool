#!/bin/bash
# ============================================================
#  OpenModelPool 增量更新 (Linux / 群晖)
#  仅替换二进制，保留配置和数据，一行命令搞定
#
#  用法:
#    curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-update.sh | sudo bash
# ============================================================
set -e

INSTALL_DIR="${1:-/opt/openmodelpool}"
RELEASE_TAG="v3.2.1-release"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  PLATFORM="linux-amd64" ;;
    aarch64|arm64) PLATFORM="linux-arm64" ;;
    armv7l)        PLATFORM="linux-armv7" ;;
    *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

URL="https://github.com/lisiyu/openmodelpool/releases/download/${RELEASE_TAG}/openmodelpool-${PLATFORM}.tar.gz"
TMP_DIR=$(mktemp -d)

echo "  OpenModelPool 增量更新 ($PLATFORM)"

# 1. 停止服务
echo "[1/4] 停止服务..."
if command -v systemctl &>/dev/null && systemctl is-active --quiet openmodelpool 2>/dev/null; then
    systemctl stop openmodelpool
elif [ -f /usr/local/etc/rc.d/openmodelpool.sh ]; then
    /usr/local/etc/rc.d/openmodelpool.sh stop 2>/dev/null || true
else
    pkill -f "openmodelpool" 2>/dev/null || true
fi
sleep 2

# 2. 下载
echo "[2/4] 下载新版本..."
curl -fsSL "$URL" -o "$TMP_DIR/pkg.tar.gz"

# 3. 替换二进制
echo "[3/4] 替换二进制..."
tar xzf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR"
cp "$TMP_DIR/openmodelpool" "$INSTALL_DIR/openmodelpool"
chmod +x "$INSTALL_DIR/openmodelpool"

# 4. 重启
echo "[4/4] 启动服务..."
if command -v systemctl &>/dev/null && [ -f /etc/systemd/system/openmodelpool.service ]; then
    systemctl start openmodelpool
elif [ -f /usr/local/etc/rc.d/openmodelpool.sh ]; then
    /usr/local/etc/rc.d/openmodelpool.sh start
else
    cd "$INSTALL_DIR" && nohup ./openmodelpool >> data/app.log 2>&1 &
fi
sleep 3

# Check
if pgrep -f "openmodelpool" >/dev/null 2>&1; then
    echo "  ✅ 更新成功！数据已保留。"
else
    echo "  ❌ 更新失败，请检查日志: $INSTALL_DIR/data/app.log"
fi

rm -rf "$TMP_DIR"
