# Phase 2 Hosts Rewrite Workbench 验证计划

关联 issue：#16。本计划的验证对象为内存 Demo Service 驱动的 Hosts Rewrite Workbench：交互闭环、状态机语义与安全文案。全程不读写真实 `/etc/hosts`，无提权操作。

## 1. 自动化验证

无窗口 UI 测试与状态机/服务测试：

```sh
go test ./internal/desktop/ -run TestHostsWorkbench -v
go test ./internal/app/demo/ -run TestHostsService -v
```

覆盖场景：

| 场景 | 测试 |
| --- | --- |
| 初始空态安全文案、Demo 徽标 | TestHostsWorkbenchInitialViewShowsSafetyCopy |
| 加载后自动选中、编辑器内容、初始应用态 | TestHostsWorkbenchLoadSelectsFirstProfile |
| 编辑 → Modified → Save → Stored | TestHostsWorkbenchEditSaveMarksStoredState |
| 脏切换确认（Stay/Switch）与草稿保留 | TestHostsWorkbenchDirtySwitchRequiresConfirmation / TestHostsWorkbenchDraftPreservedAcrossSwitch / TestHostsWorkbenchDirtySwitchDialogFromView |
| 行内诊断 + 问题面板点击跳行 | TestHostsWorkbenchViewShowsInlineDiagnosticsAndProblemsPanel |
| 预览后编辑使计划失效、原因可见 | TestHostsWorkbenchEditingInvalidatesStagedPlan |
| 干净 Profile 完整 Apply 流程 | TestHostsWorkbenchApplyCleanProfile |
| 冲突阻断 → 选择保留 IP → 放行 | TestHostsWorkbenchConflictsBlockApplyUntilResolved |
| blocker 禁用 Apply 且原因可见 | TestHostsWorkbenchBlockersPreventStagedApply |
| 写入失败回执 + Rollback 恢复 | TestHostsWorkbenchApplyFailureOffersRollback / TestHostsWorkbenchFailureReceiptOffersRollback |
| 写入中 Quit 门禁与延迟退出续跑 | TestHostsWorkbenchQuitGatedDuringApply |
| staged 预览面板、冲突选择按钮（视图层） | TestHostsWorkbenchStagedPreviewConflictsAndResolution |
| 服务层：stale 拒绝、故障注入、drift | TestHostsService* |

## 2. 手动验证（M 系列 Mac）

从 GitHub Actions 下载 Phase 2 构建 artifact（`mhost-phase-2-darwin-arm64`），按 README 移除 quarantine 后启动。

预期窗口标题：mHost Phase 2 · Hosts Rewrite Workbench。

### 2.1 基础流程

1. 空态显示 "No profile is loaded until you start the demo" 与 "Demo service · no /etc/hosts writes" 徽标。
2. 点击 Load Demo Profiles：左侧出现四个 Profile；自动选中"开发环境"，编辑器显示规则内容，Header 显示 Stored、规则数、底部显示 Managed block: 内置系统。
3. 编辑器内直接输入一行 `127.0.0.1 new.local`：Header 变 Modified，侧栏"开发环境"出现 ●，工具行显示 "Unsaved changes · Apply will save this profile first"。
4. 点击 Save：回到 Stored，● 消失；也可在下一步由 Apply… 自动完成保存。
5. 点击 Apply…：编辑器区域切换为预览面板；若刚才未手动保存，会先保存当前草稿，再展示 DiffView 中管理块从 `mhost.internal` 到开发环境规则的变化，底部 Cancel / Apply Changes。
6. 点击 Apply Changes：预览关闭，回执区显示 "Apply verified · 1 checked" 与逐项结果；底部状态变为 Managed block: 开发环境；Header 出现 Applied 徽标。
7. 验证 `/etc/hosts` 未被修改（Demo 承诺）：`cat /etc/hosts | grep mhost-managed` 应无输出（除非此前有其他来源）。

### 2.2 冲突与阻断

1. 选择"冲突样本"：gutter 在第 1、2 行显示错误标记；底部 Problems 显示 4 条诊断。
2. 点击问题面板中 "Line 2: ..."：编辑器光标跳到第 2 行。
3. 点击 Apply…：预览面板列出冲突 `api.demo.test on lines [1 2]` 与两个 Keep 按钮；Apply Changes 禁用且原因可见。
4. 点击 Keep 10.0.0.1：Apply Changes 放行，点击后应用成功；同 IP 重复的 shared.demo.test 只保留一条（可在 DiffView 中确认）。
5. 选择"解析错误样本"：Apply… 后预览面板显示 blocker 文案，Apply Changes 禁用，原因 "Fix 1 parse error(s) in the editor first"。

### 2.3 草稿保护

1. 在"开发环境"输入任意内容（不保存），直接点击"冲突样本"：无切换确认，立即切到冲突样本。
2. 切回"开发环境"：草稿内容仍在，Header 显示 Modified，侧栏保留 ●。
3. 真正退出宿主应用且仍有未保存草稿时，应显示 Save All / Quit Without Saving / Cancel；取消后草稿不丢失。

### 2.4 失败与回滚（需调试手段注入）

Demo 服务的故障注入默认不暴露 UI 开关；手动验证可临时在代码中调用 `FailNextApply()`/`FailNextReadback()`/`SetDrift(true)` 构建，或参考服务层测试的断言。预期行为：

1. Apply 失败：回执区显示 "Apply incomplete · 1 failed · backup ... available" 与错误横幅；管理块归属不变；回执区出现 Rollback to Backup。
2. 点击 Rollback to Backup：回执变为 "Apply verified"，管理块归属恢复。
3. SetDrift(true) 后：底部状态与所选 Profile 的 Header 显示 drifted/Drifted。

### 2.5 退出语义

1. Apply 执行中按 Cmd-Q：不立即退出；操作完成后应用退出。
2. 普通关闭窗口：隐藏到托盘，Dock 点击恢复后状态保留（包括未保存草稿与 staged 预览）。

## 3. 通过标准

- 2.1–2.3、2.5 全部符合预期。
- 全程 `/etc/hosts` 无 mHost 管理块写入、无提权弹窗。
- VoiceOver 能读出窗口标题、Profile 列表、操作按钮、问题面板行与回执摘要。
