#!/bin/bash
# OpenModelPool 一键安装/升级脚本
# curl -sSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/install.sh | bash
# 或指定版本: curl ... | bash -s -- 4.3.21

set -euo pipefail

REPO="lisiyu/openmodelpool"
DEFAULT_INSTALL_DIR="/opt/openmodelpool"
SERVICE_NAME="openmodelpool"
BINARY_NAME="openmodelpool"
DATA_DIR="/opt/openmodelpool/data"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}→${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}!${NC} $*"; }
fail()  { echo -e "${RED}✗${NC} $*"; exit 1; }

[[ $EUID -ne 0 ]] && fail "请使用 sudo 执行: sudo bash install.sh [版本号]"

# ─── 参数解析 ───
TARGET_VERSION="${1:-}"
if [[ -n "$TARGET_VERSION" && ! "$TARGET_VERSION" =~ ^v ]]; then
    TARGET_VERSION="v$TARGET_VERSION"
fi

# ─── 平台检测 ───
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  PLATFORM="linux-amd64" ;;
    aarch64) PLATFORM="linux-arm64" ;;
    armv7l)  PLATFORM="linux-armv7" ;;
    *)       fail "不支持的架构: $ARCH (仅支持 x86_64/aarch64/armv7l)" ;;
esac

# ─── 版本检测 ───
if [[ -z "$TARGET_VERSION" ]]; then
    info "获取最新版本..."
    TARGET_VERSION=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' | sed 's/.*"tag_name" *: *"\([^"]*\)".*/\1/')
    [[ -z "$TARGET_VERSION" ]] && fail "无法获取最新版本，请检查网络或指定版本号"
fi
info "目标版本: ${YELLOW}$TARGET_VERSION${NC} (${PLATFORM})"

# ─── 下载 URL ───
ASSET="${BINARY_NAME}-${PLATFORM}"
URL="https://github.com/$REPO/releases/download/${TARGET_VERSION}/${ASSET}"
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

# ─── 下载 ───
info "下载二进制..."
HTTP_CODE=$(curl -sL -w "%{http_code}" -o "$TMP_DIR/$ASSET" "$URL")
[[ "$HTTP_CODE" != "200" ]] && fail "下载失败 (HTTP $HTTP_CODE)，版本 $TARGET_VERSION 可能不存在"
SIZE=$(stat -c%s "$TMP_DIR/$ASSET" 2>/dev/null || stat -f%z "$TMP_DIR/$ASSET" 2>/dev/null)
ok "已下载 $(( SIZE / 1024 / 1024 )) MB"

# ─── 校验 ───
if curl -sSL "${URL}.sha256" -o "$TMP_DIR/${ASSET}.sha256" 2>/dev/null; then
    EXPECTED=$(awk '{print $1}' < "$TMP_DIR/${ASSET}.sha256")
    ACTUAL=$(sha256sum "$TMP_DIR/$ASSET" | awk '{print $1}')
    [[ "$EXPECTED" != "$ACTUAL" ]] && fail "SHA-256 校验失败"
    ok "SHA-256 校验通过"
else
    warn "校验文件不可用，跳过"
fi

# ─── systemctl 可用性检测 ───
# Coze 云主机等环境中 systemctl 可能被安全策略阻塞，需要主动探测
USE_SYSTEMCTL=false
if command -v systemctl &>/dev/null; then
    # 实际尝试 systemctl 操作（带超时），判断是否真正可用
    if timeout 5 systemctl status "$SERVICE_NAME" &>/dev/null; then
        USE_SYSTEMCTL=true
        info "systemctl 可用，使用 systemd 管理服务"
    elif timeout 5 systemctl list-units --type=service &>/dev/null; then
        USE_SYSTEMCTL=true
        info "systemctl 可用，使用 systemd 管理服务"
    else
        warn "systemctl 受限（超时或被阻塞），使用直接进程管理"
    fi
else
    warn "systemctl 不可用，使用直接进程管理"
fi

# ─── 停止服务 ───
stop_service() {
    if $USE_SYSTEMCTL; then
        if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
            info "停止服务 (systemctl)..."
            if ! timeout 10 systemctl stop "$SERVICE_NAME" 2>/dev/null; then
                warn "systemctl stop 超时，强制停止进程"
                pkill -x "$BINARY_NAME" 2>/dev/null || true
                sleep 2
            fi
            ok "服务已停止"
        fi
    else
        # Direct process management
        info "停止服务 (直接进程管理)..."
        if pgrep -x "$BINARY_NAME" &>/dev/null; then
            pkill -x "$BINARY_NAME" 2>/dev/null || true
            sleep 2
            # Force kill if still running
            if pgrep -x "$BINARY_NAME" &>/dev/null; then
                pkill -9 -x "$BINARY_NAME" 2>/dev/null || true
                sleep 1
            fi
            ok "进程已停止"
        else
            info "服务未运行"
        fi
    fi
}

stop_service

# ─── 安装目录 ───
INSTALL_DIR="$DEFAULT_INSTALL_DIR"
mkdir -p "$INSTALL_DIR" "$DATA_DIR"

# ─── 备份旧版本 ───
if [[ -f "$INSTALL_DIR/$BINARY_NAME" ]]; then
    OLD_VER=$("$INSTALL_DIR/$BINARY_NAME" --version 2>/dev/null || echo "unknown")
    BACKUP="${INSTALL_DIR}/${BINARY_NAME}.bak"
    cp "$INSTALL_DIR/$BINARY_NAME" "$BACKUP"
    ok "旧版本已备份 (${OLD_VER})"
fi

# ─── 安装 ───
cp "$TMP_DIR/$ASSET" "$INSTALL_DIR/$BINARY_NAME"
chmod 755 "$INSTALL_DIR/$BINARY_NAME"
NEW_VER=$("$INSTALL_DIR/$BINARY_NAME" --version 2>/dev/null || echo "$TARGET_VERSION")
ok "已安装 $NEW_VER"

# ─── systemd unit ───
if $USE_SYSTEMCTL; then
    if [[ ! -f /etc/systemd/system/${SERVICE_NAME}.service ]]; then
        info "创建 systemd 服务..."
        cat > /etc/systemd/system/${SERVICE_NAME}.service << UNIT
[Unit]
Description=OpenModelPool - AI Model Router & Load Balancer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BINARY_NAME
Restart=on-failure
RestartSec=10
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
        systemctl daemon-reload
        systemctl enable "$SERVICE_NAME" 2>/dev/null || true
        ok "systemd 服务已创建并启用"
    else
        info "systemd 服务已存在，跳过创建"
    fi
fi

# ─── 启动 ───
start_service() {
    if $USE_SYSTEMCTL; then
        info "启动服务 (systemctl)..."
        if ! timeout 10 systemctl start "$SERVICE_NAME" 2>/dev/null; then
            warn "systemctl start 失败，降级为直接启动"
            cd "$INSTALL_DIR"
            nohup ./$BINARY_NAME > "$INSTALL_DIR/omp.log" 2>&1 &
            sleep 3
            if ! pgrep -x "$BINARY_NAME" &>/dev/null; then
                fail "服务启动失败，检查日志:\n  cat $INSTALL_DIR/omp.log | tail -50"
            fi
        else
            sleep 3
        fi
        if ! systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null && ! pgrep -x "$BINARY_NAME" &>/dev/null; then
            fail "服务启动失败，检查日志:\n  journalctl -u $SERVICE_NAME -n 50"
        fi
    else
        info "启动服务 (直接启动)..."
        cd "$INSTALL_DIR"
        nohup ./$BINARY_NAME > "$INSTALL_DIR/omp.log" 2>&1 &
        sleep 3
        if ! pgrep -x "$BINARY_NAME" &>/dev/null; then
            fail "服务启动失败，检查日志:\n  cat $INSTALL_DIR/omp.log | tail -50"
        fi
    fi
    ok "服务运行中"
}

start_service

# ─── 健康检查 ───
sleep 2
HEALTH=$(curl -s http://localhost:8000/health 2>/dev/null || true)
if [[ -n "$HEALTH" ]]; then
    H_VER=$(echo "$HEALTH" | grep -o '"version":"[^"]*"' | cut -d'"' -f4)
    H_MOD=$(echo "$HEALTH" | grep -o '"models_available":[0-9]*' | cut -d: -f2)
    H_PROV=$(echo "$HEALTH" | grep -o '"providers_enabled":[0-9]*' | cut -d: -f2)
    ok "健康检查: version=$H_VER, models=$H_MOD, providers=$H_PROV"
else
    warn "健康检查未响应，服务可能仍在初始化"
fi

# ─── 清理旧备份 ───
if [[ -f "${INSTALL_DIR}/${BINARY_NAME}.bak" ]]; then
    OLDEST="${INSTALL_DIR}/${BINARY_NAME}.bak.old"
    [[ -f "$OLDEST" ]] && rm -f "$OLDEST"
    if [[ -f "${INSTALL_DIR}/${BINARY_NAME}.bak.bak" ]]; then
        mv "${INSTALL_DIR}/${BINARY_NAME}.bak.bak" "$OLDEST" 2>/dev/null || true
    fi
fi

# ─── 完成 ───
IP=$(curl -s --connect-timeout 3 https://icanhazip.com 2>/dev/null || echo "your-server-ip")

echo ""
echo "╔══════════════════════════════════════════╗"
echo -e "║  ${GREEN}OpenModelPool 部署完成${NC}                ║"
echo "╠══════════════════════════════════════════╣"
echo "║  版本:    $TARGET_VERSION"
echo "║  架构:    $PLATFORM"
echo "║  路径:    $INSTALL_DIR/$BINARY_NAME"
echo "║  数据:    $DATA_DIR"
echo "║  管理面板: http://$IP:8000"
if $USE_SYSTEMCTL; then
echo "║  日志:    journalctl -u $SERVICE_NAME -f"
else
echo "║  日志:    tail -f $INSTALL_DIR/omp.log"
fi
echo "╚══════════════════════════════════════════╝"
