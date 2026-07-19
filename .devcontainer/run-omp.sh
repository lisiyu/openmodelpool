#!/bin/bash
# run-omp.sh — Codespaces 容器主进程（devcontainer `command`）。
# 设计目标：无论编译/运行是否出问题，都尽量让 :8000 处于监听，便于外部探活与排错。
set -u

REPO="/workspaces/openmodelpool"
BIN="/usr/local/bin/openmodelpool"
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
echo "$(date) [run-omp] waiting 10s before fallback patrol ..." >> "$LOG"
sleep 10

mkdir -p "$FALLBACK_DIR"
echo "<html><body><h1>openmodelpool codespace</h1><p>fallback health page</p></body></html>" > "$FALLBACK_DIR/index.html"
while true; do
  if ! (ss -ltn 2>/dev/null | grep -q ':8000 '); then
    ( cd "$FALLBACK_DIR" && python3 -m http.server 8000 --bind 0.0.0.0 >> "$LOG" 2>&1 & )
    echo "$(date) [run-omp] fallback static server ensured on :8000" >> "$LOG"
  fi
  sleep 5
done &
FB_PID=$!

# 保持主进程存活
wait $OMP_PID
