# mHost-go

[mHost](https://github.com/igevin/mHost) 的 Go 重写版本：一款面向 macOS 的轻量级 Hosts 与本地 DNS 管理工具。

本项目计划使用 Go 和 [MyGo](https://github.com/egoist/mygo) 构建原生桌面应用。在延续源项目核心功能、数据兼容要求和安全边界的同时，重新设计视觉、信息架构与交互，不复刻旧版中不合理的 UI 和操作逻辑。

> **当前状态：Phase 2 Hosts 改写交互验证。** 当前应用是可运行的 Hosts Rewrite Workbench：单一可编辑表面、行内诊断、staged 内嵌预览、常驻回执与回滚，全部由内存演示服务驱动，不读写真实 `/etc/hosts`。#4 将按同一服务契约实现真实 Hosts 核心。仓库仍不提供完整产品功能或正式安装包。

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
- [#3：Phase 1 Go 工程骨架与现有存储兼容](https://github.com/igevin/mHost-go/issues/3)
- [#13：Storage Workbench UI 与内存演示服务](https://github.com/igevin/mHost-go/issues/13)
- [#14：正式存储实现与集成](https://github.com/igevin/mHost-go/issues/14)
- [#16：Hosts 改写交互重设计与 Workbench UI](https://github.com/igevin/mHost-go/issues/16)
- [Storage Workbench UI 与服务契约](docs/phase-1/storage-ui-design.md)
- [Hosts 改写 Workbench UI 与服务契约](docs/phase-2/hosts-rewrite-ui-design.md)

## 当前阶段：Phase 2

Phase 2 的 UI 部分由 #16 交付：MyGo Hosts Rewrite Workbench、行内诊断、Save/Apply 显式分离、staged 预览、常驻回执、回滚、草稿保护与 UI 测试，全部基于内存 Demo Service（`internal/app/demo.HostsService`），不碰真实 `/etc/hosts`。

后续交付：

1. #4 按 `internal/app.HostsService` 契约实现真实 parser、managed block 合并/冲突、原子写、备份、读回验证与回滚。
2. #5 将已验证的交互产品化（正式布局、Profile CRUD、真实服务接入、安装包）。

Phase 1 的 Storage Workbench 代码保留在树中并由测试覆盖，但主入口不再启动它；#14 落地后由正式存储替换其 Demo Service。Phase 0 已完成技术验证和行为基线，报告见 [Phase 0 报告](docs/phase-0/report.md)。当前主入口不启动 DNS 原型，也不修改系统 DNS。

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

在 macOS 上运行 Hosts Rewrite Workbench（内存演示服务）：

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

日常开发（调试、测试、打包）推荐直接使用 `make`，命令清单见 `Makefile`，详细说明见 [docs/development.md](docs/development.md)。

## 运行测试构建

Phase 2 构建产物仅用于专用 M 系列 Mac 测试环境。从 GitHub Actions 下载 `mhost-phase-2-darwin-arm64` artifact 后，需要在“隐私与安全性”中显式允许打开，或仅对测试包移除 quarantine 属性：

```sh
xattr -dr com.apple.quarantine "/path/to/mHost Phase 2.app"
```

ad-hoc 签名不代表通过 Apple 公证，Gatekeeper 提示属于预期行为，不以 `spctl` 通过作为验收条件。

当前 Workbench 由内存演示服务驱动：改写流程、冲突、失败与回滚全部发生在内存中，不会写入 `/etc/hosts`。手动验证步骤见 [Hosts Rewrite Workbench 验证计划](docs/phase-2/hosts-rewrite-ui-validation.md)；Storage Workbench 的历史验证记录见 [Storage Workbench 验证计划](docs/phase-1/storage-ui-validation.md)。Phase 0 的历史验证记录仍保留在 [Phase 0 报告](docs/phase-0/report.md)。

## 安全边界

- 当前 Phase 2 Demo Service 只保存内存状态，不读取或写入真实 `/etc/hosts`；真实 Hosts 核心安全语义由 #4 实现并在受控环境验证。
- Phase 2 主入口不启动 DNS server/proxy，不请求 DNS 提权，不修改系统 DNS。
- Phase 1 Demo Service 只保存内存状态，不读取或写入用户选择的目录；真实存储安全语义由 #14 实现并通过隔离 fixture 验证。
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
