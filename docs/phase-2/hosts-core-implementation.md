# Phase 2 Hosts 核心实现（issue #4）

关联 issue：#4（本文件对应其实现）；#16（交互与 `app.HostsService` 契约，本实现不得削弱其语义）。

## 目标与边界

用 Go 完整实现 Hosts 核心链路：parser / formatter / validator、规则合并与冲突检测、diff、managed block 处理、系统 hosts 安全写入（备份、原子替换、读回验证、自动回滚、按 ID 回滚）、Apply Lock 串行化、applied-state 与 drift 检测。规则行为与源项目 mHost v0.4.2（commit `ef1502e`）一致，由 Rust/Go 差分测试锁定。

不在本 issue 范围：MyGo UI（#16 已交付 Demo 驱动版本；真实服务接线归 #5）、提权写入与 DNS cache flush 的真实平台实现（#6；写入器已通过 `Mover`/`Flusher` 接口预留）。

## 包结构

| 包 | 内容 |
| --- | --- |
| `internal/hostsfile` | 纯逻辑核心：`parser.go` `validate.go` `formatter.go` `merge.go` `diff.go` `content.go` `verify.go` `writer.go` |
| `internal/app/hostssvc` | `app.HostsService` 真实实现：`service.go`（Profile 读写）`apply.go`（Preview/Apply/Rollback/AppliedState） |

关键语义决策：

- **管理块标记**用源项目字面量 `# ---- mHost start ----` / `# ---- mHost end ----`，与旧版本写过的 `/etc/hosts` 兼容。
- **注释占位规则** `enabled=false`（源项目 `HostRule::comment_only`）；文本模型中"禁用规则 = 注释行"（`# ip domain` 解析为注释占位规则，渲染时禁用规则输出为注释行），与源项目"合并时跳过注释/禁用规则"语义一致。
- **SaveProfile 强化**：含解析错误的文本在保存时拒绝（`ErrorValidation` 逐行列出原因），保证存储内永远是合法规则；`PreviewApply` 的 Blockers 保留为防御性兜底。这是相对 demo 语义的**增强**（demo 允许保存错误文本），不削弱契约。
- **冲突默认保留**首个出现行的 IP（未给出 resolution 时）；Apply 用调用方 resolutions 重算合并结果（修订号校验保证预览后内容未变）。
- **Save 丢失规则来源标注**：文本模型无法表达 Remote/AdBlock 来源，保存后新规则一律记为 Manual。Hosts-only Alpha 阶段可接受；订阅类 profile 的编辑保护在后续阶段处理。
- **备份**为写前全量 hosts 快照（0600），索引 `backups/index.json` 记录 ID/profile/上一个 applied profile/内容哈希，保留最近 10 份（源项目 `MAX_BACKUPS`）。
- **AppliedState**：`apply-state.json` 记录管理块内容 SHA256 与归属 profile；drift = 当前块哈希 ≠ 记录（覆盖外部改写、块删除、他端应用）。
- **Apply Lock**：`Service.applyMu` 串行化 Apply/Rollback；并发 Apply 靠修订号检查保证只有一个成功，测试验证不产生交叉写入。

## SystemWriter 写入流程（移植自 mhost-apply）

1. `ensureRegularFile`：Lstat 拒绝符号链接与非常规文件（源项目 #19 安全修复）。
2. 读当前全量内容。
3. 写前备份（`createBackup`，失败即中止，hosts 不变）。
4. `BuildHostsContent`：有块按字节区间原位替换，无块末尾追加，空规则移除块；块外内容逐字节保留。
5. 原子写：目标同目录临时文件（保留原权限位）+ `Mover.Move`。生产 `Mover` 为同盘 rename；提权平台在 #6 替换实现（对应源项目 `elevated_move`）。
6. `Flusher.FlushDNS`：失败不致命（源项目行为）。
7. 读回验证（`verify`）：块存在性 + 每个 `ip domain` 行存在于块内（存在性检查，不拒绝多余行，与源项目一致）。
8. 验证失败 → 从备份自动恢复并再次读回比对；回滚也失败时返回合并错误。

## Rust/Go 差分测试

`fixtures/hosts/corpus/*.txt`（19 份语料：标准/注释/CRLF/禁用/重复/同异 IP 冲突/非法 IP/畸形行/非法域名/IP 外形域名/URL token/管理块/空白/IPv6 全展开/4-in-6 等）→ Rust 侧 harness `tools/rust-harness/parse_dump.rs`（拷入源仓库 mhost-hosts crate 作为 example 运行）→ `fixtures/hosts/rust-golden/*.json`。

重新生成：

```sh
MHOST_SRC=/path/to/mHost tools/rust-harness/generate.sh
```

Go 侧 `internal/hostsfile/diff_rust_test.go` 对每份语料解析并语义比对 golden。开发期靠它发现并锁定了两处易错语义：注释占位规则 `enabled=false`、`\r` 剥离与 `str::lines()` 对齐。

## 受控环境真实写入验证

`SystemWriter` 与 `hostssvc` 的全部测试都在真实文件系统上对临时目录执行完整 Apply/Rollback 流程（临时 hosts 文件 + 真实 rename 落位），覆盖：

- 正常替换/追加/移除块，块外内容保留，文件权限保留；
- 备份 0600、索引、按 ID 回滚、裁剪保留 10 份；
- 写入失败（Mover 注入）：原文件不变，回执 backup written / managed_block failed / verification skipped，可再 Rollback；
- 读回验证失败（首写坏内容注入）：自动回滚至备份内容；
- 回滚也失败：返回合并错误信息；
- 符号链接拒绝（Apply 与 Rollback 双路径，符号链接目标不被修改）；
- 并发 Apply（8 goroutines，race detector）：无交叉写入；
- FaultyVolume 备份写入失败：整体中止，hosts 不变。

`make test test-race` 全绿即上述验证的记录；macOS 真机 `/etc/hosts` 端到端验证随 #5（真实服务接线）进行。

## 已知限制

- 写真实 `/etc/hosts` 需要 root；非提权环境下 Apply 会以 `ErrorWrite` 失败且原文件不变（安全语义正确）。提权 helper 归 #6。
- 进程重启后内存中的 PreviewPlan 失效（需重新预览），与 demo 语义一致。
- `PreviewApply` 无独立的解析错误入口（Save 已拒绝错误文本）；从 v1 迁移上来的 profile 若含脏数据，在迁移层（#14）已被拦截。
