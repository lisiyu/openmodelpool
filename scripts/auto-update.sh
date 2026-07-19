#!/bin/bash
# OpenModelPool 自动更新脚本
# 检查 GitHub 最新 Release，如有新版本则自动下载部署
# 用法：手动执行 或 加入 crontab 定期执行
#
# 安全说明：
#   - 当前版本通过公开的 /api/version 端点获取，无需任何账号口令或登录。
#   - 下载的是裸二进制 + 对应的 .sha256 校验文件，下载后进行 sha256sum -c 校验，
#     校验失败不会替换现有二进制。
#   - 前端资源（HTML/JS）已编译进二进制，无需单独更新，因此本脚本不复制任何前端文件。

set -e

GITHUB_REPO="lisiyu/openmodelpool"
INSTALL_DIR="/opt/openmodelpool"
BINARY_NAME="openmodelpool"
LOG_FILE="/tmp/omp-auto-update.log"
ARCH=$(uname -m)

# 映射架构到裸二进制包名（与 release 产物一致，无 tar 打包）
case "$ARCH" in
  x86_64|amd64)  PKG="openmodelpool-linux-amd64" ;;
  aarch64|arm64) PKG="openmodelpool-linux-arm64" ;;
  armv7l|arm)    PKG="openmodelpool-linux-armv7" ;;
  windows)       PKG="openmodelpool-windows-amd64.exe" ;;
  *) echo "[$(date)] 不支持的架构: $ARCH" >> "$LOG_FILE"; exit 1 ;;
esac

# 安装后的二进制名（windows 包带 .exe 后缀）
case "$PKG" in
  *.exe) INSTALLED="$BINARY_NAME.exe" ;;
  *)     INSTALLED="$BINARY_NAME" ;;
esac

# 归一化版本号：去掉前缀 v 与后缀 -release / 预发布段
# 例: v4.0.1-release -> 4.0.1 ; v4.0.1 -> 4.0.1 ; v4.0.1-beta.1 -> 4.0.1
normalize_version() {
  local v="$1"
  v="${v#v}"
  v="${v%-release}"
  v="${v%%-*}"
  v="${v%%+*}"
  echo "$v"
}

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

# 获取当前版本（公开端点，无需登录/口令）
CURRENT_VERSION=$(curl -s http://localhost:8000/api/version 2>/dev/null | \
  python3 -c 'import sys,json;print(json.load(sys.stdin).get("version",""))' 2>/dev/null)

# 获取 GitHub 最新 Release tag
LATEST_TAG=$(curl -s "https://api.github.com/repos/$GITHUB_REPO/releases/latest" 2>/dev/null | \
  python3 -c 'import sys,json;print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null)

if [ -z "$LATEST_TAG" ]; then
    echo "[$(date)] 无法获取最新 Release tag" >> "$LOG_FILE"
    exit 1
fi

echo "[$(date)] 当前版本: $CURRENT_VERSION | 最新 Release: $LATEST_TAG" >> "$LOG_FILE"

CUR_N=$(normalize_version "$CURRENT_VERSION")
LAT_N=$(normalize_version "$LATEST_TAG")

# 版本相同则跳过
if [ "$CUR_N" = "$LAT_N" ]; then
    echo "[$(date)] 已是最新版本，跳过更新" >> "$LOG_FILE"
    exit 0
fi

echo "[$(date)] 发现新版本，开始更新..." >> "$LOG_FILE"

# 下载最新裸二进制 + 校验文件
DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$LATEST_TAG/$PKG"
CHECK_URL="$DOWNLOAD_URL.sha256"
TMP_DIR=$(mktemp -d)
echo "[$(date)] 下载: $DOWNLOAD_URL" >> "$LOG_FILE"

if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$PKG" 2>>"$LOG_FILE"; then
    echo "[$(date)] 下载失败" >> "$LOG_FILE"
    rm -rf "$TMP_DIR"
    exit 1
fi
curl -fsSL "$CHECK_URL" -o "$TMP_DIR/$PKG.sha256" 2>>"$LOG_FILE" || true

# SHA256 校验（失败则告警并退出，不替换现有二进制）
if [ -s "$TMP_DIR/$PKG.sha256" ] && command -v sha256sum >/dev/null 2>&1; then
    if ! ( cd "$TMP_DIR" && sha256sum -c "$PKG.sha256" ) >>"$LOG_FILE" 2>&1; then
        echo "[$(date)] ❌ SHA256 校验失败，终止更新，现有二进制保持不变" >> "$LOG_FILE"
        rm -rf "$TMP_DIR"
        exit 1
    fi
    echo "[$(date)] ✅ SHA256 校验通过" >> "$LOG_FILE"
else
    echo "[$(date)] ⚠️ 未找到校验文件或 sha256sum 不可用，跳过校验" >> "$LOG_FILE"
fi

NEW_BINARY="$TMP_DIR/$PKG"

if [ ! -f "$NEW_BINARY" ]; then
    echo "[$(date)] 下载后未找到二进制文件" >> "$LOG_FILE"
    rm -rf "$TMP_DIR"
    exit 1
fi

# 备份当前二进制
cp "$INSTALL_DIR/$INSTALLED" "$INSTALL_DIR/${INSTALLED}.bak" 2>/dev/null || true

# 停止服务
echo "[$(date)] 停止服务..." >> "$LOG_FILE"
pkill -f "$BINARY_NAME" 2>/dev/null || true
sleep 2

# 替换二进制（前端已嵌入，无需复制 HTML）
cp "$NEW_BINARY" "$INSTALL_DIR/$INSTALLED"
chmod +x "$INSTALL_DIR/$INSTALLED"

# 启动服务
echo "[$(date)] 启动服务..." >> "$LOG_FILE"
cd "$INSTALL_DIR"
nohup ./"$INSTALLED" > /tmp/omp.log 2>&1 < /dev/null &
sleep 3

# 验证
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8000/admin | grep -q "200"; then
    echo "[$(date)] ✅ 更新成功！版本: $LATEST_TAG" >> "$LOG_FILE"
else
    echo "[$(date)] ❌ 更新后服务未正常启动，回滚..." >> "$LOG_FILE"
    pkill -f "$BINARY_NAME" 2>/dev/null || true
    sleep 1
    cp "$INSTALL_DIR/${INSTALLED}.bak" "$INSTALL_DIR/$INSTALLED" 2>/dev/null || true
    cd "$INSTALL_DIR" && nohup ./"$INSTALLED" > /tmp/omp.log 2>&1 < /dev/null &
    echo "[$(date)] 已回滚到上一版本" >> "$LOG_FILE"
fi

# 检查浏览器依赖
check_browser_deps

# 清理
rm -rf "$TMP_DIR"
echo "[$(date)] 更新流程结束" >> "$LOG_FILE"
