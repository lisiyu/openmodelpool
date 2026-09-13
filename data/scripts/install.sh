#!/bin/bash
# OpenModelPool 一键安装/升级脚本（组件化，支持单独升级各依赖组件）
#
#   curl -sSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/install.sh | bash
#
# 组件列表（均可单独升级）:
#   core         OpenModelPool 主程序（release 资产，强制 SHA-256 校验）
#   xray         XTLS/Xray-core —— provider 用 vmess:// vless:// 时的本地代理
#   cloudflared  Cloudflare 隧道客户端（tunnel 模式经 PATH 查找）
#   frp          frps + frpc（FRP 自建隧道可选组件）
#   ngrok        ngrok agent（ngrok 隧道可选组件）
#   browser      内置浏览器核心（chromedp 登录依赖的 headless Chrome）
#   status       查看所有组件安装状态与版本
#
# 用例:
#   sudo bash install.sh                      全部组件各取最新
#   sudo bash install.sh 4.5.34               全部，核心固定 v4.5.34（向后兼容）
#   sudo bash install.sh core 4.5.34          仅升级核心到固定版本
#   sudo bash install.sh xray                 仅升级 Xray
#   sudo bash install.sh cloudflared          仅升级 Cloudflare 隧道
#   sudo bash install.sh frp                  仅升级 frp
#   sudo bash install.sh ngrok                仅升级 ngrok
#   sudo bash install.sh browser              仅升级内置浏览器核心
#   sudo bash install.sh status               查看组件状态
#
# 跳过可选组件（仅对"全部"生效）:
#   OMP_SKIP_XRAY=1 OMP_SKIP_CLOUDFLARED=1 OMP_SKIP_FRP=1 \
#   OMP_SKIP_NGROK=1 OMP_SKIP_BROWSER=1 sudo bash install.sh

set -euo pipefail

# ===== 并发锁：防止多个 install.sh 实例同时运行损坏文件 =====
LOCK_FILE="/tmp/omp-install.lock"
if command -v flock >/dev/null 2>&1; then
    exec 8>"$LOCK_FILE"
    if ! flock -n 8; then
        echo "⚠️  检测到另一个 install.sh 正在运行，已退出以避免冲突"
        echo "   如确认无其他实例，可删除 $LOCK_FILE 后重试"
        exit 1
    fi
fi

REPO="lisiyu/openmodelpool"
XRAY_REPO="XTLS/Xray-core"
CLOUDFLARED_REPO="cloudflare/cloudflared"
FRP_REPO="fatedier/frp"
NGROK_REPO="ngrok/ngrok-v3"
DEFAULT_INSTALL_DIR="/opt/openmodelpool"
SERVICE_NAME="openmodelpool"
BINARY_NAME="openmodelpool"
DATA_DIR="$DEFAULT_INSTALL_DIR/data"
XRAY_DIR="$DEFAULT_INSTALL_DIR/xray"
XRAY_BIN="$XRAY_DIR/xray"
BROWSER_DIR="$DEFAULT_INSTALL_DIR/browser"
LOCAL_BIN="/usr/local/bin"

# 端口：支持 OMP_PORT 环境变量覆盖，默认 8000
OMP_PORT="${OMP_PORT:-8000}"
# 校验端口合法性
if ! [[ "$OMP_PORT" =~ ^[0-9]+$ ]] || [ "$OMP_PORT" -lt 1 ] || [ "$OMP_PORT" -gt 65535 ]; then
    fail "无效端口号: $OMP_PORT (必须为 1-65535 的整数)"
fi

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; NC=$'\033[0m'
info() { echo -e "${CYAN}->${NC} $*"; }
ok()   { echo -e "${GREEN}ok${NC} $*"; }
warn() { echo -e "${YELLOW}!${NC} $*"; }
fail() { echo -e "${RED}x${NC} $*"; exit 1; }

[[ $EUID -ne 0 ]] && fail "请使用 sudo 执行: sudo bash install.sh [组件] [版本号]"

show_help() {
    cat <<'EOF'
用法: sudo bash install.sh [组件] [版本]

组件（不传或 all 时核心可选固定版本，向后兼容旧用法）:
  (空) | all          全部组件升级
  core [<版本>]       仅核心，版本如 4.5.34
  xray                仅 Xray（vmess/vless 本地代理）
  cloudflared         仅 Cloudflare 隧道客户端
  frp                 仅 frps + frpc
  ngrok               仅 ngrok agent
  browser             仅内置浏览器核心（headless Chrome）
  status              查看各组件安装状态与版本
  help                显示本帮助

跳过可选组件（对"全部"生效）:
  OMP_SKIP_XRAY=1 OMP_SKIP_CLOUDFLARED=1 OMP_SKIP_FRP=1
  OMP_SKIP_NGROK=1 OMP_SKIP_BROWSER=1 sudo bash install.sh

已有安装时的行为:
  all 模式  检测到已安装组件默认直接复用（[Y/n]），回车即跳过下载；非交互环境亦复用
  子命令    默认下载并升级到最新（[y/N]，输入 Y 才复用）—— 版本更高/相同也想切换时可交互选择
EOF
}

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  PLATFORM="linux-amd64" ;;
    aarch64) PLATFORM="linux-arm64" ;;
    armv7l)  PLATFORM="linux-armv7" ;;
    *)       fail "不支持的架构: $ARCH (仅支持 x86_64/aarch64/armv7l)" ;;
esac

detect_region() {
    local ip country
    ip=$(curl -s --connect-timeout 3 https://ifconfig.me 2>/dev/null) || \
    ip=$(curl -s --connect-timeout 3 https://api.ipify.org 2>/dev/null) || \
    ip=$(curl -s --connect-timeout 3 https://icanhazip.com 2>/dev/null) || true
    if [[ -z "$ip" ]]; then echo "global"; return; fi
    country=$(curl -s --connect-timeout 3 "https://ipapi.co/${ip}/country_code/" 2>/dev/null) || country=""
    if [[ "$country" == "CN" ]]; then echo "cn"; else echo "global"; fi
}

REGION=$(detect_region)
if [[ "$REGION" == "cn" ]]; then
    info "检测到中国大陆网络环境，优先使用镜像下载"
else
    info "检测到海外网络环境，优先直连 GitHub"
fi

mirrors_for() {
    local url="$1"
    if [[ "$url" != https://github.com/* ]]; then
        echo "$url"   # 非 GitHub 源（如 Chrome for Testing）不做镜像代理
        return
    fi
    if [[ "$REGION" == "cn" ]]; then
        echo "https://ghfast.top/$url|https://gh-proxy.com/$url|https://ghproxy.net/$url|$url"
    else
        echo "$url|https://ghfast.top/$url|https://gh-proxy.com/$url|https://ghproxy.net/$url"
    fi
}

download_with_retry() {
    local url="$1" dest="$2" max_tries="${3:-3}" timeout="${4:-120}"
    local attempt=1 last_err=""
    while [ $attempt -le $max_tries ]; do
        if [ $attempt -gt 1 ]; then
            info "重试第 ${attempt} 次（等待 $(( attempt * 2 ))s）..."
            sleep $(( attempt * 2 ))
        fi
        local http_code
        http_code=$(curl -sSL --connect-timeout 30 --max-time "$timeout" \
            -w "%{http_code}" -o "$dest" "$url" 2>/dev/null) || http_code="000"
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

download_multisource() {
    local raw_url="$1" dest="$2" label="$3"
    local src_list old_ifs
    old_ifs=$IFS; IFS='|'
    src_list=$(mirrors_for "$raw_url")
    for src_url in $src_list; do
        local src_label
        src_label=$(echo "$src_url" | sed 's|https://||;s|/.*||;s|^$|github-direct|')
        info "尝试源 ($label): $src_label"
        if download_with_retry "$src_url" "$dest" 2 90; then
            IFS=$old_ifs
            return 0
        fi
        rm -f "$dest"
    done
    IFS=$old_ifs
    return 1
}

get_latest_tag() {
    local repo="$1"
    curl -sSL --connect-timeout 10 --max-time 30 \
        "https://api.github.com/repos/$repo/releases/latest" 2>/dev/null \
        | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4
}

# ══════════════════════════════════════════════════
#  组件: core —— OpenModelPool 主程序
# ══════════════════════════════════════════════════

stop_core_service() {
    local use_systemctl="$1"
    if $use_systemctl; then
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
        if pgrep -x "$BINARY_NAME" &>/dev/null; then
            info "停止服务 (直接进程管理)..."
            pkill -x "$BINARY_NAME" 2>/dev/null || true
            sleep 2
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

start_core_service() {
    local use_systemctl="$1" install_dir="$2"
    if $use_systemctl; then
        info "启动服务 (systemctl)..."
        if ! timeout 10 systemctl start "$SERVICE_NAME" 2>/dev/null; then
            warn "systemctl start 失败，降级为直接启动"
            cd "$install_dir"
            nohup ./$BINARY_NAME > "$install_dir/omp.log" 2>&1 &
            sleep 3
            if ! pgrep -x "$BINARY_NAME" &>/dev/null; then
                fail "服务启动失败，检查日志: cat $install_dir/omp.log | tail -50"
            fi
        else
            sleep 3
        fi
        if ! systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null && ! pgrep -x "$BINARY_NAME" &>/dev/null; then
            fail "服务启动失败，检查日志: journalctl -u $SERVICE_NAME -n 50"
        fi
    else
        info "启动服务 (直接启动)..."
        cd "$install_dir"
        nohup ./$BINARY_NAME > "$install_dir/omp.log" 2>&1 &
        sleep 3
        if ! pgrep -x "$BINARY_NAME" &>/dev/null; then
            fail "服务启动失败，检查日志: cat $install_dir/omp.log | tail -50"
        fi
    fi
    ok "服务运行中"
}

install_core() {
    local TARGET_VERSION="${1:-}"
    local TMP_DIR ASSET URL SIZE HEALTH H_VER H_MOD H_PROV
    TMP_DIR=$(mktemp -d)

    if [[ -z "$TARGET_VERSION" ]]; then
        info "获取 OpenModelPool 最新版本..."
        TARGET_VERSION=$(get_latest_tag "$REPO")
        [[ -z "$TARGET_VERSION" ]] && { rm -rf "$TMP_DIR"; fail "无法获取最新版本，请检查网络或指定版本号"; }
    elif [[ ! "$TARGET_VERSION" =~ ^v ]]; then
        TARGET_VERSION="v$TARGET_VERSION"
    fi
    info "核心版本: ${YELLOW}${TARGET_VERSION}${NC} (${PLATFORM})"

    ASSET="${BINARY_NAME}-${PLATFORM}"
    URL="https://github.com/${REPO}/releases/download/${TARGET_VERSION}/${ASSET}"

    info "下载核心二进制（多源兜底）..."
    if ! download_multisource "$URL" "$TMP_DIR/$ASSET" "core"; then
        rm -rf "$TMP_DIR"
        fail "核心二进制下载失败，版本 ${TARGET_VERSION} 可能不存在或网络不可达"
    fi
    SIZE=$(stat -c%s "$TMP_DIR/$ASSET" 2>/dev/null || stat -f%z "$TMP_DIR/$ASSET" 2>/dev/null)
    ok "已下载 $(( SIZE / 1024 / 1024 )) MB（核心 ${TARGET_VERSION}）"

    # SEC-P2-12 fail-closed: 仅 GitHub 官方 sha256 资产作为校验源，镜像只信字节
    SHA_OK=false
    if curl -sSL --connect-timeout 10 --max-time 30 "${URL}.sha256" -o "$TMP_DIR/$ASSET.sha256" 2>/dev/null; then
        if grep -qE '^[a-f0-9]{64}' "$TMP_DIR/$ASSET.sha256" 2>/dev/null; then SHA_OK=true; fi
    fi
    if ! $SHA_OK; then
        rm -rf "$TMP_DIR"
        fail "无法从 GitHub 官方获取 SHA-256 校验文件，已中止安装（fail-closed）"
    fi
    EXPECTED=$(awk '{print $1}' < "$TMP_DIR/$ASSET.sha256")
    ACTUAL=$(sha256sum "$TMP_DIR/$ASSET" | awk '{print $1}')
    if [[ "$EXPECTED" != "$ACTUAL" ]]; then
        rm -rf "$TMP_DIR"
        fail "SHA-256 校验失败！expected=$EXPECTED actual=$ACTUAL"
    fi
    ok "SHA-256 校验通过"

    USE_SYSTEMCTL=false
    if command -v systemctl &>/dev/null; then
        if timeout 5 systemctl status "$SERVICE_NAME" &>/dev/null || timeout 5 systemctl list-units --type=service &>/dev/null; then
            USE_SYSTEMCTL=true
            info "systemctl 可用，使用 systemd 管理服务"
        else
            warn "systemctl 受限（超时或被阻塞），使用直接进程管理"
        fi
    else
        warn "systemctl 不可用，使用直接进程管理"
    fi

    stop_core_service "$USE_SYSTEMCTL"

    mkdir -p "$DEFAULT_INSTALL_DIR" "$DATA_DIR"

    if [[ -f "$DEFAULT_INSTALL_DIR/$BINARY_NAME" ]]; then
        cp "$DEFAULT_INSTALL_DIR/$BINARY_NAME" "$DEFAULT_INSTALL_DIR/$BINARY_NAME.bak"
        ok "旧版本已备份"
    fi

    cp "$TMP_DIR/$ASSET" "$DEFAULT_INSTALL_DIR/$BINARY_NAME"
    chmod 755 "$DEFAULT_INSTALL_DIR/$BINARY_NAME"
    rm -rf "$TMP_DIR"
    ok "核心已安装 ${YELLOW}${TARGET_VERSION}${NC}"

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
WorkingDirectory=$DEFAULT_INSTALL_DIR
ExecStart=$DEFAULT_INSTALL_DIR/$BINARY_NAME
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

    start_core_service "$USE_SYSTEMCTL" "$DEFAULT_INSTALL_DIR"

    sleep 2
    HEALTH=$(curl -s http://localhost:${OMP_PORT}/health 2>/dev/null || true)
    if [[ -n "$HEALTH" ]]; then
        H_VER=$(echo "$HEALTH" | grep -o '"version":"[^"]*"' | cut -d'"' -f4)
        H_MOD=$(echo "$HEALTH" | grep -o '"models_available":[0-9]*' | cut -d: -f2)
        H_PROV=$(echo "$HEALTH" | grep -o '"providers_enabled":[0-9]*' | cut -d: -f2)
        ok "健康检查: version=$H_VER, models=$H_MOD, providers=$H_PROV"
    else
        warn "健康检查未响应，服务可能仍在初始化"
    fi
}

extract_home_path() {
    local dg="$1" fname="$2" h
    h=$(grep -i "$fname" "$dg" 2>/dev/null | grep -oE '[a-f0-9]{64}' | head -1)
    if [[ -z "$h" && "$(grep -cE '[a-f0-9]{64}' "$dg" 2>/dev/null)" = "1" ]]; then
        h=$(grep -oE '[a-f0-9]{64}' "$dg" | head -1)
    fi
    echo "$h"
}

# ══════════════════════════════════════════════════
#  组件: xray —— XTLS/Xray-core（vmess/vless 本地代理依赖）
# ══════════════════════════════════════════════════

# 交互询问是否跳过下载、复用已有安装。
#   $1 = 默认值 (y=默认复用, n=默认下载)；$2 = 组件名
#   非交互环境（stdin 非 TTY）按默认值静默处理，不阻塞管道用法。
prompt_reuse() {
    local def="$1" name="$2" ans
    if [[ ! -t 0 ]]; then
        if [[ "$def" == "y" ]]; then
            info "非交互环境，默认复用已有 $name"
            return 0
        fi
        return 1
    fi
    if [[ "$def" == "y" ]]; then
        read -r -p "  检测到已有 $name，跳过下载、直接复用？[Y/n] " ans
        if [[ -z "$ans" || "$ans" =~ ^[Yy] ]]; then return 0; fi
    else
        read -r -p "  检测到已有 $name，跳过下载、直接复用？[y/N] " ans
        if [[ "$ans" =~ ^[Yy] ]]; then return 0; fi
    fi
    return 1
}

# 在常见位置查找已有的 xray 可执行文件，找到就复用，避免重复下载
_find_existing_xray() {
    local candidates=(
        "$XRAY_BIN"                    # 标准安装位置
        "$DEFAULT_INSTALL_DIR/data/xray/xray"  # 兼容手动放置到 data 目录
        "$LOCAL_BIN/xray"              # PATH 常见位置
    )
    # PATH 兜底
    if command -v xray &>/dev/null; then
        candidates+=("$(command -v xray)")
    fi
    for p in "${candidates[@]}"; do
        if [[ -x "$p" ]]; then
            echo "$p"
            return 0
        fi
    done
    return 1
}

install_xray() {
    # $1 = reuse 默认值：all 模式传 y（默认复用），显式子命令传 n（默认下载最新）
    local reuse_default="${1:-n}"
    local existing
    if existing=$(_find_existing_xray); then
        local cur
        cur=$("$existing" version 2>/dev/null | grep -o 'Xray [^ ]*' | head -1 | cut -d' ' -f2)
        info "Xray 已存在: ${cur:-unknown} ($existing)"
        if prompt_reuse "$reuse_default" "Xray ${cur:-}"; then
            # 选择复用：非标准位置统一复制到 $XRAY_BIN
            if [[ "$existing" != "$XRAY_BIN" ]]; then
                info "  复制到标准位置 $XRAY_BIN"
                mkdir -p "$XRAY_DIR"
                local existing_dir
                existing_dir=$(dirname "$existing")
                cp "$existing" "$XRAY_BIN" 2>/dev/null || true
                chmod 755 "$XRAY_BIN" 2>/dev/null || true
                # 同步 geoip.dat / geosite.dat（如果有）
                for f in geoip.dat geosite.dat; do
                    [[ -f "$existing_dir/$f" ]] && cp "$existing_dir/$f" "$XRAY_DIR/" 2>/dev/null || true
                done
                if [[ ! -x "$XRAY_BIN" ]]; then
                    warn "复制到标准位置失败，改为下载最新版本"
                else
                    ok "Xray 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                    return 0
                fi
            else
                ok "Xray 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                return 0
            fi
        fi
        info "  将下载最新版本"
    fi

    local VER ASSET ZIP_URL TMP_DIR
    VER=$(get_latest_tag "$XRAY_REPO")
    [[ -z "$VER" ]] && { warn "获取 Xray 版本失败，跳过"; return 1; }

    case "$PLATFORM" in
        linux-amd64)  ASSET="Xray-linux-64.zip" ;;
        linux-arm64)  ASSET="Xray-linux-arm64-v8a.zip" ;;
        linux-armv7)  ASSET="Xray-linux-arm32-v7a.zip" ;;
        *)  warn "不支持 Xray 平台: $PLATFORM，跳过"; return 1 ;;
    esac

    info "Xray 版本: ${YELLOW}${VER}${NC} ($ASSET)"
    ZIP_URL="https://github.com/${XRAY_REPO}/releases/download/${VER}/${ASSET}"
    TMP_DIR=$(mktemp -d)

    if ! download_multisource "$ZIP_URL" "$TMP_DIR/xray.zip" "xray"; then
        rm -rf "$TMP_DIR"
        warn "Xray 下载失败，跳过（vmess/vless 代理将不可用）"
        return 1
    fi

    # SHA256 校验（fail-closed：仅从 GitHub 官方直连获取 .dgst 校验文件）
    local dg_dir="$TMP_DIR/dgst" exp act
    mkdir -p "$dg_dir"
    # 从 GitHub 官方 canonical 源获取校验和（不走镜像，确保可信）
    local XRAY_CANONICAL="https://github.com/${XRAY_REPO}/releases/download/${XRAY_VER}/${ASSET}.dgst"
    if ! curl -sSL --connect-timeout 10 --max-time 30 "$XRAY_CANONICAL" -o "$dg_dir/$ASSET.dgst" 2>/dev/null; then
        rm -rf "$TMP_DIR"
        warn "Xray SHA-256 校验失败（无法从 GitHub 官方获取校验文件），跳过安装"
        return 1
    fi
    exp=$(extract_home_path "$dg_dir/$ASSET.dgst" "$ASSET")
    act=$(sha256sum "$TMP_DIR/xray.zip" | awk '{print $1}')
    if [[ -z "$exp" || "$exp" != "$act" ]]; then
        rm -rf "$TMP_DIR"
        warn "Xray SHA-256 校验不匹配，跳过安装（expected=$exp actual=$act）"
        return 1
    fi
    ok "Xray SHA-256 校验通过（来源：GitHub 官方）"

    mkdir -p "$XRAY_DIR"
    if command -v unzip &>/dev/null; then
        unzip -o -q "$TMP_DIR/xray.zip" -d "$XRAY_DIR" || true
    elif command -v python3 &>/dev/null; then
        python3 -m zipfile -e "$TMP_DIR/xray.zip" "$XRAY_DIR" || true
    else
        rm -rf "$TMP_DIR"
        warn "未找到 unzip/python3 无法解压 Xray，跳过"
        return 1
    fi
    rm -rf "$TMP_DIR"
    chmod 755 "$XRAY_BIN" 2>/dev/null || true

    if [[ -x "$XRAY_BIN" ]]; then
        ok "Xray 已安装: ${XRAY_BIN} (${VER})"
    else
        warn "Xray 解压后未找到可执行文件，vmess/vless 代理将不可用"
    fi
}

# ══════════════════════════════════════════════════
#  组件: cloudflared —— Cloudflare 隧道客户端（tunnel 模式）
# ══════════════════════════════════════════════════

install_cloudflared() {
    # $1 = reuse 默认值：all 传 y，显式子命令传 n
    local reuse_default="${1:-n}"
    # 扫描常见位置已有安装，找到就复用
    local cf_bin cf_candidates=(
        "$LOCAL_BIN/cloudflared"                    # 标准安装位置
        "$DEFAULT_INSTALL_DIR/cloudflared"          # 兼容手动放置到安装目录
        "/usr/bin/cloudflared"
    )
    if command -v cloudflared &>/dev/null; then
        cf_candidates+=("$(command -v cloudflared)")
    fi
    for p in "${cf_candidates[@]}"; do
        if [[ -x "$p" ]]; then cf_bin="$p"; break; fi
    done
    if [[ -n "$cf_bin" ]]; then
        local cur
        cur=$("$cf_bin" --version 2>/dev/null | head -1)
        info "cloudflared 已存在: ${cur:-unknown} ($cf_bin)"
        if prompt_reuse "$reuse_default" "cloudflared ${cur:-}"; then
            # 非标准位置复制一份统一管理
            if [[ "$cf_bin" != "$LOCAL_BIN/cloudflared" ]]; then
                info "  复制到标准位置 $LOCAL_BIN/cloudflared"
                mkdir -p "$LOCAL_BIN"
                cp "$cf_bin" "$LOCAL_BIN/cloudflared" 2>/dev/null || true
                chmod 755 "$LOCAL_BIN/cloudflared" 2>/dev/null || true
                if [[ ! -x "$LOCAL_BIN/cloudflared" ]]; then
                    warn "复制到标准位置失败，改为下载最新版本"
                else
                    ok "cloudflared 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                    return 0
                fi
            else
                ok "cloudflared 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                return 0
            fi
        fi
        info "  将下载最新版本"
    fi

    local VER ASSET UV TMP_DIR
    VER=$(get_latest_tag "$CLOUDFLARED_REPO")
    [[ -z "$VER" ]] && { warn "获取 cloudflared 版本失败，跳过"; return 1; }

    case "$PLATFORM" in
        linux-amd64)  ASSET="cloudflared-linux-amd64" ;;
        linux-arm64)  ASSET="cloudflared-linux-arm64" ;;
        linux-armv7)  ASSET="cloudflared-linux-arm" ;;
    esac
    if [[ -z "${ASSET:-}" ]]; then warn "不支持 cloudflared 平台: $PLATFORM，跳过"; return 1; fi

    info "cloudflared 版本: ${YELLOW}${VER}${NC}"
    UV="https://github.com/${CLOUDFLARED_REPO}/releases/download/${VER}/${ASSET}"
    TMP_DIR=$(mktemp -d)

    if ! download_multisource "$UV" "$TMP_DIR/cloudflared" "cloudflared"; then
        rm -rf "$TMP_DIR"
        warn "cloudflared 下载失败，跳过（tunnel 模式将不可用）"
        return 1
    fi

    # SHA256 校验（fail-closed：仅从 GitHub 官方直连获取 checksums.txt）
    local CHECKSUMS_URL="https://github.com/${CLOUDFLARED_REPO}/releases/download/${VER}/checksums.txt"
    local SHA_FILE="$TMP_DIR/checksums.txt"
    local EXPECTED_SHA=""
    if curl -fsSL --connect-timeout 10 --max-time 30 "$CHECKSUMS_URL" -o "$SHA_FILE" 2>/dev/null; then
        EXPECTED_SHA=$(grep -E "[a-f0-9]{64}.*${ASSET}" "$SHA_FILE" 2>/dev/null | awk '{print $1}' | head -1)
    fi
    if [[ -z "$EXPECTED_SHA" ]]; then
        rm -rf "$TMP_DIR"
        warn "cloudflared SHA256 校验失败（无法从 GitHub 官方获取校验和），跳过安装"
        return 1
    fi
    local ACTUAL_SHA
    ACTUAL_SHA=$(sha256sum "$TMP_DIR/cloudflared" | awk '{print $1}')
    if [[ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]]; then
        rm -rf "$TMP_DIR"
        warn "cloudflared SHA256 校验不匹配，跳过安装（可能文件被篡改）"
        return 1
    fi
    ok "cloudflared SHA256 校验通过"

    install -m 755 "$TMP_DIR/cloudflared" "$LOCAL_BIN/cloudflared"
    rm -rf "$TMP_DIR"
    if "$LOCAL_BIN/cloudflared" --version &>/dev/null; then
        ok "cloudflared 已安装: $LOCAL_BIN/cloudflared (${VER})"
    else
        warn "cloudflared 安装后无法运行，请检查"
    fi
}

# ══════════════════════════════════════════════════
#  组件: frp —— frps + frpc（FRP 自建隧道可选组件）
# ══════════════════════════════════════════════════

install_frp() {
    # $1 = reuse 默认值：all 传 y，显式子命令传 n
    local reuse_default="${1:-n}"
    # 扫描常见位置已有安装，找到就复用
    local frps_bin frpc_bin found_frps=0 found_frpc=0
    local frp_candidates=(
        "$LOCAL_BIN/frps"                          # 标准安装位置
        "$DEFAULT_INSTALL_DIR/frp/frps"            # 兼容手动放置
        "/usr/bin/frps"
    )
    for p in "${frp_candidates[@]}"; do
        if [[ -x "$p" ]]; then frps_bin="$p"; found_frps=1; break; fi
    done
    if command -v frps &>/dev/null; then
        frps_bin="$(command -v frps)"; found_frps=1
    fi
    local frpc_candidates=(
        "$LOCAL_BIN/frpc"
        "$DEFAULT_INSTALL_DIR/frp/frpc"
        "/usr/bin/frpc"
    )
    for p in "${frpc_candidates[@]}"; do
        if [[ -x "$p" ]]; then frpc_bin="$p"; found_frpc=1; break; fi
    done
    if command -v frpc &>/dev/null; then
        frpc_bin="$(command -v frpc)"; found_frpc=1
    fi
    if [[ $found_frps -eq 1 && $found_frpc -eq 1 ]]; then
        local cur
        cur=$("$frps_bin" --version 2>/dev/null | head -1)
        info "frp 已存在: ${cur:-unknown} (frps=$frps_bin, frpc=$frpc_bin)"
        if prompt_reuse "$reuse_default" "frp ${cur:-}"; then
            local needs_copy=0
            if [[ "$frps_bin" != "$LOCAL_BIN/frps" ]]; then needs_copy=1; fi
            if [[ "$frpc_bin" != "$LOCAL_BIN/frpc" ]]; then needs_copy=1; fi
            if [[ $needs_copy -eq 1 ]]; then
                info "  复制到标准位置 $LOCAL_BIN/"
                mkdir -p "$LOCAL_BIN"
                cp "$frps_bin" "$LOCAL_BIN/frps" 2>/dev/null || true
                cp "$frpc_bin" "$LOCAL_BIN/frpc" 2>/dev/null || true
                chmod 755 "$LOCAL_BIN/frps" "$LOCAL_BIN/frpc" 2>/dev/null || true
                if [[ ! -x "$LOCAL_BIN/frps" || ! -x "$LOCAL_BIN/frpc" ]]; then
                    warn "复制到标准位置失败，改为下载最新版本"
                else
                    ok "frp 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                    return 0
                fi
            else
                ok "frp 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                return 0
            fi
        fi
        info "  将下载最新版本"
    elif [[ $found_frps -eq 1 || $found_frpc -eq 1 ]]; then
        info "frp 部分存在（frps=${frps_bin:-无}, frpc=${frpc_bin:-无}），将下载并按最新版本补齐"
    fi

    local VER V ASSET UV TMP_DIR
    VER=$(get_latest_tag "$FRP_REPO")
    [[ -z "$VER" ]] && { warn "获取 frp 版本失败，跳过"; return 1; }
    V="${VER#v}"

    case "$PLATFORM" in
        linux-amd64)  FRP_OSA="linux_amd64" ;;
        linux-arm64)  FRP_OSA="linux_arm64" ;;
        linux-armv7)  FRP_OSA="linux_armv7" ;;
        *)  warn "不支持 frp 平台: $PLATFORM，跳过"; return 1 ;;
    esac
    ASSET="frp_${V}_${FRP_OSA}.tar.gz"
    UV="https://github.com/${FRP_REPO}/releases/download/${VER}/${ASSET}"

    info "frp 版本: ${YELLOW}${VER}${NC} ($ASSET)"
    TMP_DIR=$(mktemp -d)
    if ! download_multisource "$UV" "$TMP_DIR/frp.tar.gz" "frp"; then
        rm -rf "$TMP_DIR"
        warn "frp 下载失败，跳过"
        return 1
    fi

    mkdir -p "$TMP_DIR/unpack"
    tar -xzf "$TMP_DIR/frp.tar.gz" -C "$TMP_DIR/unpack" 2>/dev/null || { rm -rf "$TMP_DIR"; warn "frp 解压失败，跳过"; return 1; }
    local frps_src frpc_src
    frps_src=$(find "$TMP_DIR/unpack" -type f -name frps | head -1)
    frpc_src=$(find "$TMP_DIR/unpack" -type f -name frpc | head -1)
    if [[ -n "$frps_src" ]]; then install -m 755 "$frps_src" "$LOCAL_BIN/frps"; fi
    if [[ -n "$frpc_src" ]]; then install -m 755 "$frpc_src" "$LOCAL_BIN/frpc"; fi
    rm -rf "$TMP_DIR"

    if [[ -x "$LOCAL_BIN/frps" ]]; then
        ok "frps 已安装: $LOCAL_BIN/frps (${VER})"
    else
        warn "frps 未找到，安装失败"
    fi
    if [[ -x "$LOCAL_BIN/frpc" ]]; then
        ok "frpc 已安装: $LOCAL_BIN/frpc (${VER})"
    fi
}

# ══════════════════════════════════════════════════
#  组件: ngrok —— ngrok agent（ngrok 隧道可选组件）
# ══════════════════════════════════════════════════

install_ngrok() {
    # $1 = reuse 默认值：all 传 y，显式子命令传 n
    local reuse_default="${1:-n}"
    # 扫描常见位置已有安装，找到就复用
    local ng_bin ng_candidates=(
        "$LOCAL_BIN/ngrok"                          # 标准安装位置
        "$DEFAULT_INSTALL_DIR/ngrok/ngrok"          # 兼容手动放置
        "/usr/bin/ngrok"
    )
    if command -v ngrok &>/dev/null; then
        ng_candidates+=("$(command -v ngrok)")
    fi
    for p in "${ng_candidates[@]}"; do
        if [[ -x "$p" ]]; then ng_bin="$p"; break; fi
    done
    if [[ -n "$ng_bin" ]]; then
        local cur
        cur=$("$ng_bin" version 2>/dev/null | head -1)
        info "ngrok 已存在: ${cur:-unknown} ($ng_bin)"
        if prompt_reuse "$reuse_default" "ngrok ${cur:-}"; then
            if [[ "$ng_bin" != "$LOCAL_BIN/ngrok" ]]; then
                info "  复制到标准位置 $LOCAL_BIN/ngrok"
                mkdir -p "$LOCAL_BIN"
                cp "$ng_bin" "$LOCAL_BIN/ngrok" 2>/dev/null || true
                chmod 755 "$LOCAL_BIN/ngrok" 2>/dev/null || true
                if [[ ! -x "$LOCAL_BIN/ngrok" ]]; then
                    warn "复制到标准位置失败，改为下载最新版本"
                else
                    ok "ngrok 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                    return 0
                fi
            else
                ok "ngrok 已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                return 0
            fi
        fi
        info "  将下载最新版本"
    fi

    local VER V ASSET UV TMP_DIR
    VER=$(get_latest_tag "$NGROK_REPO")
    [[ -z "$VER" ]] && { warn "获取 ngrok 版本失败，跳过"; return 1; }
    V="${VER#v}"

    case "$PLATFORM" in
        linux-amd64)  NGROK_OSA="linux-amd64" ;;
        linux-arm64)  NGROK_OSA="linux-arm64" ;;
        linux-armv7)  NGROK_OSA="linux-arm" ;;
        *)  warn "不支持 ngrok 平台: $PLATFORM，跳过"; return 1 ;;
    esac
    ASSET="ngrok-v3-${V}-${NGROK_OSA}.tar.gz"
    UV="https://github.com/${NGROK_REPO}/releases/download/${VER}/${ASSET}"

    info "ngrok 版本: ${YELLOW}${VER}${NC} ($ASSET)"
    TMP_DIR=$(mktemp -d)
    if ! download_multisource "$UV" "$TMP_DIR/ngrok.tar.gz" "ngrok"; then
        rm -rf "$TMP_DIR"
        warn "ngrok 下载失败，跳过"
        return 1
    fi

    mkdir -p "$TMP_DIR/unpack"
    tar -xzf "$TMP_DIR/ngrok.tar.gz" -C "$TMP_DIR/unpack" 2>/dev/null || { rm -rf "$TMP_DIR"; warn "ngrok 解压失败，跳过"; return 1; }
    local ng_src
    ng_src=$(find "$TMP_DIR/unpack" -type f -name ngrok | head -1)
    if [[ -n "$ng_src" ]]; then
        install -m 755 "$ng_src" "$LOCAL_BIN/ngrok"
        rm -rf "$TMP_DIR"
        ok "ngrok 已安装: $LOCAL_BIN/ngrok (${VER})"
    else
        rm -rf "$TMP_DIR"
        warn "ngrok 解压后未找到二进制，跳过"
    fi
}

# ══════════════════════════════════════════════════
#  组件: browser —— 内置浏览器核心（chromedp 依赖的 headless Chrome）
# ══════════════════════════════════════════════════

install_browser() {
    # $1 = reuse 默认值：all 传 y，显式子命令传 n
    local reuse_default="${1:-n}"
    # 扫描常见位置已有安装，找到就复用
    local browser_bin browser_candidates=(
        "$BROWSER_DIR/chrome-headless-shell"        # 标准安装位置
        "$DEFAULT_INSTALL_DIR/browser/chrome-headless-shell"
    )
    for p in "${browser_candidates[@]}"; do
        if [[ -x "$p" ]]; then browser_bin="$p"; break; fi
    done
    if command -v chrome-headless-shell &>/dev/null; then
        browser_bin="$(command -v chrome-headless-shell)"
    fi
    if [[ -n "$browser_bin" ]]; then
        local cur
        cur=$("$browser_bin" --version 2>/dev/null | head -1)
        info "浏览器核心已存在: ${cur:-unknown} ($browser_bin)"
        if prompt_reuse "$reuse_default" "浏览器核心 ${cur:-}"; then
            # 不在标准位置时复制整个目录
            if [[ "$(dirname "$browser_bin")" != "$BROWSER_DIR" ]]; then
                info "  复制到标准位置 $BROWSER_DIR/"
                mkdir -p "$BROWSER_DIR"
                cp -a "$(dirname "$browser_bin")"/* "$BROWSER_DIR/" 2>/dev/null || true
                chmod -R 755 "$BROWSER_DIR/" 2>/dev/null || true
                if [[ ! -x "$BROWSER_DIR/chrome-headless-shell" ]]; then
                    warn "复制到标准位置失败，改为下载最新版本"
                else
                    ok "浏览器核心已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                    return 0
                fi
            else
                ok "浏览器核心已就绪: ${cur:-unknown}（复用现有安装，跳过下载）"
                return 0
            fi
        fi
        info "  将下载最新版本"
    fi

    case "$PLATFORM" in
        linux-amd64)  CFT_PLAT="linux64" ;;
        linux-arm64)  CFT_PLAT="linux-arm64" ;;
        linux-armv7)  CFT_PLAT="linux-arm" ;;
        *)  warn "不支持 browser 平台: $PLATFORM，跳过"; return 1 ;;
    esac

    info "获取 Chrome for Testing 最新 headless shell..."
    local json url
    json=$(curl -sSL --connect-timeout 10 --max-time 30 \
        "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json" 2>/dev/null) || true
    url=$(echo "$json" | grep -o "https://[^\" ]*chrome-headless-shell-${CFT_PLAT}.zip" | head -1)
    if [[ -z "$url" ]]; then
        warn "无法获取 headless shell 下载地址，跳过（浏览器登录将不可用）"
        return 1
    fi
    local ver fn
    ver=$(echo "$url" | sed -E 's|.*chrome-for-testing-public/([0-9.]+)/.*|\1|' | head -1)
    fn=$(basename "$url")

    local TMP_DIR
    TMP_DIR=$(mktemp -d)
    info "下载浏览器核心: ${YELLOW}${ver:-latest}${NC} ($fn)"
    if ! download_multisource "$url" "$TMP_DIR/$fn" "browser"; then
        rm -rf "$TMP_DIR"
        warn "浏览器核心下载失败，跳过"
        return 1
    fi

    mkdir -p "$BROWSER_DIR"
    if command -v unzip &>/dev/null; then
        unzip -o -q "$TMP_DIR/$fn" -d "$BROWSER_DIR" || true
    elif command -v python3 &>/dev/null; then
        python3 -m zipfile -e "$TMP_DIR/$fn" "$BROWSER_DIR" || true
    else
        rm -rf "$TMP_DIR"
        warn "未找到 unzip/python3 无法解压浏览器核心，跳过"
        return 1
    fi
    rm -rf "$TMP_DIR"

    local SHELL_BIN
    SHELL_BIN=$(find "$BROWSER_DIR" -type f -name chrome-headless-shell -perm -u+x | head -1)
    if [[ -z "$SHELL_BIN" ]]; then
        warn "浏览器核心解压后未找到可执行文件，跳过"
        return 1
    fi
    chmod 755 "$SHELL_BIN"

    # 让 OMP 通过 OMP_CHROME_PATH 找到该核心（systemd drop-in；未用 systemd 时提示 export）
    if command -v systemctl &>/dev/null && [[ -d /etc/systemd/system ]]; then
        mkdir -p /etc/systemd/system/openmodelpool.service.d
        printf '[Service]\nEnvironment=OMP_CHROME_PATH=%s\n' "$SHELL_BIN" > /etc/systemd/system/openmodelpool.service.d/omp-browser.conf
        systemctl daemon-reload 2>/dev/null || true
        ok "已写入 OMP_CHROME_PATH 到 systemd drop-in"
    else
        info "非 systemd 环境：请设置 export OMP_CHROME_PATH=$SHELL_BIN"
    fi
    ok "浏览器核心已安装: ${SHELL_BIN} (${ver:-latest})"
}

# ══════════════════════════════════════════════════
#  状态查看
# ══════════════════════════════════════════════════

ver_of_bin() { "$1" --version 2>/dev/null | head -1; }

cmd_status() {
    echo ""
    echo "OpenModelPool 组件状态 (${PLATFORM})"
    echo "----------------------------------------"

    if [[ -x "$DEFAULT_INSTALL_DIR/$BINARY_NAME" ]]; then
        local hv vv
        hv=$(curl -s --max-time 3 http://localhost:${OMP_PORT}/health 2>/dev/null || true)
        vv=$(echo "$hv" | grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4)
        echo "  core        : 已安装 ${vv:-版本未知（服务未运行?）}"
    else
        echo "  core        : 未安装（${DEFAULT_INSTALL_DIR}/openmodelpool 不存在）"
    fi

    if [[ -x "$XRAY_BIN" ]]; then
        echo "  xray        : 已安装 $(ver_of_bin "$XRAY_BIN" | head -c 40)"
    else
        echo "  xray        : 未安装（vmess/vless 代理将不可用）"
    fi

    if [[ -x "$LOCAL_BIN/cloudflared" ]]; then
        echo "  cloudflared : 已安装 $(ver_of_bin "$LOCAL_BIN/cloudflared" | head -c 60)"
    else
        echo "  cloudflared : 未安装（tunnel 模式将不可用）"
    fi

    for pair in "frps:$LOCAL_BIN/frps" "frpc:$LOCAL_BIN/frpc" "ngrok:$LOCAL_BIN/ngrok"; do
        local name="${pair%%:*}" path="${pair##*:}"
        if [[ -x "$path" ]]; then
            echo "  ${name}       : 已安装 $(ver_of_bin "$path" | head -c 60)"
        else
            echo "  ${name}       : 未安装"
        fi
    done

    local shell_bin
    shell_bin=$(find "$BROWSER_DIR" -type f -name chrome-headless-shell -perm -u+x 2>/dev/null | head -1)
    if [[ -n "$shell_bin" ]]; then
        echo "  browser     : 已安装 ${shell_bin}"
    else
        echo "  browser     : 未安装（浏览器登录将不可用）"
    fi
    echo ""
}

# ══════════════════════════════════════════════════
#  主路由
# ══════════════════════════════════════════════════

COMPONENT="${1:-all}"
VERSION_ARG="${2:-}"

case "$COMPONENT" in
    ""|all)
        # all 模式：检测到已有安装时默认复用（跳过下载），交互可改选升级
        install_core "$VERSION_ARG"
        [[ "${OMP_SKIP_XRAY:-0}" != "1" ]] && install_xray y
        [[ "${OMP_SKIP_CLOUDFLARED:-0}" != "1" ]] && install_cloudflared y
        [[ "${OMP_SKIP_FRP:-0}" != "1" ]] && install_frp y
        [[ "${OMP_SKIP_NGROK:-0}" != "1" ]] && install_ngrok y
        [[ "${OMP_SKIP_BROWSER:-0}" != "1" ]] && install_browser y
        ;;
    core)      install_core "$VERSION_ARG" ;;
    xray)      install_xray n ;;
    cloudflared) install_cloudflared n ;;
    frp)       install_frp n ;;
    ngrok)     install_ngrok n ;;
    browser)   install_browser n ;;
    status)    cmd_status ;;
    help|-h|--help) show_help ;;
    *)         # 向后兼容: 旧用法 install.sh <版本号> = 全部组件 + 核心固定版本
        install_core "$COMPONENT"
        [[ "${OMP_SKIP_XRAY:-0}" != "1" ]] && install_xray y
        [[ "${OMP_SKIP_CLOUDFLARED:-0}" != "1" ]] && install_cloudflared y
        [[ "${OMP_SKIP_FRP:-0}" != "1" ]] && install_frp y
        [[ "${OMP_SKIP_NGROK:-0}" != "1" ]] && install_ngrok y
        [[ "${OMP_SKIP_BROWSER:-0}" != "1" ]] && install_browser y
        ;;
esac

echo ""
echo "== OpenModelPool 安装/升级完成 =="
echo "  管理面板: http://<服务器IP>:8000"
echo "  数据目录: $DATA_DIR"
echo "  组件状态: sudo bash $0 status"
echo "  全局帮助: sudo bash $0 help"