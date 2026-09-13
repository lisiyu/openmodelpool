#!/bin/bash
# OpenModelPool 一键部署脚本 - Linux (amd64)
# 使用方法: chmod +x deploy-linux.sh && ./deploy-linux.sh [安装目录] [端口]
set -e

INSTALL_DIR="${1:-/opt/openmodelpool}"
PORT="${2:-8000}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=============================="
echo " OpenModelPool 一键部署"
echo " 平台: Linux x86_64"
echo " 安装目录: $INSTALL_DIR"
echo " 端口: $PORT"
echo "=============================="

if [ "$EUID" -ne 0 ]; then
  echo "[错误] 请使用 root 权限运行: sudo ./deploy-linux.sh"
  exit 1
fi

mkdir -p "$INSTALL_DIR/data"
cd "$INSTALL_DIR"

echo "[1/4] 复制程序文件..."
if [ -f "$SCRIPT_DIR/openmodelpool-linux-amd64" ]; then
  cp "$SCRIPT_DIR/openmodelpool-linux-amd64" "$INSTALL_DIR/openmodelpool"
elif [ -f "$SCRIPT_DIR/openmodelpool" ]; then
  cp "$SCRIPT_DIR/openmodelpool" "$INSTALL_DIR/openmodelpool"
else
  echo "[错误] 找不到可执行文件"
  exit 1
fi
chmod +x "$INSTALL_DIR/openmodelpool"

if [ -f "$SCRIPT_DIR/admin.html" ]; then
  cp "$SCRIPT_DIR/admin.html" "$INSTALL_DIR/admin.html"
fi
if [ -d "$SCRIPT_DIR/docs" ]; then
  cp -r "$SCRIPT_DIR/docs" "$INSTALL_DIR/docs"
fi

echo "[2/4] 配置端口 ($PORT)..."
cat > "$INSTALL_DIR/start.sh" << EOF
#!/bin/bash
cd "$INSTALL_DIR"
export PORT="$PORT"
exec ./openmodelpool >> "$INSTALL_DIR/data/app.log" 2>&1
EOF
chmod +x "$INSTALL_DIR/start.sh"

echo "[3/4] 配置 systemd 服务..."
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

echo "[4/4] 启动服务..."
systemctl restart openmodelpool
sleep 2

if systemctl is-active --quiet openmodelpool; then
  echo ""
  echo "✅ 部署成功！"
  echo "=============================="
  echo " 管理面板: http://$(hostname -I | awk '{print $1}'):$PORT/admin"
  echo " 服务状态: systemctl status openmodelpool"
  echo " 日志文件: $INSTALL_DIR/data/app.log"
  echo " 停止服务: systemctl stop openmodelpool"
  echo " 重启服务: systemctl restart openmodelpool"
  echo "=============================="
  echo ""
  echo "⚠️  首次使用请访问管理面板设置管理员账号"
else
  echo "[错误] 服务启动失败，请检查日志: $INSTALL_DIR/data/app.log"
  systemctl status openmodelpool
  exit 1
fi
