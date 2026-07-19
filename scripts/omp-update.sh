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
# RELEASE_TAG 默认动态获取最新 GitHub Release；可通过环境变量 OMP_RELEASE_TAG
# 或第 2 个位置参数覆盖：$0 <安装目录> <ReleaseTag>
RELEASE_TAG="${OMP_RELEASE_TAG:-${2:-}}"
XRAY_VERSION="v26.3.27"

# Detect architecture -> 裸二进制包名（与 release 产物一致，无 tar 打包）
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  PKG="openmodelpool-linux-amd64" ;;
    aarch64|arm64) PKG="openmodelpool-linux-arm64" ;;
    armv7l|arm)    PKG="openmodelpool-linux-armv7" ;;
    *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

GITHUB_REPO="lisiyu/openmodelpool"
if [ -z "$RELEASE_TAG" ]; then
  RELEASE_TAG=$(curl -s "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" 2>/dev/null | \
    python3 -c 'import sys,json;print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null)
fi
if [ -z "$RELEASE_TAG" ]; then
  echo "无法获取最新 Release tag"
  exit 1
fi
echo "目标版本: $RELEASE_TAG"

URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}/${PKG}"
CHECK_URL="${URL}.sha256"
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

# 2. 下载 (裸二进制 + SHA256 校验，无 tar 打包)
echo "[2/5] 下载新版本: $PKG ..."
curl -fsSL "$URL" -o "$TMP_DIR/$PKG"
curl -fsSL "$CHECK_URL" -o "$TMP_DIR/$PKG.sha256" 2>/dev/null || true

# 2.5 校验 SHA256（失败则终止，不替换）
echo "[2.5/5] 校验 SHA256..."
if [ -s "$TMP_DIR/$PKG.sha256" ] && command -v sha256sum &>/dev/null; then
  ( cd "$TMP_DIR" && sha256sum -c "$PKG.sha256" ) || { echo "SHA256 校验失败，终止更新"; rm -rf "$TMP_DIR"; exit 1; }
  echo "  校验通过"
else
  echo "  ⚠️ 未找到校验文件或 sha256sum 不可用，跳过校验"
fi

# 3. 替换二进制（前端已嵌入，无需复制 HTML）
echo "[3/5] 替换二进制..."
cp "$TMP_DIR/$PKG" "$INSTALL_DIR/openmodelpool"
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
