#!/bin/bash
# ============================================================
#  OpenModelPool 全功能管理脚本 (Linux / 群晖 NAS)
#  集成：安装 / 升级 / 卸载 / 穿透配置(CF/FRP) / 修改端口 / 查看状态 / 重启
#
#  用法:
#    curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh | sudo bash
#    或:
#    sudo bash omp-manager.sh [安装目录] [端口]
# ============================================================

GITHUB_REPO="lisiyu/openmodelpool"
XRAY_VERSION="v26.3.27"
INSTALL_DIR="${1:-/opt/openmodelpool}"
PORT="${2:-8000}"

# 群晖默认路径
if [ -d /volume1 ]; then
  INSTALL_DIR="${1:-/volume1/@appstore/openmodelpool}"
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ============================================================
# 工具函数
# ============================================================
write_title() {
    echo ""
    echo -e "${CYAN}  ╔══════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}  ║  $1${NC}"
    echo -e "${CYAN}  ╚══════════════════════════════════════════╝${NC}"
}

write_step() {
    echo -e "  ${YELLOW}[$1/$2]${NC} $3"
}

write_ok() {
    echo -e "  ${GREEN}✓ $1${NC}"
}

write_err() {
    echo -e "  ${RED}✗ $1${NC}"
}

write_info() {
    echo -e "  ${CYAN}$1${NC}"
}

# 获取最新 Release tag
get_release_tag() {
    RELEASE_TAG="${OMP_RELEASE_TAG:-}"
    if [ -z "$RELEASE_TAG" ]; then
        RELEASE_TAG=$(curl -s "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" 2>/dev/null | \
            python3 -c 'import sys,json;print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null)
    fi
    if [ -z "$RELEASE_TAG" ]; then
        write_err "无法获取最新 Release tag"
        return 1
    fi
    echo "$RELEASE_TAG"
}

# 检测架构
detect_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64)  PKG="openmodelpool-linux-amd64";  XRAY_PKG="Xray-linux-64.zip" ;;
        aarch64|arm64) PKG="openmodelpool-linux-arm64";  XRAY_PKG="Xray-linux-arm64-v8a.zip" ;;
        armv7l|arm)    PKG="openmodelpool-linux-armv7";  XRAY_PKG="Xray-linux-arm32-v7a.zip" ;;
        *)
            write_err "不支持的架构: $ARCH"
            return 1
            ;;
    esac
}

# 检测系统类型
detect_system() {
    IS_SYNOLOGY=false
    if [ -f /etc.defaults/VERSION ] || [ -f /etc/synoinfo.conf ]; then
        IS_SYNOLOGY=true
    fi
}

# 停止 OMP 服务
stop_omp() {
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet openmodelpool 2>/dev/null; then
        systemctl stop openmodelpool 2>/dev/null || true
    elif [ -f /usr/local/etc/rc.d/openmodelpool.sh ]; then
        /usr/local/etc/rc.d/openmodelpool.sh stop 2>/dev/null || true
    else
        pkill -f "$INSTALL_DIR/openmodelpool" 2>/dev/null || true
    fi
}

# 启动 OMP 服务
start_omp() {
    if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/openmodelpool.service ]; then
        systemctl start openmodelpool
    elif [ -f /usr/local/etc/rc.d/openmodelpool.sh ]; then
        /usr/local/etc/rc.d/openmodelpool.sh start
    else
        cd "$INSTALL_DIR" && nohup ./openmodelpool >> data/app.log 2>&1 &
    fi
}

# 停止所有隧道
stop_all_tunnels() {
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop cloudflared 2>/dev/null || true
        systemctl stop frpc 2>/dev/null || true
    fi
    pkill -f "cloudflared tunnel" 2>/dev/null || true
    pkill -f "frpc " 2>/dev/null || true
}

# ============================================================
# 1. 安装
# ============================================================
install_omp() {
    write_title "OpenModelPool 全新安装"

    if [ -f "$INSTALL_DIR/openmodelpool" ]; then
        write_info "检测到已有安装: $INSTALL_DIR"
        read -p "  是否覆盖安装？[y/N] " confirm
        if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
            write_info "已取消"
            return
        fi
    fi

    detect_arch || return 1
    detect_system

    RELEASE_TAG=$(get_release_tag) || return 1
    write_info "目标版本: $RELEASE_TAG"
    write_info "架构: $ARCH"

    write_step 0 3 "清理旧版本..."
    stop_omp
    stop_all_tunnels
    write_ok "清理完成"

    # 下载
    write_step 1 3 "下载 $PKG ..."
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}/${PKG}"
    CHECK_URL="${DOWNLOAD_URL}.sha256"
    TMP_DIR=$(mktemp -d)

    curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$PKG" || {
        write_err "下载失败"
        rm -rf "$TMP_DIR"
        return 1
    }
    curl -fsSL "$CHECK_URL" -o "$TMP_DIR/$PKG.sha256" 2>/dev/null || true

    if [ -s "$TMP_DIR/$PKG.sha256" ] && command -v sha256sum >/dev/null 2>&1; then
        write_step 2 3 "校验 SHA256..."
        ( cd "$TMP_DIR" && sha256sum -c "$PKG.sha256" ) || {
            write_err "SHA256 校验失败"
            rm -rf "$TMP_DIR"
            return 1
        }
        write_ok "校验通过"
    else
        write_step 2 3 "跳过 SHA256 校验（无校验文件或 sha256sum 不可用）"
    fi

    # 安装
    write_step 3 3 "安装到 $INSTALL_DIR ..."
    mkdir -p "$INSTALL_DIR/data"
    cp "$TMP_DIR/$PKG" "$INSTALL_DIR/openmodelpool"
    chmod +x "$INSTALL_DIR/openmodelpool"
    write_ok "安装完成"

    # 安装 Xray
    echo ""
    write_info "安装 Xray (VMess 代理支持)..."
    XRAY_DIR="$INSTALL_DIR/xray"
    mkdir -p "$XRAY_DIR"
    XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_PKG}"
    if curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip" 2>/dev/null; then
        if unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray" 2>/dev/null || \
           python3 -c "import zipfile; zipfile.ZipFile('$TMP_DIR/xray.zip').extractall('$TMP_DIR/xray')" 2>/dev/null; then
            cp "$TMP_DIR/xray/xray" "$XRAY_DIR/xray" 2>/dev/null && chmod +x "$XRAY_DIR/xray"
            cp "$TMP_DIR/xray/geoip.dat" "$XRAY_DIR/" 2>/dev/null
            cp "$TMP_DIR/xray/geosite.dat" "$XRAY_DIR/" 2>/dev/null
            write_ok "Xray 安装完成"
        else
            echo -e "  ${YELLOW}⚠️ Xray 解压失败，VMess 代理不可用（不影响其他功能）${NC}"
        fi
    else
        echo -e "  ${YELLOW}⚠️ Xray 下载失败，VMess 代理不可用（不影响其他功能）${NC}"
    fi

    # 检查内置浏览器依赖
    echo ""
    write_info "检查内置浏览器依赖 (Chrome + Xvfb)..."
    check_browser_deps

    # 配置服务
    echo ""
    write_step 4 4 "配置服务 (端口 ${PORT})..."
    setup_service

    # 启动
    write_step 5 5 "启动服务..."
    stop_omp
    sleep 1
    start_omp
    sleep 3

    if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null 2>&1; then
        NAS_IP=$(ip addr show | grep -oP 'inet \K[0-9.]+' | grep -v '127.0.0.1' | head -1)
        echo ""
        echo -e "${GREEN}  ╔══════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}  ║            ✅ 安装成功！                  ║${NC}"
        echo -e "${GREEN}  ╚══════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "  管理面板:  ${CYAN}http://${NAS_IP}:${PORT}/admin${NC}"
        echo -e "  安装目录:  $INSTALL_DIR"
        echo -e "  日志文件:  $INSTALL_DIR/data/app.log"
        echo ""
    else
        write_err "服务启动失败"
        echo "  查看日志: tail -f $INSTALL_DIR/data/app.log"
    fi

    rm -rf "$TMP_DIR"

    # 询问穿透
    echo ""
    write_info "是否配置外网穿透？"
    setup_tunnel_menu
}

# 检查并安装浏览器依赖
check_browser_deps() {
    NEED_CHROME=false
    NEED_XVFB=false

    if command -v google-chrome >/dev/null 2>&1; then
        write_ok "Chrome 已安装"
    elif command -v chromium >/dev/null 2>&1; then
        write_ok "Chromium 已安装"
    else
        echo -e "  ${YELLOW}Chrome 未安装${NC}"
        NEED_CHROME=true
    fi

    if command -v Xvfb >/dev/null 2>&1; then
        write_ok "Xvfb 已安装"
    else
        echo -e "  ${YELLOW}Xvfb 未安装${NC}"
        NEED_XVFB=true
    fi

    if [ "$NEED_CHROME" = true ] || [ "$NEED_XVFB" = true ]; then
        if command -v apt-get >/dev/null 2>&1; then
            write_info "使用 apt-get 安装依赖..."
            apt-get update -qq 2>/dev/null
            [ "$NEED_XVFB" = true ] && apt-get install -y -qq xvfb 2>/dev/null
            if [ "$NEED_CHROME" = true ]; then
                wget -q -O /tmp/chrome-signing-key.pub https://dl.google.com/linux/linux_signing_key.pub 2>/dev/null
                apt-key add /tmp/chrome-signing-key.pub 2>/dev/null || true
                echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list 2>/dev/null
                apt-get update -qq 2>/dev/null
                apt-get install -y -qq google-chrome-stable 2>/dev/null || \
                apt-get install -y -qq chromium-browser 2>/dev/null || \
                apt-get install -y -qq chromium 2>/dev/null || true
                apt-get install -y -qq fonts-liberation fonts-noto-cjk 2>/dev/null || true
            fi
            command -v google-chrome >/dev/null 2>&1 || command -v chromium >/dev/null 2>&1 && write_ok "Chrome/Chromium 安装完成" || \
                echo -e "  ${YELLOW}⚠️ Chrome 安装失败，手动: apt-get install google-chrome-stable xvfb${NC}"
        elif command -v yum >/dev/null 2>&1; then
            write_info "使用 yum 安装依赖..."
            [ "$NEED_XVFB" = true ] && yum install -y -q xorg-x11-server-Xvfb 2>/dev/null
            if [ "$NEED_CHROME" = true ]; then
                cat > /etc/yum.repos.d/google-chrome.repo << 'EOF'
[google-chrome]
name=google-chrome
baseurl=https://dl.google.com/linux/chrome/rpm/stable/x86_64
enabled=1
gpgcheck=1
gpgkey=https://dl.google.com/linux/linux_signing_key.pub
EOF
                yum install -y -q google-chrome-stable 2>/dev/null || true
            fi
        elif command -v apk >/dev/null 2>&1; then
            write_info "使用 apk 安装依赖..."
            [ "$NEED_CHROME" = true ] && apk add --no-cache chromium nss freetype harfbuzz ttf-freefont 2>/dev/null
            [ "$NEED_XVFB" = true ] && apk add --no-cache xvfb-run 2>/dev/null || true
        else
            echo -e "  ${YELLOW}⚠️ 无法识别的包管理器，请手动安装 Chrome 和 Xvfb${NC}"
        fi
    else
        write_ok "内置浏览器依赖已就绪"
    fi
}

# 配置 systemd / rc.d 服务
setup_service() {
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

    if [ "$IS_SYNOLOGY" = true ]; then
        RC_SCRIPT="/usr/local/etc/rc.d/openmodelpool.sh"
        mkdir -p /usr/local/etc/rc.d
        cat > "$RC_SCRIPT" << EOF
#!/bin/bash
case "\$1" in
  start)  su root -c "$INSTALL_DIR/start.sh &" ;;
  stop)   $INSTALL_DIR/stop.sh ;;
  restart) \$0 stop; sleep 2; \$0 start ;;
  status) pgrep -f "$INSTALL_DIR/openmodelpool" && echo "运行中" || echo "未运行" ;;
  *) echo "Usage: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
exit 0
EOF
        chmod +x "$RC_SCRIPT"
        write_ok "开机自启: $RC_SCRIPT"
    elif command -v systemctl >/dev/null 2>&1; then
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
        write_ok "开机自启: systemd"
    else
        echo "su root -c '$INSTALL_DIR/start.sh &'" >> /etc/rc.local 2>/dev/null
        write_ok "开机自启: /etc/rc.local"
    fi
}

# ============================================================
# 2. 升级
# ============================================================
upgrade_omp() {
    write_title "OpenModelPool 增量升级"

    if [ ! -f "$INSTALL_DIR/openmodelpool" ]; then
        write_err "未检测到安装: $INSTALL_DIR/openmodelpool"
        return
    fi

    detect_arch || return 1
    RELEASE_TAG=$(get_release_tag) || return 1
    write_info "目标版本: $RELEASE_TAG"

    write_step 1 5 "停止服务..."
    stop_omp
    sleep 2
    write_ok "已停止"

    write_step 2 5 "下载新版本: $PKG ..."
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}/${PKG}"
    CHECK_URL="${DOWNLOAD_URL}.sha256"
    TMP_DIR=$(mktemp -d)

    curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$PKG" || {
        write_err "下载失败"
        rm -rf "$TMP_DIR"
        return 1
    }
    curl -fsSL "$CHECK_URL" -o "$TMP_DIR/$PKG.sha256" 2>/dev/null || true
    write_ok "下载完成"

    write_step 3 5 "校验 SHA256..."
    if [ -s "$TMP_DIR/$PKG.sha256" ] && command -v sha256sum >/dev/null 2>&1; then
        ( cd "$TMP_DIR" && sha256sum -c "$PKG.sha256" ) || {
            write_err "SHA256 校验失败，终止更新"
            rm -rf "$TMP_DIR"
            return 1
        }
        write_ok "校验通过"
    else
        echo -e "  ${YELLOW}⚠️ 跳过校验${NC}"
    fi

    write_step 4 5 "替换二进制..."
    cp "$TMP_DIR/$PKG" "$INSTALL_DIR/openmodelpool"
    chmod +x "$INSTALL_DIR/openmodelpool"
    write_ok "替换完成"

    # 检查 Xray
    XRAY_DIR="$INSTALL_DIR/xray"
    if [ ! -f "$XRAY_DIR/xray" ]; then
        write_info "安装 Xray (VMess 代理支持)..."
        mkdir -p "$XRAY_DIR"
        XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_PKG}"
        if curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip" 2>/dev/null; then
            unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray" 2>/dev/null || \
                python3 -c "import zipfile; zipfile.ZipFile('$TMP_DIR/xray.zip').extractall('$TMP_DIR/xray')" 2>/dev/null
            cp "$TMP_DIR/xray/xray" "$XRAY_DIR/xray" 2>/dev/null && chmod +x "$XRAY_DIR/xray"
            cp "$TMP_DIR/xray/geoip.dat" "$XRAY_DIR/" 2>/dev/null
            cp "$TMP_DIR/xray/geosite.dat" "$XRAY_DIR/" 2>/dev/null
            write_ok "Xray 安装完成"
        else
            echo -e "  ${YELLOW}⚠️ Xray 下载失败（不影响其他功能）${NC}"
        fi
    else
        write_ok "Xray 已存在，跳过"
    fi

    rm -rf "$TMP_DIR"

    write_step 5 5 "启动服务..."
    start_omp
    sleep 3

    if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null 2>&1; then
        write_ok "升级成功！数据已保留。"
    else
        write_err "启动失败，请检查日志: $INSTALL_DIR/data/app.log"
    fi
}

# ============================================================
# 3. 卸载
# ============================================================
uninstall_omp() {
    write_title "OpenModelPool 卸载"

    write_info "将删除以下内容："
    echo -e "    - 二进制: $INSTALL_DIR/openmodelpool"
    echo -e "    - Xray:   $INSTALL_DIR/xray/"
    echo -e "    - 脚本:   $INSTALL_DIR/*.sh"
    echo -e "    - 服务:   systemd / rc.d"
    echo -e "    - 隧道:   cloudflared / frpc"
    echo ""
    write_info "${RED}数据目录 $INSTALL_DIR/data/ 默认保留${NC}（可手动删除）"
    echo ""
    read -p "  确认卸载？输入 yes 继续: " confirm
    if [ "$confirm" != "yes" ]; then
        write_info "已取消"
        return
    fi

    write_step 1 4 "停止所有服务..."
    stop_omp
    stop_all_tunnels
    sleep 2
    write_ok "已停止"

    write_step 2 4 "移除系统服务..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl disable openmodelpool 2>/dev/null || true
        rm -f /etc/systemd/system/openmodelpool.service
        systemctl daemon-reload 2>/dev/null || true
        systemctl disable cloudflared 2>/dev/null || true
        rm -f /etc/systemd/system/cloudflared.service 2>/dev/null || true
        systemctl disable frpc 2>/dev/null || true
        rm -f /etc/systemd/system/frpc.service 2>/dev/null || true
    fi
    rm -f /usr/local/etc/rc.d/openmodelpool.sh 2>/dev/null || true
    write_ok "已移除"

    write_step 3 4 "删除文件..."
    rm -f "$INSTALL_DIR/openmodelpool"
    rm -f "$INSTALL_DIR/start.sh" "$INSTALL_DIR/stop.sh" "$INSTALL_DIR/status.sh"
    rm -rf "$INSTALL_DIR/xray"
    write_ok "已删除"

    write_step 4 4 "清理隧道配置..."
    rm -rf /root/.cloudflared 2>/dev/null || true
    rm -rf "$HOME/.cloudflared" 2>/dev/null || true
    rm -rf /etc/frp 2>/dev/null || true
    write_ok "已清理"

    echo ""
    write_ok "卸载完成"
    echo -e "  数据目录保留: $INSTALL_DIR/data/"
    echo -e "  如需彻底删除: rm -rf $INSTALL_DIR"
}

# ============================================================
# 4. 配置穿透 (子菜单)
# ============================================================
setup_tunnel_menu() {
    echo ""
    echo -e "  请选择穿透方案："
    echo -e "    ${GREEN}1${NC}) Cloudflare Tunnel — 免费，固定域名+HTTPS"
    echo -e "    ${GREEN}2${NC}) FRP              — 免费，固定IP+端口"
    echo -e "    ${GREEN}3${NC}) 跳过"
    read -p "  请选择 [1/2/3]: " tunnel_choice

    case "$tunnel_choice" in
        1) setup_cloudflare ;;
        2) setup_frp ;;
        3) write_info "跳过" ;;
        *) write_err "无效选项" ;;
    esac
}

setup_cloudflare() {
    echo ""
    write_info "[Cloudflare Tunnel]"
    echo -e "  需要："
    echo -e "    - 一个托管在 Cloudflare 的域名"
    echo -e "    - Cloudflare 账号（免费注册）"
    echo ""

    # Install cloudflared
    if ! command -v cloudflared >/dev/null 2>&1; then
        write_step 1 5 "安装 cloudflared..."
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  CFARCH="amd64" ;;
            aarch64|arm64) CFARCH="arm64" ;;
            armv7l)        CFARCH="arm" ;;
            *) write_err "不支持的架构: $ARCH"; return 1 ;;
        esac
        if command -v apt-get >/dev/null 2>&1; then
            curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null 2>&1
            echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | tee /etc/apt/sources.list.d/cloudflared.list >/dev/null 2>&1
            apt-get update -qq && apt-get install -y cloudflared 2>/dev/null || {
                curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CFARCH}" -o /usr/local/bin/cloudflared
                chmod +x /usr/local/bin/cloudflared
            }
        else
            curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CFARCH}" -o /usr/local/bin/cloudflared
            chmod +x /usr/local/bin/cloudflared
        fi
        write_ok "cloudflared 安装完成"
    else
        write_step 1 5 "cloudflared 已安装"
    fi

    # Login
    echo ""
    write_step 2 5 "登录 Cloudflare..."
    echo -e "  即将打开浏览器授权，请在浏览器中选择你的域名并授权"
    cloudflared tunnel login || {
        write_err "登录失败，请稍后手动执行: cloudflared tunnel login"
        return 1
    }

    # Create tunnel
    echo ""
    write_step 3 5 "创建隧道..."
    TUNNEL_NAME="openmodelpool"
    TUNNEL_ID=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1 | grep -oP '[a-f0-9-]{36}' | head -1)
    if [ -z "$TUNNEL_ID" ]; then
        # 可能已存在，尝试获取
        TUNNEL_ID=$(cloudflared tunnel list 2>/dev/null | grep "$TUNNEL_NAME" | grep -oP '[a-f0-9-]{36}' | head -1)
        if [ -n "$TUNNEL_ID" ]; then
            write_ok "隧道已存在: $TUNNEL_ID"
        else
            write_err "隧道创建失败"
            return 1
        fi
    else
        write_ok "隧道已创建: $TUNNEL_ID"
    fi

    # Get domain
    echo ""
    write_step 4 5 "绑定域名..."
    CONFIG_DIR="/root/.cloudflared"
    [ ! -d "$CONFIG_DIR" ] && CONFIG_DIR="$HOME/.cloudflared"
    EXISTING_HOST=""
    if [ -f "$CONFIG_DIR/config.yml" ]; then
        EXISTING_HOST=$(grep -m1 "hostname:" "$CONFIG_DIR/config.yml" | sed 's/hostname:[[:space:]]*//' | tr -d '[:space:]')
    fi

    SKIP_DNS=0
    if [ -n "$EXISTING_HOST" ]; then
        write_info "检测到已绑定的域名: $EXISTING_HOST"
        read -p "  是否复用此域名？[Y/n] " REUSE
        if [ "$REUSE" != "n" ] && [ "$REUSE" != "N" ]; then
            SUBDOMAIN="$EXISTING_HOST"
            SKIP_DNS=1
            write_ok "复用域名: $SUBDOMAIN（跳过DNS绑定）"
        else
            echo -e "  请输入要绑定的子域名（例如: omp.yourdomain.com）:"
            read -p "  > " SUBDOMAIN
        fi
    else
        echo -e "  请输入要绑定的子域名（例如: omp.yourdomain.com）:"
        read -p "  > " SUBDOMAIN
    fi

    if [ "$SKIP_DNS" -eq 0 ]; then
        cloudflared tunnel route dns "$TUNNEL_NAME" "$SUBDOMAIN" 2>/dev/null || true
        write_ok "域名已绑定: $SUBDOMAIN"
    fi

    # Create config
    echo ""
    write_step 5 5 "配置并启动服务..."
    mkdir -p "$CONFIG_DIR"
    cat > "$CONFIG_DIR/config.yml" << EOF
tunnel: $TUNNEL_ID
credentials-file: $CONFIG_DIR/$TUNNEL_ID.json

ingress:
  - hostname: $SUBDOMAIN
    service: http://localhost:$PORT
  - service: http_status:404
EOF

    if systemctl is-active --quiet cloudflared 2>/dev/null; then
        write_ok "cloudflared 服务已运行，重启中..."
        systemctl restart cloudflared
    elif systemctl list-unit-files 2>/dev/null | grep -q cloudflared; then
        write_ok "cloudflared 服务已存在，重启中..."
        systemctl restart cloudflared
    else
        cloudflared service install 2>/dev/null || {
            echo -e "  ${YELLOW}systemd 安装失败，使用后台进程${NC}"
            nohup cloudflared tunnel run "$TUNNEL_NAME" >> "$INSTALL_DIR/data/cloudflared.log" 2>&1 &
        }
    fi

    echo ""
    write_ok "Cloudflare Tunnel 配置完成！"
    echo -e "  外网地址: ${CYAN}https://$SUBDOMAIN${NC}"
    echo -e "  管理面板: ${CYAN}https://$SUBDOMAIN/admin${NC}"
    echo -e "  已设置开机自启"
}

setup_frp() {
    echo ""
    write_info "[FRP 内网穿透]"
    echo ""
    echo -e "  FRP 需要一台有公网 IP 的服务器作为中转。"
    echo ""

    read -p "  FRP 服务器公网 IP: " FRP_SERVER
    [ -z "$FRP_SERVER" ] && { write_err "服务器地址不能为空"; return 1; }

    read -p "  FRP 认证 Token: " FRP_TOKEN
    [ -z "$FRP_TOKEN" ] && { write_err "Token 不能为空"; return 1; }

    read -p "  远程映射端口（默认 8001）: " REMOTE_PORT
    REMOTE_PORT="${REMOTE_PORT:-8001}"

    # 检测已有配置，复用
    if [ -f /etc/frp/frpc.toml ]; then
        write_info "检测到已有 FRP 配置: /etc/frp/frpc.toml"
        read -p "  是否复用已有配置？[Y/n] " REUSE
        if [ "$REUSE" != "n" ] && [ "$REUSE" != "N" ]; then
            write_ok "复用已有配置，跳过配置创建"
            if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/frpc.service ]; then
                systemctl restart frpc
                write_ok "frpc 服务已重启"
            fi
            return
        fi
    fi

    # Install frpc
    if ! command -v frpc >/dev/null 2>&1; then
        write_step 1 4 "安装 frpc..."
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  FRPARCH="amd64" ;;
            aarch64|arm64) FRPARCH="arm64" ;;
            armv7l)        FRPARCH="armv7" ;;
            *) write_err "不支持的架构: $ARCH"; return 1 ;;
        esac
        FRP_VER="0.61.1"
        TMP=$(mktemp -d)
        curl -fsSL "https://github.com/fatedier/frp/releases/download/v${FRP_VER}/frp_${FRP_VER}_linux_${FRPARCH}.tar.gz" -o "$TMP/frp.tar.gz"
        tar xzf "$TMP/frp.tar.gz" -C "$TMP"
        cp "$TMP/frp_${FRP_VER}_linux_${FRPARCH}/frpc" /usr/local/bin/frpc
        chmod +x /usr/local/bin/frpc
        rm -rf "$TMP"
        write_ok "frpc 安装完成"
    else
        write_step 1 4 "frpc 已安装"
    fi

    write_step 2 4 "创建配置..."
    mkdir -p /etc/frp
    NODE_NAME=$(hostname | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-')
    cat > /etc/frp/frpc.toml << EOF
serverAddr = "$FRP_SERVER"
serverPort = 7000
auth.token = "$FRP_TOKEN"

[[proxies]]
name = "omp-$NODE_NAME"
type = "tcp"
localIP = "127.0.0.1"
localPort = $PORT
remotePort = $REMOTE_PORT
EOF
    write_ok "配置已写入 /etc/frp/frpc.toml"

    write_step 3 4 "测试连接..."
    timeout 5 /usr/local/bin/frpc -c /etc/frp/frpc.toml 2>&1 | head -5 || true
    write_ok "配置完成"

    write_step 4 4 "设置开机自启..."
    if command -v systemctl >/dev/null 2>&1; then
        cat > /etc/systemd/system/frpc.service << EOF
[Unit]
Description=frpc client
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/frpc -c /etc/frp/frpc.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable frpc
        systemctl start frpc
        write_ok "已设置 systemd 服务并启动"
    else
        nohup /usr/local/bin/frpc -c /etc/frp/frpc.toml >> "$INSTALL_DIR/data/frpc.log" 2>&1 &
        echo -e "  ${YELLOW}⚠ 非 systemd 系统，已后台启动${NC}"
    fi

    echo ""
    write_ok "FRP 穿透配置完成！"
    echo -e "  外网地址: ${CYAN}http://$FRP_SERVER:$REMOTE_PORT${NC}"
    echo -e "  管理面板: ${CYAN}http://$FRP_SERVER:$REMOTE_PORT/admin${NC}"
}

# ============================================================
# 5. 重置穿透
# ============================================================
reset_tunnel_menu() {
    echo ""
    echo -e "  请选择要重置的方案："
    echo -e "    ${GREEN}1${NC}) 重置 Cloudflare Tunnel"
    echo -e "    ${GREEN}2${NC}) 重置 FRP"
    echo -e "    ${GREEN}3${NC}) 重置全部"
    echo -e "    ${GREEN}4${NC}) 返回"
    read -p "  请选择 [1/2/3/4]: " choice

    case "$choice" in
        1) reset_cloudflare ;;
        2) reset_frp ;;
        3) reset_cloudflare; reset_frp ;;
        4) return ;;
        *) write_err "无效选项" ;;
    esac
}

reset_cloudflare() {
    write_title "重置 Cloudflare Tunnel"
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop cloudflared 2>/dev/null || true
        systemctl disable cloudflared 2>/dev/null || true
        rm -f /etc/systemd/system/cloudflared.service 2>/dev/null || true
        systemctl daemon-reload 2>/dev/null || true
    fi
    pkill -f "cloudflared tunnel" 2>/dev/null || true
    rm -rf /root/.cloudflared 2>/dev/null || true
    rm -rf "$HOME/.cloudflared" 2>/dev/null || true
    rm -f /etc/apt/sources.list.d/cloudflared.list 2>/dev/null || true
    write_ok "Cloudflare Tunnel 已重置"
}

reset_frp() {
    write_title "重置 FRP"
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop frpc 2>/dev/null || true
        systemctl disable frpc 2>/dev/null || true
        rm -f /etc/systemd/system/frpc.service 2>/dev/null || true
        systemctl daemon-reload 2>/dev/null || true
    fi
    pkill -f "frpc " 2>/dev/null || true
    rm -rf /etc/frp 2>/dev/null || true
    write_ok "FRP 已重置"
}

# ============================================================
# 6. 修改端口
# ============================================================
change_port() {
    write_title "修改端口"
    write_info "当前端口: $PORT"
    read -p "  请输入新端口: " NEW_PORT
    if [ -z "$NEW_PORT" ]; then
        write_err "端口不能为空"
        return
    fi

    # 更新 start.sh
    cat > "$INSTALL_DIR/start.sh" << EOF
#!/bin/bash
cd "$INSTALL_DIR"
export PORT="$NEW_PORT"
exec ./openmodelpool >> "$INSTALL_DIR/data/app.log" 2>&1
EOF
    chmod +x "$INSTALL_DIR/start.sh"

    # 更新 cloudflared config
    CONFIG_DIR="/root/.cloudflared"
    [ ! -d "$CONFIG_DIR" ] && CONFIG_DIR="$HOME/.cloudflared"
    if [ -f "$CONFIG_DIR/config.yml" ]; then
        sed -i "s/http:\/\/localhost:[0-9]*/http:\/\/localhost:$NEW_PORT/g" "$CONFIG_DIR/config.yml"
        if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet cloudflared 2>/dev/null; then
            systemctl restart cloudflared
        fi
        write_ok "Cloudflare 配置已更新"
    fi

    # 更新 frpc config
    if [ -f /etc/frp/frpc.toml ]; then
        sed -i "s/localPort = [0-9]*/localPort = $NEW_PORT/g" /etc/frp/frpc.toml
        if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet frpc 2>/dev/null; then
            systemctl restart frpc
        fi
        write_ok "FRP 配置已更新"
    fi

    # 更新 systemd
    if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/openmodelpool.service ]; then
        systemctl daemon-reload
    fi

    # 重启 OMP
    stop_omp
    sleep 2
    PORT="$NEW_PORT"
    start_omp
    sleep 3

    if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null 2>&1; then
        write_ok "端口已修改为 $NEW_PORT，服务已重启"
        NAS_IP=$(ip addr show | grep -oP 'inet \K[0-9.]+' | grep -v '127.0.0.1' | head -1)
        echo -e "  管理面板: ${CYAN}http://${NAS_IP}:${NEW_PORT}/admin${NC}"
    else
        write_err "重启失败，请检查日志"
    fi
}

# ============================================================
# 7. 查看状态
# ============================================================
show_status() {
    write_title "OpenModelPool 状态"

    echo ""
    echo -e "  ${CYAN}── OMP 服务 ──${NC}"
    if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null 2>&1; then
        PID=$(pgrep -f "$INSTALL_DIR/openmodelpool" | head -1)
        write_ok "OMP 运行中 (PID: $PID)"
    else
        write_err "OMP 未运行"
    fi
    echo -e "  安装目录: $INSTALL_DIR"
    echo -e "  端口: $PORT"

    # 版本
    if [ -f "$INSTALL_DIR/openmodelpool" ]; then
        echo -e "  二进制: $(ls -lh $INSTALL_DIR/openmodelpool | awk '{print $5, $6, $7, $8}')"
    fi

    # Xray
    echo ""
    echo -e "  ${CYAN}── Xray (VMess 代理) ──${NC}"
    if [ -f "$INSTALL_DIR/xray/xray" ]; then
        write_ok "Xray 已安装"
    else
        echo -e "  ${YELLOW}⚠️ Xray 未安装${NC}"
    fi

    # Cloudflare
    echo ""
    echo -e "  ${CYAN}── Cloudflare Tunnel ──${NC}"
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet cloudflared 2>/dev/null; then
        write_ok "cloudflared 运行中"
    elif pgrep -f "cloudflared tunnel" >/dev/null 2>&1; then
        write_ok "cloudflared 运行中 (后台进程)"
    else
        echo -e "  ${YELLOW}○ cloudflared 未运行${NC}"
    fi
    CONFIG_DIR="/root/.cloudflared"
    [ ! -d "$CONFIG_DIR" ] && CONFIG_DIR="$HOME/.cloudflared"
    if [ -f "$CONFIG_DIR/config.yml" ]; then
        HOST=$(grep -m1 "hostname:" "$CONFIG_DIR/config.yml" | sed 's/hostname:[[:space:]]*//' | tr -d '[:space:]')
        echo -e "  域名: ${CYAN}$HOST${NC}"
    fi

    # FRP
    echo ""
    echo -e "  ${CYAN}── FRP ──${NC}"
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet frpc 2>/dev/null; then
        write_ok "frpc 运行中"
    elif pgrep -f "frpc " >/dev/null 2>&1; then
        write_ok "frpc 运行中 (后台进程)"
    else
        echo -e "  ${YELLOW}○ frpc 未运行${NC}"
    fi
    if [ -f /etc/frp/frpc.toml ]; then
        FRP_SERVER=$(grep "serverAddr" /etc/frp/frpc.toml | sed 's/serverAddr = "//' | sed 's/"//')
        REMOTE=$(grep "remotePort" /etc/frp/frpc.toml | sed 's/remotePort = //')
        echo -e "  服务器: ${CYAN}$FRP_SERVER:$REMOTE${NC}"
    fi

    # 日志
    echo ""
    echo -e "  ${CYAN}── 日志 ──${NC}"
    echo -e "  OMP:   $INSTALL_DIR/data/app.log"
    if [ -f "$INSTALL_DIR/data/cloudflared.log" ]; then
        echo -e "  CF:    $INSTALL_DIR/data/cloudflared.log"
    fi
    if [ -f "$INSTALL_DIR/data/frpc.log" ]; then
        echo -e "  FRP:   $INSTALL_DIR/data/frpc.log"
    fi
}

# ============================================================
# 8. 重启服务
# ============================================================
restart_all() {
    write_title "重启所有服务"

    write_step 1 4 "重启 OMP..."
    stop_omp
    sleep 2
    start_omp
    sleep 3
    if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null 2>&1; then
        write_ok "OMP 已启动"
    else
        write_err "OMP 启动失败"
    fi

    write_step 2 4 "重启 Cloudflare..."
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet cloudflared 2>/dev/null; then
        systemctl restart cloudflared
        sleep 2
        write_ok "cloudflared 已重启"
    elif pgrep -f "cloudflared tunnel" >/dev/null 2>&1; then
        pkill -f "cloudflared tunnel"
        sleep 1
        TUNNEL_NAME="openmodelpool"
        nohup cloudflared tunnel run "$TUNNEL_NAME" >> "$INSTALL_DIR/data/cloudflared.log" 2>&1 &
        write_ok "cloudflared 已重启 (后台进程)"
    else
        echo -e "  ${YELLOW}○ Cloudflare 未配置，跳过${NC}"
    fi

    write_step 3 4 "重启 FRP..."
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet frpc 2>/dev/null; then
        systemctl restart frpc
        sleep 2
        write_ok "frpc 已重启"
    elif pgrep -f "frpc " >/dev/null 2>&1; then
        pkill -f "frpc "
        sleep 1
        nohup /usr/local/bin/frpc -c /etc/frp/frpc.toml >> "$INSTALL_DIR/data/frpc.log" 2>&1 &
        write_ok "frpc 已重启 (后台进程)"
    else
        echo -e "  ${YELLOW}○ FRP 未配置，跳过${NC}"
    fi

    write_step 4 4 "完成"
    echo ""
    write_ok "重启完成"
}

# ============================================================
# 主菜单
# ============================================================
if [ "$(id -u)" -ne 0 ]; then
    write_err "请使用 root 权限运行"
    exit 1
fi

detect_system

while true; do
    echo ""
    echo -e "${CYAN}  ╔══════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}  ║       OpenModelPool 全功能管理工具        ║${NC}"
    echo -e "${CYAN}  ╚══════════════════════════════════════════╝${NC}"
    echo -e "    1. 安装          全新安装 OMP"
    echo -e "    2. 升级          增量更新 (保留配置)"
    echo -e "    3. 卸载          彻底删除所有组件"
    echo -e "    4. 配置穿透      Cloudflare / FRP"
    echo -e "    5. 重置穿透      选择重置任一/全部隧道"
    echo -e "    6. 修改端口      更换 OMP 服务端口"
    echo -e "    7. 查看状态      检查所有组件运行情况"
    echo -e "    8. 重启服务      重启 OMP + 所有隧道"
    echo -e "    0. 退出"
    echo -e "${CYAN}  ══════════════════════════════════════════${NC}"
    echo -e "  安装目录: $INSTALL_DIR  端口: $PORT"
    read -p "  请选择 [0-8]: " choice

    case "$choice" in
        1) install_omp ;;
        2) upgrade_omp ;;
        3) uninstall_omp ;;
        4) setup_tunnel_menu ;;
        5) reset_tunnel_menu ;;
        6) change_port ;;
        7) show_status ;;
        8) restart_all ;;
        0) echo "  Bye!"; exit 0 ;;
        *) write_err "无效选项" ;;
    esac
done
