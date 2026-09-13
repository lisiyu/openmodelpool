#!/bin/bash
# ============================================================
# OpenModelPool 群晖 NAS 一键部署脚本
# 使用方法:
#   1. 将本脚本与程序文件放到同一目录（如 /volume1/docker/openmodelpool/）
#   2. SSH 登录群晖后执行: sudo bash deploy-synology.sh
#   3. 可选参数: sudo bash deploy-synology.sh [安装目录] [端口]
# ============================================================
set -e

INSTALL_DIR="${1:-/volume1/@appstore/openmodelpool}"
PORT="${2:-8000}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ---- 颜色 ----
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "============================================"
echo "  OpenModelPool 群晖 NAS 一键部署"
echo "============================================"
echo -e "${NC}"

# ---- 检查 root ----
if [ "$(id -u)" -ne 0 ]; then
  echo -e "${RED}[错误] 请使用 root 权限运行: sudo bash deploy-synology.sh${NC}"
  exit 1
fi

# ---- 检测 CPU 架构 ----
ARCH=$(uname -m)
echo -e "${CYAN}[检测] CPU 架构: ${ARCH}${NC}"

BINARY=""
if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then
  BINARY="openmodelpool-linux-amd64"
  ARCH_LABEL="x86_64 (Intel/AMD)"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
  BINARY="openmodelpool-linux-arm64"
  ARCH_LABEL="ARM64 (AArch64)"
elif [ "$ARCH" = "armv7l" ] || [ "$ARCH" = "arm" ]; then
  BINARY="openmodelpool-linux-armv7"
  ARCH_LABEL="ARMv7"
else
  echo -e "${RED}[错误] 不支持的架构: ${ARCH}${NC}"
  echo "支持的架构: x86_64, ARM64, ARMv7"
  exit 1
fi

echo -e "${GREEN}[OK] 匹配二进制: ${BINARY} (${ARCH_LABEL})${NC}"

# ---- 定位二进制文件 ----
BIN_PATH=""
for candidate in \
  "$SCRIPT_DIR/$BINARY" \
  "$SCRIPT_DIR/openmodelpool" \
  "$SCRIPT_DIR/openmodelpool-linux-amd64" \
  "$SCRIPT_DIR/openmodelpool-linux-arm64" \
  "$SCRIPT_DIR/openmodelpool-linux-armv7"; do
  if [ -f "$candidate" ]; then
    BIN_PATH="$candidate"
    break
  fi
done

if [ -z "$BIN_PATH" ]; then
  echo -e "${RED}[错误] 找不到可执行文件${NC}"
  echo "请确保以下文件之一与此脚本在同一目录:"
  echo "  - $BINARY"
  echo "  - openmodelpool"
  exit 1
fi

echo -e "${GREEN}[OK] 使用文件: ${BIN_PATH}${NC}"

# ---- 检测 DSM 版本 ----
DSM_VERSION=""
if [ -f /etc.defaults/VERSION ]; then
  source /etc.defaults/VERSION
  DSM_VERSION="${majorversion}.${minorversion}"
  echo -e "${CYAN}[检测] DSM 版本: ${DSM_VERSION}${NC}"
elif [ -f /etc/synoinfo.conf ]; then
  DSM_VERSION=$(grep "^adminui=" /etc/synoinfo.conf 2>/dev/null | cut -d= -f2 | tr -d '"' || echo "unknown")
  echo -e "${CYAN}[检测] DSM 版本: ${DSM_VERSION}${NC}"
else
  echo -e "${YELLOW}[警告] 无法检测 DSM 版本，按 DSM 7+ 处理${NC}"
  DSM_VERSION="7"
fi

# ---- 创建安装目录 ----
echo ""
echo -e "${CYAN}[1/6] 创建安装目录...${NC}"
mkdir -p "$INSTALL_DIR/data"
echo -e "${GREEN}  -> $INSTALL_DIR${NC}"

# ---- 复制文件 ----
echo -e "${CYAN}[2/6] 复制程序文件...${NC}"
cp "$BIN_PATH" "$INSTALL_DIR/openmodelpool"
chmod +x "$INSTALL_DIR/openmodelpool"

if [ -f "$SCRIPT_DIR/admin.html" ]; then
  cp "$SCRIPT_DIR/admin.html" "$INSTALL_DIR/admin.html"
  echo -e "${GREEN}  -> admin.html${NC}"
fi

if [ -d "$SCRIPT_DIR/docs" ]; then
  cp -r "$SCRIPT_DIR/docs" "$INSTALL_DIR/docs"
  echo -e "${GREEN}  -> docs/${NC}"
fi

echo -e "${GREEN}  -> openmodelpool (${ARCH_LABEL})${NC}"

# ---- 创建启动脚本 ----
echo -e "${CYAN}[3/6] 创建启动脚本...${NC}"
cat > "$INSTALL_DIR/start.sh" << EOF
#!/bin/bash
cd "$INSTALL_DIR"
export PORT="$PORT"
exec ./openmodelpool >> "$INSTALL_DIR/data/app.log" 2>&1
EOF
chmod +x "$INSTALL_DIR/start.sh"
echo -e "${GREEN}  -> start.sh${NC}"

# ---- 创建停止脚本 ----
cat > "$INSTALL_DIR/stop.sh" << EOF
#!/bin/bash
PIDFILE="$INSTALL_DIR/data/openmodelpool.pid"
if [ -f "\$PIDFILE" ]; then
  PID=\$(cat "\$PIDFILE")
  kill "\$PID" 2>/dev/null && echo "已停止 (PID: \$PID)"
  rm -f "\$PIDFILE"
else
  PIDS=\$(pgrep -f "$INSTALL_DIR/openmodelpool")
  if [ -n "\$PIDS" ]; then
    kill \$PIDS && echo "已停止"
  else
    echo "服务未运行"
  fi
fi
EOF
chmod +x "$INSTALL_DIR/stop.sh"

# ---- 创建状态脚本 ----
cat > "$INSTALL_DIR/status.sh" << EOF
#!/bin/bash
PIDS=\$(pgrep -f "$INSTALL_DIR/openmodelpool")
if [ -n "\$PIDS" ]; then
  echo "✅ 运行中 (PID: \$PIDS)"
  echo "端口: $PORT"
else
  echo "❌ 未运行"
fi
EOF
chmod +x "$INSTALL_DIR/status.sh"

# ---- 配置开机自启 ----
echo -e "${CYAN}[4/6] 配置开机自启...${NC}"

# 方法1: DSM 7+ 的触发计划任务
if [ -n "$DSM_VERSION" ] && [ "${DSM_VERSION%%.*}" -ge 7 ] 2>/dev/null; then
  # 使用 /usr/local/etc/rc.d/ (DSM 7 仍支持)
  RC_SCRIPT="/usr/local/etc/rc.d/openmodelpool.sh"
  cat > "$RC_SCRIPT" << EOF
#!/bin/bash
case "\$1" in
  start)
    su root -c "$INSTALL_DIR/start.sh &"
    echo "OpenModelPool started"
    ;;
  stop)
    $INSTALL_DIR/stop.sh
    echo "OpenModelPool stopped"
    ;;
  restart)
    \$0 stop
    sleep 2
    \$0 start
    ;;
  status)
    $INSTALL_DIR/status.sh
    ;;
  *)
    echo "Usage: \$0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
EOF
  chmod +x "$RC_SCRIPT"
  echo -e "${GREEN}  -> 开机自启: $RC_SCRIPT${NC}"
  echo -e "${YELLOW}  -> 也可在 DSM 控制面板 > 任务计划 > 新增 > 触发的任务 > 启动 > 运行 $INSTALL_DIR/start.sh${NC}"
else
  # DSM 6
  RC_SCRIPT="/usr/local/etc/rc.d/openmodelpool.sh"
  cat > "$RC_SCRIPT" << EOF
#!/bin/bash
case "\$1" in
  start)
    $INSTALL_DIR/start.sh &
    echo "OpenModelPool started"
    ;;
  stop)
    $INSTALL_DIR/stop.sh
    echo "OpenModelPool stopped"
    ;;
  restart)
    \$0 stop
    sleep 2
    \$0 start
    ;;
  *)
    echo "Usage: \$0 {start|stop|restart}"
    exit 1
    ;;
esac
exit 0
EOF
  chmod +x "$RC_SCRIPT"
  echo -e "${GREEN}  -> 开机自启: $RC_SCRIPT${NC}"
fi

# ---- 防火墙放行 ----
echo -e "${CYAN}[5/6] 配置防火墙...${NC}"
# 尝试通过 synowebapi 添加防火墙规则（可能需要 DSM 7）
if command -v synowebapi &>/dev/null; then
  echo -e "${YELLOW}  -> 请在 DSM 控制面板 > 安全性 > 防火墙中放行端口 $PORT${NC}"
else
  echo -e "${YELLOW}  -> 请确保防火墙已放行端口 $PORT${NC}"
fi

# ---- 启动服务 ----
echo -e "${CYAN}[6/6] 启动服务...${NC}"

# 先停掉可能已有的进程
pkill -f "$INSTALL_DIR/openmodelpool" 2>/dev/null || true
sleep 1

# 启动
$INSTALL_DIR/start.sh &
echo $! > "$INSTALL_DIR/data/openmodelpool.pid"
sleep 3

# ---- 检查结果 ----
if pgrep -f "$INSTALL_DIR/openmodelpool" >/dev/null; then
  NAS_IP=$(ip addr show | grep -oP 'inet \K[0-9.]+' | grep -v '127.0.0.1' | head -1)
  echo ""
  echo -e "${GREEN}============================================${NC}"
  echo -e "${GREEN}  ✅ 部署成功！${NC}"
  echo -e "${GREEN}============================================${NC}"
  echo ""
  echo -e "  管理面板:  ${CYAN}http://${NAS_IP}:$PORT/admin${NC}"
  echo -e "  API 地址:  ${CYAN}http://${NAS_IP}:$PORT/v1${NC}"
  echo ""
  echo -e "  安装目录:  $INSTALL_DIR"
  echo -e "  日志文件:  $INSTALL_DIR/data/app.log"
  echo -e "  DSM 版本:  $DSM_VERSION"
  echo -e "  CPU 架构:  $ARCH_LABEL"
  echo ""
  echo -e "  ${YELLOW}常用命令:${NC}"
  echo -e "    启动:     bash $INSTALL_DIR/start.sh"
  echo -e "    停止:     bash $INSTALL_DIR/stop.sh"
  echo -e "    状态:     bash $INSTALL_DIR/status.sh"
  echo -e "    查看日志: tail -f $INSTALL_DIR/data/app.log"
  echo -e "    开机自启: $RC_SCRIPT (已配置)"
  echo ""
  echo -e "  ${YELLOW}⚠️  首次使用请访问管理面板设置管理员账号${NC}"
  echo ""
else
  echo -e "${RED}[错误] 服务启动失败${NC}"
  echo "请检查日志: tail -f $INSTALL_DIR/data/app.log"
  exit 1
fi
