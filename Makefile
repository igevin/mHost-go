# mHost-go 常用开发命令
#
# 快速开始：
#   make help                查看全部命令
#   make test                跑全部单元测试
#   make build-darwin-arm64  本地交叉编译 macOS arm64 .app
#
# Go 工具链默认取 PATH 中的 `go`，本机若通过 mise 管理 Go，可用：
#   make GO="mise exec go@1.27.3 -- go" test

SHELL := /bin/bash

GO      ?= go
DLV     ?= dlv
GOFMT   := $(shell $(GO) env GOROOT)/bin/gofmt
# 脚本内部直接调用 `go`，这里把工具链 bin 目录注入 PATH，保证脚本与用户选的 GO 一致
GOBIN   := $(shell $(GO) env GOROOT)/bin
GOBUILD := CGO_ENABLED=0 $(GO) build

BUILD_DIR   := build
PROXY_BIN   := resources/darwin-arm64/bin/mhost-dns-proxy
COVER_OUT   := coverage.out
COVER_HTML  := coverage.html

.PHONY: help tidy fmt fmt-check vet test test-race test-cover build \
	run run-proxy run-storage-probe run-hosts-fixture \
	debug-mhost debug-proxy debug-test \
	build-darwin-arm64 verify-darwin-arm64 package-darwin clean

help: ## 查看全部命令
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

## 代码质量

tidy: ## 整理 go.mod / go.sum
	$(GO) mod tidy

fmt: ## gofmt 格式化全部代码
	$(GO) fmt ./...

fmt-check: ## 检查格式（不改写，CI 用）
	@test -z "$$($(GOFMT) -l .)" || (echo "需要 make fmt"; exit 1)

vet: ## go vet 静态检查
	$(GO) vet ./...

## 测试

test: ## 跑全部单元测试
	$(GO) test ./...

test-race: ## 跑全部单元测试（竞态检测）
	$(GO) test -race ./...

test-cover: ## 跑测试并生成覆盖率报告
	$(GO) test -coverprofile=$(COVER_OUT) ./...
	$(GO) tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
	@echo "覆盖率报告: $(COVER_HTML)"

## 构建与本地运行

build: ## 编译全部二进制到 build/local/
	$(GOBUILD) -trimpath -o $(BUILD_DIR)/local/ ./cmd/...

run: ## 运行桌面端原型（仅 macOS 可起 GUI）
	$(GO) run ./cmd/mhost

run-proxy: ## 运行 DNS proxy（高位端口，无需 root）
	$(GO) run ./cmd/mhost-dns-proxy \
		-listen 127.0.0.1:15353 -target 127.0.0.1:11053

run-storage-probe: ## 用 v2-full fixture 跑存储探针
	$(GO) run ./cmd/storage-probe -root fixtures/storage/v2-full

run-hosts-fixture: ## 生成 10 万行 hosts 冒烟样本
	$(GO) run ./cmd/hosts-fixture

## 本地调试（需要 dlv：go install github.com/go-delve/delve/cmd/dlv@latest）

debug-proxy: ## dlv 调试 DNS proxy（高位端口）
	$(DLV) debug ./cmd/mhost-dns-proxy -- \
		-listen 127.0.0.1:15353 -target 127.0.0.1:11053

debug-mhost: ## dlv 调试桌面端入口（仅 macOS）
	$(DLV) debug ./cmd/mhost

debug-test: ## dlv 调试测试（PKG/PKGTEST 可指定，如 make debug-test PKG=./internal/dnslifecycle PKGTEST=TestManager）
	$(DLV) test $(PKG) -- -test.run '$(PKGTEST)'

## macOS arm64 打包

build-darwin-arm64: ## 交叉编译 darwin/arm64 .app + 包内 proxy
	PATH="$(GOBIN):$$PATH" ./scripts/build-darwin-arm64.sh

verify-darwin-arm64: ## 校验打包产物（签名 / lipo / 自检 / DMG，仅 macOS）
	PATH="$(GOBIN):$$PATH" ./scripts/verify-darwin-arm64.sh

package-darwin: build-darwin-arm64 verify-darwin-arm64 ## 完整打包并校验（需 macOS 才能通过 verify）

## 清理

clean: ## 删除 build/、覆盖率报告和包内 proxy 二进制
	rm -rf $(BUILD_DIR) $(COVER_OUT) $(COVER_HTML) $(PROXY_BIN)
