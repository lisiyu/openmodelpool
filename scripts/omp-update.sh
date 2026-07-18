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
XRAY_VERSION="v26.3.27"

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

# 4. 安装/更新 Xray (VMess 代理支持)
echo "[4/5] 检查 Xray..."
XRAY_DIR="$INSTALL_DIR/xray"
XRAY_BIN="$XRAY_DIR/xray"
NEED_XRAY=false
if [ ! -f "$XRAY_BIN" ]; then
  NEED_XRAY=true
elif [ "$1" = "--with-xray" ]; then
  NEED_XRAY=true
fi
if [ "$NEED_XRAY" = true ]; then
  mkdir -p "$XRAY_DIR"
  case "$ARCH" in
    x86_64|amd64)  XRAY_PKG="Xray-linux-64.zip" ;;
    aarch64|arm64) XRAY_PKG="Xray-linux-arm64-v8a.zip" ;;
    armv7l)        XRAY_PKG="Xray-linux-arm32-v7a.zip" ;;
  esac
  XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_PKG}"
  echo "  下载 Xray..."
  if curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip" 2>/dev/null || wget -q -O "$TMP_DIR/xray.zip" "$XRAY_URL" 2>/dev/null; then
    unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray" 2>/dev/null || python3 -c "import zipfile; zipfile.ZipFile('$TMP_DIR/xray.zip').extractall('$TMP_DIR/xray')" 2>/dev/null
    cp "$TMP_DIR/xray/xray" "$XRAY_BIN" 2>/dev/null && chmod +x "$XRAY_BIN"
    cp "$TMP_DIR/xray/geoip.dat" "$XRAY_DIR/" 2>/dev/null
    cp "$TMP_DIR/xray/geosite.dat" "$XRAY_DIR/" 2>/dev/null
    echo "  ✅ Xray 已安装"
  else
    echo "  ⚠️ Xray 下载失败，VMess 代理不可用（不影响其他功能）"
  fi
else
  echo "  ✅ Xray 已存在，跳过"
fi

# 5. 重启
echo "[5/5] 启动服务..."
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
