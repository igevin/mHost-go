# Phase 0 技术验证报告

## 结论

**继续，进入下一阶段。**

当前实现使用 MyGo 官方 v0.10.0 标准编辑器、UI 测试、托盘/退出事件和平台资源签名机制，没有使用私有 fork。2026-11-26 在专用 M 系列 Mac 上完成真机验收（放行运行、主窗口/编辑器/托盘/隐藏恢复/两种 Cmd-Q、10 万行真实窗口编辑）；DNS 恢复矩阵经项目 owner 决定不在 Phase 0 真机执行，延后到后续阶段验证，不计入本阶段验收阻塞。无需要长期维护私有 fork 的未决阻塞项。

## 验证基线

- 源项目：`igevin/mHost` v0.4.2，commit `ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39`
- MyGo：`v0.10.0`
- Go：`1.27.3`
- 目标：`darwin/arm64`
- 签名：ad-hoc，无 Developer ID、公证、stapling 或自动更新

功能地图见 [`feature-map.md`](feature-map.md)，合成语料见 [`fixtures/`](../../fixtures/README.md)，真机步骤见 [`manual-validation.md`](manual-validation.md)。

## 原型与运行方法

- 存储探针：`go run ./cmd/storage-probe -root fixtures/storage/v2-full`
- 损坏样本：`go run ./cmd/storage-probe -root fixtures/storage/mixed-invalid`（预期非零退出）
- Hosts 生成器：`go run ./cmd/hosts-fixture -lines 100000 -output /tmp/mhost-phase0.hosts`
- UI 开发：`go tool mygo dev`
- DNS proxy 非提权自检：`mhost-dns-proxy -check`
- 完整构建：`./scripts/build-darwin-arm64.sh`
- macOS 包内验证：`./scripts/verify-darwin-arm64.sh`

存储探针只读，不创建输入目录、不写回或迁移。UI 使用内存假数据；DNS 默认不启动，只有 DNS Lab 的显式 Start 操作可以请求提权并修改系统 DNS。

## 自动化验证

### 本地 Linux x86_64

- `go test ./...`：通过。
- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...`：通过。
- `./scripts/build-darwin-arm64.sh`：MyGo v0.10.0 成功生成 `mHost Phase 0.app`；Linux 按框架预期跳过 DMG 和 macOS 签名。
- `file` 确认主程序及 `.app/Contents/Resources/bin/mhost-dns-proxy` 都是 Mach-O 64-bit arm64。
- MyGo 无窗口测试覆盖编辑、撤销/重做、搜索、替换、弹窗、隐藏恢复，以及 100,000 行生成文本的跳行、滚动和搜索完成性；未设置性能阈值。

### GitHub Actions macOS arm64

最终代码验证 run：[Phase 0 validation #45619481582](https://github.com/igevin/mHost-go/actions/runs/45619481582)，commit `2f48ceb50865ba76d2ba0ad911dbe9e9e587f1f7`，全部步骤通过。

- [x] `go test ./...` 和 `go vet ./...` 通过。
- [x] 只构建 darwin/arm64，runner 架构为 arm64。
- [x] `.app` 和 DMG 成功生成并上传。
- [x] 主程序与包内 proxy 的 `lipo`/`codesign --verify` 通过。
- [x] 从 `.app` 内执行 proxy `-check` 成功。
- [x] artifact 包含构建 commit、Go/MyGo 版本和 SHA-256。

已实际下载该 run 的 `mhost-phase-0-darwin-arm64` artifact。`SHA256SUMS` 校验通过；下载后的主程序和包内 proxy 经 `file` 复核均为 Mach-O 64-bit arm64。构建元数据确认 Go 1.27.3 darwin/arm64、MyGo v0.10.0 和上述代码 commit。后续报告文档提交不改变该代码基线。

## 源项目兼容与差异

- 已验证 v1/v2 Manifest、Hosts/DNS Profile、设置合成视图、快照和广告配置的只读解析。
- 缺失 `mode` 默认 Hosts；必要字段缺失、错误类型和损坏 JSON 按文件报告，不阻断其他有效 Profile。
- `original_dns` 支持 tagged Manual/DHCP、旧空数组、`["Empty"]` 和 loopback 过滤。
- 源项目设置实际使用 localStorage；`settings.json` 仅是探针合成视图，不是已确认的现有磁盘契约。
- Go DNS 适配器的 `networksetup` 读操作使用 argv；写操作先经过服务白名单和 IP 校验，再安全 quoting 并通过 `osascript ... with administrator privileges` 执行，不接受未校验的 shell 命令。所有系统 DNS 操作有 15 秒上限，恢复必须读回验证后才清 marker。
- Phase 0 DNS 服务只应答 `phase0.test`，不包含正式上游 resolver、缓存、广告引擎或热加载。

## Review 回归覆盖

- DNS 写操作与 proxy 使用一致的管理员提权通道；Capture/Set/Current 均有有界超时。
- proxy 优雅停止超时后依次升级为 SIGTERM、SIGKILL；生命周期操作锁不再阻塞 UI 的 `Running()` 状态读取。
- 1053 端口占用导致 DNS server 启动失败时，退出不再发生 typed-nil panic，DNS 后端可用性独立表示。
- tagged manual `original_dns` 过滤 loopback 后为空时降级为 DHCP/Empty。
- snapshot 元数据 ID 必须通过 UUID 校验，不能越出只读探针根目录。
- macOS CI 新增 `go test -race ./...` 约束。

## 真机结果

2026-11-26 在专用 M 系列 Mac 上按 [`manual-validation.md`](manual-validation.md) 完成验收：

- [x] 下载 artifact 后显式放行可运行。
- [x] 主窗口、编辑器、托盘、隐藏恢复和两种 Cmd-Q 状态。
- [x] 10 万行真实窗口编辑、搜索、滚动和定位无阻断。

DNS 恢复矩阵（正常 Stop、Tray Quit、Cmd-Q、SIGTERM，proxy 启动失败/运行中退出，应用 SIGKILL 后下次启动恢复）经项目 owner 决定不在 Phase 0 真机执行，不计入本阶段验收阻塞；相关路径的自动化单测与 CI 约束继续有效，真机验证延后到后续阶段。

## 风险与后续影响

1. **真实权限链路延后验证**：proxy 与 DNS 写操作的 osascript 提权、root proxy readiness、优雅停止及 SIGTERM/SIGKILL 升级经项目 owner 决定不在 Phase 0 真机执行，延后到后续阶段在目标 macOS 上验证；自动化单测与 CI 约束继续有效。
2. **活动服务白名单是有意的 Phase 0 限制**：非白名单服务会安全失败；完整网络/DHCP/授权矩阵留到后续阶段。
3. **UI 不是最终设计**：当前 Sidebar 和 DNS Lab 用于技术验证，进入 #5 前先评审关键页面和核心交互。
4. **无长期 fork 决定**：目前无需 fork；如果真机发现框架缺陷，先评估边界明确的应用层 workaround，再更新继续/调整/停止结论。

## 最终决策记录

**继续**（2026-11-26）：真机验收通过（放行运行、UI 生命周期、10 万行编辑；DNS 恢复矩阵按 owner 决定延后至后续阶段），不存在需要长期维护私有 MyGo fork 的未决阻塞，DNS 恢复路径有明确的应用层实现与自动化约束。Phase 0 通过，进入下一阶段（#5 Hosts-only Alpha，先做关键页面与核心交互评审）。
