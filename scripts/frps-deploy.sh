#!/bin/bash
# ============================================
# OpenModelPool frp 服务端一键部署脚本
# 用法: curl -sL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/frps-deploy.sh | sudo bash
# 或:   sudo bash frps-deploy.sh [服务器公网IP]
# ============================================

set -e

FRP_VERSION="0.61.1"
FRP_TOKEN="${FRP_TOKEN:-YOUR_FRP_TOKEN}"
ARCH=$(dpkg --print-architecture 2>/dev/null || echo "amd64")
SERVER_IP="${1:-$(curl -s ifconfig.me 2>/dev/null || echo 'YOUR_SERVER_IP')}"

echo "╔══════════════════════════════════════════╗"
echo "║   OpenModelPool frp 服务端一键部署      ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "  版本: $FRP_VERSION | 架构: $ARCH"
echo "  公网IP: $SERVER_IP"
echo ""

# 1. 下载 frp
cd /tmp
echo "📥 下载 frp v${FRP_VERSION}..."
wget -q --show-progress "https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_linux_${ARCH}.tar.gz" -O frp.tar.gz

# 2. 解压安装
echo "📦 安装 frps..."
tar -xzf frp.tar.gz
cp "frp_${FRP_VERSION}_linux_${ARCH}/frps" /usr/local/bin/
chmod +x /usr/local/bin/frps
rm -rf "frp_${FRP_VERSION}_linux_${ARCH}" frp.tar.gz

# 3. 创建配置
mkdir -p /etc/frp
cat > /etc/frp/frps.toml << EOF
bindPort = 7000
auth.token = "${FRP_TOKEN}"

webServer.addr = "0.0.0.0"
webServer.port = 7500
webServer.user = "admin"
webServer.password = "omp2026admin"

allowPorts = [
  { start = 8000, end = 9000 },
]

log.to = "/var/log/frps.log"
log.level = "info"
log.maxDays = 7
EOF

# 4. systemd 服务
cat > /etc/systemd/system/frps.service << 'EOF'
[Unit]
Description=frp server (frps) for OpenModelPool
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/frps -c /etc/frp/frps.toml
Restart=always
RestartSec=5
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

# 5. 启动
systemctl daemon-reload
systemctl enable frps
systemctl restart frps

# 6. 防火墙
if command -v ufw &>/dev/null; then
    ufw allow 7000/tcp >/dev/null 2>&1 || true
    ufw allow 8000/tcp >/dev/null 2>&1 || true
    ufw allow 7500/tcp >/dev/null 2>&1 || true
    echo "✅ ufw 规则已添加"
fi

if command -v firewall-cmd &>/dev/null; then
    firewall-cmd --permanent --add-port=7000/tcp >/dev/null 2>&1 || true
    firewall-cmd --permanent --add-port=8000/tcp >/dev/null 2>&1 || true
    firewall-cmd --permanent --add-port=7500/tcp >/dev/null 2>&1 || true
    firewall-cmd --reload >/dev/null 2>&1 || true
    echo "✅ firewalld 规则已添加"
fi

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║           ✅ 部署完成！                  ║"
echo "╠══════════════════════════════════════════╣"
echo "║                                          ║"
echo "║  状态: $(systemctl is-active frps)                       ║"
echo "║                                          ║"
echo "║  📊 管理面板:                            ║"
echo "║  http://${SERVER_IP}:7500                ║"
echo "║  用户名: admin                           ║"
echo "║  密码:   omp2026admin                    ║"
echo "║                                          ║"
echo "║  🔌 转发端口: 8000                       ║"
echo "║                                          ║"
echo "╠══════════════════════════════════════════╣"
echo "║  ⚠️  请确保云服务商安全组放行:           ║"
echo "║     - 7000/tcp (frp 通信)                ║"
echo "║     - 8000/tcp (Web 服务)                ║"
echo "║     - 7500/tcp (管理面板,可选)           ║"
echo "╚══════════════════════════════════════════╝"
