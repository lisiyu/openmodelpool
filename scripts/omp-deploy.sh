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

# ---- 配置 ----
GITHUB_REPO="lisiyu/openmodelpool"
RELEASE_TAG="v3.2.0-release"
INSTALL_DIR="${1:-/opt/openmodelpool}"
PORT="${2:-8000}"

# 群晖默认路径检测
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

# ---- 检查 root ----
if [ "$(id -u)" -ne 0 ]; then
  echo -e "${RED}[错误] 请使用 root 权限运行${NC}"
  echo "  sudo bash omp-deploy.sh"
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
    echo "支持: x86_64, ARM64, ARMv7"
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

# ---- 检测下载工具 ----
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
echo -e "${CYAN}[4/7] 解压到临时目录...${NC}"
tar xzf "$TMP_DIR/${PKG}.tar.gz" -C "$TMP_DIR"
echo -e "${GREEN}       解压完成${NC}"

# ---- 安装 ----
echo -e "${CYAN}[5/7] 安装到 ${INSTALL_DIR}...${NC}"
mkdir -p "$INSTALL_DIR/data"
cp "$TMP_DIR/openmodelpool" "$INSTALL_DIR/openmodelpool"
chmod +x "$INSTALL_DIR/openmodelpool"
cp "$TMP_DIR/admin.html" "$INSTALL_DIR/admin.html" 2>/dev/null
cp -r "$TMP_DIR/docs" "$INSTALL_DIR/docs" 2>/dev/null
echo -e "${GREEN}       安装完成${NC}"

# ---- 创建管理脚本 ----
echo -e "${CYAN}[6/7] 配置服务 (端口 ${PORT})...${NC}"

# 启动脚本
cat > "$INSTALL_DIR/start.sh" << EOF
#!/bin/bash
cd "$INSTALL_DIR"
export OMP_PORT="$PORT"
exec ./openmodelpool >> "$INSTALL_DIR/data/app.log" 2>&1
EOF
chmod +x "$INSTALL_DIR/start.sh"

# 停止脚本
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

# 状态脚本
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

# ---- 配置开机自启 ----
if $IS_SYNOLOGY; then
  # 群晖: rc.d 脚本
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
  # 通用 Linux: systemd
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
  echo -e "${GREEN}       开机自启: systemd (openmodelpool.service)${NC}"
else
  # 通用 Linux: rc.local
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
  echo -e "  API 地址:  ${CYAN}http://${NAS_IP}:${PORT}/v1${NC}"
  echo -e "  安装目录:  $INSTALL_DIR"
  echo -e "  日志文件:  $INSTALL_DIR/data/app.log"
  echo ""
  echo -e "  ${YELLOW}常用命令:${NC}"
  echo -e "    启动:  bash $INSTALL_DIR/start.sh"
  echo -e "    停止:  bash $INSTALL_DIR/stop.sh"
  echo -e "    状态:  bash $INSTALL_DIR/status.sh"
  echo -e "    日志:  tail -f $INSTALL_DIR/data/app.log"
  if $IS_SYNOLOGY; then
    echo -e "    开机:  $RC_SCRIPT (已配置)"
  else
    echo -e "    开机:  systemctl enable openmodelpool (已配置)"
  fi
  echo ""
  echo -e "  ${YELLOW}⚠️  首次使用请访问管理面板设置管理员账号${NC}"
  echo ""
else
  echo -e "${RED}[错误] 服务启动失败${NC}"
  echo "  查看日志: tail -f $INSTALL_DIR/data/app.log"
  exit 1
fi
