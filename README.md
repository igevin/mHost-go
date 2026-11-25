# mHost-go

[mHost](https://github.com/igevin/mHost) 的 Go 重写版本：一款面向 macOS 的轻量级 Hosts 与本地 DNS 管理工具。

本项目计划使用 Go 和 [MyGo](https://github.com/egoist/mygo) 构建原生桌面应用。在延续源项目核心功能、数据兼容要求和安全边界的同时，重新设计视觉、信息架构与交互，不复刻旧版中不合理的 UI 和操作逻辑。

> **当前状态：Phase 0 技术验证。** 仓库尚不提供可用的产品功能或正式安装包。当前交付物是技术原型、行为基线、构建流程和验证报告，而非完整重写版本。

## 项目方向

后续版本将围绕源项目的主要功能域逐步实施：

- Hosts Profile 管理、导入导出、快照、Apply 和 Rollback。
- DNS Profile、DNS 服务启停与系统 DNS 恢复。
- 广告屏蔽、托盘、窗口生命周期、设置和更新检查。
- 兼容源项目的代表性存储格式，并保留关键默认值语义。
- 优先保证 DNS 恢复、用户数据保护和权限操作的安全性。

详细需求与阶段安排见：

- [#1：Go 重写总体规划](https://github.com/igevin/mHost-go/issues/1)
- [#2：Phase 0 技术验证与行为基线](https://github.com/igevin/mHost-go/issues/2)
- [Phase 0 执行终稿](https://github.com/igevin/mHost-go/issues/2#issuecomment-7157833490)

## 当前阶段：Phase 0

Phase 0 是后续投入的 go/no-go 验收门，主要工作包括：

1. 建立源项目功能地图，收集虚构、脱敏或程序生成的代表性测试语料。
2. 实现 Go 只读存储探针，验证样本解析、关键默认值和损坏输入处理。
3. 建立 MyGo 单窗口技术原型，验证编辑器、弹窗、托盘及退出生命周期。
4. 建立最小 DNS proxy 生命周期原型，验证正常退出、异常退出和下次启动恢复。
5. 建立 GitHub Actions macOS 构建，生成可供 M 系列 Mac 验证的 arm64 应用和 DMG。
6. 汇总验证结果、风险、workaround 和明确的继续、调整或停止结论。

本阶段使用内存假数据构建 UI 原型，不实现正式存储层和完整产品工作流，也不进行正式性能对照。编辑器仅以 10 万行 Hosts 样本进行无阻断冒烟验证。

## 技术基线

- 源项目版本：`v0.4.2`
- 源项目 commit：[`ef1502e`](https://github.com/igevin/mHost/commit/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39)
- MyGo：`v0.10.0`
- Go：`1.27.3+`，以 MyGo 的工具链要求为准
- 目标平台：仅 `darwin/arm64`，不支持 Intel 或 universal 构建
- 签名方式：ad-hoc 签名，不使用 Apple 开发者账号、Developer ID、公证或 stapling

Phase 0 期间固定上述验证版本。确需调整时，必须在验证报告中记录原因和影响。

## 构建与验证

需要 Go `1.27.3+`。运行全部单元测试和无窗口 UI 测试：

```sh
go test ./...
go vet ./...
```

在 macOS 上运行原生 UI 原型：

```sh
go tool mygo dev
```

只读解析合成存储样本：

```sh
go run ./cmd/storage-probe -root fixtures/storage/v2-full
go run ./cmd/storage-probe -root fixtures/storage/mixed-invalid
```

第二个命令会输出逐文件错误并以非零状态退出，这是损坏语料的预期结果。探针不会写回或迁移输入。

构建 arm64 主程序和包内 proxy：

```sh
./scripts/build-darwin-arm64.sh
```

脚本先交叉构建 `resources/darwin-arm64/bin/mhost-dns-proxy`，再执行 `go tool mygo build -platform darwin/arm64`。macOS runner 还会验证架构、ad-hoc 签名和包内 proxy 自检，并生成 DMG、`SHA256SUMS` 与构建元数据。

## 运行测试构建

Phase 0 构建产物仅用于专用 M 系列 Mac 测试环境。从 GitHub Actions 下载 `mhost-phase-0-darwin-arm64` artifact 后，需要在“隐私与安全性”中显式允许打开，或仅对测试包移除 quarantine 属性：

```sh
xattr -dr com.apple.quarantine "/path/to/mHost Phase 0.app"
```

ad-hoc 签名不代表通过 Apple 公证，Gatekeeper 提示属于预期行为，不以 `spctl` 通过作为验收条件。

完整编辑、退出和 DNS 恢复步骤见 [Phase 0 真机验证手册](docs/phase-0/manual-validation.md)。启动 UI 不会主动修改 DNS；只有在 DNS Lab 中显式点击 Start DNS Test 才会请求授权。验证结果记录在 [Phase 0 报告](docs/phase-0/report.md)。

## 安全边界

- Phase 0 不写真实用户目录，不执行正式数据迁移，不修改生产 `/etc/hosts`。
- DNS 原型仅在专用 macOS 测试环境修改系统 DNS；GitHub Actions 不执行真实提权或系统 DNS 修改。
- 修改 DNS 前必须持久化原始状态和恢复标记，恢复并验证成功后才能删除标记。
- 调用系统工具时直接传递参数，不拼接 shell 命令，并保留接口名白名单和 DNS IP 校验。
- 不收集真实用户私有配置。

## Phase 0 非目标

- 不完成最终 UI 设计，也不要求复刻旧版页面和交互。
- 不实现正式 Profile、快照、导入导出或 Apply 工作流。
- 不实现完整 DNS resolver、缓存、广告规则引擎或热加载。
- 不覆盖完整的 Wi-Fi、DHCP 和授权异常矩阵。
- 不实现自动更新或正式发布流程。
- 不测启动时间、内存、p95，也不预设性能收益。

## 开发约定

- AI Agent 在本仓库工作时必须阅读并遵守 [AGENTS.md](AGENTS.md)。
- 除非明确授权，不在 `main`、`master`、`dev` 分支直接修改、提交或合并；默认从这些保护分支新建工作分支。
- 开发遵循 TDD：RED → GREEN → REFACTOR；纯配置、文档、重命名和 Wire DI 接线除外。
- Go 开发规范由用户级 `go-convention` skill 提供，项目约定作为补充。
- 不删除或覆盖 `.gitignore` 保护的本地独有配置文件。

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。
