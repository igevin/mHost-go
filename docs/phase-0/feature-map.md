# Phase 0 源项目功能地图

## 基线与使用方式

- 源项目：`igevin/mHost` v0.4.2
- 固定 commit：`ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39`
- 本文中的源码链接均指向该 commit；后续源 `master` 变化通过增量条目跟踪，不在验证期间移动基线。
- 本文件是迁移地图，不是完整需求规格。详细行为在对应阶段展开。

结论含义：**对齐**表示保留行为与安全语义；**调整**表示功能保留但 UI/交互或实现可重设计；**暂不迁移**表示留到对应后续阶段，不属于 Phase 0 产品功能。

## 1. Profile 管理与导入导出

- **入口与流程**：Sidebar 选择 Profile；创建、编辑、启停、删除、复制；文件或文本导入，Hosts/JSON 导出；Hosts 与 DNS Profile 分模式列出。
- **源码**：[`src/components/Sidebar.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/Sidebar.tsx)、[`src/components/HostsProfileView.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/HostsProfileView.tsx)、[`src/components/DnsProfileList.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/DnsProfileList.tsx)、[`src-tauri/src/commands/profile.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/profile.rs)、[`profile_io.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/profile_io.rs)。
- **已有测试**：`src/components/__tests__/{Sidebar,CreateProfileDialog,ImportDialog}.test.tsx`、`src/stores/__tests__/profiles.test.ts`；`profile.rs`、`profile_io.rs` 内 Rust 单元测试。
- **兼容与安全**：Profile 必需字段和 UUID 不能静默丢失；缺失 `mode` 默认为 `hosts`；写盘需原子化，模式切换不能留下双文件歧义；保护项不可误删；导入需验证，导出不得泄露到未选择路径。
- **初步结论**：功能对齐，信息架构和操作可调整。Phase 0 只读样本；正式 CRUD、导入导出留到 Phase 1+。
- **交互观察**：创建、编辑、启停和危险操作分散在多个入口，后续应统一选中态、 dirty 状态和删除确认，不照搬弹窗层级。

## 2. 快照

- **入口与流程**：手动/自动保存全部 Profile 快照，列表、查看、恢复和删除；`snapshots/index.json` 加速列表，`pending/` 事务记录用于崩溃后协调。
- **源码**：[`src/pages/Snapshot.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/pages/Snapshot.tsx)、[`src/components/SnapshotPanel.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/SnapshotPanel.tsx)、[`src-tauri/src/commands/snapshot.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/snapshot.rs)。
- **已有测试**：`SnapshotPanel.test.tsx`；`snapshot.rs` 内覆盖索引重建、事务残留、数量上限、权限和自动快照的测试。
- **兼容与安全**：保留 Snapshot/Metadata 字段、RFC 3339 时间、索引版本和 pending 恢复语义；索引/快照包含内部域名，写入需原子且 owner-only；损坏索引可重建但不能误删唯一快照。
- **初步结论**：数据与恢复边界对齐，页面交互可调整；Phase 0 只解析代表样本，不实现快照工作流。

## 3. Hosts 解析、Apply 与 Rollback

- **入口与流程**：编辑规则并验证，生成合并/冲突/diff 预览，确认后写入 `/etc/hosts` 管理块；快速 Apply 或强制预览；写入前备份，写后验证，失败回滚。
- **源码**：[`src/components/RuleEditor.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/RuleEditor.tsx)、[`src-tauri/crates/mhost-hosts/src/parser.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-hosts/src/parser.rs)、[`mhost-apply/src/lib.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-apply/src/lib.rs)、[`src-tauri/src/commands/apply.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/apply.rs)。
- **已有测试**：`mhost-hosts/src/{parser,formatter,validator}.rs`、`mhost-apply/src/writer/{tests,verification,backup}.rs`、`mhost-apply/src/integration_tests.rs`、`commands/apply.rs` 及前端 RuleEditor/ApplyConfirmDialog 测试。
- **兼容与安全**：注释、多域名、禁用规则、同/异 IP 重复规则和管理块标记语义需保留；只修改管理块；路径校验、原子写入、备份、读回验证和可验证回滚不可削弱。
- **初步结论**：核心解析与安全语义对齐，编辑器视觉和确认交互可调整。Phase 0 仅验证 MyGo 编辑器/诊断及只读 Profile，不写真实 `/etc/hosts`。

## 4. DNS Profile、启停、取消与恢复

- **入口与流程**：DNS Profile 管理；开启时启动 1053 服务和提权 53 proxy，捕获原 DNS 后切至 loopback；关闭、取消、Tray Quit、Cmd-Q、SIGTERM 和异常退出均进入清理；下次启动处理遗留状态。
- **源码**：[`src/components/DnsProfileView.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/components/DnsProfileView.tsx)、[`src-tauri/src/commands/dns.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/dns.rs)、[`mhost-dns/src/platform.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-dns/src/platform.rs)、[`proxy.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-dns/src/proxy.rs)。
- **已有测试**：`src/stores/__tests__/dns.test.ts`、`DnsProfileList.test.tsx`、`StatusBar.test.tsx`；`dns.rs`、`platform.rs`、`proxy.rs`、`server.rs` 内测试。
- **兼容与安全**：区分 Manual 与 DhcpEmpty，接受旧数组和 `["Empty"]`；过滤原快照中的 loopback；先持久化恢复信息再改系统；接口白名单、IP 校验、进程身份校验、恢复后读回验证和 marker 保留是硬边界。
- **初步结论**：安全目标对齐，但 Go 原型按 #2 要求直接用 argv 调用 `networksetup`，不复制源项目的 shell 字符串拼接。Phase 0 只验证最小生命周期，不迁移完整 resolver 和网络切换矩阵。
- **交互观察**：DNS 内存状态与 OS 状态可能分离；后续应把“应用认为已开启”和“系统实际指向”合并成可理解的状态与恢复引导，而不是只显示开关。

## 5. 广告屏蔽

- **入口与流程**：DNS 模式下启停广告屏蔽；管理来源、响应类型、白名单、刷新周期；手动/自动刷新，显示规则数、错误、耗时和来源重叠。
- **源码**：[`src/pages/AdBlock.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/pages/AdBlock.tsx)、[`src-tauri/src/commands/adblock.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/adblock.rs)、[`mhost-storage/src/adblock.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-storage/src/adblock.rs)、[`mhost-dns/src/adblock.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/crates/mhost-dns/src/adblock.rs)。
- **已有测试**：`src/pages/__tests__/AdBlock.test.tsx`；`commands/adblock.rs`、`mhost-dns/src/{adblock,trie}.rs`、`mhost-storage/src/adblock.rs` 内测试。
- **兼容与安全**：`adblock.json`、来源 UUID、whitelist、hosts/domains 格式及 null 字段需兼容；缓存按来源隔离，失败保留旧缓存；规则上限 fail-closed；损坏配置先备份，不能静默清空。
- **初步结论**：数据与 fail-closed 边界对齐，复杂状态展示可重组；Phase 0 只读配置，不实现规则引擎、网络刷新或热加载。

## 6. 托盘与窗口生命周期

- **入口与流程**：托盘切换 Profile、刷新、打开广告页、显示主窗口和退出；用户关闭窗口时隐藏，托盘/Dock 再打开；退出时清理 DNS。
- **源码**：[`src-tauri/src/tray.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/tray.rs)、[`tray_logic.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/tray_logic.rs)、[`src-tauri/src/lib.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/lib.rs)、[`src/hooks/useGlobalKeybindings.ts`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/hooks/useGlobalKeybindings.ts)。
- **已有测试**：`tray_logic.rs` 内菜单解析/更新测试；`src/components/__tests__/Layout.test.tsx`；退出清理由 `dns.rs`、`lib.rs` 注释与测试覆盖部分竞态。
- **兼容与安全**：Hide 与 Quit 必须严格区分；多退出入口幂等，不能递归退出；隐藏后编辑状态保留；仅托盘驻留时 Cmd-Q 仍可清理。
- **初步结论**：生命周期对齐，菜单密度与窗口导航可调整。Phase 0 用最小 Open/Quit 验证真实行为。

## 7. 设置

- **入口与流程**：DNS 开关与恢复、Quick Apply 偏好、命令面板历史等；部分状态来自后端，部分偏好存于 WebView localStorage。
- **源码**：[`src/pages/Settings.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/pages/Settings.tsx)、[`src/lib/atomWithLocalStorage.ts`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/lib/atomWithLocalStorage.ts)、[`src/stores/profiles/state.ts`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/stores/profiles/state.ts)。
- **已有测试**：`src/pages/__tests__/Settings.test.tsx`、`src/lib/__tests__/atomWithLocalStorage.test.ts`、`src/components/__tests__/StatusBar.test.tsx`。
- **兼容与安全**：quick-apply 和 palette-history 使用 localStorage key/JSON 值及 shape guard；DNS 状态以 OS 只读探测校正。源项目没有实际 `settings.json` 读写实现，不能仅凭旧注释假定其格式。
- **初步结论**：偏好语义对齐，存储位置在 Go 原生 UI 阶段重新设计并制定迁移方案。`fixtures/storage/v2-full/settings.json` 只是探针用的合成视图，不宣称是现有磁盘契约。

## 8. 更新检查

- **入口与流程**：设置页触发 GitHub latest release 查询，比较当前版本，展示新版本标题、说明和发布页链接。
- **源码**：[`src/pages/Settings.tsx`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src/pages/Settings.tsx)、[`src-tauri/src/commands/update.rs`](https://github.com/igevin/mHost/blob/ef1502e83a179e0b9b104a4246fe7f3c5d8f6e39/src-tauri/src/commands/update.rs)。
- **已有测试**：`update.rs` 内版本比较测试；`Settings.test.tsx` 覆盖设置页交互。
- **兼容与安全**：HTTPS、User-Agent、超时、响应大小限制、错误分类和版本比较行为需保留；网络失败不应影响本地功能。
- **初步结论**：检查能力后续对齐，正式发布流程不在 Phase 0；不实现自动更新，不为技术原型申请签名或公证凭据。

## Phase 0 跟踪规则

发现源行为不明确、文档与代码冲突或样本解析差异时，写入 `report.md` 的差异/风险清单，并链接到对应源码和 fixture。新 UI 只记录优化机会，最终页面与核心交互在进入 #5 后先评审再实施。
