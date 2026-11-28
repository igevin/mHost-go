# Phase 3 Hosts-only Alpha 实现（#5）

关联 issue：#5（父）、#25（主链路）、#26（页面与导入导出）、#23（可处置性，已并入）、#32（Apply 主流程与退出草稿保护）。

## 目标与边界

第一个可日常试用的 Go + MyGo 版本：用户脱离源项目完成日常 Hosts 管理。DNS 与广告屏蔽全程隐藏。真机打包与试用验证归 #27（用户侧）。

## 启动链路（alphaboot）

1. 数据目录：默认 `os.UserConfigDir()/mHost`（macOS `~/Library/Application Support/mHost`），`MHOST_DATA_DIR` 覆盖。
2. 有 `manifest.json` 且 version=1 → `storage.InspectWritable` 原地迁移（v1→v2，迁移前备份语义由存储服务保证）；迁移回执与 issue 汇总进启动横幅。
3. 全新安装（无 manifest）→ 写出最小 v2 manifest（快照扫描依赖）。
4. 无 hosts profile → 创建「默认」profile。
5. 构造 `hostssvc.Service`：hostsPath 默认 `/etc/hosts`（`MHOST_HOSTS_PATH` 覆盖），备份目录 `<data>/backups`。

`storage.InspectWritable` 是新加的存储服务入口：常规「探查 → 工作副本」流程仍用 `Inspect` + `CreateWorkspace`，原地直接管理走 `InspectWritable`，避免误把源目录当副本写。

## 应用壳（desktop.AlphaModel）

- 导航：Hosts（主链路）/ Snapshots / System Hosts / Settings；DNS、AdBlock 不出现。
- 启动横幅：一次性展示迁移回执摘要与扫描 issue，可关闭。
- 生命周期（关窗隐藏、托盘、退出门禁、写不可取消）复用 `HostsWorkbenchModel` 与 `App` 的既有实现；`App` 新增 `Start()` 可选钩子在窗口就绪后自动加载。

## Hosts 主链路增强

- Profile CRUD（`hostssvc.ProfileAdmin`，demo 不实现则入口隐藏）：新建（名称去重）、复制（规则新 ID、副本不继承受保护）、启停（存储态语义，不等于已应用）、删除（受保护拒绝、二次确认、未消费计划失效）。
- Apply 主流程（#32）：有未保存草稿时，Apply… 先保存并刷新列表，成功后自动生成预览；保存失败不进预览。取消预览只关闭计划，不改写系统 Hosts，并提示自动保存的 Profile 仍已保存。
- 草稿切换：切换 Profile 不再弹确认；草稿按 Profile 保留在内存中并显示未保存标记。Cmd+Q / 托盘退出遇到未保存草稿时展示 `Save All`、`Quit Without Saving`、`Cancel`；Save All 任一失败都保持应用打开并显示原因。
- 搜索：sidebar 过滤（大小写不敏感）。
- 导入导出：mygo v0.10 无文件对话框，采用目录选择器方案——Export 写 `<名称>.hosts` 到所选目录（非法文件名字符替换 `_`）；Import 读取所选目录全部 `.hosts` 文件逐个创建（校验失败跳过并报告数量）。
- 编辑器诊断统一走 `hostsfile.Parse`（与真实服务、Rust 差分测试同一语义来源），不再维护桌面侧第二份解析逻辑。

## 页面

- **System Hosts**：只读全量视图 + 管理块行号定位；读取失败给可见原因。
- **Snapshots**：存储扫描的快照列表 + issue（含 #23 孤儿快照）展示；创建/恢复留待后续切片。
- **Settings**：数据目录、系统 hosts 路径、apply 备份数量、导入导出入口说明。

## #23 可处置性

1. 存储 Workbench 回执表格增加 Issue 列；带 issue 的行（skipped/failed）逐条列出 Message + Detail——「manifest 为什么没升级」这类状态可见。
2. `Volume` 新增 `ReadDir`；扫描阶段把「存在但未被 index 引用」的快照文件报为 `orphan_snapshot`（warning、recoverable，提示删文件清理）。Alpha 快照页与启动横幅都会展示。

## 测试

- `alphaboot`：v1 fixture 迁移、空目录建默认 profile（幂等）、v2 原样保留。
- `hostssvc`：CRUD 全流、导入导出、文件名清理、受保护拒绝。
- `desktop`：CRUD 模型流（创建即选中、删除需确认、搜索过滤）、导出导入闭环、快照页数据、系统 Hosts 页数据。
- `storage`：孤儿快照扫描。

`make fmt vet test test-race` 全绿。

## 已知限制

- Apply/Rollback 写 `/etc/hosts` 经 macOS 管理员密码框提权（#30，`internal/elevate` + `PrivilegedMover`）；取消授权按"未改动"提示，不是可重试错误。
- 未保存草稿只在当前进程内存中保留；关闭窗口隐藏到托盘不会丢失，真正退出时通过 #32 的选择框处理。
- mygo 无文件对话框，导入导出为目录粒度。
- 快照创建/恢复未开放（列表与孤儿可见性已就位）。
- Save 文本不保留规则来源标注（Remote/AdBlock 记 Manual）。
