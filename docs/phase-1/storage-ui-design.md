# Phase 1 Storage Workbench UI 与服务契约

关联 issue：#13。功能实现由 #14 按本文契约完成；本文不包含界面或存储实现。

## 1. 目标与边界

Storage Workbench 是 Phase 1 的开发验证工具，用来证明 Go/MyGo 应用能够安全读取、迁移和写入源项目兼容数据。它不是 Hosts-only Alpha，也不提供正式 Profile 管理体验。

设计目标：

- 所有读取都由用户显式选择目录触发，不在启动时扫描真实用户目录。
- 源目录始终只读；迁移、验证写入、备份恢复只发生在独立工作副本。
- 读取结果、局部损坏、迁移差异和每次写入结果均可见，不以静默降级代替错误反馈。
- UI 只依赖应用服务 DTO，不直接访问文件、路径拼接或存储内部类型。
- 复用 Phase 0 已验证的窗口生命周期和无窗口 UI 测试能力。

非目标：

- 不实现完整 Profile CRUD、Hosts 文本编辑、正式导入导出或 #5 的产品导航与视觉系统。
- 不实现 Hosts Apply/Rollback，不写 `/etc/hosts`。
- 不启动 DNS server/proxy，不读取或修改系统 DNS。
- 不实现广告规则刷新、白名单编辑或自动更新。
- 不使用真实用户数据作为开发或验收语料。

## 2. Phase 0 原型处置

### 2.1 复用

| 现有能力 | 位置 | Phase 1 用法 |
| --- | --- | --- |
| App/Edit/Window 菜单与 Cmd-Q | `internal/desktop/app.go` | 保留标准菜单和退出入口 |
| 普通关闭隐藏、Dock 激活恢复 | `App.shouldHideOnClose`、`OnActivate` | 保留，隐藏不销毁 Workbench 状态 |
| Tray Open/Quit | `App.ready`、`showWindow` | 保留，Quit 增加写入中退出门禁 |
| 幂等清理 | `App.Cleanup` | 保留 `sync.Once` 语义，清理 Phase 1 应用服务 |
| 后台任务回主线程更新 | `Window.Update` | 所有异步结果统一在主线程合并 |
| 无窗口交互测试 | `internal/desktop/model_test.go` | 复用 `ui.NewTester`、按钮点击、隐藏恢复和文本断言 |
| 逐文件错误模型 | `internal/legacyprobe` | 仅作为行为参考，不让 UI 直接依赖 probe |

### 2.2 从 Phase 1 应用组装中移除

- `DNS Lab`、DNS 状态和 DNS manager/server 依赖。
- 启动时监听 `127.0.0.1:1053`、执行 DNS Recover 的副作用。
- 内存 Hosts 编辑器、100k 样本按钮和 Settings Placeholder。
- Phase 0 Sidebar 与三页面原型结构。
- `mHost Phase 0` 的窗口标题和应用标识应在正式 Phase 1 构建任务中单独调整；本设计不要求复用旧标识。

Phase 0 原型代码是否保留为独立技术样本由 #14 的工程整理决定，但 Phase 1 主入口不得继续产生上述 DNS 副作用。

## 3. 信息架构

### 3.1 单窗口结构

默认窗口建议为 1120×760，最小 900×620。窗口只有一个任务上下文，不使用正式产品 Sidebar。

```text
┌──────────────────────────────────────────────────────────────────────────┐
│ mHost  Phase 1 · Storage Workbench                  [Open Source…]        │
├──────────────────────────────────────────────────────────────────────────┤
│ SOURCE  /full/path/to/source                              [Read only]       │
│ WORK    /full/path/to/working-copy                     [Working copy]       │
│ Status icon + persistent operation/result summary             [Details…]    │
├──────────────────────────────────────────────────────────────────────────┤
│ [Overview] [Profiles] [Snapshots] [Settings & AdBlock] [Issues (n)]        │
│ ┌──────────────────────────────────────────────────────────────────────┐ │
│ │ Selected tab content: summary cards, tables and read-only details    │ │
│ │                                                                      │ │
│ └──────────────────────────────────────────────────────────────────────┘ │
├──────────────────────────────────────────────────────────────────────────┤
│ Contextual safety text                    [Create Working Copy…] /         │
│                                           [Preview Migration…] /           │
│                                           [Preview Validation Changes…]    │
└──────────────────────────────────────────────────────────────────────────┘
```

规则：

- 顶部始终显示当前模式；未选择目录时不显示伪造路径或空表格。
- `SOURCE` 与 `WORK` 不同时出现两个可编辑目标。源模式只有 `SOURCE`，工作副本模式同时保留来源和当前写入目标。
- 根路径完整可见；空间不足时允许换行，并提供 `Copy Path`。不能只显示无法区分的尾段。
- Tabs 只组织只读验证结果，不构成正式产品导航。
- 底部只显示当前模式可执行的主操作，避免同时出现多个含义相近的写入按钮。
- 操作结果保留到下一次操作或用户显式关闭，不用短暂 toast 作为唯一回执。

### 3.2 初始空状态

```text
                 Storage compatibility validation

       Inspect synthetic mHost data without changing the source.
       Migration and write checks run only in a separate working copy.

                         [Open Source…]

       No directory is accessed until you choose one.
```

- 初始焦点位于 `Open Source…`。
- 不默认打开上次目录，不请求真实 Application Support 目录。
- 原生目录选择器使用 `Directory: true`、`CreateDirectories: true`，标题为 `Choose Synthetic Source Data`。

### 3.3 各 Tab 内容

| Tab | 内容 | 操作 |
| --- | --- | --- |
| Overview | Manifest 版本、应用版本、更新时间、Profile/Snapshot/Issue 数量、DNS 字段兼容结果 | Reload、Copy Summary |
| Profiles | Name、Mode、Rules、Enabled、Protected、相对路径；选中后显示只读领域字段和 DNS 兼容信息 | 无 CRUD |
| Snapshots | Name、Created、Profiles、索引状态、pending 状态、相对路径 | Backups/Recovery 仅在工作副本上下文出现 |
| Settings & AdBlock | 新版设置迁移状态及来源；AdBlock 开关、来源数、白名单数和配置问题 | 不编辑偏好或规则 |
| Issues | Severity、相对路径、错误类别、摘要；选中后显示技术详情与可用恢复动作 | Copy Details、Retry/Recover（按错误类型） |

`fixtures/storage/v2-full/settings.json` 只能作为 Phase 0 合成数据展示。UI 不得标注为“旧版 settings.json”，也不得暗示源项目已有该磁盘契约。设置区域的正式来源和迁移状态以 #14 确认的 localStorage 语义及新版原生设置格式为准。

## 4. 核心交互

### 4.1 选择并读取源目录

1. 点击 `Open Source…`，进入 `selecting`；重复点击不打开第二个选择器。
2. 取消选择：无错误提示，恢复之前的状态和焦点。
3. 确认目录：进入 `loading`，显示 `Inspecting source…` 和 `Cancel`。
4. 服务校验目录并返回 Workspace 与 StorageSummary。
5. 成功：显示 `Read only`、版本和各数据概况；有局部问题时显示 `Loaded with n issues`，有效数据仍可查看。
6. 根目录不可读或不是目录：显示失败页，提供 `Retry` 与 `Choose Another Source…`，不保留伪造摘要。

重新选择或 Reload 会取消当前读取并创建新操作 ID。旧请求即使后返回，也不能覆盖新目录的结果。

### 4.2 创建工作副本

1. 源模式点击 `Create Working Copy…`。
2. 原生选择器标题为 `Choose an Empty Working-Copy Directory`，允许新建目录。
3. 确认前显示目标路径和说明：`The source remains read only. Only this working copy can be changed.`
4. 服务拒绝以下目标：与源相同、位于源内部、包含源、非空目录、无法验证路径边界或存在符号链接逃逸。
5. 创建期间显示 `Creating working copy…`，禁止更换源和启动其他写操作。
6. 成功后切换到 `Working copy`，重新 Inspect 并显示副本版本；失败则保留源模式，报告目标路径和可重试性。

仓库 fixture 可以作为只读源，但永远不能作为工作目标。

### 4.3 v1→v2 迁移

1. 工作副本为 v1 时，底部主操作为 `Preview Migration…`，不自动迁移。
2. 预览弹窗显示源版本、目标版本、逐类变更、保留字段、会生成/更新的文件和阻塞项。
3. `Cancel` 是默认按钮；取消只关闭预览，不改变副本。
4. 存在阻塞项时禁用 `Apply Migration`，用户可查看 Issues 或重新创建副本。
5. 点击 `Apply Migration` 后按 Plan ID 和 Base Revision 执行；副本发生变化时旧计划失效，必须重新预览。
6. 每项写入先备份、原子替换并读回验证。完成后显示迁移回执；任何失败都显示为 `Migration incomplete` 或 `Migration failed`，不得显示全局成功。
7. 可选损坏组件若不能迁移，必须在计划和回执中逐项列出并保留原文件；Manifest/Profile 等语义不明确的数据属于阻塞项，不能静默跳过。

### 4.4 验证变更与保存

本工具不提供任意 JSON 编辑。#14 通过服务生成覆盖 Manifest、Profile、设置和快照存储的最小验证变更集，UI 只展示服务给出的领域级摘要。

1. v2 工作副本点击 `Preview Validation Changes…`。
2. 弹窗列出每类数据的变更摘要、目标相对路径和备份要求，不包含系统 Hosts 或 DNS 操作。
3. 默认操作为 `Cancel`；确认按钮为 `Save to Working Copy`。
4. 保存使用 Preview ID 和 Base Revision，过期预览必须重新生成。
5. 写入期间禁用所有写操作，显示持久状态 `Saving validation changes…`。
6. 回执逐项显示 `Written`、`Unchanged`、`Skipped` 或 `Failed`，以及备份、读回验证和错误信息。
7. 多项写入不暗示跨文件事务；只有全部必需项成功且读回验证通过时才显示 `All validation changes verified`。部分完成必须明确列出已写入和未写入项。

### 4.5 备份与恢复

- 每次已有文件的变更都必须先产生可定位的 BackupRef；备份失败时不得继续覆盖目标。
- `Backups…` 只列出当前工作副本内由服务登记的备份，包括实体、相对路径、创建时间和来源操作。
- 恢复前显示当前文件与备份的标识、时间和目标路径，默认 `Cancel`，确认按钮为 `Restore Backup`。
- 恢复当前文件前先生成安全备份，恢复后重新读取并验证。
- 只有读回验证成功才显示 `Restore verified`；失败时显示 `Preserve status unknown` 或实际状态，不能笼统显示“已恢复”。
- 此恢复只针对存储文件备份，与 #4 的 `/etc/hosts` Rollback 无关。

### 4.6 损坏与错误恢复

- 局部损坏：保留有效 Tab 内容，Issues 按相对路径稳定排序；不自动修复、删除或重写损坏文件。
- 根目录错误：整页错误状态，保留 `Retry` 和重新选择入口。
- 写入错误：保留最后一份已验证摘要并标记 `May be stale`，操作完成后允许 Reload。
- 可恢复错误提供明确动作，如 `Retry`、`Choose Another Directory`、`Restore Backup`；不可恢复错误只提供查看详情和安全退出，不展示无效按钮。

## 5. 状态模型与并发

### 5.1 状态划分

主 Phase 与瞬时结果分开保存：`succeeded` 不是独立主 Phase，而是 `ready` 下的 WriteReceipt；失败后也尽量回到可查看数据的 `ready` 并展示 UIError。

| Phase | 页面与可用操作 | 禁止行为 | Quit |
| --- | --- | --- | --- |
| empty | 初始说明、Open Source | Reload、写入 | 直接清理退出 |
| selecting | 原生目录选择器 | 第二个选择器、其他操作 | 取消选择后退出 |
| loading | Spinner、Cancel；保留旧数据时标记 stale | 写入、重复读取 | 取消读取后退出 |
| ready/read-only | 查看、Reload、Create Working Copy | 迁移、保存、恢复 | 直接清理退出 |
| copying | 当前路径、创建副本状态 | 更换源、读取切换、其他写入 | 登记 Quit，等待安全边界 |
| ready/writable | 查看、Reload、迁移或验证变更、Backups | 写源目录 | 直接清理退出 |
| previewing | 生成迁移/变更预览、Cancel | 写入、重复预览 | 取消后退出 |
| writing | 当前操作、不可误触的进度状态 | 所有新写入、切换 Workspace | 登记 Quit，等待安全边界 |
| failed | 错误摘要、详情、有效旧数据（若有）、恢复动作 | 无依据的重试或成功声明 | 无写入时直接退出；否则先等待 |

UI Model 至少保存：`phase`、当前 Workspace、Summary、选中 Tab、操作 ID、Preview、Receipt、UIError 和 `quitRequested`。视图只根据这些状态渲染，不在 View 中启动 goroutine。

### 5.2 操作规则

- 同一时刻至多一个会改变 Workspace 的操作；所有写操作串行。
- 每个异步操作获得单调递增 Operation ID，并携带 `context.Context`。
- 点击处理在 MyGo 主线程只更新状态并调用 controller；实际 I/O 在 goroutine 中执行。
- 完成后通过 `Window.Update` 回主线程；只有 Operation ID 与当前 ID 一致才合并结果。
- `context.Canceled` 是取消，不显示为红色失败；原生 picker 的取消也不构成错误。
- 读取和预览可取消。复制和写入一旦开始，UI 不提供“取消写入”；应用退出也不能绕过服务安全边界。
- 普通 Close 始终可 Hide，后台状态保留；恢复窗口后显示最新操作状态。
- 写入中 Cmd-Q/Tray Quit：阻止立即退出，显示 `Quit after the current storage operation` 和 `Cancel Quit`。操作结束并产生明确回执后再继续此前登记的 Quit。
- `OnQuitCanceled` 清除 `quitRequested`。多个退出入口不得并行执行清理或重复退出。

## 6. UI—应用服务契约

以下为 #14 实现的逻辑契约，建议放在应用层（如 `internal/app`），`internal/desktop` 只依赖该层。类型可分为多个 Go 文件，但语义和调用顺序不得削弱。

### 6.1 DirectoryPicker

```go
type DirectoryPicker interface {
    PickDirectory(ctx context.Context, opts DirectoryOptions) (path string, err error)
}

type DirectoryOptions struct {
    Title             string
    DefaultPath       string
    ButtonLabel       string
    Message           string
    CreateDirectories bool
}
```

- macOS 实现使用 `mygo.Dialog.Open`，且 `Directory: true`。
- 取消返回 `ErrPickerCancelled`；其他错误按 `picker_failed` 映射。
- 原生调用可阻塞，controller 在 goroutine 中调用，结果通过 `Window.Update` 合并。
- 无窗口测试使用 fake picker，不触发原生 UI。

### 6.2 StorageService

```go
type StorageService interface {
    Inspect(ctx context.Context, sourceRoot string) (Workspace, error)
    CreateWorkspace(ctx context.Context, sourceID, destinationRoot string) (Workspace, error)

    PreviewMigration(ctx context.Context, workspaceID string) (MigrationPlan, error)
    ApplyMigration(ctx context.Context, workspaceID, planID string) (WriteReceipt, error)

    PreviewValidationChange(ctx context.Context, workspaceID string) (ChangePreview, error)
    SaveValidationChange(ctx context.Context, workspaceID, previewID string) (WriteReceipt, error)

    ListBackups(ctx context.Context, workspaceID string) ([]BackupRef, error)
    RestoreBackup(ctx context.Context, workspaceID, backupID string) (WriteReceipt, error)
}
```

调用约束：

- `Inspect` 返回的 Workspace 为只读；`CreateWorkspace` 返回的 Workspace 为可写。
- 后续方法只接受不透明 Workspace ID，不接受 UI 自拼文件路径。
- `CreateWorkspace` 必须重新验证源状态和目录边界，不能信任 picker 或先前 UI 校验。
- Preview 与 Apply/Save 通过 ID 和 Base Revision 配对；过期、重复或不属于当前 Workspace 的 ID 返回 `ErrorKindStaleOperation`。
- Apply/Save/Restore 即使返回非 nil error，也可返回非零 WriteReceipt，用于表达已完成的原子步骤；调用方不得只凭 error 推断磁盘状态。
- 所有成功写回执都必须包含读回验证结果。备份失败、写入失败和读回不一致是不同错误类别。
- 服务负责 owner-only 权限、原子写、备份、路径约束和领域校验；UI 不重复实现这些安全规则。

### 6.3 DTO

```go
type AccessMode string
const (
    AccessReadOnly AccessMode = "read_only"
    AccessWritable AccessMode = "writable"
)

type Workspace struct {
    ID             string
    Root           string
    SourceRoot     string
    Access         AccessMode
    StorageVersion int
    Revision       uint64
    Summary        StorageSummary
}

type StorageSummary struct {
    Manifest *ManifestSummary
    Profiles []ProfileSummary
    Snapshots []SnapshotSummary
    Settings SettingsSummary
    AdBlock  *AdBlockSummary
    Issues   []StorageIssue
}

type StorageIssue struct {
    Code        string
    Severity    IssueSeverity
    Path        string // 相对于 Workspace.Root；根错误为 "."
    Message     string
    Detail      string
    Recoverable bool
}

type MigrationPlan struct {
    ID           string
    WorkspaceID  string
    BaseRevision uint64
    FromVersion  int
    ToVersion    int
    Changes      []ChangeItem
    Blockers     []StorageIssue
}

type ChangePreview struct {
    ID           string
    WorkspaceID  string
    BaseRevision uint64
    Changes      []ChangeItem
}

type ChangeItem struct {
    Entity       string
    Path         string
    Summary      string
    RequiresBackup bool
}

type BackupRef struct {
    ID          string
    WorkspaceID string
    Entity      string
    Path        string
    CreatedAt   time.Time
    OperationID string
}

type WriteReceipt struct {
    OperationID string
    WorkspaceID string
    BaseRevision uint64
    Revision     uint64
    Results      []WriteResult
}

type WriteResult struct {
    Entity           string
    Path             string
    Status           WriteStatus // written/unchanged/skipped/failed
    Backup           *BackupRef
    ReadbackVerified bool
    Preservation     PreservationStatus // preserved/restored/unknown/not_applicable
    Issue            *StorageIssue
}
```

Manifest/Profile/Snapshot/Settings/AdBlock Summary 只包含验证界面需要的只读领域字段，不暴露 `*os.File`、文件系统接口、存储锁或具体 Repository。集合使用非 nil 空切片，便于 UI 区分“零项”和“未加载”。

### 6.4 错误映射

服务错误通过分类错误或 `errors.As` 转换，不通过匹配错误字符串判断。controller 将错误转换为以下展示模型；`Path` 优先使用根目录内相对路径，根目录错误使用完整根路径。

```go
type UIError struct {
    Kind         UIErrorKind
    Operation    string
    Path         string
    Message      string
    Detail       string
    Retryable    bool
    Preservation PreservationStatus
}
```

| UIErrorKind | 典型原因 | 主文案与动作 |
| --- | --- | --- |
| picker_cancelled | 用户取消 | 不显示错误，恢复原状态 |
| picker_failed | 原生目录选择器失败 | 显示可重试摘要，保留原状态 |
| invalid_path | 非目录、非空目标、相同/嵌套路径、符号链接逃逸 | 说明约束，Choose Another Directory |
| permission_denied | 源不可读、目标不可写、owner-only 校验失败 | 指出根路径/相对路径，重试或换目录 |
| not_found | 文件或 Workspace 消失 | Reload 或重新选择 |
| invalid_json | JSON 损坏 | Issues 中定位文件，不自动覆盖 |
| validation_failed | 缺字段、错类型、UUID/领域校验失败 | 显示字段与技术详情，按可恢复性给动作 |
| migration_blocked | 不支持的版本或语义不明确 | 禁用 Apply，查看 Blockers |
| stale_operation | Revision/Preview/Operation 已过期 | 丢弃结果，重新预览或 Reload |
| backup_failed | 无法创建或验证备份 | 停止该文件写入，显示 Preservation 状态 |
| write_failed | 临时文件、同步或原子替换失败 | 不显示成功，提供回执与恢复入口 |
| readback_mismatch | 写入后领域模型不一致 | 标记未验证，禁止全局成功 |
| canceled | context 取消 | 恢复 ready/empty，不作为故障 |
| internal | 未分类错误 | 通用摘要 + 可复制详情，不暴露堆栈作为唯一说明 |

技术详情可包含 Go error 链和绝对根路径，但文件路径优先以根目录内相对路径展示。不得把内部域名、完整配置或真实用户数据写入测试日志。

## 7. 轻量视觉与可访问性

- 使用 `c.Theme()` 的 Surface、Border、Text、TextMuted 和状态色，支持系统明暗色，不硬编码旧版配色。
- 采用 8pt 基础间距：页面 24、区块 16、同组控件 8；标题 24/20、正文 13、辅助文字 11–12，路径和技术详情使用等宽字体。
- 成功、警告、失败均使用“图标/文字 + 颜色”，例如 `Verified`、`Loaded with issues`、`Write failed`，不能只改变 Badge 颜色。
- Spinner 必须伴随操作名称；不确定进度不伪造百分比。
- 表格支持键盘选择，Issues 可水平滚动；长内容在详情区域换行，不依赖 hover 才能读取。
- 所有按钮有明确动词。Escape 始终取消非写入弹窗；写确认弹窗默认焦点为 Cancel，不设置写操作快捷键。
- 弹窗关闭后焦点返回触发按钮；错误出现后焦点移动到错误摘要，技术详情不自动抢焦点。
- 900×620 下保留主操作和状态，Tabs 可横向滚动，不遮挡路径或错误。
- 真机检查 VoiceOver 能读出窗口标题、模式、操作按钮、表格行和错误摘要。

## 8. 验收追踪

| #13 验收要求 | 本文对应部分 |
| --- | --- |
| 覆盖读取、迁移、写入、备份和损坏处理 | 第 3、4、6 节 |
| 写入仅在显式选择的独立副本，路径清晰 | 第 1、3、4.2、6.2 节 |
| UI 契约不直接访问存储内部 | 第 6 节 |
| 隐藏、恢复、退出与写入状态明确 | 第 5 节 |
| 设计评审后才集成实现 | 本文评审合并后由 #14 实施 |

无窗口测试场景与真机验收步骤见 [Storage Workbench 验证计划](storage-ui-validation.md)。
