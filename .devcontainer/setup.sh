#!/bin/bash
# OpenModelPool Codespaces 初始化：装浏览器依赖、构建并后台启动服务
set -e

echo "[devcontainer] installing chromium + fonts ..."
sudo apt-get update -qq 2>/dev/null
sudo apt-get install -y -qq chromium fonts-liberation fonts-noto-cjk 2>/dev/null || true

echo "[devcontainer] building openmodelpool ..."
go build -o openmodelpool .

echo "[devcontainer] starting openmodelpool (background) ..."
nohup ./openmodelpool > /tmp/openmodelpool.log 2>&1 < /dev/null &

# 给服务一点启动时间
sleep 4

if curl -s -o /dev/null -w "%{http_code}" http://localhost:8000/health | grep -q "200"; then
  echo "[devcontainer] ✅ openmodelpool is up. First run: open /setup to configure the admin account."
else
  echo "[devcontainer] ⚠️ openmodelpool did not respond on /health. Check /tmp/openmodelpool.log"
fi
