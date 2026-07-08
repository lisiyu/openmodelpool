# OpenModelPool Makefile
# ─────────────────────────────────────────

BINARY    := openmodelpool
MODULE    := github.com/lisiyu/openmodelpool
GO        := /usr/local/go/bin/go
GOPROXY   := https://goproxy.cn,direct
OUTPUT    := dist

# 版本信息
VERSION   := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev-$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)")
BUILD_TIME:= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT:= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 编译参数
LDFLAGS   := -s -w \
             -X main.version=$(VERSION) \
             -X main.buildTime=$(BUILD_TIME) \
             -X main.gitCommit=$(GIT_COMMIT)
GOFLAGS   := -trimpath

export GOPROXY
export CGO_ENABLED=0

# ─── 基础命令 ───────────────────────────

.PHONY: build run clean test fmt lint

## 编译当前平台
build:
	@echo "▸ 编译 $(BINARY) ($(VERSION))..."
	$(GO) build -ldflags="$(LDFLAGS)" $(GOFLAGS) -o $(BINARY) .
	@ls -lh $(BINARY)
	@echo "✓ 编译完成"

## 运行
run: build
	./$(BINARY)

## 清理
clean:
	rm -rf $(OUTPUT) $(BINARY) $(BINARY).exe
	@echo "✓ 已清理"

## 测试
test:
	$(GO) test -v -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

## 格式化
fmt:
	$(GO) fmt ./...

## 代码检查
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "请安装 golangci-lint"; exit 1; }
	golangci-lint run

# ─── 交叉编译 ───────────────────────────

.PHONY: build-all build-linux build-darwin build-windows

## 编译所有平台
build-all:
	@bash build-all.sh

## 仅 Linux
build-linux:
	@bash build-all.sh linux-amd64
	@bash build-all.sh linux-arm64
	@bash build-all.sh linux-armv7

## 仅 macOS
build-darwin:
	@bash build-all.sh darwin-amd64
	@bash build-all.sh darwin-arm64

## 仅 Windows
build-windows:
	@bash build-all.sh windows-amd64

# ─── Docker ─────────────────────────────

.PHONY: docker docker-push docker-compose

DOCKER_IMAGE := openmodelpool
DOCKER_TAG   := $(VERSION)

## 构建 Docker 镜像
docker:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest .

## 推送 Docker 镜像
docker-push:
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

## Docker Compose 启动
docker-compose:
	docker compose up -d

## Docker Compose 停止
docker-compose-down:
	docker compose down

# ─── 发布 ───────────────────────────────

.PHONY: release checksum

## 生成校验文件
checksum:
	@mkdir -p $(OUTPUT)
	@echo "# $(BINARY) $(VERSION) SHA256 Checksums" > $(OUTPUT)/checksums.txt
	@for f in $(OUTPUT)/$(BINARY)-*; do \
		[ -f "$$f" ] && [ "$${f##*.}" != "sha256" ] && \
		sha256sum "$$f" >> $(OUTPUT)/checksums.txt; \
	done
	@echo "✓ 校验文件: $(OUTPUT)/checksums.txt"

## 完整发布流程
release: clean build-all checksum
	@echo ""
	@echo "════════════════════════════════════════"
	@echo "  发布就绪: $(VERSION)"
	@echo "════════════════════════════════════════"
	@ls -lh $(OUTPUT)/ | grep -v "^total"
	@echo ""

# ─── 开发辅助 ────────────────────────────

.PHONY: dev deps tidy

## 安装开发依赖
deps:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

## 整理依赖
tidy:
	$(GO) mod tidy

## 开发模式（热重载）
dev:
	@command -v air >/dev/null 2>&1 || { echo "请安装 air: go install github.com/air-verse/air@latest"; exit 1; }
	air

# ─── 帮助 ───────────────────────────────

.PHONY: help
help:
	@echo "OpenModelPool 构建命令"
	@echo ""
	@echo "  make build            编译当前平台"
	@echo "  make build-all        编译所有平台 (6个目标)"
	@echo "  make build-linux      编译 Linux (amd64/arm64/armv7)"
	@echo "  make build-darwin     编译 macOS (amd64/arm64)"
	@echo "  make build-windows    编译 Windows (amd64)"
	@echo "  make clean            清理编译产物"
	@echo "  make test             运行测试"
	@echo "  make docker           构建 Docker 镜像"
	@echo "  make docker-compose   启动 Docker Compose"
	@echo "  make release          完整发布流程"
	@echo "  make help             显示此帮助"
