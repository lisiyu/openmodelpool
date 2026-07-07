#!/bin/bash
# ModelMux Cloudflare 命名隧道配置脚本
# 用途：配置 zuiniu.com 作为固定公网域名

set -e

echo "=========================================="
echo "ModelMux Cloudflare 命名隧道配置"
echo "=========================================="
echo ""

# 检查 cloudflared 是否安装
if ! command -v cloudflared &> /dev/null; then
    echo "❌ cloudflared 未安装"
    echo "请先安装: https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/"
    exit 1
fi

echo "✅ cloudflared 已安装: $(cloudflared --version)"
echo ""

# 步骤 1: 登录 Cloudflare
echo "📝 步骤 1/4: 登录 Cloudflare"
echo "   即将打开浏览器，请登录并授权..."
cloudflared login
echo "✅ 登录成功"
echo ""

# 步骤 2: 创建隧道
TUNNEL_NAME="modelmux"
echo "📝 步骤 2/4: 创建隧道 (名称: $TUNNEL_NAME)"
if cloudflared tunnel list 2>/dev/null | grep -q "$TUNNEL_NAME"; then
    echo "⚠️  隧道 '$TUNNEL_NAME' 已存在，跳过创建"
else
    cloudflared tunnel create $TUNNEL_NAME
    echo "✅ 隧道创建成功"
fi
echo ""

# 步骤 3: 配置 DNS 路由
echo "📝 步骤 3/4: 配置 DNS 路由 (zuiniu.com -> $TUNNEL_NAME)"
read -p "   确认将 zuiniu.com 指向此隧道？(y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    cloudflared tunnel route dns $TUNNEL_NAME zuiniu.com
    echo "✅ DNS 路由配置成功"
else
    echo "⚠️  跳过 DNS 配置"
fi
echo ""

# 步骤 4: 生成配置文件
echo "📝 步骤 4/4: 生成隧道配置文件"
TUNNEL_ID=$(cloudflared tunnel list 2>/dev/null | grep "$TUNNEL_NAME" | awk '{print $1}')
if [ -z "$TUNNEL_ID" ]; then
    echo "❌ 无法获取隧道 ID"
    exit 1
fi

cat > config.yml << EOF
# ModelMux Cloudflare Tunnel 配置
# 隧道名称: $TUNNEL_NAME
# 隧道 ID: $TUNNEL_ID
# 域名: zuiniu.com

tunnel: $TUNNEL_ID
credentials-file: $HOME/.cloudflared/$TUNNEL_ID.json

ingress:
  - hostname: zuiniu.com
    service: http://localhost:8000
  - service: http_status:404
EOF

echo "✅ 配置文件已生成: config.yml"
echo ""
echo "=========================================="
echo "配置完成！"
echo "=========================================="
echo ""
echo "📋 后续步骤："
echo ""
echo "1. 将 config.yml 上传到服务器的 ~/.cloudflared/ 目录："
echo "   scp config.yml root@YOUR_SERVER:~/.cloudflared/config.yml"
echo ""
echo "2. 在服务器上启动隧道："
echo "   cloudflared tunnel run $TUNNEL_NAME"
echo ""
echo "3. 或者使用 systemd 服务（推荐）："
echo "   sudo cloudflared service install"
echo "   sudo systemctl start cloudflared"
echo "   sudo systemctl enable cloudflared"
echo ""
echo "4. 验证："
echo "   curl https://zuiniu.com/health"
echo ""
echo "=========================================="
