#!/bin/bash
# ModelMux 一键部署脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 配置变量
CONTAINER_NAME="modelmux"
IMAGE_NAME="modelmux:latest"
PORT=${PORT:-8080}
DATA_DIR=${DATA_DIR:-"./data"}

# 打印带颜色的信息
info() { echo -e "${BLUE}[INFO]${NC} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 检查 Docker
check_docker() {
    if ! command -v docker &> /dev/null; then
        error "Docker 未安装，请先安装 Docker"
        echo "安装指南: https://docs.docker.com/get-docker/"
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        error "Docker 服务未运行，请先启动 Docker"
        exit 1
    fi
    
    success "Docker 检查通过"
}

# 检查端口是否被占用
check_port() {
    if lsof -Pi :$PORT -sTCP:LISTEN -t &> /dev/null || \
       netstat -tuln 2>/dev/null | grep -q ":$PORT " || \
       ss -tuln 2>/dev/null | grep -q ":$PORT "; then
        error "端口 $PORT 已被占用"
        echo "请使用: PORT=<其他端口> ./deploy.sh"
        exit 1
    fi
    success "端口 $PORT 可用"
}

# 停止已存在的容器
stop_existing() {
    if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        warn "发现已存在的容器 $CONTAINER_NAME，正在停止..."
        docker stop $CONTAINER_NAME &> /dev/null || true
        docker rm $CONTAINER_NAME &> /dev/null || true
        success "已清理旧容器"
    fi
}

# 创建数据目录
create_data_dir() {
    if [ ! -d "$DATA_DIR" ]; then
        info "创建数据目录: $DATA_DIR"
        mkdir -p "$DATA_DIR"
    fi
    success "数据目录就绪: $DATA_DIR"
}

# 构建镜像
build_image() {
    info "正在构建 Docker 镜像..."
    docker build -t $IMAGE_NAME .
    success "镜像构建完成: $IMAGE_NAME"
}

# 启动容器
start_container() {
    info "正在启动容器..."
    
    docker run -d \
        --name $CONTAINER_NAME \
        --restart unless-stopped \
        -p $PORT:8080 \
        -v $(pwd)/$DATA_DIR:/app/data \
        -e TZ=Asia/Shanghai \
        $IMAGE_NAME
    
    success "容器启动成功"
}

# 显示部署信息
show_info() {
    echo ""
    echo "=========================================="
    echo -e "${GREEN}  ModelMux 部署完成！${NC}"
    echo "=========================================="
    echo ""
    echo "  访问地址: http://localhost:$PORT"
    echo "  管理面板: http://localhost:$PORT/admin"
    echo ""
    echo "  数据目录: $(pwd)/$DATA_DIR"
    echo "  容器名称: $CONTAINER_NAME"
    echo ""
    echo "  常用命令:"
    echo "    查看日志: docker logs -f $CONTAINER_NAME"
    echo "    停止服务: docker stop $CONTAINER_NAME"
    echo "    重启服务: docker restart $CONTAINER_NAME"
    echo "    删除容器: docker rm -f $CONTAINER_NAME"
    echo ""
    echo "  环境变量:"
    echo "    修改端口: PORT=9090 ./deploy.sh"
    echo "    修改数据目录: DATA_DIR=/path/to/data ./deploy.sh"
    echo ""
    echo "=========================================="
}

# 主流程
main() {
    echo ""
    echo "=========================================="
    echo "  ModelMux 一键部署"
    echo "=========================================="
    echo ""
    
    check_docker
    check_port
    stop_existing
    create_data_dir
    build_image
    start_container
    show_info
}

# 执行主流程
main
