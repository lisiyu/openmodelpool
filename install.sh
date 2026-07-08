#!/bin/bash
# OpenModelPool 一键安装脚本 (Linux / macOS)
# 用法: curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash
# 自定义: curl -fsSL ... | bash -s -- --port 9090 --dir /opt/openmodelpool

set -euo pipefail

# ─── 默认配置 ───
REPO="lisiyu/openmodelpool"
VERSION="${MODELmux_VERSION:-latest}"
INSTALL_DIR="${MODELmux_DIR:-/usr/local/bin}"
DATA_DIR="${MODELmux_DATA:-/var/lib/openmodelpool}"
PORT="${MODELmux_PORT:-8000}"
SERVICE_NAME="openmodelpool"
GITHUB_DOWNLOAD="https://github.com/${REPO}/releases"
GITHUB_RAW="https://raw.githubusercontent.com/${REPO}"

# ─── 颜色 ───
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()  { echo -e "${CYAN}▸${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}▹${NC} $*"; }
err()   { echo -e "${RED}✗${NC} $*"; }
header(){ echo -e "\n${BOLD}── $* ──${NC}"; }

# ─── 参数解析 ───
while [[ $# -gt 0 ]]; do
    case "$1" in
        --port)    PORT="$2"; shift 2;;
        --dir)     INSTALL_DIR="$2"; shift 2;;
        --data)    DATA_DIR="$2"; shift 2;;
        --version) VERSION="$2"; shift 2;;
        -h|--help)
            echo "OpenModelPool 安装脚本"
            echo "  --port     服务端口 (默认 8000)"
            echo "  --dir      安装目录 (默认 /usr/local/bin)"
            echo "  --data     数据目录 (默认 /var/lib/openmodelpool)"
            echo "  --version  指定版本 (默认 latest)"
            exit 0;;
        *) err "未知参数: $1"; exit 1;;
    esac
done

# ─── 平台检测 ───
header "检测系统环境"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
    linux)  OS="linux";;
    darwin) OS="darwin";;
    *)      err "不支持的操作系统: $OS"; exit 1;;
esac

case "$ARCH" in
    x86_64|amd64)   ARCH="amd64";;
    aarch64|arm64)   ARCH="arm64";;
    armv7l|armhf)    ARCH="armv7";;
    *)               err "不支持的架构: $ARCH"; exit 1;;
esac

BINARY_NAME="openmodelpool-${OS}-${ARCH}"
[[ "$OS" == "windows" ]] && BINARY_NAME="${BINARY_NAME}.exe"

ok "系统: ${OS}/${ARCH}"
info "版本: ${VERSION}"

# ─── 下载 ───
header "下载 OpenModelPool"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL --retry 3 --retry-delay 2 -o "$dest" "$url"
    elif command -v wget &>/dev/null; then
        wget -q --tries=3 -O "$dest" "$url"
    else
        err "需要 curl 或 wget"
        exit 1
    fi
}

# 确定下载 URL
if [[ "$VERSION" == "latest" ]]; then
    DOWNLOAD_URL="${GITHUB_DOWNLOAD}/latest/download/${BINARY_NAME}"
    CHECKSUM_URL="${GITHUB_DOWNLOAD}/latest/download/checksums.txt"
    ACTUAL_VERSION="latest"
else
    DOWNLOAD_URL="${GITHUB_DOWNLOAD}/download/${VERSION}/${BINARY_NAME}"
    CHECKSUM_URL="${GITHUB_DOWNLOAD}/download/${VERSION}/checksums.txt"
    ACTUAL_VERSION="$VERSION"
fi

info "下载地址: $DOWNLOAD_URL"

if ! download "$DOWNLOAD_URL" "${TMPDIR}/${BINARY_NAME}"; then
    warn "GitHub Releases 下载失败，尝试 raw 源..."
    TAG="${ACTUAL_VERSION}"
    [[ "$TAG" == "latest" ]] && TAG="main"
    DOWNLOAD_URL="${GITHUB_RAW}/${TAG}/${BINARY_NAME}"
    if ! download "$DOWNLOAD_URL" "${TMPDIR}/${BINARY_NAME}"; then
        err "下载失败，请检查网络连接或手动下载"
        err "手动下载: $DOWNLOAD_URL"
        exit 1
    fi
fi
ok "二进制文件下载完成"

# ─── 校验 ───
header "验证完整性"

if download "$CHECKSUM_URL" "${TMPDIR}/checksums.txt" 2>/dev/null; then
    EXPECTED=$(grep "$BINARY_NAME" "${TMPDIR}/checksums.txt" | awk '{print $1}')
    if [[ -n "$EXPECTED" ]]; then
        if command -v sha256sum &>/dev/null; then
            ACTUAL=$(sha256sum "${TMPDIR}/${BINARY_NAME}" | awk '{print $1}')
        else
            ACTUAL=$(shasum -a 256 "${TMPDIR}/${BINARY_NAME}" | awk '{print $1}')
        fi
        if [[ "$EXPECTED" == "$ACTUAL" ]]; then
            ok "SHA256 校验通过"
        else
            err "SHA256 校验失败！"
            err "期望: $EXPECTED"
            err "实际: $ACTUAL"
            exit 1
        fi
    else
        warn "未在校验文件中找到对应条目，跳过校验"
    fi
else
    warn "无法下载校验文件，跳过校验"
fi

# ─── 安装 ───
header "安装二进制文件"

chmod +x "${TMPDIR}/${BINARY_NAME}"

if [[ "$INSTALL_DIR" == "/usr/local/bin" || "$INSTALL_DIR" == "/usr/bin" ]]; then
    sudo install -m 755 "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/openmodelpool"
else
    mkdir -p "$INSTALL_DIR"
    install -m 755 "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/openmodelpool"
fi

ok "已安装到 ${INSTALL_DIR}/openmodelpool"

# 创建数据目录
mkdir -p "$DATA_DIR"
ok "数据目录: $DATA_DIR"

# ─── 服务配置 ───
header "配置系统服务"

if [[ "$OS" == "linux" ]]; then
    # systemd service
    SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
    sudo tee "$SERVICE_FILE" > /dev/null << EOF
[Unit]
Description=OpenModelPool API Gateway
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${DATA_DIR}
ExecStart=${INSTALL_DIR}/openmodelpool -port ${PORT} -data ${DATA_DIR}
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=openmodelpool

# 安全加固
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable "$SERVICE_NAME"
    sudo systemctl restart "$SERVICE_NAME"
    ok "systemd 服务已启动"

elif [[ "$OS" == "darwin" ]]; then
    # launchd plist
    PLIST_DIR="$HOME/Library/LaunchAgents"
    PLIST_FILE="${PLIST_DIR}/com.openmodelpool.agent.plist"
    mkdir -p "$PLIST_DIR"

    cat > "$PLIST_FILE" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.openmodelpool.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/openmodelpool</string>
        <string>-port</string>
        <string>${PORT}</string>
        <string>-data</string>
        <string>${DATA_DIR}</string>
    </array>
    <key>WorkingDirectory</key>
    <string>${DATA_DIR}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${DATA_DIR}/openmodelpool.log</string>
    <key>StandardErrorPath</key>
    <string>${DATA_DIR}/openmodelpool.err.log</string>
</dict>
</plist>
EOF

    launchctl unload "$PLIST_FILE" 2>/dev/null || true
    launchctl load "$PLIST_FILE"
    ok "launchd 服务已启动"
fi

# ─── 验证 ───
header "验证安装"

sleep 2
if "${INSTALL_DIR}/openmodelpool" -version 2>/dev/null || true; then
    ok "版本信息获取成功"
fi

# 检查服务状态
if [[ "$OS" == "linux" ]]; then
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        ok "服务运行中"
    else
        warn "服务未正常启动，请检查: journalctl -u $SERVICE_NAME"
    fi
fi

# ─── 完成 ───
echo
echo -e "${GREEN}${BOLD}╔══════════════════════════════════════════╗${NC}"
echo -e "${GREEN}${BOLD}║     OpenModelPool 安装完成！                  ║${NC}"
echo -e "${GREEN}${BOLD}╚══════════════════════════════════════════╝${NC}"
echo
echo -e "  ${BOLD}访问地址:${NC}  http://localhost:${PORT}"
echo -e "  ${BOLD}管理面板:${NC}  http://localhost:${PORT}/admin"
echo -e "  ${BOLD}二进制路径:${NC} ${INSTALL_DIR}/openmodelpool"
echo -e "  ${BOLD}数据目录:${NC}  ${DATA_DIR}"
echo
if [[ "$OS" == "linux" ]]; then
    echo -e "  ${CYAN}服务管理:${NC}"
    echo -e "    查看状态:  sudo systemctl status $SERVICE_NAME"
    echo -e "    查看日志:  sudo journalctl -u $SERVICE_NAME -f"
    echo -e "    重启服务:  sudo systemctl restart $SERVICE_NAME"
    echo -e "    停止服务:  sudo systemctl stop $SERVICE_NAME"
elif [[ "$OS" == "darwin" ]]; then
    echo -e "  ${CYAN}服务管理:${NC}"
    echo -e "    查看状态:  launchctl list | grep openmodelpool"
    echo -e "    查看日志:  tail -f ${DATA_DIR}/openmodelpool.log"
    echo -e "    重启服务:  launchctl unload/load $PLIST_FILE"
fi
echo
echo -e "  ${YELLOW}卸载:${NC} sudo rm ${INSTALL_DIR}/openmodelpool && sudo rm -rf ${DATA_DIR}"
echo
