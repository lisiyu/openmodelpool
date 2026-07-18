#!/bin/bash
# OpenModelPool 自动更新脚本
# 检查 GitHub 最新 Release，如有新版本则自动下载部署
# 用法：手动执行 或 加入 crontab 定期执行

set -e

GITHUB_REPO="lisiyu/openmodelpool"
INSTALL_DIR="/root/modelmux-deploy"
BINARY_NAME="modelmux"
LOG_FILE="/tmp/omp-auto-update.log"
ARCH=$(uname -m)

# 映射架构到下载包名
case "$ARCH" in
  x86_64|amd64)  PKG="openmodelpool-linux-amd64.tar.gz"; DIR="linux-amd64" ;;
  aarch64|arm64) PKG="openmodelpool-linux-arm64.tar.gz"; DIR="linux-arm64" ;;
  armv7l|arm)    PKG="openmodelpool-linux-armv7.tar.gz"; DIR="linux-armv7" ;;
  *) echo "[$(date)] 不支持的架构: $ARCH" >> "$LOG_FILE"; exit 1 ;;
esac


# ---- 检查内置浏览器依赖 ----
check_browser_deps() {
    local need_install=false
    
    # Check Chrome
    if ! command -v google-chrome &>/dev/null && ! command -v chromium &>/dev/null && ! command -v chromium-browser &>/dev/null; then
        echo "[$(date)] Chrome 未安装，尝试安装..." >> "$LOG_FILE"
        need_install=true
    else
        local chrome_bin="google-chrome"
        command -v google-chrome &>/dev/null || chrome_bin="chromium"
        command -v chromium &>/dev/null || chrome_bin="chromium-browser"
        local chrome_ver=$($chrome_bin --version 2>/dev/null | grep -oP '\d+' | head -1)
        if [ -n "$chrome_ver" ] && [ "$chrome_ver" -lt 120 ]; then
            echo "[$(date)] Chrome 版本过旧 (v$chrome_ver)，尝试更新..." >> "$LOG_FILE"
            need_install=true
        fi
    fi
    
    # Check Xvfb
    if ! command -v Xvfb &>/dev/null; then
        echo "[$(date)] Xvfb 未安装，尝试安装..." >> "$LOG_FILE"
        need_install=true
    fi
    
    if [ "$need_install" = true ]; then
        if command -v apt-get &>/dev/null; then
            apt-get update -qq 2>/dev/null
            command -v Xvfb &>/dev/null || apt-get install -y -qq xvfb 2>/dev/null
            if ! command -v google-chrome &>/dev/null; then
                wget -q -O /tmp/chrome-signing-key.pub https://dl.google.com/linux/linux_signing_key.pub 2>/dev/null &&                     apt-key add /tmp/chrome-signing-key.pub 2>/dev/null
                echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list 2>/dev/null
                apt-get update -qq 2>/dev/null
                apt-get install -y -qq google-chrome-stable 2>/dev/null
            else
                apt-get install -y -qq --only-upgrade google-chrome-stable 2>/dev/null
            fi
        elif command -v yum &>/dev/null; then
            command -v Xvfb &>/dev/null || yum install -y -q xorg-x11-server-Xvfb 2>/dev/null
            yum install -y -q google-chrome-stable 2>/dev/null
        elif command -v dnf &>/dev/null; then
            command -v Xvfb &>/dev/null || dnf install -y -q xorg-x11-server-Xvfb 2>/dev/null
            dnf install -y -q google-chrome-stable 2>/dev/null
        fi
        
        # Verify
        if command -v google-chrome &>/dev/null && command -v Xvfb &>/dev/null; then
            echo "[$(date)] ✅ 浏览器依赖已就绪" >> "$LOG_FILE"
        else
            echo "[$(date)] ⚠️ 部分浏览器依赖缺失，内置浏览器可能不可用" >> "$LOG_FILE"
        fi
    else
        echo "[$(date)] 浏览器依赖已就绪" >> "$LOG_FILE"
    fi
}

# 获取当前版本
CURRENT_VERSION=$(curl -s http://localhost:8000/api/federation/config \
  -H "Authorization: Bearer $(curl -s -X POST http://localhost:8000/api/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Omp@2026Go!"}' 2>/dev/null | \
    python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))' 2>/dev/null)" 2>/dev/null | \
  python3 -c 'import sys,json;print(json.load(sys.stdin).get("federation_doc_version",""))' 2>/dev/null)

# 获取 GitHub 最新 Release tag
LATEST_TAG=$(curl -s "https://api.github.com/repos/$GITHUB_REPO/releases/latest" 2>/dev/null | \
  python3 -c 'import sys,json;print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null)

if [ -z "$LATEST_TAG" ]; then
    echo "[$(date)] 无法获取最新 Release tag" >> "$LOG_FILE"
    exit 1
fi

echo "[$(date)] 当前版本: $CURRENT_VERSION | 最新 Release: $LATEST_TAG" >> "$LOG_FILE"

# 版本相同则跳过
if [ "$CURRENT_VERSION" = "$LATEST_TAG" ]; then
    echo "[$(date)] 已是最新版本，跳过更新" >> "$LOG_FILE"
    exit 0
fi

echo "[$(date)] 发现新版本，开始更新..." >> "$LOG_FILE"

# 下载最新二进制
DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$LATEST_TAG/$PKG"
TMP_DIR=$(mktemp -d)
echo "[$(date)] 下载: $DOWNLOAD_URL" >> "$LOG_FILE"

if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$PKG" 2>>"$LOG_FILE"; then
    echo "[$(date)] 下载失败" >> "$LOG_FILE"
    rm -rf "$TMP_DIR"
    exit 1
fi

# 解压
tar xzf "$TMP_DIR/$PKG" -C "$TMP_DIR" 2>>"$LOG_FILE"
NEW_BINARY="$TMP_DIR/$DIR/openmodelpool"

if [ ! -f "$NEW_BINARY" ]; then
    echo "[$(date)] 解压后未找到二进制文件" >> "$LOG_FILE"
    rm -rf "$TMP_DIR"
    exit 1
fi

# 备份当前二进制
cp "$INSTALL_DIR/$BINARY_NAME" "$INSTALL_DIR/${BINARY_NAME}.bak"

# 停止服务
echo "[$(date)] 停止服务..." >> "$LOG_FILE"
pkill -f "$BINARY_NAME" 2>/dev/null || true
sleep 2

# 替换二进制
cp "$NEW_BINARY" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

# 同步前端文件
if [ -d "$TMP_DIR/$DIR" ]; then
    for f in admin.html admin-provider.html admin-models.html admin-browser-login.html admin-common.js admin-settings.js admin-network.js admin-share.js admin-logs.js login.html setup.html; do
        if [ -f "$TMP_DIR/$DIR/$f" ]; then
            cp "$TMP_DIR/$DIR/$f" "$INSTALL_DIR/$f"
        fi
    done
fi

# 启动服务
echo "[$(date)] 启动服务..." >> "$LOG_FILE"
cd "$INSTALL_DIR"
nohup ./$BINARY_NAME > /tmp/omp.log 2>&1 < /dev/null &
sleep 3

# 验证
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8000/admin | grep -q "200"; then
    echo "[$(date)] ✅ 更新成功！版本: $LATEST_TAG" >> "$LOG_FILE"
else
    echo "[$(date)] ❌ 更新后服务未正常启动，回滚..." >> "$LOG_FILE"
    pkill -f "$BINARY_NAME" 2>/dev/null || true
    sleep 1
    cp "$INSTALL_DIR/${BINARY_NAME}.bak" "$INSTALL_DIR/$BINARY_NAME"
    cd "$INSTALL_DIR" && nohup ./$BINARY_NAME > /tmp/omp.log 2>&1 < /dev/null &
    echo "[$(date)] 已回滚到上一版本" >> "$LOG_FILE"
fi

# 检查浏览器依赖
check_browser_deps

# 清理
rm -rf "$TMP_DIR"
echo "[$(date)] 更新流程结束" >> "$LOG_FILE"
