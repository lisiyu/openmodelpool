#!/bin/bash
# start-omp.sh — Codespaces postStartCommand.
# 目标：让 codespace 在「创建后」以及「从 stopped 状态重新启动」时自动恢复运行环境，
# 无需人工 SSH 进容器手动拉起。
set -u

REPO="/workspaces/openmodelpool"
BIN="$REPO/openmodelpool"
LOG="/tmp/openmodelpool-start.log"

echo "$(date) [start-omp] === postStartCommand start ===" >> "$LOG"

# 1) 启动 cron（keepalive 自保活依赖它；cron 不随容器重启自启，必须在此显式拉起）
if command -v cron >/dev/null 2>&1; then
  if pgrep -x cron >/dev/null 2>&1; then
    echo "$(date) [start-omp] cron already running" >> "$LOG"
  else
    ( sudo service cron start 2>>"$LOG" || cron 2>>"$LOG" ) && \
      echo "$(date) [start-omp] cron started" >> "$LOG" || \
      echo "$(date) [start-omp] cron start FAILED" >> "$LOG"
  fi
else
  echo "$(date) [start-omp] cron binary not found, skip" >> "$LOG"
fi

# 2) 确保 openmodelpool 主进程在跑（带崩溃自愈 supervisor）。
#    run-omp.sh 自带 supervisor + fallback 探活，但本运行时并不执行 `command` 字段，
#    这里用 setsid 把 run-omp.sh 作为后台常驻主进程拉起；若已在运行则不重复。
if pgrep -f "run-omp.sh" >/dev/null 2>&1; then
  echo "$(date) [start-omp] run-omp.sh already running" >> "$LOG"
elif pgrep -x openmodelpool >/dev/null 2>&1; then
  echo "$(date) [start-omp] openmodelpool binary already running (skip)" >> "$LOG"
else
  echo "$(date) [start-omp] launching run-omp.sh via setsid ..." >> "$LOG"
  setsid bash "/workspaces/openmodelpool/.devcontainer/run-omp.sh" >>"$LOG" 2>&1 </dev/null &
  disown 2>/dev/null || true
fi

# 3) 给主进程一点时间绑定端口，回报状态
sleep 6
if (ss -ltn 2>/dev/null | grep -q ':8000 '); then
  echo "$(date) [start-omp] OK :8000 listening" >> "$LOG"
else
  echo "$(date) [start-omp] WARN :8000 not listening yet, see $LOG" >> "$LOG"
fi

exit 0
