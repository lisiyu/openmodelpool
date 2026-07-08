#!/usr/bin/env bash
#
# OpenModelPool 一键部署脚本
# 
# 用法:
#   bash deploy.sh                          # 交互式部署
#   OMP_PORT=9000 bash deploy.sh            # 自定义端口
#   OMP_SOURCE_URL=https://xxx.zip bash deploy.sh  # 指定源码
#
# 环境变量（可选）:
#   OMP_PORT         服务端口（默认 8000）
#   OMP_INSTALL_DIR  安装目录（默认 /opt/openmodelpool）
#   OMP_SOURCE_URL   源码下载地址（zip/tar.gz）
#   OMP_SKIP_TUNNEL  设为 1 跳过 SSH 隧道
#
set -euo pipefail

# ─── 颜色 ─────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

# ─── 配置 ─────────────────────────────────
PORT="${OMP_PORT:-8000}"
INSTALL_DIR="${OMP_INSTALL_DIR:-/opt/openmodelpool}"
SOURCE_URL="${OMP_SOURCE_URL:-}"
SKIP_TUNNEL="${OMP_SKIP_TUNNEL:-0}"

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║     OpenModelPool 一键部署脚本           ║"
echo "╚══════════════════════════════════════════╝"
echo ""

# ─── 1. 系统检测 ────────────────────────────
info "检测系统环境..."
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7*)  ARCH="armv7" ;;
    *)       fail "不支持的架构: $ARCH" ;;
esac
info "系统: $OS / $ARCH"

# ─── 2. 安装 Go ─────────────────────────────
GO_VERSION="1.23.4"

install_go() {
    if command -v go &>/dev/null; then
        info "Go 已安装: $(go version | grep -oP '\d+\.\d+\.\d+')"
        return 0
    fi
    for p in /usr/local/go/bin/go /usr/lib/go/bin/go; do
        if [ -x "$p" ]; then
            export PATH="$PATH:$(dirname "$p")"
            info "Go 已找到: $p"
            return 0
        fi
    done
    info "安装 Go $GO_VERSION ..."
    local go_tar="go${GO_VERSION}.${OS}-${ARCH}.tar.gz"
    cd /tmp
    curl -fsSL "https://go.dev/dl/${go_tar}" -o "$go_tar" || fail "下载 Go 失败"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$go_tar"
    rm -f "$go_tar"
    export PATH="/usr/local/go/bin:$PATH"
    ok "Go 安装完成"
}
install_go

# ─── 3. 安装依赖工具 ────────────────────────
if [ "$SKIP_TUNNEL" != "1" ] && ! command -v ssh &>/dev/null; then
    info "安装 OpenSSH..."
    if command -v apt-get &>/dev/null; then
        apt-get update -qq && apt-get install -y -qq openssh-client >/dev/null 2>&1
    elif command -v yum &>/dev/null; then
        yum install -y -q openssh-clients >/dev/null 2>&1
    else
        warn "无法自动安装 SSH，跳过隧道"; SKIP_TUNNEL=1
    fi
fi

# ─── 4. 获取源码 ────────────────────────────
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

if [ -f "openmodelpool" ] && [ -f "go.mod" ] && [ -z "$SOURCE_URL" ]; then
    ok "检测到已有安装，跳过下载"
elif [ -n "$SOURCE_URL" ]; then
    info "下载源码: $SOURCE_URL"
    if [[ "$SOURCE_URL" == *.zip ]]; then
        curl -fsSL "$SOURCE_URL" -o /tmp/omp-source.zip
        unzip -qo /tmp/omp-source.zip -d "$INSTALL_DIR"
        rm -f /tmp/omp-source.zip
    elif [[ "$SOURCE_URL" == *.tar.gz ]] || [[ "$SOURCE_URL" == *.tgz ]]; then
        curl -fsSL "$SOURCE_URL" -o /tmp/omp-source.tar.gz
        tar -xzf /tmp/omp-source.tar.gz -C "$INSTALL_DIR" --strip-components=1
        rm -f /tmp/omp-source.tar.gz
    else
        fail "不支持的源码格式，请使用 .zip 或 .tar.gz"
    fi
    ok "源码下载完成"
else
    fail "请设置 OMP_SOURCE_URL 指向源码包地址（.zip 或 .tar.gz）"
fi

# ─── 5. 编译 ─────────────────────────────────
[ -f "go.mod" ] || fail "源码目录不正确，缺少 go.mod"
info "编译 OpenModelPool ..."
export GOPROXY="https://goproxy.cn,direct"
export CGO_ENABLED=0
go build -ldflags="-s -w" -trimpath -o openmodelpool . || fail "编译失败"
chmod +x openmodelpool
ok "编译完成 ($(du -h openmodelpool | cut -f1))"

# ─── 6. 初始化配置 ──────────────────────────
mkdir -p "$INSTALL_DIR/data"

if [ ! -f "$INSTALL_DIR/data/config.json" ]; then
    cat > "$INSTALL_DIR/data/config.json" << CONF
{
  "service_port": "${PORT}",
  "network_mode": "shared",
  "network_consent": true,
  "federation_enabled": true,
  "federation_relay_enabled": true,
  "relay_enabled": true
}
CONF
    ok "配置文件已生成 (端口: $PORT)"
else
    ok "配置文件已存在"
fi

# ─── 7. 创建系统服务 ────────────────────────
if command -v systemctl &>/dev/null; then
    info "创建 systemd 服务..."
    cat > /etc/systemd/system/openmodelpool.service << SERVICE
[Unit]
Description=OpenModelPool Node
After=network.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/openmodelpool
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
    systemctl daemon-reload
    systemctl enable openmodelpool >/dev/null 2>&1
    systemctl start openmodelpool
    ok "系统服务已启动"
else
    info "启动服务（后台模式）..."
    nohup ./openmodelpool > server.log 2>&1 &
    echo $! > server.pid
    ok "服务已启动 (PID: $(cat server.pid))"
fi

sleep 3

# ─── 8. 端口检查 ────────────────────────────
if ss -tlnp 2>/dev/null | grep -q ":${PORT}" || netstat -tlnp 2>/dev/null | grep -q ":${PORT}"; then
    ok "服务正在监听端口 $PORT"
else
    warn "端口 $PORT 未监听，服务可能启动失败"
    warn "查看日志: tail -50 ${INSTALL_DIR}/server.log"
fi

# ─── 9. SSH 隧道（公网访问）─────────────────
TUNNEL_URL=""
if [ "$SKIP_TUNNEL" != "1" ]; then
    info "建立 SSH 隧道（临时公网地址）..."
    pkill -f "ssh.*serveo" 2>/dev/null || true; sleep 1
    nohup ssh -o StrictHostKeyChecking=no -o ServerAliveInterval=60 \
        -R 80:localhost:${PORT} serveo.net > /tmp/omp-tunnel.log 2>&1 &
    echo $! > /tmp/omp-tunnel.pid
    sleep 5
    TUNNEL_URL=$(sed 's/\x1b\[[0-9;]*m//g' /tmp/omp-tunnel.log | grep -o 'https://[^[:space:]]*' | head -1)
    [ -n "$TUNNEL_URL" ] && ok "隧道已建立" || warn "隧道建立失败"
fi

# ─── 10. 显示结果 ───────────────────────────
LOCAL_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
PUBLIC_IP=$(curl -s --connect-timeout 5 https://api.ipify.org 2>/dev/null || echo "")

echo ""
echo "══════════════════════════════════════════"
echo "  ✅ 部署完成！"
echo "══════════════════════════════════════════"
echo ""

if [ -n "$TUNNEL_URL" ]; then
    echo "🌐 临时公网地址（重启后变化）:"
    echo "   API:  ${TUNNEL_URL}/v1"
    echo "   管理: ${TUNNEL_URL}/admin"
    echo ""
fi

if [ -n "$PUBLIC_IP" ]; then
    echo "📡 固定公网地址（需开放端口）:"
    echo "   API:  http://${PUBLIC_IP}:${PORT}/v1"
    echo "   管理: http://${PUBLIC_IP}:${PORT}/admin"
    echo ""
fi

if [ -n "$LOCAL_IP" ]; then
    echo "🏠 局域网地址:"
    echo "   API:  http://${LOCAL_IP}:${PORT}/v1"
    echo "   管理: http://${LOCAL_IP}:${PORT}/admin"
    echo ""
fi

echo "══════════════════════════════════════════"
echo "  首次使用请访问管理面板完成初始化"
echo "══════════════════════════════════════════"
echo ""
echo "常用命令:"
echo "  查看日志:  tail -f ${INSTALL_DIR}/server.log"
echo "  重启服务:  systemctl restart openmodelpool"
echo "  停止服务:  systemctl stop openmodelpool"
echo "  查看状态:  systemctl status openmodelpool"
echo ""
