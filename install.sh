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
DATA_DIR=${DATA_DIR:-""}
SKIP_DOCKER_INSTALL=${SKIP_DOCKER_INSTALL:-false}

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
        --skip-docker) SKIP_DOCKER_INSTALL=true; shift ;;
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

# ==================== 环境检测与安装 ====================

# 检测操作系统
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        OS_VERSION=$VERSION_ID
    elif [ -f /etc/redhat-release ]; then
        OS="centos"
    elif [ -f /etc/debian_version ]; then
        OS="debian"
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        OS="macos"
    else
        OS="unknown"
    fi
    echo "$OS"
}

# 安装 Docker
install_docker() {
    if [ "$SKIP_DOCKER_INSTALL" = "true" ]; then
        error "Docker 未安装，已跳过自动安装。请先手动安装 Docker"
    fi
    
    local os=$(detect_os)
    info "检测到操作系统: $os"
    info "正在安装 Docker..."
    
    case "$os" in
        ubuntu|debian)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq ca-certificates curl gnupg lsb-release > /dev/null 2>&1
            
            install -m 0755 -d /etc/apt/keyrings
            curl -fsSL https://download.docker.com/linux/$os/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg > /dev/null 2>&1
            chmod a+r /etc/apt/keyrings/docker.gpg
            
            echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$os $(lsb_release -cs) stable" > /etc/apt/sources.list.d/docker.list
            
            apt-get update -qq
            apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin > /dev/null 2>&1
            ;;
        centos|rhel|fedora|rocky|almalinux)
            yum install -y -q yum-utils > /dev/null 2>&1 || dnf install -y -q yum-utils > /dev/null 2>&1
            yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo > /dev/null 2>&1
            yum install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin > /dev/null 2>&1
            ;;
        alpine)
            apk add --no-cache docker docker-cli-compose > /dev/null 2>&1
            rc-update add docker boot > /dev/null 2>&1 || true
            ;;
        macos)
            warn "macOS 检测到。请手动安装 Docker Desktop: https://docs.docker.com/desktop/install/mac-install/"
            error "安装 Docker 后重新运行此脚本"
            ;;
        *)
            warn "未识别的操作系统: $os"
            info "尝试通用安装方式..."
            curl -fsSL https://get.docker.com | sh
            ;;
    esac
    
    # 启动 Docker 服务
    if command -v systemctl &> /dev/null; then
        systemctl start docker > /dev/null 2>&1 || true
        systemctl enable docker > /dev/null 2>&1 || true
    elif command -v service &> /dev/null; then
        service docker start > /dev/null 2>&1 || true
    fi
    
    # 验证安装
    if docker info &> /dev/null; then
        success "Docker 安装成功"
    else
        error "Docker 安装失败，请手动安装: https://docs.docker.com/get-docker/"
    fi
}

# 检查并安装 Docker
check_docker() {
    if command -v docker &> /dev/null; then
        if docker info &> /dev/null; then
            success "Docker 环境正常"
            return 0
        else
            warn "Docker 已安装但未运行，尝试启动..."
            if command -v systemctl &> /dev/null; then
                systemctl start docker > /dev/null 2>&1 || sudo systemctl start docker > /dev/null 2>&1 || true
            elif command -v service &> /dev/null; then
                service docker start > /dev/null 2>&1 || sudo service docker start > /dev/null 2>&1 || true
            fi
            
            if docker info &> /dev/null; then
                success "Docker 已启动"
                return 0
            fi
        fi
    fi
    
    warn "Docker 未安装或未运行"
    install_docker
}

# 检查端口
check_port() {
    if command -v ss &> /dev/null; then
        if ss -tuln 2>/dev/null | grep -q ":$PORT "; then
            error "端口 $PORT 已被占用，请使用 --port 指定其他端口"
        fi
    elif command -v lsof &> /dev/null; then
        if lsof -Pi :$PORT -sTCP:LISTEN -t &> /dev/null; then
            error "端口 $PORT 已被占用，请使用 --port 指定其他端口"
        fi
    elif command -v netstat &> /dev/null; then
        if netstat -tuln 2>/dev/null | grep -q ":$PORT "; then
            error "端口 $PORT 已被占用，请使用 --port 指定其他端口"
        fi
    fi
    success "端口 $PORT 可用"
}

# 检查 Git
check_git() {
    if ! command -v git &> /dev/null; then
        info "Git 未安装，正在安装..."
        local os=$(detect_os)
        case "$os" in
            ubuntu|debian)
                apt-get update -qq && apt-get install -y -qq git > /dev/null 2>&1
                ;;
            centos|rhel|fedora|rocky|almalinux)
                yum install -y -q git > /dev/null 2>&1 || dnf install -y -q git > /dev/null 2>&1
                ;;
            alpine)
                apk add --no-cache git > /dev/null 2>&1
                ;;
            *)
                error "请手动安装 Git 后重试"
                ;;
        esac
    fi
    success "Git 就绪"
}

# ==================== 主流程 ====================

# 前置检查
check_git
check_docker
check_port

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
echo ""
echo -e "  一行更新:"
echo -e "  ${CYAN}curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash${NC}"
echo ""
