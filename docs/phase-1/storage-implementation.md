# Phase 1 存储实现与迁移策略

关联 issue：#14。本文记录 `internal/storage` 的正式磁盘实现：存储布局、读写路径、v1→v2 迁移语义、设置迁移策略与安全性质。#13 的 [UI 与服务契约](storage-ui-design.md)仍然有效，本文是其存储侧的落地说明。

## 1. 包结构

| 文件 | 职责 |
| --- | --- |
| `model.go` | 正式领域模型（Manifest/Profile/Snapshot/AdBlock/Settings），v1/v2 兼容解析、语法错误与校验错误分类（`ModelError` / `ClassifyModelError`） |
| `volume.go` | `Volume` 文件系统抽象、`OSVolume` 真实实现、`FaultyVolume` 故障注入；原子写（临时文件 + fsync + rename） |
| `store.go` | 目录扫描（逐文件问题局部上报）、工作副本树复制、目标路径边界校验、备份登记（`backups/` + `backups/index.json`） |
| `service.go` | `app.StorageService` 实现：Workspace 登记、Preview/Apply 配对、Revision 递增、回执生成 |

Phase 0 的 `internal/legacyprobe` 保持独立，作为兼容性差分测试的对照实现（`compatibility_test.go` 逐 fixture 比对两边解析出的领域模型）。

## 2. 存储布局

所有路径相对 Workspace.Root：

```text
manifest.json              # 存储版本（1 或 2）
profiles/hosts/<uuid>.json
profiles/dns/<uuid>.json
snapshots/index.json       # 快照索引
snapshots/<uuid>.json
adblock.json               # 可选
settings.json              # 可选，仅 v2 原生格式
backups/<backup-id>.json   # 备份内容（原始字节）
backups/index.json         # 备份登记
```

权限约定：目录 `0700`、文件 `0600`（owner-only），工作副本复制时统一收紧到该权限。

## 3. 读取语义

- `Inspect`/`Refresh` 只读。manifest 缺失、损坏或版本不受支持（非 1/2）是**致命错误**——无法判定存储版本；Profile/快照/广告/设置的问题以 `StorageIssue` 局部上报，不阻断有效数据（「Loaded with n issues」）。
- 解析不发明数据：v1 manifest 没有 `dns_enabled`/`original_dns`，读取结果就是 nil，默认值只在迁移时物化。
- `OriginalDNS` 兼容两种历史形态：v2 tagged object 与 v1 legacy 数组（`["Empty"]` 表示 DHCP 空配置；回环/未指定地址会被过滤）。
- 快照索引引用了不存在的快照文件、或快照文件损坏，都记为问题；索引本身损坏可重建（扫描会重新发现孤儿快照文件，对应 UI 里的 pending 语义）。

## 4. v1→v2 迁移

预览（`PreviewMigration`）列出逐类变更；应用（`ApplyMigration`）按 Plan ID + Base Revision 执行，副本变化后旧计划即过期（`ErrorStaleOperation`）。

- **manifest**：升级到 version 2，物化 `dns_enabled=false` 与 `original_dns={kind: dhcp_empty}`（v1 没有 DNS 状态记录，这是「无信息」的保守默认，不是猜测）。`app_version` 保留，`updated_at` 刷新为迁移时间。
- **Profile**：逐个 round-trip（解析后按 v2 字段默认值重新序列化）。v1 文件没有 `mode` 字段时按所在目录推断（`profiles/dns/` 下物化为 `dns`，其余为 `hosts`），保证迁移后磁盘语义与目录一致；内容语义不变。损坏的 Profile 是**阻塞项**，禁止迁移（语义不明确的数据不能静默跳过）。
- **可选组件**（快照、广告配置）：不是迁移对象，原样保留；已损坏时在计划与回执中逐项列出并标记 `skipped` + warning issue。
- 每个被覆盖的文件：先登记备份 → 原子替换 → 读回字节比对。任一步失败，该文件保持原样，回执逐项列出成功/失败，不显示全局成功。

## 5. 设置迁移策略

源项目 v1 的设置存放在应用内 **localStorage，没有磁盘契约**。因此：

- 读取 v1 目录时设置摘要为 `Legacy settings (localStorage; no disk contract)`，迁移状态 `Pending v1 → v2 migration`；
- v1→v2 迁移**不**生成 settings.json——它不属于旧版磁盘数据；设置文件由后续的 v2 原生写入（验证变更或 #5 的正式设置界面）按默认值物化；
- Phase 0 合成的 `fixtures/storage/v2-full/settings.json` 只是 v2 原生格式的样例，**不是**旧版契约；解析 `settings.json` 时缺字段按默认值补齐（`quick_apply_on_toggle: false`、`palette_history: []`）。

## 6. 写入与恢复路径

统一写入路径（`writeContent`）：

1. 目标已存在且字节相同 → `unchanged`（不产生备份）；
2. 已存在且不同 → `RecordBackup` 复制原始字节到 `backups/<id>.json` 并登记，**备份失败即中止该文件写入**；
3. `WriteAtomic`：同目录临时文件 + fsync + rename；失败不留半文件、不改原文件；
4. 读回字节比对，不一致记 `readback_mismatch`，禁止全局成功声明。

恢复（`RestoreBackup`）：恢复当前文件前先生成安全备份；恢复后读回验证；回执 `Preservation: restored` 并附带安全备份引用。恢复只允许在工作副本上进行。

备份登记（`backups/index.json`）随工作副本持久化，`ListBackups` 返回当前副本内登记的全部备份（实体、相对路径、时间、来源操作）。

## 7. 失败注入与并发

- `FaultyVolume` 支持在原子写前、备份复制前注入失败；`WithVerifyFault` 注入读回失败。测试覆盖：写入失败原文件不变、备份失败中止覆盖、读回不一致被报告、多项写入部分完成的回执语义。
- 所有写操作在单互斥锁下串行；Revision 单调递增，`commit` 后重新扫描刷新摘要。并发契约测试（`-race`）验证预览过期后重试、Revision 不重复。

## 8. 测试边界

- 测试一律复制 fixture 到 `t.TempDir()` 隔离目录，从不写仓库内的 `fixtures/`；
- 差分测试对照 fixture：`v1-hosts`、`v1-full`、`v2-full`、`mixed-invalid` 及 original_dns 各变体（`v2-legacy-*`、`v2-tagged-dhcp`）；
- UI 层契约测试 `internal/desktop/workbench_real_test.go` 用真实服务驱动 #13 的全部主流程（打开源、建副本、迁移、验证写入、备份恢复、失败可见性）。
