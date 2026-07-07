#!/bin/bash
# ModelMux 远程一键安装脚本
# 使用方式: curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash
# 或指定参数: curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash -s -- --port 9090 --dir /data/modelmux

set -e

# 默认配置
REPO_URL="https://github.com/lisiyu/modelmux.git"
INSTALL_DIR="${INSTALL_DIR:-$HOME/modelmux}"
CONTAINER_NAME="modelmux"
IMAGE_NAME="modelmux:latest"
PORT=${PORT:-8080}
DATA_DIR=${DATA_DIR:-""}  # 空则使用 INSTALL_DIR/data

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

info() { echo -e "${BLUE}[INFO]${NC} $1"; }
success() { echo -e "${GREEN}[OK]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# 解析参数
while [[ $# -gt 0 ]]; do
    case $1 in
        --port) PORT="$2"; shift 2 ;;
        --dir) INSTALL_DIR="$2"; shift 2 ;;
        --data) DATA_DIR="$2"; shift 2 ;;
        *) shift ;;
    esac
done

[ -z "$DATA_DIR" ] && DATA_DIR="$INSTALL_DIR/data"

echo ""
echo -e "${CYAN}╔══════════════════════════════════════╗${NC}"
echo -e "${CYAN}║       ModelMux 一键安装              ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════╝${NC}"
echo ""
info "安装目录: $INSTALL_DIR"
info "数据目录: $DATA_DIR"
info "服务端口: $PORT"
echo ""

# 检查 Docker
if ! command -v docker &> /dev/null; then
    error "Docker 未安装。请先安装: https://docs.docker.com/get-docker/"
fi
if ! docker info &> /dev/null; then
    error "Docker 未运行，请先启动 Docker 服务"
fi
success "Docker 环境正常"

# 检查端口
if command -v ss &> /dev/null; then
    if ss -tuln 2>/dev/null | grep -q ":$PORT "; then
        error "端口 $PORT 已被占用，请使用 --port 指定其他端口"
    fi
elif command -v lsof &> /dev/null; then
    if lsof -Pi :$PORT -sTCP:LISTEN -t &> /dev/null; then
        error "端口 $PORT 已被占用，请使用 --port 指定其他端口"
    fi
fi
success "端口 $PORT 可用"

# 克隆或更新仓库
if [ -d "$INSTALL_DIR/.git" ]; then
    info "更新已有仓库..."
    cd "$INSTALL_DIR"
    git pull --quiet origin main
    success "代码已更新"
else
    info "克隆仓库..."
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone --quiet --depth 1 "$REPO_URL" "$INSTALL_DIR"
    success "仓库克隆完成"
fi

cd "$INSTALL_DIR"

# 创建数据目录
mkdir -p "$DATA_DIR"

# 停止旧容器
if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    warn "清理旧容器..."
    docker stop $CONTAINER_NAME &> /dev/null || true
    docker rm $CONTAINER_NAME &> /dev/null || true
fi

# 构建镜像
info "构建 Docker 镜像（首次可能需要几分钟）..."
docker build -t $IMAGE_NAME . 2>&1 | tail -3
success "镜像构建完成"

# 启动容器
info "启动容器..."
docker run -d \
    --name $CONTAINER_NAME \
    --restart unless-stopped \
    -p $PORT:8080 \
    -v "$DATA_DIR":/app/data \
    -e TZ=Asia/Shanghai \
    $IMAGE_NAME > /dev/null

success "启动成功！"

# 获取 IP
HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")

echo ""
echo -e "${GREEN}╔══════════════════════════════════════╗${NC}"
echo -e "${GREEN}║       ✅ 安装完成！                  ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════╝${NC}"
echo ""
echo -e "  管理面板: ${CYAN}http://${HOST_IP}:${PORT}/admin${NC}"
echo -e "  API 地址: ${CYAN}http://${HOST_IP}:${PORT}/v1/chat/completions${NC}"
echo ""
echo -e "  数据目录: $DATA_DIR"
echo -e "  安装目录: $INSTALL_DIR"
echo ""
echo -e "  常用命令:"
echo -e "    ${YELLOW}查看日志${NC}  docker logs -f $CONTAINER_NAME"
echo -e "    ${YELLOW}停止服务${NC}  docker stop $CONTAINER_NAME"
echo -e "    ${YELLOW}重启服务${NC}  docker restart $CONTAINER_NAME"
echo -e "    ${YELLOW}更新版本${NC}  cd $INSTALL_DIR && git pull && docker stop $CONTAINER_NAME && docker rm $CONTAINER_NAME && docker build -t $IMAGE_NAME . && docker run -d --name $CONTAINER_NAME --restart unless-stopped -p $PORT:8080 -v $DATA_DIR:/app/data -e TZ=Asia/Shanghai $IMAGE_NAME"
echo ""
echo -e "  一行更新:"
echo -e "  ${CYAN}curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash${NC}"
echo ""
