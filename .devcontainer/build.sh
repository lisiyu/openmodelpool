#!/bin/bash
# build.sh — Codespaces postCreateCommand.
# 关键结论（实测）：此 Codespaces 运行时不会执行 devcontainer 的 `command` 字段，
# 但 postCreateCommand 中用 `setsid ... &` 拉起的子进程可以持久存活（诊断日志服务已验证）。
# 因此这里：构建二进制 -> 用 setsid 常驻拉起 openmodelpool -> 退出（setsid 子进程继续存活）。
set -u
REPO="/workspaces/openmodelpool"
BIN="$REPO/openmodelpool"          # 写到 vscode 可写的仓库目录，避免 /usr/local/bin 权限拒绝
LOG="/tmp/openmodelpool.log"
PROG="/tmp/omp-progress.log"

echo "$(date) [build] === postCreateCommand start ===" | tee -a "$PROG"

# 浏览器依赖（后台 best-effort，不阻塞服务）
( sudo apt-get update -qq >/dev/null 2>&1; sudo apt-get install -y -qq chromium fonts-liberation fonts-noto-cjk >/dev/null 2>&1 \
  && echo "$(date) [build] chromium OK" >>"$PROG" \
  || echo "$(date) [build] chromium FAILED (browser-login may be unavailable)" >>"$PROG" ) &

cd "$REPO" || { echo "$(date) [build] cannot cd $REPO" | tee -a "$PROG"; exit 1; }

echo "$(date) [build] go build -> $BIN" | tee -a "$PROG"
go build -o "$BIN" . >>"$PROG" 2>&1
if [ $? -ne 0 ]; then
  echo "$(date) [build] \xe2\x9d\x8c go build FAILED" | tee -a "$PROG"
  tail -n 40 "$PROG"
  exit 1
fi
echo "$(date) [build] \xe2\x9c\x85 go build OK" | tee -a "$PROG"

# 用 setsid 常驻拉起（已验证 setsid 子进程在 postCreateCommand 退出后仍存活）
# 注意：start-omp.sh 也会通过 run-omp.sh 拉起 openmodelpool，
# 这里仅在 start-omp.sh 尚未运行时才启动，避免双实例抢 :8000
if pgrep -x openmodelpool >/dev/null 2>&1; then
  echo "$(date) [build] openmodelpool already running (started by start-omp.sh), skip" | tee -a "$PROG"
else
  echo "$(date) [build] launching openmodelpool via setsid ..." | tee -a "$PROG"
  setsid "$BIN" >>"$LOG" 2>&1 &
fi

# 给一点时间绑定端口，并回报状态
sleep 6
if (ss -ltn 2>/dev/null | grep -q ':8000 '); then
  echo "$(date) [build] \xe2\x9c\x85 openmodelpool listening on :8000" | tee -a "$PROG"
else
  echo "$(date) [build] \xe2\x9a\xa0 :8000 not listening yet, check $LOG" | tee -a "$PROG"
  tail -n 30 "$LOG" >>"$PROG" 2>/dev/null
fi
exit 0
