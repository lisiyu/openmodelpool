#!/bin/bash
# OpenModelPool 多平台交叉编译脚本
# 用法:
#   ./build-all.sh                 # 编译所有平台
#   ./build-all.sh linux-amd64     # 编译单个平台
#   ./build-all.sh list            # 列出所有支持的平台

set -euo pipefail

PROJECT="openmodelpool"
OUTPUT_DIR="dist"
GO_BIN="/usr/local/go/bin/go"
export GOPROXY="https://goproxy.cn,direct"
export CGO_ENABLED=0

get_version() {
    local ver
    ver=$(git describe --tags --exact-match 2>/dev/null) || \
    ver="dev-$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
    echo "$ver"
}

VERSION=$(get_version)
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S')

declare -A TARGETS=(
    ["linux-amd64"]="openmodelpool-linux-amd64"
    ["linux-arm64"]="openmodelpool-linux-arm64"
    ["linux-armv7"]="openmodelpool-linux-armv7"
    ["darwin-amd64"]="openmodelpool-darwin-amd64"
    ["darwin-arm64"]="openmodelpool-darwin-arm64"
    ["windows-amd64"]="openmodelpool-windows-amd64.exe"
)

LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*"; }

build_target() {
    local key="$1"
    local output="${OUTPUT_DIR}/${TARGETS[$key]}"
    IFS='-' read -ra parts <<< "$key"

    local goos="${parts[0]}"
    local goarch="${parts[1]}"
    local goarm=""

    if [[ "$goarch" == "armv7" ]]; then
        goarch="arm"
        goarm="7"
    fi

    info "编译 $key -> $(basename "$output")"

    GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
        $GO_BIN build -ldflags="$LDFLAGS" -trimpath -o "$output" .

    local size
    size=$(du -h "$output" | cut -f1)

    if command -v sha256sum &>/dev/null; then
        sha256sum "$output" | awk '{print $1}' > "${output}.sha256"
    else
        shasum -a 256 "$output" | awk '{print $1}' > "${output}.sha256"
    fi

    ok "  完成: $(basename "$output") ($size)"
}

if [[ ! -x "$GO_BIN" ]]; then
    GO_BIN="$(command -v go 2>/dev/null || true)"
fi
if [[ -z "$GO_BIN" || ! -x "$GO_BIN" ]]; then
    err "未找到 Go 编译器，请安装 Go 1.21+"
    exit 1
fi

info "OpenModelPool 交叉编译"
info "版本: $VERSION"
info "编译器: $($GO_BIN version)"
echo

TARGET="${1:-all}"

if [[ "$TARGET" == "list" ]]; then
    echo "支持的平台:"
    for key in "${!TARGETS[@]}"; do
        echo "  $key -> ${TARGETS[$key]}"
    done | sort
    exit 0
fi

mkdir -p "$OUTPUT_DIR"

if [[ "$TARGET" != "all" ]]; then
    if [[ -z "${TARGETS[$TARGET]+x}" ]]; then
        err "不支持的平台: $TARGET"
        echo "运行 '$0 list' 查看支持的平台"
        exit 1
    fi
    build_target "$TARGET"
    echo
    ok "编译完成！产物在 $OUTPUT_DIR/"
    exit 0
fi

SUCCESS=0
FAIL=0
RESULTS=()

for key in linux-amd64 linux-arm64 linux-armv7 darwin-amd64 darwin-arm64 windows-amd64; do
    if build_target "$key"; then
        SUCCESS=$((SUCCESS + 1))
        RESULTS+=("${GREEN}✓${NC} $key -> ${TARGETS[$key]}")
    else
        FAIL=$((FAIL + 1))
        RESULTS+=("${RED}✗${NC} $key -> 失败")
        warn "$key 编译失败，继续..."
    fi
done

echo
echo "════════════════════════════════════════"
info "编译结果汇总"
echo "════════════════════════════════════════"
for r in "${RESULTS[@]}"; do
    echo -e "  $r"
done
echo "────────────────────────────────────────"
info "成功: $SUCCESS / 失败: $FAIL / 总计: $((SUCCESS + FAIL))"
echo

if [[ $SUCCESS -gt 0 ]]; then
    echo "# OpenModelPool $VERSION SHA256 Checksums" > "${OUTPUT_DIR}/checksums.txt"
    echo "# Build: $BUILD_TIME" >> "${OUTPUT_DIR}/checksums.txt"
    for f in "${OUTPUT_DIR}"/*.sha256; do
        name=$(basename "${f%.sha256}")
        hash=$(cat "$f")
        echo "$hash  $name" >> "${OUTPUT_DIR}/checksums.txt"
    done
    ok "校验文件: ${OUTPUT_DIR}/checksums.txt"
fi

if [[ $FAIL -gt 0 ]]; then
    exit 1
fi

ok "全部编译完成！产物在 $OUTPUT_DIR/"
