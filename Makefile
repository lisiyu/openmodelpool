# OpenModelPool Agent — build automation
# Mirrors the commands documented in README.md (make build, make build-all, ...).

BINARY  := openmodelpool
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
# Inject the version into the binary (main.AppVersion) at build time.
LDFLAGS := -s -w -X main.AppVersion=$(VERSION)
GO      ?= go

.PHONY: all build build-all build-linux build-darwin build-windows \
        clean test vet fmt lint docker docker-compose release

all: build

build:
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY) .

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-linux-arm64 .
	GOOS=linux GOARCH=arm GOARM=7 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-linux-armv7 .

build-darwin:
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-darwin-arm64 .

build-windows:
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY)-windows-amd64.exe .

build-all: build-linux build-darwin build-windows

clean:
	rm -f $(BINARY) $(BINARY)-* coverage.out

test:
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

lint:
	$(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run

docker:
	docker build -t $(BINARY):$(VERSION) .

docker-compose:
	docker compose up -d

release: build-all
	@echo "Artifacts built. Publish with: git tag vX.Y.Z && git push origin vX.Y.Z"
