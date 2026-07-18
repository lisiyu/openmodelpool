# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w -X main.AppVersion=${VERSION}" -trimpath -o /out/openmodelpool .

# --- runtime stage ---
FROM debian:bookworm-slim
RUN groupadd -r openmodelpool \
 && useradd -r -g openmodelpool openmodelpool \
 && mkdir -p /app/data \
 && chown -R openmodelpool:openmodelpool /app
WORKDIR /app
COPY --from=builder /out/openmodelpool /app/openmodelpool
EXPOSE 8000
USER openmodelpool
VOLUME ["/app/data"]
ENTRYPOINT ["/app/openmodelpool"]
