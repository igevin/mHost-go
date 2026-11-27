# 本地开发与调试指南

本文档说明如何在本机调试 mHost-go 代码，以及如何在本地编译 macOS（Apple Silicon）版应用镜像。日常命令均已收敛到 `Makefile`，`make help` 可查看全部入口。

## 环境准备

| 依赖 | 版本 | 说明 |
| --- | --- | --- |
| Go | `1.27.3+` | 以 `go.mod` 为准 |
| Delve | 最新 | 仅调试需要：`go install github.com/go-delve/delve/cmd/dlv@latest` |
| MyGo | `v0.10.0` | 已通过 `go.mod` 的 `tool` 指令内置，无需单独安装 |

本机若通过 mise 管理 Go，而 `go` 不在 PATH 中，可在 make 时覆盖变量：

```sh
make GO="mise exec go@1.27.3 -- go" test
```

## 常用命令速查

```sh
make help                # 全部命令及说明
make test                # 单元测试
make test-race           # 竞态检测
make vet                 # 静态检查
make fmt / make fmt-check
make build               # 编译全部 cmd 到 build/local/
make clean               # 清理 build/ 与覆盖率报告
```

## 本地调试

### 命令行组件（DNS proxy）

DNS proxy 是独立的 `cmd/mhost-dns-proxy`，最适合命令行调试。**不要直接监听 53 或 1053 端口**：53 需要 root，1053 是桌面端 DNS 服务默认端口，容易和残留进程冲突。调试一律用高位端口：

```sh
make debug-proxy
# 等价于：
dlv debug ./cmd/mhost-dns-proxy -- -listen 127.0.0.1:15353 -target 127.0.0.1:11053
```

常用 dlv 交互命令：`b main.main` 下断点、`c` 继续、`n` 单步、`p proxy` 打印变量、`exit` 退出。也支持无头模式（供编辑器 attach）：

```sh
dlv debug --headless --listen=:2345 --api-version=2 --accept-multiclient ./cmd/mhost-dns-proxy -- -listen 127.0.0.1:15353
```

### 单元测试

测试集中在 `internal/` 下，均为无窗口测试，Linux/macOS 均可直接调试：

```sh
# 交互式调试某个包的某个测试
dlv test ./internal/dnslifecycle -- -test.run TestManager
# 或
make debug-test PKG=./internal/dnslifecycle PKGTEST=TestManager
```

VS Code 在 `.vscode/launch.json` 中添加配置即可图形化调试（属本地编辑器配置，不入库）：

```json
{
  "name": "Debug Test",
  "type": "go",
  "request": "launch",
  "mode": "test",
  "program": "${workspaceFolder}/internal/dnslifecycle",
  "args": ["-test.run", "TestManager"]
}
```

### 桌面端（`cmd/mhost`）

桌面端基于 MyGo 原生 GUI，**只能在 macOS 上真正启动窗口**；Linux 上 `go run ./cmd/mhost` 无法起 GUI，调试 UI 逻辑请走单元测试（`internal/desktop` 的 `app_test.go`、`model_test.go` 覆盖模型层）或到 macOS 上使用：

```sh
make debug-mhost   # 仅 macOS
```

## 本地编译 mac 版

目标产物：`build/darwin-arm64/mHost Phase 2.app`（以及 macOS 上的 `.dmg`）。仅支持 `darwin/arm64`，ad-hoc 自签名，不走 Apple 开发者账号。

### 在 Linux 上交叉编译（产出 .app）

```sh
make build-darwin-arm64
```

该命令执行 `scripts/build-darwin-arm64.sh`：

1. 交叉编译包内 DNS proxy：`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build` → `resources/darwin-arm64/bin/mhost-dns-proxy`
2. `go tool mygo build -platform darwin/arm64` 产出 `.app`

**在 Linux 上 DMG 打包和 ad-hoc 签名会按预期跳过**（这是 MyGo 的平台行为，不是错误），可用于快速验证编译是否通过。生成的 `.app` 应拷贝到 macOS 上再用 `make verify-darwin-arm64` 或手动运行验收。

### 在 macOS 上完整打包（产出 .app + DMG）

```sh
make package-darwin    # = build-darwin-arm64 + verify-darwin-arm64
```

`verify-darwin-arm64.sh` 会依次校验：`.app` 与包内 proxy 均为 arm64（lipo）、ad-hoc 签名有效（`codesign --verify --strict`）、proxy 包内自检通过（`mhost-dns-proxy -check`）、DMG 存在，并生成 `build-metadata.txt` 和 `SHA256SUMS`。

macOS 首次运行时需在「系统设置 → 隐私与安全性」放行自签名应用。

### 从 CI 获取正式产物

master 或 PR 的 macOS CI（`.github/workflows/phase-2.yml`）会自动产出并上传 artifact，包含 `.app`、`.dmg`、`build-metadata.txt`、`SHA256SUMS`，可作为本地构建的对照基线。

## PR 会自动跑什么

每个指向 master 的 PR 会并行触发三个工作流：

| 工作流 | 跑在哪 | 内容 |
| --- | --- | --- |
| `CI`（`ci.yml`） | ubuntu | `gofmt` 格式门禁、`go vet`、单元测试 + 覆盖率、`go test -race` 竞态检测、`go build`、darwin/arm64 交叉编译 + vet |
| `Phase 2 validation`（`phase-2.yml`） | macOS | 打包 `.app` / DMG、签名、包内校验 |
| `AI Code Review`（`ai-review.yml`） | ubuntu | 以 `gevinyu` 身份在 PR 上发一条 AI 审查意见 |

### AI 审查的性质

审查是**建议性（advisory）**的：它发的是 `COMMENTED` review，不计入 mergeability，不会挡住合并。
判定权仍在人手里——AI 只提供信息，不构成门禁。

结论会分成三档：

- ✅ **可以合并**——没有发现需要处理的问题；
- 🟡 **建议改进**——不影响正确性，可以合并；
- ⛔ **需要修改后再合**——存在必须先处理的问题。

复审时会把上一轮结论喂回模型，因此**只报本轮新增的问题**；连续复审没有新增 🔴 时才会给「可以合并」。
每条审查开头都带被审查的 head SHA，评论里也会写明它取代上一轮哪一条。

### 几个容易误解的地方

- **fork PR 不做 AI 审查。** CI 与打包照跑，但审查会跳过——审查用的凭据是个人 PAT，不对外部作者的 PR 暴露。
- **审查依据只信 master 上的版本。** 评审标准（`.github/ai-review-prompt.md`）与防线（`.github/ai-review/guard.ts`）都从 master 恢复，PR 里的副本不可信。master 上缺这两个文件时审查直接拒绝运行，不会降级成一份无标准、无防线的自由审查。
- **审查程序本身也来自 master。** 工作流用 `pull_request_target` 触发，执行的是 master 上的工作流定义，PR 只作为**数据**被读取，从不被执行。
- **没配置密钥时是「跳过」而不是「失败」。** 缺少模型 API key 或 reviewer token 时审查不跑，check 保持绿色并在日志里告警。补齐密钥后重新触发即可自动开始。
- **diff 超过 2000 行会降级。** 此时只审核心目录（`internal`、`cmd`、依赖清单、CI 工作流与构建脚本）；PR 不涉及核心目录时改为审完整 diff。评论横幅会写明这次实际审的范围与是否被截断。

## 目录与端口约定

| 路径 / 端口 | 用途 |
| --- | --- |
| `build/local/` | `make build` 的本地二进制输出 |
| `build/darwin-arm64/` | mac 打包产物（.app / .dmg / 校验文件） |
| `resources/darwin-arm64/bin/mhost-dns-proxy` | 打进 .app 包内的 proxy 二进制 |
| `53` | DNS proxy 默认监听端口，需 root，仅产品形态使用 |
| `1053` | 桌面端内置 DNS 服务端口，勿与残留进程冲突 |
| `15353` / `11053` | 本地调试约定的 proxy 监听 / 目标高位端口 |
