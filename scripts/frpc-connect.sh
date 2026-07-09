#!/bin/bash
# ============================================
# OpenModelPool frp 客户端一键连接脚本
# 用法: sudo bash frpc-connect.sh <服务器IP>
# ============================================

set -e

SERVER_IP="${1:?请提供服务器IP，用法: sudo bash frpc-connect.sh <服务器IP>}"
FRP_VERSION="0.61.1"
FRP_TOKEN="${FRP_TOKEN:-YOUR_FRP_TOKEN}"
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l) ARCH="arm" ;;
esac

echo "╔══════════════════════════════════════════╗"
echo "║   OpenModelPool frp 客户端一键连接      ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "  服务器: $SERVER_IP:7000"
echo ""

# 检查 openmodelpool 服务是否运行
if ! curl -s http://localhost:8000/ >/dev/null 2>&1; then
    echo "❌ OpenModelPool 服务未运行 (localhost:8000 无响应)"
    echo "   请先启动 OpenModelPool 服务"
    exit 1
fi
echo "✅ OpenModelPool 服务已运行"

# 下载 frpc
cd /tmp
if [ ! -f /usr/local/bin/frpc ]; then
    echo "📥 下载 frpc v${FRP_VERSION}..."
    wget -q --show-progress "https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_linux_${ARCH}.tar.gz" -O frpc.tar.gz
    tar -xzf frpc.tar.gz
    cp "frp_${FRP_VERSION}_linux_${ARCH}/frpc" /usr/local/bin/
    chmod +x /usr/local/bin/frpc
    rm -rf "frp_${FRP_VERSION}_linux_${ARCH}" frpc.tar.gz
    echo "✅ frpc 已安装"
else
    echo "✅ frpc 已存在"
fi

# 创建配置
mkdir -p /etc/frp
cat > /etc/frp/frpc.toml << EOF
serverAddr = "${SERVER_IP}"
serverPort = 7000
auth.token = "${FRP_TOKEN}"

[[proxies]]
name = "openmodelpool-web"
type = "tcp"
localIP = "127.0.0.1"
localPort = 8000
remotePort = 8000
EOF

# systemd 服务
cat > /etc/systemd/system/frpc.service << 'EOF'
[Unit]
Description=frp client (frpc) for OpenModelPool
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/frpc -c /etc/frp/frpc.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# 启动
systemctl daemon-reload
systemctl enable frpc
systemctl restart frpc

sleep 2
if systemctl is-active --quiet frpc; then
    echo ""
    echo "╔══════════════════════════════════════════╗"
    echo "║           ✅ 连接成功！                  ║"
    echo "╠══════════════════════════════════════════╣"
    echo "║                                          ║"
    echo "║  🌐 公网访问地址:                        ║"
    echo "║  http://${SERVER_IP}:8000                ║"
    echo "║                                          ║"
    echo "║  📊 管理面板:                            ║"
    echo "║  http://${SERVER_IP}:8000/admin          ║"
    echo "║                                          ║"
    echo "╚══════════════════════════════════════════╝"
else
    echo "❌ frpc 启动失败，检查日志:"
    journalctl -u frpc --no-pager -n 10
fi
