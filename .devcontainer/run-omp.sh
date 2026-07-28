#!/bin/bash
# run-omp.sh — Codespaces 容器主进程（devcontainer `command`）。
# 设计目标：无论编译/运行是否出问题，都尽量让 :8000 处于监听，便于外部探活与排错。
set -u

REPO="/workspaces/openmodelpool"
BIN="$REPO/openmodelpool"
LOG="/tmp/openmodelpool.log"
FALLBACK_DIR="/tmp/omp-fallback"

echo "$(date) [run-omp] main process start" >> "$LOG"

cd "$REPO" || { echo "$(date) [run-omp] cannot cd $REPO" >> "$LOG"; }

# 1) 若二进制缺失，现场编译（防御：build.sh 编译可能失败）
if [ ! -x "$BIN" ]; then
  echo "$(date) [run-omp] binary missing, building now ..." >> "$LOG"
  (cd "$REPO" && go build -o "$BIN" . >> "$LOG" 2>&1) || echo "$(date) [run-omp] build failed" >> "$LOG"
fi

# 2) supervisor 循环运行 openmodelpool（前台 wait，保持容器主进程存活）
(
  while true; do
    if [ -x "$BIN" ]; then
      echo "$(date) [run-omp] starting openmodelpool" >> "$LOG"
      "$BIN" >> "$LOG" 2>&1
      code=$?
      echo "$(date) [run-omp] openmodelpool exited ($code), restart in 3s" >> "$LOG"
    fi
    sleep 3
  done
) &
OMP_PID=$!

# 3) 兜底：若 8000 未被 openmodelpool 占用，起一个静态服务（保证端口可见、可探活）
# 先等 openmodelpool 尝试绑定 :8000，避免 fallback 抢端口
# echo "$(date) [run-omp] waiting 10s before fallback patrol ..." >> "$LOG"
# sleep 10
# 
# mkdir -p "$FALLBACK_DIR"
# echo "<html><body><h1>openmodelpool codespace</h1><p>fallback health page</p></body></html>" > "$FALLBACK_DIR/index.html"
# while true; do
#   if ! (ss -ltn 2>/dev/null | grep -q ':8000 '); then
#     ( cd "$FALLBACK_DIR" && python3 -m http.server 8000 --bind 0.0.0.0 >> "$LOG" 2>&1 & )
#     echo "$(date) [run-omp] fallback static server ensured on :8000" >> "$LOG"
#   fi
#   sleep 5
# done &
FB_PID=$!

# 4) Cloudflare Tunnel 看守：codespace 重启后自动恢复 openmodelpool.io 域名绑定
#    （自愈：二进制丢失则重下到持久化家目录；幂等：仅当未运行时拉起）
CFD_BIN="$HOME/.local/bin/cloudflared"
CFD_TOKEN="$HOME/.cloudflared/tunnel-token"
CFD_LOG="/tmp/cloudflared.log"
mkdir -p "$(dirname "$CFD_BIN")" "$HOME/.cloudflared"
if [ ! -x "$CFD_BIN" ]; then
  echo "$(date) [run-omp] cloudflared missing, self-installing" >> "$CFD_LOG"
  curl -sL --retry 12 --retry-all-errors -o "$CFD_BIN" https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 || echo "$(date) [run-omp] download FAILED" >> "$CFD_LOG"
  chmod +x "$CFD_BIN" 2>/dev/null || true
fi
(
  sleep 5
  while true; do
    if [ -x "$CFD_BIN" ] && [ -r "$CFD_TOKEN" ]; then
      if ! pgrep -x cloudflared >/dev/null 2>&1; then
        echo "$(date) [run-omp] starting cloudflared tunnel" >> "$CFD_LOG"
        nohup "$CFD_BIN" tunnel --no-autoupdate run --token "$(cat "$CFD_TOKEN")" >> "$CFD_LOG" 2>&1 &
      fi
    fi
    sleep 15
  done
) &
CFD_WATCH_PID=$!
wait $OMP_PID $CFD_WATCH_PID $FB_PID
