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

# ─── 区域检测（根据 IP 判断 VPS 所在区域，优选下载源）───
# 返回: cn (中国大陆) | global (海外/其他)
detect_region() {
    local ip country
    # 尝试多个 IP 查询服务，任一成功即返回
    ip=$(curl -s --connect-timeout 3 https://ifconfig.me 2>/dev/null) || \
    ip=$(curl -s --connect-timeout 3 https://api.ipify.org 2>/dev/null) || \
    ip=$(curl -s --connect-timeout 3 https://icanhazip.com 2>/dev/null) || true
    
    if [[ -z "$ip" ]]; then
        echo "global"  # 无法获取 IP，默认海外
        return
    fi
    
    # 查询 IP 归属地
    country=$(curl -s --connect-timeout 3 "http://ip-api.com/line/${ip}?fields=countryCode" 2>/dev/null) || country=""
    
    if [[ "$country" == "CN" ]]; then
        echo "cn"
    else
        echo "global"
    fi
}

REGION=$(detect_region)
if [[ "$REGION" == "cn" ]]; then
    info "检测到中国大陆网络环境，优先使用镜像下载"
else
    info "检测到海外网络环境，优先直连 GitHub"
fi

# ─── 版本检测 ───
if [[ -z "$TARGET_VERSION" ]]; then
    info "获取最新版本..."
    TARGET_VERSION=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' | sed 's/.*"tag_name" *: *"\([^"]*\)".*/\1/')
    [[ -z "$TARGET_VERSION" ]] && fail "无法获取最新版本，请检查网络或指定版本号"
fi
info "目标版本: ${YELLOW}$TARGET_VERSION${NC} (${PLATFORM})"

# 下载源列表：根据区域自动优选
# 中国大陆：镜像优先；海外：直连优先
# 与 OMP 自动更新逻辑保持一致的多源策略
ASSET="${BINARY_NAME}-${PLATFORM}"
URL="https://github.com/$REPO/releases/download/${TARGET_VERSION}/${ASSET}"
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

if [[ "$REGION" == "cn" ]]; then
    # 中国大陆：镜像优先，直连兜底
    MIRRORS=(
        "https://ghfast.top/$URL"
        "https://gh-proxy.com/$URL"
        "https://ghproxy.net/$URL"
        "https://mirror.ghproxy.com/$URL"
        "$URL"
    )
else
    # 海外：直连优先，镜像兜底
    MIRRORS=(
        "$URL"
        "https://ghfast.top/$URL"
        "https://gh-proxy.com/$URL"
        "https://ghproxy.net/$URL"
        "https://mirror.ghproxy.com/$URL"
    )
fi

# ─── 带重试的多源下载 ───
download_with_retry() {
    local url="$1" dest="$2" max_tries="${3:-3}" timeout="${4:-120}"
    local attempt=1 last_err=""
    while [ $attempt -le $max_tries ]; do
        if [ $attempt -gt 1 ]; then
            local backoff=$(( attempt * 2 ))
            info "重试第 ${attempt} 次（等待 ${backoff}s）..."
            sleep $backoff
        fi
        local http_code
        http_code=$(curl -sSL --connect-timeout 30 --max-time "$timeout"             -w "%{http_code}" -o "$dest" "$url" 2>/dev/null) || http_code="000"
        if [ "$http_code" = "200" ]; then
            local size
            size=$(stat -c%s "$dest" 2>/dev/null || stat -f%z "$dest" 2>/dev/null || echo 0)
            if [ "$size" -lt 100000 ]; then
                last_err="文件异常 (${size}B)"
                attempt=$((attempt + 1)); continue
            fi
            return 0
        fi
        last_err="HTTP $http_code"
        attempt=$((attempt + 1))
    done
    warn "源下载失败 ($last_err): $(echo "$url" | cut -c1-80)..."
    return 1
}

info "下载二进制（多源兜底）..."
DOWNLOADED=false
USED_SOURCE=""
for src_url in "${MIRRORS[@]}"; do
    label=$(echo "$src_url" | sed 's|https://||;s|/.*||;s|^$|github-direct|')
    info "尝试源: $label"
    if download_with_retry "$src_url" "$TMP_DIR/$ASSET" 2 90; then
        DOWNLOADED=true
        USED_SOURCE="$label"
        break
    fi
    rm -f "$TMP_DIR/$ASSET"
done

if ! $DOWNLOADED; then
    fail "所有下载源均失败，版本 $TARGET_VERSION 可能不存在或网络不可达"
fi

SIZE=$(stat -c%s "$TMP_DIR/$ASSET" 2>/dev/null || stat -f%z "$TMP_DIR/$ASSET" 2>/dev/null)
ok "已下载 $(( SIZE / 1024 / 1024 )) MB（via $USED_SOURCE）"

# ─── 校验（SEC-P2-12: fail-closed，仅从 GitHub 官方获取校验，镜像只信字节）───
SHA_DOWNLOADED=false
# 只使用 GitHub 官方 release 资产的 .sha256 —— 镜像的校验文件绝不作为校验源。
SHA_URLS=("${URL}.sha256")
for sha_url in "${SHA_URLS[@]}"; do
    if curl -sSL --connect-timeout 10 --max-time 30 "$sha_url" -o "$TMP_DIR/${ASSET}.sha256" 2>/dev/null; then
        if grep -qE '^[a-f0-9]{64}' "$TMP_DIR/${ASSET}.sha256" 2>/dev/null; then
            SHA_DOWNLOADED=true
            break
        fi
    fi
done

if ! $SHA_DOWNLOADED; then
    fail "无法从 GitHub 官方获取 SHA-256 校验文件，已中止安装（fail-closed）"
fi

EXPECTED=$(awk '{print $1}' < "$TMP_DIR/${ASSET}.sha256")
ACTUAL=$(sha256sum "$TMP_DIR/$ASSET" | awk '{print $1}')
if [[ "$EXPECTED" != "$ACTUAL" ]]; then
    fail "SHA-256 校验失败！expected=$EXPECTED actual=$ACTUAL"
fi
ok "SHA-256 校验通过"

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
