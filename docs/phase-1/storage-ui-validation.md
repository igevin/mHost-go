# Phase 1 Storage Workbench 验证计划

本文定义 #13 的无窗口 UI 测试和后续 macOS 真机验收。#13 已实现核心内存 UI 场景；#14 接入真实存储后补齐其余契约测试和真机验收。未执行的场景不代表已通过。

设计依据：[Storage Workbench UI 与服务契约](storage-ui-design.md)。

## 1. 验证原则

- 自动化 UI 测试使用 fake DirectoryPicker、fake StorageService 和 MyGo `ui.NewTester`，不触发原生目录弹窗或真实文件 I/O。
- 服务与 UI 集成测试只使用临时目录中的合成数据副本；仓库 fixture 永远只读。
- 不用真实用户目录、真实 WebView localStorage、真实 `/etc/hosts` 或系统 DNS 做测试。
- 每个写操作同时验证界面回执和服务调用参数；失败场景不能仅断言“显示了错误”。
- 每个场景映射到验收 ID，避免只测试按钮存在而遗漏安全语义。

### 1.1 验收 ID

| ID | 验收要求 |
| --- | --- |
| AC1 | 覆盖读取、迁移、写入、备份和损坏处理，操作结果明确 |
| AC2 | 写入只在显式选择的独立副本，目标路径清晰，源目录不变 |
| AC3 | UI 通过应用服务契约工作，不直接访问文件或存储内部 |
| AC4 | Hide、恢复、退出和进行中的写入行为明确且幂等 |
| AC5 | 错误、取消、并发和可访问性状态可理解，不静默失败 |

## 2. Fixture 与故障注入矩阵

| Fixture/注入 | 用途 | 写入边界 |
| --- | --- | --- |
| `fixtures/storage/v1-hosts` | v1 读取、迁移预览、缺失 mode 默认值 | 原目录只读；迁移只作用于临时副本 |
| `fixtures/storage/v2-full` | v2 全量正常数据、Profile/Snapshot/AdBlock 展示、验证变更 | 原目录只读 |
| `fixtures/storage/mixed-invalid` | 有效 Profile 与损坏 JSON、缺字段、错类型并存 | 原目录只读；不自动修复 |
| `fixtures/storage/v2-legacy-empty` | 旧 `original_dns: []` 的 DHCP/Empty 展示 | 原目录只读 |
| `fixtures/storage/v2-legacy-placeholder` | 旧 `["Empty"]` 的 DHCP/Empty 展示 | 原目录只读 |
| `fixtures/storage/v2-legacy-filtered` | loopback 过滤和公网 DNS 保留 | 原目录只读 |
| `fixtures/storage/v2-tagged-dhcp` | tagged DHCP/Empty 展示 | 原目录只读 |
| 空目录、非目录、不存在目录 | 初始读取和路径错误 | 仅临时路径 |
| 相同/嵌套/非空目标、符号链接逃逸 | 工作副本边界 | 不断言 UI 自校验，由 fake 服务返回分类错误 |
| 权限拒绝 | 读取、复制、备份、写入和恢复错误 | 仅临时副本；Unix 权限测试按运行用户能力跳过 |
| backup_failed | 备份失败后不得继续目标写入 | fake 服务验证调用顺序和回执 |
| write_failed | 原子替换失败、原文件保留或状态未知 | fake 服务返回部分 WriteReceipt |
| readback_mismatch | 写后模型不一致，不得全局成功 | fake 服务返回未验证回执 |
| stale_operation | 预览过期、旧读取晚返回 | fake 服务控制完成顺序 |

`v2-full/settings.json` 仅用于兼容 Phase 0 合成视图，不作为旧版设置磁盘格式。设置迁移测试必须由 #14 先定义 localStorage 输入适配和新版原生设置 DTO，不能复用该假定绕过契约。

## 3. 无窗口 UI 测试场景

实现时可将每个场景拆成更小的 Go 测试，但测试名或注释需保留场景 ID。

### 3.1 初始、选择与读取

| ID | 操作 | 预期 | 验收 |
| --- | --- | --- | --- |
| UI-001 | 新建 Model 并渲染首帧 | 显示安全说明和 Open Source；无路径、无 Summary；未调用 picker/service | AC2、AC3 |
| UI-002 | picker 返回取消 | 恢复 empty/原 Workspace，无错误横幅，焦点回 Open Source | AC5 |
| UI-003 | 选择 v2-full 对应 fake 路径并完成 Inspect | 显示完整根路径、Read only、v2 和各数量；Write/Migrate 不可用 | AC1、AC2 |
| UI-004 | Inspect 返回 mixed-invalid Summary | 有效 Profile 可见，Issues 数量和相对路径稳定排序，无自动写入调用 | AC1、AC5 |
| UI-005 | Inspect 返回 invalid_path/permission_denied | 显示根级错误、Retry 和 Choose Another Source；不显示伪造数据 | AC5 |
| UI-006 | 加载中点击 Cancel | 显示取消而非失败；服务收到 context 取消；后续结果不能更新页面 | AC5 |
| UI-007 | 已有数据时 Reload 失败 | 旧摘要标记 May be stale，错误持久可见，不写源目录 | AC5 |
| UI-008 | 快速选择 A 后 B，让 A 后返回 | 只显示 B；A 的 Summary/Error 被 Operation ID 丢弃 | AC5 |

### 3.2 工作副本与迁移

| ID | 操作 | 预期 | 验收 |
| --- | --- | --- | --- |
| UI-009 | 从只读源创建副本成功 | 使用 sourceID 和目标路径调用服务；显示 SOURCE/WORK、Working copy 和副本 Revision | AC1、AC2、AC3 |
| UI-010 | picker 选择相同、嵌套、非空或逃逸路径 | 服务错误分类正确，保持源模式，不显示可写状态 | AC2、AC5 |
| UI-011 | 创建副本失败 | 源摘要保留，目标错误可见，不允许迁移/保存 | AC2、AC5 |
| UI-012 | v1 副本打开迁移预览后 Cancel | 未调用 ApplyMigration；副本和版本不变，焦点回 Preview Migration | AC1、AC2 |
| UI-013 | 预览包含 Blockers | Apply Migration 禁用，阻塞项可定位到 Issues | AC1、AC5 |
| UI-014 | 确认有效迁移计划 | 以 workspaceID/planID 调用；成功后显示 v2、逐项备份和 Readback verified | AC1、AC2 |
| UI-015 | Apply 返回 stale_operation | 不显示迁移成功；要求重新预览，不重复调用旧 planID | AC5 |
| UI-016 | 迁移部分失败 | 显示 Migration incomplete 和每项状态；已写项不回填为未写，失败项不标成功 | AC1、AC5 |

### 3.3 验证写入、备份与恢复

| ID | 操作 | 预期 | 验收 |
| --- | --- | --- | --- |
| UI-017 | 打开验证变更预览后 Cancel | 未调用 SaveValidationChange，Revision 不变 | AC1、AC2 |
| UI-018 | 确认验证变更并全部成功 | 使用 previewID；逐项显示 Written/Unchanged、BackupRef 和读回验证；只有此时显示全局成功 | AC1、AC2 |
| UI-019 | backup_failed | 对应项 Failed，Preservation 与实际回执一致；不得把目标写成成功 | AC1、AC5 |
| UI-020 | write_failed 并带部分回执 | 明确已完成/未完成项，旧摘要标 stale，提供 Reload/恢复入口 | AC1、AC5 |
| UI-021 | readback_mismatch | 显示未验证而非 All verified，不能因 error 为 nil 的局部项推断全局成功 | AC1、AC5 |
| UI-022 | ListBackups 成功 | 只显示当前 Workspace 的备份，包含实体、相对路径、时间和操作 ID | AC1、AC2 |
| UI-023 | Restore 弹窗 Cancel | 不调用 RestoreBackup，备份和 Revision 不变 | AC2 |
| UI-024 | Restore 成功 | 显示新的安全备份和 Restore verified；Reload 后摘要与回执一致 | AC1、AC2 |
| UI-025 | Restore 失败或 Preservation unknown | 显示实际/未知状态，不显示“已恢复” | AC5 |
| UI-026 | 连续点击 Save/Apply/Restore | 只产生一个操作 ID 和一次服务调用，所有写入口在 writing 禁用 | AC5 |

### 3.4 生命周期、视觉与可访问性

| ID | 操作 | 预期 | 验收 |
| --- | --- | --- | --- |
| UI-027 | 加载/预览中 Hide 再恢复 | Workspace、Tab、路径和操作状态保留；后台结果仍能合并 | AC4 |
| UI-028 | writing 中 Hide 再恢复 | 写操作继续且只执行一次，恢复后显示最新回执 | AC4 |
| UI-029 | 无写入时触发窗口 Cmd-Q 与 Tray Quit | 清理只执行一次，取消中的读取不会晚到更新已退出 Model | AC4 |
| UI-030 | writing 中触发两种 Quit，再 Cancel Quit | 先 PreventDefault；取消后 quitRequested 清除，页面仍可操作 | AC4、AC5 |
| UI-031 | writing 中登记 Quit，操作成功/失败结束 | 到达安全边界并生成回执后才继续 Quit；清理一次，不启动第二个写操作 | AC4 |
| UI-032 | OnQuitCanceled | quitRequested 和退出提示复位，不取消已完成的写入 | AC4 |
| UI-033 | Escape/Enter 操作迁移、保存和恢复弹窗 | Escape 取消；默认焦点为 Cancel；不会因 Enter 意外写入 | AC5 |
| UI-034 | 900×620、1120×760 和较长路径渲染 | 主操作和状态可见；路径可复制；Tabs/表格不遮挡错误 | AC5 |
| UI-035 | 明暗 Theme 下渲染成功、警告、失败 | 状态均有文字/图标，不只依赖颜色；文本对比和焦点可见 | AC5 |
| UI-036 | Settings & AdBlock 渲染新版设置迁移状态 | 不出现“旧版 settings.json”错误声明，不开放规则或偏好编辑 | AC3、AC5 |

### 3.5 契约测试

#14 另需为真实 StorageService 编写集成测试，不能只靠 fake UI 测试证明磁盘安全：

- CT-001：Inspect 只读，调用前后源目录文件内容、权限和目录项不变。
- CT-002：CreateWorkspace 拒绝相同、嵌套、非空和符号链接逃逸目标；只向空目标写入。
- CT-003：v1→v2 后新旧实现的领域模型一致，源副本保留，计划过期不能重复应用。
- CT-004：每个持久化写入经过备份、原子替换和读回验证；故障注入不留下半文件。
- CT-005：部分失败回执准确表达每个文件状态，不把多文件操作伪装成单事务。
- CT-006：备份恢复先保存当前文件，恢复后模型一致，失败时 Preservation 不虚构。
- CT-007：Settings 使用明确的新版原生格式和 localStorage 迁移策略，不假定旧版存在 settings.json。
- CT-008：测试进程默认 HOME/Application Support 指向临时隔离目录，测试结束无真实用户目录访问。

## 4. M 系列 macOS 真机验收

### 4.1 前置条件

仅当 #14 完成对应实现后执行：

- [ ] 使用 GitHub Actions 生成的 darwin/arm64、ad-hoc 签名应用，架构和校验和已验证。
- [ ] 在专用测试 Mac 上准备仅含合成数据的源目录；不使用真实 mHost 数据。
- [ ] 将仓库 fixture 复制到临时位置作为源，另准备不存在或为空的副本目标。
- [ ] 记录应用版本、commit、macOS 版本和 Mac 芯片。
- [ ] 准备文件树快照或哈希清单，用于证明源目录未变化。
- [ ] Phase 1 应用启动不监听 DNS 端口、不请求 DNS 提权、不修改系统 DNS。

### 4.2 核心流程

- [ ] MAN-001：启动后为初始空状态，未自动打开目录；显式放行自签名应用不属于数据写入。
- [ ] MAN-002：原生目录选择器的 Select、Cancel、New Folder 均可用，取消无错误，路径与 UI 一致。
- [ ] MAN-003：读取 v2-full，检查各 Tab、Profile/Snapshot 详情、AdBlock 数量和完整只读路径。
- [ ] MAN-004：读取 mixed-invalid，有效数据可查看，Issues 可定位、复制详情，无自动修复。
- [ ] MAN-005：创建 v1 工作副本，检查源哈希不变；取消迁移无变化，确认后版本、备份和读回回执正确。
- [ ] MAN-006：对 v2 副本执行验证变更，逐项核对 Manifest/Profile/设置/快照回执和磁盘模型。
- [ ] MAN-007：选择一个备份执行 Cancel 和 Restore；取消无变化，恢复有安全备份且读回验证成功。
- [ ] MAN-008：重复快速点击写按钮，只执行一个操作；成功后 Revision 与界面同步更新。

### 4.3 错误与权限

- [ ] MAN-009：只读源可以 Inspect，任何写操作都要求另建副本，源哈希保持不变。
- [ ] MAN-010：目标相同、嵌套、非空或不可写时被拒绝，错误动作可理解，源模式不丢失。
- [ ] MAN-011：通过独立故障注入构建备份失败、写失败和读回不一致，界面分别显示，不出现全局成功。
- [ ] MAN-012：迁移阻塞和损坏文件只显示实际状态，恢复失败时不宣称已恢复。
- [ ] MAN-013：长路径、含空格和非 ASCII 字符的临时目录可选择、复制路径和正确写入。

### 4.4 生命周期与可访问性

- [ ] MAN-014：普通 Close 隐藏，Dock 和 Tray Open 均可恢复；路径、Tab、选择和操作回执保留。
- [ ] MAN-015：窗口可见和隐藏两种状态下 Cmd-Q 均能退出；无写入时清理只发生一次。
- [ ] MAN-016：读取中 Quit 会取消读取；写入中 Quit 会提示并等待安全边界，Cancel Quit 可返回。
- [ ] MAN-017：写入中 Hide 后通过 Tray Quit，行为与窗口可见时一致，不静默终止写入。
- [ ] MAN-018：系统明暗色切换后文字和状态可辨；仅用键盘可完成选择、读取、预览取消和详情查看。
- [ ] MAN-019：VoiceOver 能读出窗口标题、Read only/Working copy、主按钮、表格行和错误摘要。
- [ ] MAN-020：调整至最小窗口及恢复默认尺寸，主操作、错误和路径仍可访问。

### 4.5 安全收尾

- [ ] MAN-021：整个验收过程没有真实用户目录写入、仓库 fixture 修改、`/etc/hosts` 修改或系统 DNS 修改。
- [ ] MAN-022：退出后无遗留应用进程、DNS server/proxy 或未说明的后台写操作。
- [ ] 将实际结果、失败截图/日志和结论回填到 #14；不要在本设计文档预先勾选。

## 5. 完成门槛

#13 的 UI 交付完成条件：

- 本文场景覆盖 AC1–AC5，设计中的每个主操作都有成功、取消和至少一个失败场景。
- 核心内存场景已由真实 MyGo View 和 fake/demo service 驱动，不需要视图直接访问存储。
- 真机清单只使用合成数据，并包含写入中 Quit 和源目录不变的安全验证。
- UI、交互状态机和核心无窗口测试通过评审后，#14 再按契约接入真实存储。

#13 不以真实磁盘读写或真机存储验证已经完成为关闭条件；这些执行项属于 #14 的实现验收。
