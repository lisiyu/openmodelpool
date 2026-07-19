#!/bin/bash
# build.sh — Codespaces postCreateCommand：
#   0. 立即起诊断日志 HTTP 服务（:8002），暴露 /tmp 下的进度/编译/运行日志（首要可观测性）
#   1. 后台装 chromium + 字体（browser-login 用，best-effort，不阻塞服务启动）
#   2. 前台编译 openmodelpool -> /usr/local/bin/openmodelpool（日志 /tmp/omp-build.log）
#   3. 编译完成后由 devcontainer `command`(run-omp.sh) 以主进程方式拉起服务
set -u

REPO="/workspaces/openmodelpool"
LOG="/tmp/openmodelpool.log"
PROG="/tmp/omp-progress.log"

echo "$(date) [build] === postCreateCommand start ===" | tee -a "$PROG"

# 0) 诊断日志服务（立即起，先于一切，确保外部始终能 curl :8002 看进度）
echo "$(date) [build] installing python3 for diagnostics (best-effort) ..." | tee -a "$PROG"
sudo apt-get update -qq >/dev/null 2>&1 || true
sudo apt-get install -y -qq python3 >/dev/null 2>&1 || true
if command -v python3 >/dev/null 2>&1; then
  setsid bash -c 'cd /tmp && python3 -m http.server 8002 --bind 0.0.0.0 >/tmp/omp-logserver.log 2>&1' </dev/null >/dev/null 2>&1 &
  echo "$(date) [build] diagnostics log server up on :8002 (serves /tmp)" | tee -a "$PROG"
else
  echo "$(date) [build] WARNING python3 unavailable, no diagnostics server" | tee -a "$PROG"
fi

# 1) 浏览器依赖（后台，best-effort，不阻塞服务）
echo "$(date) [build] chromium install scheduled in background ..." | tee -a "$PROG"
( sudo apt-get install -y -qq chromium fonts-liberation fonts-noto-cjk >/dev/null 2>&1 \
    && echo "$(date) [build] chromium install OK" >> "$PROG" \
    || echo "$(date) [build] chromium install FAILED (browser-login may be unavailable)" >> "$PROG" ) &

# 2) 编译
echo "$(date) [build] go build -> /usr/local/bin/openmodelpool (log: /tmp/omp-build.log)" | tee -a "$PROG"
cd "$REPO" || { echo "$(date) [build] cannot cd $REPO" | tee -a "$PROG"; exit 1; }
go build -o /usr/local/bin/openmodelpool . > /tmp/omp-build.log 2>&1
if [ $? -ne 0 ]; then
  echo "$(date) [build] ❌ go build FAILED. See /tmp/omp-build.log via :8002" | tee -a "$PROG"
  tail -n 60 /tmp/omp-build.log >> "$PROG"
  exit 1
fi
echo "$(date) [build] ✅ go build OK -> /usr/local/bin/openmodelpool" | tee -a "$PROG"

# 3) command(run-omp.sh) 将作为主进程拉起服务；此处仅确认二进制存在
if [ -x /usr/local/bin/openmodelpool ]; then
  echo "$(date) [build] binary present; container main process will launch it" | tee -a "$PROG"
else
  echo "$(date) [build] ❌ binary missing after build" | tee -a "$PROG"
  exit 1
fi
echo "$(date) [build] === postCreateCommand done ===" | tee -a "$PROG"
