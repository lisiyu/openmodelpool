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
# 动态获取最新 Release tag（可通过环境变量 OMP_RELEASE_TAG 覆盖）
if [ -n "$OMP_RELEASE_TAG" ]; then
  RELEASE_TAG="$OMP_RELEASE_TAG"
else
  RELEASE_TAG=$(curl -fsSL "https://api.github.com/repos/$GITHUB_REPO/releases/latest" 2>/dev/null | grep -oP '"tag_name":\s*"\K[^"]+' || echo "v4.5.0")
fi
INSTALL_DIR="${1:-/opt/openmodelpool}"
PORT="${2:-8000}"

# 端口范围校验
if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo -e "\033[0;31m[错误] 无效端口号: $PORT (必须为 1-65535 的整数)\033[0m"
    exit 1
fi

# 规范化安装目录路径
if command -v realpath >/dev/null 2>&1; then
    INSTALL_DIR=$(realpath -m "$INSTALL_DIR")
fi

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

# ---- SHA256 校验（fail-closed，仅从 GitHub 官方直连获取校验和） ----
echo -e "${CYAN}[3.5/7] SHA256 完整性校验...${NC}"
SHA256_URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}/${PKG}.tar.gz.sha256"
SHA256_FILE="$TMP_DIR/${PKG}.tar.gz.sha256"

if command -v curl &>/dev/null; then
  curl -fsSL --connect-timeout 10 --max-time 30     "$SHA256_URL" -o "$SHA256_FILE" 2>/dev/null || true
elif command -v wget &>/dev/null; then
  wget -q -T 30 -O "$SHA256_FILE" "$SHA256_URL" 2>/dev/null || true
fi

if [ ! -s "$SHA256_FILE" ]; then
  echo -e "${RED}[错误] 无法获取 SHA256 校验和（fail-closed），已中止${NC}"
  echo "       请检查网络或设置 OMP_ALLOW_UNSIGNED=1 跳过（不推荐）"
  exit 1
fi

# 校验
EXPECTED_HASH=$(head -1 "$SHA256_FILE" | awk '{print $1}')
if command -v sha256sum &>/dev/null; then
  ACTUAL_HASH=$(sha256sum "$TMP_DIR/${PKG}.tar.gz" | awk '{print $1}')
elif command -v shasum &>/dev/null; then
  ACTUAL_HASH=$(shasum -a 256 "$TMP_DIR/${PKG}.tar.gz" | awk '{print $1}')
else
  echo -e "${RED}[错误] 缺少 sha256sum/shasum，无法校验完整性${NC}"
  exit 1
fi

if [ "$EXPECTED_HASH" != "$ACTUAL_HASH" ]; then
  echo -e "${RED}[错误] SHA256 校验失败，二进制可能被篡改，已中止${NC}"
  echo "       期望: $EXPECTED_HASH"
  echo "       实际: $ACTUAL_HASH"
  exit 1
fi
echo -e "${GREEN}       SHA256 校验通过${NC}"

# ---- 解压 ----
echo -e "${CYAN}[5/7] 解压...${NC}"
tar xzf "$TMP_DIR/${PKG}.tar.gz" -C "$TMP_DIR"
echo -e "${GREEN}       解压完成${NC}"

# ---- 安装 ----
echo -e "${CYAN}[6/7] 安装到 ${INSTALL_DIR}...${NC}"
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

# ---- 创建管理脚本 ----
echo -e "${CYAN}[7/7] 配置服务 (端口 ${PORT})...${NC}"

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
echo -e "${CYAN}[8/7] 启动服务...${NC}"
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
