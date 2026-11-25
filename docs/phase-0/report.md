# Phase 0 技术验证报告

## 结论

**暂定继续，最终 go/no-go 待 M 系列 Mac 真机验收。**

当前实现使用 MyGo 官方 v0.10.0 标准编辑器、UI 测试、托盘/退出事件和平台资源签名机制，没有使用私有 fork。自动化结果支持继续验证；真实下载放行、提权 proxy 和 DNS 恢复矩阵尚未在本机执行，不能视为已通过。

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
- `go vet ./...`：通过。
- `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...`：通过。
- `./scripts/build-darwin-arm64.sh`：MyGo v0.10.0 成功生成 `mHost Phase 0.app`；Linux 按框架预期跳过 DMG 和 macOS 签名。
- `file` 确认主程序及 `.app/Contents/Resources/bin/mhost-dns-proxy` 都是 Mach-O 64-bit arm64。
- MyGo 无窗口测试覆盖编辑、撤销/重做、搜索、替换、弹窗、隐藏恢复，以及 100,000 行生成文本的跳行、滚动和搜索完成性；未设置性能阈值。

### GitHub Actions macOS arm64

待分支推送后记录 run URL、commit 和结果。验收项目：

- [ ] `go test ./...` 和 `go vet ./...` 通过。
- [ ] 只构建 darwin/arm64，runner 架构为 arm64。
- [ ] `.app` 和 DMG 成功生成并上传。
- [ ] 主程序与包内 proxy 的 `lipo`/`codesign --verify` 通过。
- [ ] 从 `.app` 内执行 proxy `-check` 成功。
- [ ] artifact 包含构建 commit、Go/MyGo 版本和 SHA-256。

## 源项目兼容与差异

- 已验证 v1/v2 Manifest、Hosts/DNS Profile、设置合成视图、快照和广告配置的只读解析。
- 缺失 `mode` 默认 Hosts；必要字段缺失、错误类型和损坏 JSON 按文件报告，不阻断其他有效 Profile。
- `original_dns` 支持 tagged Manual/DHCP、旧空数组、`["Empty"]` 和 loopback 过滤。
- 源项目设置实际使用 localStorage；`settings.json` 仅是探针合成视图，不是已确认的现有磁盘契约。
- Go DNS 适配器按 #2 要求使用 argv 调用 `networksetup`，没有复制源项目的 shell 命令拼接；恢复必须读回验证后才清 marker。
- Phase 0 DNS 服务只应答 `phase0.test`，不包含正式上游 resolver、缓存、广告引擎或热加载。

## 真机结果

待用户在专用 M 系列 Mac 按手册执行。以下项目当前全部未声明通过：

- [ ] 下载 artifact 后显式放行可运行。
- [ ] 主窗口、编辑器、托盘、隐藏恢复和两种 Cmd-Q 状态。
- [ ] 10 万行真实窗口编辑、搜索、滚动和定位无阻断。
- [ ] 正常 Stop、Tray Quit、Cmd-Q、SIGTERM 的 DNS 恢复。
- [ ] proxy 启动失败和运行中退出的 DNS 恢复。
- [ ] 应用 SIGKILL 后下次启动恢复。

## 风险与后续影响

1. **真实权限链路仍待验证**：osascript 提权、root proxy readiness、用户态停止和 `networksetup` 写权限必须在目标 macOS 上验证。
2. **活动服务白名单是有意的 Phase 0 限制**：非白名单服务会安全失败；完整网络/DHCP/授权矩阵留到后续阶段。
3. **UI 不是最终设计**：当前 Sidebar 和 DNS Lab 用于技术验证，进入 #5 前先评审关键页面和核心交互。
4. **无长期 fork 决定**：目前无需 fork；如果真机发现框架缺陷，先评估边界明确的应用层 workaround，再更新继续/调整/停止结论。

## 最终决策记录

待 CI 与真机结果完成后填写：继续 / 调整 / 停止或更换方案。
