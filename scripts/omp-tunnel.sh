#!/bin/bash
# ============================================================
#  OpenModelPool 外网穿透配置 (Linux / 群晖)
#  支持 Cloudflare Tunnel 和 FRP 两种方案
#
#  用法:
#    curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.sh | sudo bash
# ============================================================
set -e

INSTALL_DIR="${1:-/opt/openmodelpool}"
LOCAL_PORT="${2:-8000}"

# Colors
CYAN='\033[0;36m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'; RED='\033[0;31m'; NC='\033[0m'

echo ""
echo -e "${CYAN}  ╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}  ║   OpenModelPool 外网穿透配置向导        ║${NC}"
echo -e "${CYAN}  ╚══════════════════════════════════════════╝${NC}"
echo ""
echo -e "  请选择穿透方案："
echo -e "    ${GREEN}1${NC}) Cloudflare Tunnel  — 完全免费，固定域名+HTTPS，需自有域名"
echo -e "    ${GREEN}2${NC}) FRP              — 免费，固定IP+端口，需公网服务器"
echo -e "    ${GREEN}3${NC}) 跳过"
echo ""
read -p "  请输入选项 [1/2/3]: " choice

# ============================================================
# Cloudflare Tunnel
# ============================================================
setup_cloudflare() {
    echo ""
    echo -e "${YELLOW}[Cloudflare Tunnel]${NC}"
    echo -e "  需要准备："
    echo -e "    - 一个托管在 Cloudflare 的域名"
    echo -e "    - Cloudflare 账号（免费注册）"
    echo ""

    # Install cloudflared
    if ! command -v cloudflared &>/dev/null; then
        echo -e "${YELLOW}[1/5] 安装 cloudflared...${NC}"
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  CFARCH="amd64" ;;
            aarch64|arm64) CFARCH="arm64" ;;
            armv7l)        CFARCH="arm" ;;
            *) echo -e "${RED}不支持的架构: $ARCH${NC}"; exit 1 ;;
        esac
        
        # Try package manager first, fallback to binary
        if command -v apt-get &>/dev/null; then
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
        echo -e "  ${GREEN}✓ cloudflared 安装完成${NC}"
    else
        echo -e "${YELLOW}[1/5] cloudflared 已安装${NC}"
    fi

    # Login
    echo ""
    echo -e "${YELLOW}[2/5] 登录 Cloudflare...${NC}"
    echo -e "  即将打开浏览器授权，请在浏览器中选择你的域名并授权"
    echo -e "  如果没有浏览器环境，请手动在另一台电脑上执行 cloudflared login"
    cloudflared tunnel login || {
        echo -e "${RED}  登录失败，请稍后手动执行: cloudflared tunnel login${NC}"
        exit 1
    }

    # Create tunnel
    echo ""
    echo -e "${YELLOW}[3/5] 创建隧道...${NC}"
    TUNNEL_NAME="openmodelpool"
    TUNNEL_ID=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1 | grep -oP '[a-f0-9-]{36}' | head -1)
    if [ -z "$TUNNEL_ID" ]; then
        echo -e "${RED}  隧道创建失败${NC}"
        exit 1
    fi
    echo -e "  ${GREEN}✓ 隧道已创建: $TUNNEL_ID${NC}"

    # Get domain
    echo ""
    echo -e "${YELLOW}[4/5] 绑定域名...${NC}"
    
    # Check existing config for hostname
    CONFIG_DIR="/root/.cloudflared"
    [ ! -d "$CONFIG_DIR" ] && CONFIG_DIR="$HOME/.cloudflared"
    EXISTING_HOST=""
    if [ -f "$CONFIG_DIR/config.yml" ]; then
        EXISTING_HOST=$(grep -m1 "hostname:" "$CONFIG_DIR/config.yml" | sed 's/hostname:[[:space:]]*//' | tr -d '[:space:]')
    fi
    
    SKIP_DNS=0
    if [ -n "$EXISTING_HOST" ]; then
        echo -e "  ${GREEN}检测到已绑定的域名: $EXISTING_HOST${NC}"
        read -p "  是否复用此域名？[Y/n] " REUSE
        if [ "$REUSE" != "n" ] && [ "$REUSE" != "N" ]; then
            SUBDOMAIN="$EXISTING_HOST"
            SKIP_DNS=1
            echo -e "  ${GREEN}✓ 复用域名: $SUBDOMAIN（跳过DNS绑定）${NC}"
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
        echo -e "  ${GREEN}✓ 域名已绑定: $SUBDOMAIN${NC}"
    fi

    # Create config
    echo ""
    echo -e "${YELLOW}[5/5] 配置并启动服务...${NC}"
    CONFIG_DIR="/root/.cloudflared"
    [ ! -d "$CONFIG_DIR" ] && CONFIG_DIR="$HOME/.cloudflared"
    
    cat > "$CONFIG_DIR/config.yml" << EOF
tunnel: $TUNNEL_ID
credentials-file: $CONFIG_DIR/$TUNNEL_ID.json

ingress:
  - hostname: $SUBDOMAIN
    service: http://localhost:$LOCAL_PORT
  - service: http_status:404
EOF

    # Install as service (or restart if already exists)
    if systemctl is-active --quiet cloudflared 2>/dev/null; then
        echo -e "  ${GREEN}✓ cloudflared 服务已运行，重启中...${NC}"
        systemctl restart cloudflared
    elif systemctl list-unit-files 2>/dev/null | grep -q cloudflared; then
        echo -e "  ${GREEN}✓ cloudflared 服务已存在，重启中...${NC}"
        systemctl restart cloudflared
    else
        cloudflared service install 2>/dev/null || {
            echo -e "  ${YELLOW}systemd 服务安装失败，使用后台进程启动${NC}"
            nohup cloudflared tunnel run "$TUNNEL_NAME" >> "$INSTALL_DIR/data/cloudflared.log" 2>&1 &
        }
    fi

    echo ""
    echo -e "  ${GREEN}╔══════════════════════════════════════════╗${NC}"
    echo -e "  ${GREEN}║  Cloudflare Tunnel 配置完成！            ║${NC}"
    echo -e "  ${GREEN}╠══════════════════════════════════════════╣${NC}"
    echo -e "  ${GREEN}║  外网地址: https://$SUBDOMAIN${NC}"
    echo -e "  ${GREEN}║  管理面板: https://$SUBDOMAIN/admin${NC}"
    echo -e "  ${GREEN}║  已设置开机自启                          ║${NC}"
    echo -e "  ${GREEN}╚══════════════════════════════════════════╝${NC}"
}

# ============================================================
# FRP
# ============================================================
setup_frp() {
    echo ""
    echo -e "${YELLOW}[FRP 内网穿透]${NC}"
    echo ""
    echo -e "  FRP 需要一台有公网 IP 的服务器作为中转。"
    echo -e "  如果还没有，请参考下方说明搭建。"
    echo ""
    echo -e "  ${CYAN}──────────────────────────────────────────${NC}"
    echo -e "  ${CYAN} 如何搭建 FRP 服务器（在公网服务器上执行）${NC}"
    echo -e "  ${CYAN}──────────────────────────────────────────${NC}"
    echo -e "  1. 购买一台云服务器（腾讯云/阿里云轻量级即可，约 ¥30-50/月）"
    echo -e "  2. 在公网服务器上执行以下命令："
    echo -e ""
    echo -e "  ${GREEN}# 下载 FRP${NC}"
    echo -e "  wget https://github.com/fatedier/frp/releases/download/v0.61.1/frp_0.61.1_linux_amd64.tar.gz"
    echo -e "  tar xzf frp_0.61.1_linux_amd64.tar.gz && cd frp_0.61.1_linux_amd64"
    echo -e ""
    echo -e "  ${GREEN}# 创建配置${NC}"
    echo -e '  cat > frps.toml << EOF'
    echo -e '  bindPort = 7000'
    echo -e '  auth.token = "your-secret-token-here"'
    echo -e '  EOF'
    echo -e ""
    echo -e "  ${GREEN}# 启动并设为开机自启${NC}"
    echo -e "  ./frps -c frps.toml"
    echo -e ""
    echo -e "  ${GREEN}# 开机自启 (systemd)${NC}"
    echo -e '  sudo tee /etc/systemd/system/frps.service << EOF'
    echo -e '  [Unit]'
    echo -e '  Description=frps server'
    echo -e '  After=network.target'
    echo -e '  [Service]'
    echo -e '  Type=simple'
    echo -e '  ExecStart=/root/frp_0.61.1_linux_amd64/frps -c /root/frp_0.61.1_linux_amd64/frps.toml'
    echo -e '  Restart=always'
    echo -e '  RestartSec=5'
    echo -e '  [Install]'
    echo -e '  WantedBy=multi-user.target'
    echo -e '  EOF'
    echo -e '  sudo systemctl enable frps && sudo systemctl start frps'
    echo -e ""
    echo -e "  ${GREEN}# 安全组放行端口${NC}"
    echo -e "  在云服务器控制台安全组中放行: TCP 7000 + 你要映射的端口(如 8001-8010)"
    echo -e ""
    echo -e "  ${CYAN}──────────────────────────────────────────${NC}"
    echo ""
    echo -e "  搭建完成后，请在下方填写你的 FRP 服务器信息："
    echo ""
    
    # Ask for FRP server
    read -p "  FRP 服务器公网 IP: " FRP_SERVER
    if [ -z "$FRP_SERVER" ]; then
        echo -e "${RED}  服务器地址不能为空${NC}"
        exit 1
    fi

    read -p "  FRP 认证 Token: " FRP_TOKEN
    if [ -z "$FRP_TOKEN" ]; then
        echo -e "${RED}  Token 不能为空${NC}"
        exit 1
    fi

    read -p "  远程映射端口（此节点在公网服务器上占用的端口，如 8001）: " REMOTE_PORT
    REMOTE_PORT="${REMOTE_PORT:-8001}"

    # Install frpc
    if ! command -v frpc &>/dev/null; then
        echo ""
        echo -e "${YELLOW}[1/4] 安装 frpc...${NC}"
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  FRPARCH="amd64" ;;
            aarch64|arm64) FRPARCH="arm64" ;;
            armv7l)        FRPARCH="armv7" ;;
            *) echo -e "${RED}不支持的架构: $ARCH${NC}"; exit 1 ;;
        esac
        
        FRP_VER="0.61.1"
        TMP=$(mktemp -d)
        curl -fsSL "https://github.com/fatedier/frp/releases/download/v${FRP_VER}/frp_${FRP_VER}_linux_${FRPARCH}.tar.gz" -o "$TMP/frp.tar.gz"
        tar xzf "$TMP/frp.tar.gz" -C "$TMP"
        cp "$TMP/frp_${FRP_VER}_linux_${FRPARCH}/frpc" /usr/local/bin/frpc
        chmod +x /usr/local/bin/frpc
        rm -rf "$TMP"
        echo -e "  ${GREEN}✓ frpc 安装完成${NC}"
    else
        echo -e "${YELLOW}[1/4] frpc 已安装${NC}"
    fi

    # Config
    echo ""
    echo -e "${YELLOW}[2/4] 创建配置...${NC}"
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
localPort = $LOCAL_PORT
remotePort = $REMOTE_PORT
EOF
    echo -e "  ${GREEN}✓ 配置已写入 /etc/frp/frpc.toml${NC}"

    # Test connection
    echo ""
    echo -e "${YELLOW}[3/4] 测试连接...${NC}"
    timeout 5 /usr/local/bin/frpc -c /etc/frp/frpc.toml 2>&1 | head -5 || true
    echo -e "  ${GREEN}✓ 配置完成${NC}"

    # Install as service
    echo ""
    echo -e "${YELLOW}[4/4] 设置开机自启...${NC}"
    if command -v systemctl &>/dev/null; then
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
        echo -e "  ${GREEN}✓ 已设置 systemd 服务并启动${NC}"
    else
        # 群晖或其他
        nohup /usr/local/bin/frpc -c /etc/frp/frpc.toml >> "$INSTALL_DIR/data/frpc.log" 2>&1 &
        echo -e "  ${YELLOW}⚠ 非 systemd 系统，已后台启动（未设置开机自启）${NC}"
        echo -e "  群晖用户请参考 rc.d 脚本设置自启"
    fi

    echo ""
    echo -e "  ${GREEN}╔══════════════════════════════════════════╗${NC}"
    echo -e "  ${GREEN}║  FRP 穿透配置完成！                     ║${NC}"
    echo -e "  ${GREEN}╠══════════════════════════════════════════╣${NC}"
    echo -e "  ${GREEN}║  外网地址: http://$FRP_SERVER:$REMOTE_PORT${NC}"
    echo -e "  ${GREEN}║  管理面板: http://$FRP_SERVER:$REMOTE_PORT/admin${NC}"
    echo -e "  ${GREEN}║  已设置开机自启                          ║${NC}"
    echo -e "  ${GREEN}╚══════════════════════════════════════════╝${NC}"
}

# ============================================================
# Main
# ============================================================
case "$choice" in
    1) setup_cloudflare ;;
    2) setup_frp ;;
    3) echo -e "  ${YELLOW}跳过外网穿透配置。后续可随时运行此脚本配置。${NC}" ;;
    *) echo -e "${RED}无效选项${NC}"; exit 1 ;;
esac
