#!/bin/bash
# ============================================================
#  OpenModelPool 一键部署脚本 (Linux / 群晖 NAS)
#  自动从 GitHub 下载对应架构的二进制文件
#  
#  使用方法:
#    curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.sh | sudo bash
#    或:
#    sudo bash omp-deploy.sh [安装目录] [端口]
# ============================================================
set -e

GITHUB_REPO="lisiyu/openmodelpool"
RELEASE_TAG="v3.4.1-release"
XRAY_VERSION="v26.3.27"
INSTALL_DIR="${1:-/opt/openmodelpool}"
PORT="${2:-8000}"

if [ -d /volume1 ]; then
  INSTALL_DIR="${1:-/volume1/@appstore/openmodelpool}"
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "  ╔══════════════════════════════════════════╗"
echo "  ║     OpenModelPool 一键部署 (自动下载)    ║"
echo "  ╚══════════════════════════════════════════╝"
echo -e "${NC}"

if [ "$(id -u)" -ne 0 ]; then
  echo -e "${RED}[错误] 请使用 root 权限运行${NC}"
  exit 1
fi

# ---- 检测架构 ----
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  PKG="openmodelpool-linux-amd64";  ARCH_LABEL="x86_64 (Intel/AMD)";;
  aarch64|arm64) PKG="openmodelpool-linux-arm64";  ARCH_LABEL="ARM64 (AArch64)";;
  armv7l|arm)    PKG="openmodelpool-linux-armv7";  ARCH_LABEL="ARMv7";;
  *)
    echo -e "${RED}[错误] 不支持的架构: ${ARCH}${NC}"
    exit 1
    ;;
esac
echo -e "${GREEN}[1/7] 架构: ${ARCH_LABEL}${NC}"

# ---- 检测系统类型 ----
IS_SYNOLOGY=false
DSM_VERSION=""
if [ -f /etc.defaults/VERSION ]; then
  IS_SYNOLOGY=true
  source /etc.defaults/VERSION
  DSM_VERSION="${majorversion}.${minorversion}"
  echo -e "${GREEN}[2/7] 系统: 群晖 DSM ${DSM_VERSION}${NC}"
elif [ -f /etc/synoinfo.conf ]; then
  IS_SYNOLOGY=true
  echo -e "${GREEN}[2/7] 系统: 群晖 NAS${NC}"
else
  echo -e "${GREEN}[2/7] 系统: Linux ($(cat /etc/os-release 2>/dev/null | grep ^PRETTY_NAME | cut -d= -f2 | tr -d '"' || echo 'unknown'))${NC}"
fi

# ---- 下载 ----
DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}/${PKG}.tar.gz"
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

echo -e "${CYAN}[3/7] 下载: ${PKG}.tar.gz${NC}"
echo "       ${DOWNLOAD_URL}"

if command -v curl &>/dev/null; then
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/${PKG}.tar.gz"
elif command -v wget &>/dev/null; then
  wget -q -O "$TMP_DIR/${PKG}.tar.gz" "$DOWNLOAD_URL"
else
  echo -e "${RED}[错误] 需要 curl 或 wget${NC}"
  exit 1
fi

if [ ! -s "$TMP_DIR/${PKG}.tar.gz" ]; then
  echo -e "${RED}[错误] 下载失败${NC}"
  exit 1
fi
echo -e "${GREEN}       下载完成 ($(du -h "$TMP_DIR/${PKG}.tar.gz" | cut -f1))${NC}"

# ---- 解压 ----
echo -e "${CYAN}[4/7] 解压...${NC}"
tar xzf "$TMP_DIR/${PKG}.tar.gz" -C "$TMP_DIR"
echo -e "${GREEN}       解压完成${NC}"

# ---- 安装 ----
echo -e "${CYAN}[5/7] 安装到 ${INSTALL_DIR}...${NC}"
mkdir -p "$INSTALL_DIR/data"
cp "$TMP_DIR/openmodelpool" "$INSTALL_DIR/openmodelpool"
chmod +x "$INSTALL_DIR/openmodelpool"

# 复制所有 HTML 文件
for html in admin.html setup.html login.html; do
  if [ -f "$TMP_DIR/$html" ]; then
    cp "$TMP_DIR/$html" "$INSTALL_DIR/$html"
  fi
done

cp -r "$TMP_DIR/docs" "$INSTALL_DIR/docs" 2>/dev/null
echo -e "${GREEN}       安装完成${NC}"

# ---- 安装 Xray (VMess 代理支持) ----
echo -e "${CYAN}[5.5/7] 安装 Xray (VMess 代理支持)...${NC}"
XRAY_DIR="$INSTALL_DIR/xray"
mkdir -p "$XRAY_DIR"
case "$ARCH" in
  x86_64|amd64)  XRAY_PKG="Xray-linux-64.zip" ;;
  aarch64|arm64) XRAY_PKG="Xray-linux-arm64-v8a.zip" ;;
  armv7l|arm)    XRAY_PKG="Xray-linux-arm32-v7a.zip" ;;
esac
XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_PKG}"
if curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip" 2>/dev/null || wget -q -O "$TMP_DIR/xray.zip" "$XRAY_URL" 2>/dev/null; then
  if unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray" 2>/dev/null || python3 -c "import zipfile; zipfile.ZipFile('$TMP_DIR/xray.zip').extractall('$TMP_DIR/xray')" 2>/dev/null; then
    cp "$TMP_DIR/xray/xray" "$XRAY_DIR/xray" 2>/dev/null && chmod +x "$XRAY_DIR/xray"
    cp "$TMP_DIR/xray/geoip.dat" "$XRAY_DIR/" 2>/dev/null
    cp "$TMP_DIR/xray/geosite.dat" "$XRAY_DIR/" 2>/dev/null
    echo -e "${GREEN}       Xray 安装完成${NC}"
  else
    echo -e "${YELLOW}       ⚠️ Xray 解压失败，VMess 代理不可用（不影响其他功能）${NC}"
  fi
else
  echo -e "${YELLOW}       ⚠️ Xray 下载失败，VMess 代理不可用（不影响其他功能）${NC}"
  echo -e "       可手动下载: $XRAY_URL"
fi


# ---- 安装内置浏览器依赖 (Chrome + Xvfb) ----
echo -e "${CYAN}[5.6/7] 检查内置浏览器依赖...${NC}"

NEED_CHROME=false
NEED_XVFB=false

# --- 检查 Chrome ---
if command -v google-chrome &>/dev/null; then
  CHROME_VER=$(google-chrome --version 2>/dev/null | grep -oP '\d+' | head -1)
  if [ -n "$CHROME_VER" ] && [ "$CHROME_VER" -ge 120 ]; then
    echo -e "${GREEN}       Chrome 已安装 (v$(google-chrome --version 2>/dev/null | awk '{print $3}'))${NC}"
  else
    echo -e "${YELLOW}       Chrome 版本过旧 (v${CHROME_VER})，需要更新${NC}"
    NEED_CHROME=true
  fi
elif command -v chromium &>/dev/null; then
  CHROME_VER=$(chromium --version 2>/dev/null | grep -oP '\d+' | head -1)
  if [ -n "$CHROME_VER" ] && [ "$CHROME_VER" -ge 120 ]; then
    echo -e "${GREEN}       Chromium 已安装 (v$(chromium --version 2>/dev/null | awk '{print $2}'))${NC}"
  else
    echo -e "${YELLOW}       Chromium 版本过旧，需要更新${NC}"
    NEED_CHROME=true
  fi
else
  echo -e "${YELLOW}       Chrome 未安装${NC}"
  NEED_CHROME=true
fi

# --- 检查 Xvfb ---
if command -v Xvfb &>/dev/null; then
  echo -e "${GREEN}       Xvfb 已安装${NC}"
else
  echo -e "${YELLOW}       Xvfb 未安装${NC}"
  NEED_XVFB=true
fi

# --- 安装缺失的依赖 ---
if [ "$NEED_CHROME" = true ] || [ "$NEED_XVFB" = true ]; then
  # 检测包管理器
  if command -v apt-get &>/dev/null; then
    PKG_MANAGER="apt-get"
    echo -e "${CYAN}       使用 apt-get 安装依赖...${NC}"
    apt-get update -qq 2>/dev/null

    if [ "$NEED_XVFB" = true ]; then
      apt-get install -y -qq xvfb 2>/dev/null
      if command -v Xvfb &>/dev/null; then
        echo -e "${GREEN}       Xvfb 安装完成${NC}"
      else
        echo -e "${YELLOW}       ⚠️ Xvfb 安装失败，内置浏览器将不可用${NC}"
      fi
    fi

    if [ "$NEED_CHROME" = true ]; then
      # 安装 Chrome 依赖库
      apt-get install -y -qq wget gnupg2 2>/dev/null
      # 尝试通过官方源安装
      if [ ! -f /etc/apt/sources.list.d/google-chrome.list ] || [ "$NEED_CHROME" = true ]; then
        wget -q -O /tmp/chrome-signing-key.pub https://dl.google.com/linux/linux_signing_key.pub 2>/dev/null
        if [ $? -eq 0 ]; then
          apt-key add /tmp/chrome-signing-key.pub 2>/dev/null || true
          echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list
          apt-get update -qq 2>/dev/null
          apt-get install -y -qq google-chrome-stable 2>/dev/null
        fi
      fi

      # 验证安装
      if command -v google-chrome &>/dev/null; then
        echo -e "${GREEN}       Chrome 安装完成 (v$(google-chrome --version 2>/dev/null | awk '{print $3}'))${NC}"
      else
        # 降级方案：尝试安装 chromium
        echo -e "${YELLOW}       尝试安装 Chromium 作为替代...${NC}"
        apt-get install -y -qq chromium-browser 2>/dev/null || apt-get install -y -qq chromium 2>/dev/null
        if command -v chromium &>/dev/null || command -v chromium-browser &>/dev/null; then
          echo -e "${GREEN}       Chromium 安装完成${NC}"
        else
          echo -e "${RED}       ⚠️ Chrome/Chromium 安装失败！${NC}"
          echo -e "       内置浏览器功能将不可用（不影响其他功能）"
          echo -e "       手动安装: apt-get install google-chrome-stable xvfb"
        fi
      fi
    fi

  elif command -v yum &>/dev/null; then
    PKG_MANAGER="yum"
    echo -e "${CYAN}       使用 yum 安装依赖...${NC}"

    if [ "$NEED_XVFB" = true ]; then
      yum install -y -q xorg-x11-server-Xvfb 2>/dev/null
      if command -v Xvfb &>/dev/null; then
        echo -e "${GREEN}       Xvfb 安装完成${NC}"
      else
        echo -e "${YELLOW}       ⚠️ Xvfb 安装失败${NC}"
      fi
    fi

    if [ "$NEED_CHROME" = true ]; then
      cat > /etc/yum.repos.d/google-chrome.repo << 'CHROME_REPO'
[google-chrome]
name=google-chrome
baseurl=https://dl.google.com/linux/chrome/rpm/stable/x86_64
enabled=1
gpgcheck=1
gpgkey=https://dl.google.com/linux/linux_signing_key.pub
CHROME_REPO
      yum install -y -q google-chrome-stable 2>/dev/null
      if command -v google-chrome &>/dev/null; then
        echo -e "${GREEN}       Chrome 安装完成${NC}"
      else
        echo -e "${RED}       ⚠️ Chrome 安装失败，手动安装: yum install google-chrome-stable${NC}"
      fi
    fi

  elif command -v dnf &>/dev/null; then
    PKG_MANAGER="dnf"
    echo -e "${CYAN}       使用 dnf 安装依赖...${NC}"

    if [ "$NEED_XVFB" = true ]; then
      dnf install -y -q xorg-x11-server-Xvfb 2>/dev/null
      if command -v Xvfb &>/dev/null; then
        echo -e "${GREEN}       Xvfb 安装完成${NC}"
      fi
    fi

    if [ "$NEED_CHROME" = true ]; then
      dnf install -y -q https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm 2>/dev/null
      if command -v google-chrome &>/dev/null; then
        echo -e "${GREEN}       Chrome 安装完成${NC}"
      else
        echo -e "${RED}       ⚠️ Chrome 安装失败${NC}"
      fi
    fi

  elif command -v apk &>/dev/null; then
    # Alpine Linux (如在 Docker 中)
    echo -e "${CYAN}       使用 apk 安装依赖...${NC}"
    if [ "$NEED_CHROME" = true ]; then
      apk add --no-cache chromium nss freetype harfbuzz ttf-freefont 2>/dev/null
      echo -e "${GREEN}       Chromium 安装完成 (Alpine)${NC}"
    fi
    if [ "$NEED_XVFB" = true ]; then
      apk add --no-cache xvfb-run 2>/dev/null || echo -e "${YELLOW}       ⚠️ Xvfb 在 Alpine 上不可用${NC}"
    fi

  else
    echo -e "${YELLOW}       ⚠️ 无法识别的包管理器，请手动安装:${NC}"
    echo -e "       Chrome: https://www.google.com/chrome/"
    echo -e "       Xvfb:   系统包管理器安装 xvfb 或 xorg-x11-server-Xvfb"
  fi
else
  echo -e "${GREEN}       内置浏览器依赖已就绪${NC}"
fi

# 安装 Chrome 需要的额外字体（中文等）
if [ "$NEED_CHROME" = true ] && command -v apt-get &>/dev/null; then
  apt-get install -y -qq fonts-liberation fonts-noto-cjk 2>/dev/null || true
fi

# ---- 创建管理脚本 ----
echo -e "${CYAN}[6/7] 配置服务 (端口 ${PORT})...${NC}"

cat > "$INSTALL_DIR/start.sh" << EOF
#!/bin/bash
cd "$INSTALL_DIR"
export PORT="$PORT"
exec ./openmodelpool >> "$INSTALL_DIR/data/app.log" 2>&1
EOF
chmod +x "$INSTALL_DIR/start.sh"

cat > "$INSTALL_DIR/stop.sh" << 'EOF'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
PIDS=$(pgrep -f "$DIR/openmodelpool")
if [ -n "$PIDS" ]; then
  kill $PIDS && echo "已停止 (PID: $PIDS)"
else
  echo "服务未运行"
fi
EOF
chmod +x "$INSTALL_DIR/stop.sh"

cat > "$INSTALL_DIR/status.sh" << 'EOF'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
PIDS=$(pgrep -f "$DIR/openmodelpool")
if [ -n "$PIDS" ]; then
  echo "✅ 运行中 (PID: $PIDS)"
else
  echo "❌ 未运行"
fi
EOF
chmod +x "$INSTALL_DIR/status.sh"

# ---- 开机自启 ----
if $IS_SYNOLOGY; then
  RC_SCRIPT="/usr/local/etc/rc.d/openmodelpool.sh"
  mkdir -p /usr/local/etc/rc.d
  cat > "$RC_SCRIPT" << EOF
#!/bin/bash
case "\$1" in
  start)  su root -c "$INSTALL_DIR/start.sh &" ;;
  stop)   $INSTALL_DIR/stop.sh ;;
  restart) \$0 stop; sleep 2; \$0 start ;;
  status) $INSTALL_DIR/status.sh ;;
  *) echo "Usage: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
exit 0
EOF
  chmod +x "$RC_SCRIPT"
  echo -e "${GREEN}       开机自启: $RC_SCRIPT${NC}"
elif command -v systemctl &>/dev/null; then
  cat > /etc/systemd/system/openmodelpool.service << EOF
[Unit]
Description=OpenModelPool - AI Model Router & Load Balancer
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/start.sh
Restart=on-failure
RestartSec=5
WorkingDirectory=$INSTALL_DIR
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable openmodelpool
  echo -e "${GREEN}       开机自启: systemd${NC}"
else
  echo "su root -c '$INSTALL_DIR/start.sh &'" >> /etc/rc.local 2>/dev/null
  echo -e "${YELLOW}       开机自启: /etc/rc.local${NC}"
fi

# ---- 启动 ----
echo -e "${CYAN}[7/7] 启动服务...${NC}"
pkill -f "$INSTALL_DIR/openmodelpool" 2>/dev/null || true
sleep 1
$INSTALL_DIR/start.sh &
sleep 3

if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null; then
  NAS_IP=$(ip addr show | grep -oP 'inet \K[0-9.]+' | grep -v '127.0.0.1' | head -1)
  echo ""
  echo -e "${GREEN}  ╔══════════════════════════════════════════╗${NC}"
  echo -e "${GREEN}  ║            ✅ 部署成功！                  ║${NC}"
  echo -e "${GREEN}  ╚══════════════════════════════════════════╝${NC}"
  echo ""
  echo -e "  管理面板:  ${CYAN}http://${NAS_IP}:${PORT}/admin${NC}"
  echo -e "  安装目录:  $INSTALL_DIR"
  echo -e "  日志文件:  $INSTALL_DIR/data/app.log"
  echo ""
  echo -e "  ${YELLOW}常用命令:${NC}"
  echo -e "    启动:  bash $INSTALL_DIR/start.sh"
  echo -e "    停止:  bash $INSTALL_DIR/stop.sh"
  echo -e "    状态:  bash $INSTALL_DIR/status.sh"
  echo -e "    日志:  tail -f $INSTALL_DIR/data/app.log"
  echo ""
  echo -e "  ${YELLOW}⚠️  首次使用请访问管理面板设置管理员账号${NC}"
  echo ""
else
  echo -e "${RED}[错误] 服务启动失败${NC}"
  echo "  查看日志: tail -f $INSTALL_DIR/data/app.log"
  exit 1
fi

# ============================================================
# 外网穿透配置（可选）
# ============================================================
echo ""
echo -e "${CYAN}  是否配置外网穿透？${NC}"
echo -e "    ${GREEN}1${NC}) Cloudflare Tunnel — 免费，固定域名+HTTPS"
echo -e "    ${GREEN}2${NC}) FRP — 免费，固定IP+端口"
echo -e "    ${GREEN}3${NC}) 跳过（稍后可单独配置）"
read -p "  请选择 [1/2/3]: " tunnel_choice

if [ "$tunnel_choice" = "1" ] || [ "$tunnel_choice" = "2" ]; then
    echo ""
    echo -e "${YELLOW}  正在下载穿透配置脚本...${NC}"
    curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.sh" | bash -s -- "$INSTALL_DIR" "$PORT"
else
    echo -e "  ${YELLOW}跳过外网穿透配置。后续可运行:${NC}"
    echo -e "    curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.sh | sudo bash"
fi
echo ""
