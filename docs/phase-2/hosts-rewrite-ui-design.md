# Phase 2 Hosts 改写 Workbench UI 与服务契约

关联 issue：#16。本文对应已实现的 MyGo UI、交互状态机和内存 Demo Service；#4 按第 6 节的契约实现真实 Hosts 核心（parser、managed block 写入、备份回滚）。本文中的改写流程全部在内存中模拟，不读写真实 `/etc/hosts`。

## 1. 目标与边界

Hosts Rewrite Workbench 是 #16 的交互验证台：证明新设计的 hosts 改写交互（编辑 → 诊断 → staged 预览 → 应用 → 常驻回执 → 回滚）在 Go/MyGo 中成立。它不是 Hosts-only Alpha（#5），不提供正式 Profile CRUD、导入导出或快照。

设计目标：

- 单一可编辑表面：选中 Profile 即可编辑，取消源项目的只读/编辑模式切换。
- 诊断落到行：解析错误与不同 IP 冲突在编辑器 gutter 行内标注，并在问题面板中与行双向联动。
- 阻断类问题必有可见原因和解决路径，不得只禁用按钮。
- Save（存草稿）与 Apply（改写管理块）仍然显式区分；有未保存草稿时 Apply… 先自动保存，再进入预览和最终确认，顺序及失败原因可见。
- staged 内嵌预览替代模态弹窗：diff、冲突与保留选择、备份说明在同一面板内。
- Apply 回执常驻；失败时 Rollback 是回执区的持久动作，不依赖弹窗存续。
- 存储态（Stored/Modified）与应用态（Applied/Drifted）分离展示。

非目标：

- 不读写真实 `/etc/hosts`，不申请提权，不启动 DNS server/proxy。
- 不实现正式 Profile CRUD、导入导出、快照或系统 Hosts 页面（归 #5）。
- 不实现真实 parser/formatter/合并器（归 #4，但契约语义不得与本文件冲突）。
- 不使用真实用户数据作为语料。

## 2. 源系统交互对照

源项目（mHost v0.4.2，commit `ef1502e`）的对应实现为 `RuleEditor.tsx`、`HostsProfileView.tsx`、`ApplyConfirmDialog.tsx`。本设计明确丢弃的行为：

| 源系统行为 | 本设计替代 |
| --- | --- |
| 默认只读，点 Edit Rules 进入编辑，草稿在 View/Editor 双份维护 | 选中即编辑；草稿存 per-profile 草稿表，切换不丢失 |
| 校验错误列表堆在编辑器下方，与行脱节 | editor 原生 diagnostics 落到行；问题面板点击跳行 |
| different-IP 冲突静默禁用 Confirm | 冲突列出 domain/行/IP，必须选择保留哪个 IP 才能 Apply |
| Enable/Disable 开关同时改 Profile 态并触发改写 | Save 为显式保存；Apply… 是带预览的最终改写入口，必要时先保存草稿；Header 分示 Stored/Modified 与 Applied/Drifted |
| 预览/执行中/成功/失败四态挤一个模态弹窗 | staged 内嵌预览面板（DiffView）+ 常驻回执区 |
| Rollback 只在弹窗存活期间可用 | Rollback 是回执区的持久按钮 |
| 带未保存修改切 Profile 直接丢草稿 | 直接切换且草稿按 Profile 保留；确认只出现在真正退出并可能丢草稿时 |
| 搜索是独立悬浮组件 | editor 原生 FindBar（Cmd-F） |

## 3. 信息架构

单窗口 1120×760（最小 900×620）：

```text
┌──────────────────────────────────────────────────────────────────────┐
│ mHost  Phase 2 · Hosts Rewrite Workbench            [Reload Profiles] │
├──────────────┬───────────────────────────────────────────────────────┤
│ PROFILES     │ Profile 名称  [Stored|Modified] [Applied|Drifted] 规则数│
│  开发环境 ●   │ [Save]                    [Apply…]                   │
│  冲突样本     │ ┌─ 编辑器（gutter 诊断、Cmd-F、行号）────────────────┐ │
│  解析错误样本 │ └────────────────────────────────────────────────────┘ │
│  内置系统 🔒  │ Ln/Col · 行数 · 诊断数                                │
│              │ Problems (n) · 点击跳行                               │
│              │ 常驻回执区（含 Rollback to Backup）                      │
├──────────────┴───────────────────────────────────────────────────────┤
│ Managed block: 当前归属 Profile · drifted 状态                         │
└──────────────────────────────────────────────────────────────────────┘
```

staged 预览状态替换编辑器区域：

```text
┌─ Apply preview · replaces the mHost managed block (simulated) ─────────┐
│ From n lines to m lines · backup created before writing                │
│ [blocker 文案]                                                         │
│ api.demo.test on lines [1 2]:  [Keep 10.0.0.1] [Keep 192.168.1.1]      │
│ ┌─ DiffView：CurrentBlock → MergedBlock ────────────────────────────┐ │
│ └────────────────────────────────────────────────────────────────────┘ │
│ Cancel keeps the current block unchanged.        [Cancel] [Apply Changes]│
└─────────────────────────────────────────────────────────────────────────┘
```

## 4. 核心交互

### 4.1 加载与选择

1. 初始空态只显示安全文案和 `Load Demo Profiles`；不访问任何真实数据。
2. 加载后自动选中第一个 Profile，编辑器直接可编辑（单一可编辑表面）。
3. Protected Profile 编辑器只读（🔒 标记）。
4. `Reload Profiles` 重新读取列表与应用态，保留未保存草稿。

### 4.2 编辑与诊断

1. 编辑直接写入当前 Profile 的草稿；Header 显示 `Modified`，侧栏对应项标记 ●。
2. 诊断在 Buffer Version 变化时重算（沿用 Phase 0 模式）：非法 IP、同域不同 IP 为 Error（阻断类），同 IP 重复为 Warning。
3. 底部问题面板列出全部诊断；点击一条将光标跳到对应行（GoTo）。

### 4.3 保存

1. `Save` 仅写 Profile 存储（Demo 中为内存），成功后回到 `Stored`，侧栏 ● 消失。
2. 保存按 Load 时的 Revision 乐观并发；冲突返回 stale 错误。
3. 保存会使已 staged 的预览失效（服务侧 Revision 变化）。

### 4.4 改写（Apply）

1. `Apply…` 生成计划（Plan ID + Base Revision）。当前草稿已修改时，入口先执行 Save；保存失败留在编辑态并显示原因，保存成功后继续生成计划。
2. staged 预览面板展示：管理块前后行数、DiffView（CurrentBlock → MergedBlock）、blocker 文案、冲突保留选择、备份说明。
3. 存在 blocker（解析错误）时 `Apply Changes` 禁用，原因："Fix n parse error(s) in the editor first"。
4. 存在冲突时必须为每个冲突域选择保留 IP，否则禁用，原因："Choose which IP to keep for ..."。选择只影响改写结果，不改编辑器内容；用户也可以回编辑器修改后重新预览。
5. 草稿在预览后被编辑 → 计划标记 Stale，必须重新预览（本地标记 + 服务侧 Base Revision 校验双保险）。
6. `Cancel` 默认操作，关闭预览不改写系统 Hosts；若刚才由 Apply… 自动保存了草稿，回执区明确说明“Profile 已保存，系统 Hosts 未修改”。
7. Apply 执行期间进入 `applying`：不可取消，Cmd-Q/Tray Quit 延迟到安全边界；失败则取消延迟退出并展示回执。

### 4.5 回执与回滚

1. Apply 回执常驻工作区：逐项显示 backup / managed_block / verification 的 written/failed/skipped 与 readback 状态。
2. 全部成功且读回验证通过显示 "Apply verified"；有失败显示 "Apply incomplete · n failed · backup ... available"。
3. 只要回执带 RollbackRef，回执区提供 `Rollback to Backup`；回滚结果同样有逐项读回回执。
4. 回执保留到下一次操作。

### 4.6 存储态与应用态

- Header 显示当前 Profile 的 `Stored`/`Modified`。
- 当管理块当前归属所选 Profile 时显示 `Applied`；Demo 服务可标记 drift，此时显示 `Drifted`。
- 底部状态栏常显管理块归属与 drift 状态。

## 5. 状态模型与并发

| Phase | 可用操作 | 禁止行为 | Quit |
| --- | --- | --- | --- |
| idle | 选择 Profile、编辑、Save、Apply…、Rollback | 并发第二个操作 | 直接退出 |
| loading / previewing | Cancel（通过退出或重新操作取消） | 写入 | 取消当前操作后退出 |
| applying | 仅查看进度 | 一切新操作、切换 Profile | 登记 Quit，等待安全边界 |

- 每个异步操作带单调递增 Operation ID + context；过期结果不合并。
- 写入操作（Save/Apply/Rollback）一旦开始不可取消。
- 写入中退出：阻止立即退出，操作到达安全边界后继续此前登记的 Quit；操作失败则取消延迟退出，保持应用打开展示回执。
- UI Model 不启动 goroutine；点击处理只更新状态，I/O 在 worker 中执行，经 Dispatch 回主线程合并。

## 6. UI—应用服务契约

契约定义在 `internal/app/hosts.go`（`HostsService`），Demo 实现在 `internal/app/demo/hosts.go`，`internal/desktop` 只依赖该层。#4 的真实实现不得削弱这些语义。

```go
type HostsService interface {
    ListProfiles(ctx context.Context) ([]HostsProfile, error)
    LoadProfile(ctx context.Context, profileID string) (HostsProfileDetail, error)
    SaveProfile(ctx context.Context, profileID string, content string, baseRevision uint64) (HostsProfileDetail, error)
    PreviewApply(ctx context.Context, profileID string, resolutions map[string]string) (ApplyPlan, error)
    Apply(ctx context.Context, planID string, resolutions map[string]string) (ApplyReceipt, error)
    Rollback(ctx context.Context, backupID string) (ApplyReceipt, error)
    ListApplyBackups(ctx context.Context) ([]ApplyBackupRef, error)
    AppliedState(ctx context.Context) (AppliedState, error)
}
```

调用约束：

- `SaveProfile` 的 `baseRevision` 必须与 Load 时一致，否则 `ErrStaleOperation`。
- `PreviewApply` 与 `Apply` 都接受 `resolutions`（domain → 保留的 IP）。预览按待选选择渲染 MergedBlock，未解决的冲突按保留首个 IP 渲染；用户每做一次保留选择，UI 重新预览，使 staged diff 与最终写入一致。
- `PreviewApply` 返回的计划携带 `BaseRevision`；Profile Revision 变化后旧 Plan ID 必须返回 `ErrStaleOperation`。
- `Apply` 要求 `resolutions` 覆盖全部冲突域，缺失或非法返回 `ErrorValidation`；存在 blocker 时同样拒绝。
- `Apply`/`Rollback` 即使返回 error 也返回非零回执，表达已完成的原子步骤；调用方不得只凭 error 推断状态。
- 每次 Apply 必须先创建备份；备份失败不得改写管理块。
- `AppliedState` 返回管理块当前归属与 drift 标记。

相对 #16 issue 正文的契约有两处微调，均已在 #16 评论同步：`Apply` 与 `PreviewApply` 均增加 `resolutions map[string]string` 参数（冲突的保留选择同时驱动预览渲染与最终写入）。

## 7. Demo 场景

内置四个合成 Profile 覆盖源项目解析语义形态：

| Profile | 覆盖形态 |
| --- | --- |
| 开发环境 | 注释行、行尾注释、多域名、禁用规则（不写入管理块） |
| 冲突样本 | 同域不同 IP（阻断冲突）、同 IP 重复（静默合并） |
| 解析错误样本 | 非法 IP 行（blocker） |
| 内置系统 | Protected + 初始管理块归属 |

故障注入（供测试与手动验证）：

- `FailNextApply()`：备份成功、managed_block 写入失败，回执带 RollbackRef。
- `FailNextReadback()`：写入成功、读回验证失败。
- `SetDrift(bool)`：管理块标记为外部漂移。

## 8. 视觉与可访问性

- 沿用 `c.Theme()`，明暗随系统；阻断用 Error 色 + 文字，不只改 Badge 颜色。
- 编辑器为系统等宽字体；诊断在 gutter、下划线、hover 与滚动条标记多处可见。
- 所有按钮有明确动词；危险操作（改写系统块）的确认面板默认操作为 Cancel。
- 表格与问题面板支持键盘操作；长文案换行不依赖 hover。

## 9. 验收追踪

| #16 验收要求 | 本文对应 |
| --- | --- |
| 无真实 /etc/hosts 写入、无提权 | 第 1 节、Demo 实现 |
| 阻断类问题有原因和解决路径 | 4.2、4.4 |
| 行内诊断与问题面板双向联动 | 4.2 |
| 回执常驻、失败可回滚、回滚有读回验证 | 4.5 |
| 状态机覆盖 + 过期预览失效 | 4.4、第 5 节 |
| 草稿跨切换保留、脏切换确认 | 4.1、4.2 |
| Operation ID stale 丢弃、写不可取消、Quit 门禁、默认 Cancel | 第 5 节 |
| 源解析语义形态覆盖 | 第 7 节 |

无窗口测试场景见 `internal/desktop/hosts_workbench_test.go`；手动验证步骤见 [Hosts Rewrite Workbench 验证计划](hosts-rewrite-ui-validation.md)。
