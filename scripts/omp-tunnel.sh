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
C='\033[0;36m'; Y='\033[1;33m'; G='\033[0;32m'; R='\033[0;31m'; N='\033[0m'

echo ""
echo -e "${C}  ╔══════════════════════════════════════════╗${N}"
echo -e "${C}  ║   OpenModelPool 外网穿透配置向导        ║${N}"
echo -e "${C}  ╚══════════════════════════════════════════╝${N}"
echo ""
echo -e "  请选择穿透方案："
echo -e "    ${G}1${N}) Cloudflare Tunnel  — 完全免费，固定域名+HTTPS，需自有域名"
echo -e "    ${G}2${N}) FRP              — 免费，固定IP+端口，需公网服务器"
echo -e "    ${G}3${N}) 跳过"
echo ""
read -p "  请输入选项 [1/2/3]: " choice

# ============================================================
# Cloudflare Tunnel
# ============================================================
setup_cloudflare() {
    echo ""
    echo -e "${Y}[Cloudflare Tunnel]${N}"
    echo -e "  需要准备："
    echo -e "    - 一个托管在 Cloudflare 的域名"
    echo -e "    - Cloudflare 账号（免费注册）"
    echo ""

    # Install cloudflared
    if ! command -v cloudflared &>/dev/null; then
        echo -e "${Y}[1/5] 安装 cloudflared...${N}"
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  CFARCH="amd64" ;;
            aarch64|arm64) CFARCH="arm64" ;;
            armv7l)        CFARCH="arm" ;;
            *) echo -e "${R}不支持的架构: $ARCH${N}"; exit 1 ;;
        esac
        
        # Try package manager first, fallback to binary
        if command -v apt-get &>/dev/null; then
            curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null 2>&1
            echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | tee /etc/apt/sources.list.d/cloudflared.list >/dev/null 2>&1
            apt-get update -qq && apt-get install -y cloudflared 2>/dev/null || {
                # Fallback to binary
                curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CFARCH}" -o /usr/local/bin/cloudflared
                chmod +x /usr/local/bin/cloudflared
            }
        else
            curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CFARCH}" -o /usr/local/bin/cloudflared
            chmod +x /usr/local/bin/cloudflared
        fi
        echo -e "  ${G}✓ cloudflared 安装完成${N}"
    else
        echo -e "${Y}[1/5] cloudflared 已安装${N}"
    fi

    # Login
    echo ""
    echo -e "${Y}[2/5] 登录 Cloudflare...${N}"
    echo -e "  即将打开浏览器授权，请在浏览器中选择你的域名并授权"
    echo -e "  如果没有浏览器环境，请手动在另一台电脑上执行 cloudflared login"
    cloudflared tunnel login || {
        echo -e "${R}  登录失败，请稍后手动执行: cloudflared tunnel login${N}"
        exit 1
    }

    # Create tunnel
    echo ""
    echo -e "${Y}[3/5] 创建隧道...${N}"
    TUNNEL_NAME="openmodelpool"
    TUNNEL_ID=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1 | grep -oP '[a-f0-9-]{36}' | head -1)
    if [ -z "$TUNNEL_ID" ]; then
        echo -e "${R}  隧道创建失败${N}"
        exit 1
    fi
    echo -e "  ${G}✓ 隧道已创建: $TUNNEL_ID${N}"

    # Get domain
    echo ""
    echo -e "${Y}[4/5] 绑定域名...${N}"
    CERT_FILE=$(find /root/.cloudflared/ -name "cert.pem" 2>/dev/null | head -1)
    if [ -z "$CERT_FILE" ]; then
        CERT_FILE=$(find ~/.cloudflared/ -name "cert.pem" 2>/dev/null | head -1)
    fi
    
    # Extract available domain from cert
    AVAILABLE_DOMAIN=$(cloudflared tunnel route dns "$TUNNEL_NAME" 2>&1 | grep -oP '[\w.-]+\.\w+' | head -1)
    
    echo -e "  请输入要绑定的子域名（例如: omp.yourdomain.com）:"
    read -p "  > " SUBDOMAIN
    
    cloudflared tunnel route dns "$TUNNEL_NAME" "$SUBDOMAIN" 2>/dev/null || true
    echo -e "  ${G}✓ 域名已绑定: $SUBDOMAIN${N}"

    # Create config
    echo ""
    echo -e "${Y}[5/5] 配置并启动服务...${N}"
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

    # Install as service
    cloudflared service install 2>/dev/null || {
        echo -e "  ${Y}systemd 服务安装失败，使用后台进程启动${N}"
        nohup cloudflared tunnel run "$TUNNEL_NAME" >> "$INSTALL_DIR/data/cloudflared.log" 2>&1 &
    }

    echo ""
    echo -e "  ${G}╔══════════════════════════════════════════╗${N}"
    echo -e "  ${G}║  Cloudflare Tunnel 配置完成！            ║${N}"
    echo -e "  ${G}╠══════════════════════════════════════════╣${N}"
    echo -e "  ${G}║  外网地址: https://$SUBDOMAIN${N}"
    echo -e "  ${G}║  管理面板: https://$SUBDOMAIN/admin${N}"
    echo -e "  ${G}║  已设置开机自启                          ║${N}"
    echo -e "  ${G}╚══════════════════════════════════════════╝${N}"
}

# ============================================================
# FRP
# ============================================================
setup_frp() {
    echo ""
    echo -e "${Y}[FRP 内网穿透]${N}"
    echo ""

    # Ask for FRP server
    echo -e "  请输入 FRP 服务器地址（直接回车使用默认公网服务器）:"
    read -p "  FRP Server [YOUR_FRP_SERVER_IP]: " FRP_SERVER
    FRP_SERVER="${FRP_SERVER:-YOUR_FRP_SERVER_IP}"

    echo -e "  请输入 FRP 认证 Token（直接回车使用默认）:"
    read -p "  Token [使用默认]: " FRP_TOKEN
    FRP_TOKEN="${FRP_TOKEN:-YOUR_FRP_TOKEN}"

    echo -e "  请输入远程映射端口（本节点在公网服务器上占用的端口）:"
    read -p "  Remote Port [8001]: " REMOTE_PORT
    REMOTE_PORT="${REMOTE_PORT:-8001}"

    # Install frpc
    if ! command -v frpc &>/dev/null; then
        echo ""
        echo -e "${Y}[1/4] 安装 frpc...${N}"
        ARCH=$(uname -m)
        case "$ARCH" in
            x86_64|amd64)  FRPARCH="amd64" ;;
            aarch64|arm64) FRPARCH="arm64" ;;
            armv7l)        FRPARCH="armv7" ;;
            *) echo -e "${R}不支持的架构: $ARCH${N}"; exit 1 ;;
        esac
        
        FRP_VER="0.61.1"
        TMP=$(mktemp -d)
        curl -fsSL "https://github.com/fatedier/frp/releases/download/v${FRP_VER}/frp_${FRP_VER}_linux_${FRPARCH}.tar.gz" -o "$TMP/frp.tar.gz"
        tar xzf "$TMP/frp.tar.gz" -C "$TMP"
        cp "$TMP/frp_${FRP_VER}_linux_${FRPARCH}/frpc" /usr/local/bin/frpc
        chmod +x /usr/local/bin/frpc
        rm -rf "$TMP"
        echo -e "  ${G}✓ frpc 安装完成${N}"
    else
        echo -e "${Y}[1/4] frpc 已安装${N}"
    fi

    # Config
    echo ""
    echo -e "${Y}[2/4] 创建配置...${N}"
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
    echo -e "  ${G}✓ 配置已写入 /etc/frp/frpc.toml${N}"

    # Test connection
    echo ""
    echo -e "${Y}[3/4] 测试连接...${N}"
    timeout 5 /usr/local/bin/frpc -c /etc/frp/frpc.toml 2>&1 | head -5 || true
    echo -e "  ${G}✓ 配置完成${N}"

    # Install as service
    echo ""
    echo -e "${Y}[4/4] 设置开机自启...${N}"
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
        echo -e "  ${G}✓ 已设置 systemd 服务并启动${N}"
    else
        # 群晖或其他
        nohup /usr/local/bin/frpc -c /etc/frp/frpc.toml >> "$INSTALL_DIR/data/frpc.log" 2>&1 &
        echo -e "  ${Y}⚠ 非 systemd 系统，已后台启动（未设置开机自启）${N}"
        echo -e "  群晖用户请参考 rc.d 脚本设置自启"
    fi

    echo ""
    echo -e "  ${G}╔══════════════════════════════════════════╗${N}"
    echo -e "  ${G}║  FRP 穿透配置完成！                     ║${N}"
    echo -e "  ${G}╠══════════════════════════════════════════╣${N}"
    echo -e "  ${G}║  外网地址: http://$FRP_SERVER:$REMOTE_PORT${N}"
    echo -e "  ${G}║  管理面板: http://$FRP_SERVER:$REMOTE_PORT/admin${N}"
    echo -e "  ${G}║  已设置开机自启                          ║${N}"
    echo -e "  ${G}╚══════════════════════════════════════════╝${N}"
}

# ============================================================
# Main
# ============================================================
case "$choice" in
    1) setup_cloudflare ;;
    2) setup_frp ;;
    3) echo -e "  ${Y}跳过外网穿透配置。后续可随时运行此脚本配置。${N}" ;;
    *) echo -e "${R}无效选项${N}"; exit 1 ;;
esac
