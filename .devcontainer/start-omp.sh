#!/bin/bash
# start-omp.sh — Codespaces postStartCommand.
# 目标：让 codespace 在「创建后」以及「从 stopped 状态重新启动」时自动恢复运行环境，
# 无需人工 SSH 进容器手动拉起。
set -u

REPO="/workspaces/openmodelpool"
BIN="$REPO/openmodelpool"
LOG="/tmp/openmodelpool-start.log"

echo "$(date) [start-omp] === postStartCommand start ===" >> "$LOG"

# 0) Cloudflare Tunnel token 持久化：从 Codespaces Secret 注入到文件
#    设置方法：gh secret set -c <codespace-name> CLOUDFLARE_TUNNEL_TOKEN < token.txt
#    这样 codespace 重建后 token 不会丢失
CFD_TOKEN="$HOME/.cloudflared/tunnel-token"
mkdir -p "$HOME/.cloudflared"
if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ] && [ ! -r "$CFD_TOKEN" -o "$(wc -c < "$CFD_TOKEN" 2>/dev/null)" -lt 100 ]; then
  echo -n "$CLOUDFLARE_TUNNEL_TOKEN" > "$CFD_TOKEN"
  chmod 600 "$CFD_TOKEN"
  echo "$(date) [start-omp] tunnel token injected from Codespaces Secret" >> "$LOG"
elif [ -r "$CFD_TOKEN" ] && [ "$(wc -c < "$CFD_TOKEN")" -ge 100 ]; then
  echo "$(date) [start-omp] tunnel token file OK ($(wc -c < "$CFD_TOKEN") bytes)" >> "$LOG"
else
  echo "$(date) [start-omp] WARN: no tunnel token available (neither Secret nor file)" >> "$LOG"
fi

# 1) 启动 cron（端口 watchdog 依赖它；devcontainer 镜像常未预装 cron，缺失时自动安装）
if ! command -v cron >/dev/null 2>&1; then
  echo "$(date) [start-omp] cron not found, installing ..." >> "$LOG"
  ( sudo apt-get update -qq && sudo apt-get install -y -qq cron ) >>"$LOG" 2>&1 || \
    echo "$(date) [start-omp] cron install FAILED" >> "$LOG"
fi
if command -v cron >/dev/null 2>&1; then
  if pgrep -x cron >/dev/null 2>&1; then
    echo "$(date) [start-omp] cron already running" >> "$LOG"
  else
    ( sudo service cron start 2>>"$LOG" || cron 2>>"$LOG" ) && \
      echo "$(date) [start-omp] cron started" >> "$LOG" || \
      echo "$(date) [start-omp] cron start FAILED" >> "$LOG"
  fi
  # 端口 watchdog：每 5 分钟探活 :8000，挂了则通过 run-omp.sh supervisor 重启
  WATCHDOG='/workspaces/openmodelpool/.devcontainer/watchdog.sh'
  cat > "$WATCHDOG" <<'EOF'
#!/bin/bash
if ! (ss -ltn 2>/dev/null | grep -q ':8000 '); then
  setsid bash /workspaces/openmodelpool/.devcontainer/run-omp.sh >>/tmp/openmodelpool.log 2>&1 </dev/null &
fi
EOF
  chmod +x "$WATCHDOG"
  ( crontab -l 2>/dev/null | grep -v 'watchdog.sh'; echo "*/5 * * * * $WATCHDOG" ) | crontab - 2>>"$LOG" \
    && echo "$(date) [start-omp] watchdog crontab installed" >> "$LOG" \
    || echo "$(date) [start-omp] watchdog crontab FAILED" >> "$LOG"
else
  echo "$(date) [start-omp] cron unavailable, skip watchdog" >> "$LOG"
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
