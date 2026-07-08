# ─── 构建阶段 ───
FROM golang:1.23-alpine AS builder

WORKDIR /build

# 先复制依赖文件，利用 Docker 缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码
COPY *.go ./
COPY *.html ./

# 编译优化: 去除调试信息 + 去除本地路径
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o openmodelpool .

# ─── 运行阶段 ───
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata curl

# 创建非 root 用户
RUN addgroup -S openmodelpool && adduser -S openmodelpool -G openmodelpool

WORKDIR /app

COPY --from=builder /build/openmodelpool .
COPY --from=builder /build/*.html ./

# 数据目录
RUN mkdir -p /app/data && chown -R openmodelpool:openmodelpool /app

USER openmodelpool

EXPOSE 8000

# 健康检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8000/health || exit 1

ENTRYPOINT ["./openmodelpool"]
CMD ["-data", "/app/data"]
