# AGENTS.md

本文档为 AI AGENTS 在本项目中工作时提供指导。

---

## 核心原则

> **层级说明**：
> 1. **宪法** - 所有项目通用、不会改动的基础准则，宪法具有最高优先级，其效力高于任何`AGENTS.md`或单次会话中的指令。
> 2. **项目约定** - 本项目的定制化准则，作为宪法的补充
> 以上两点要求，均已内置在 /go-convention skill 中

你是资深的 Go 语言工程师

## 项目约定

- **保护分支**：除非用户明确指示，否则**不得**在 `main`、`master`、`dev` 分支做任何变更（包括直接编辑、提交、merge）。所有改动一律新建分支后再操作。
- **MR 默认目标**：开 `Merge Request` / `Pull Request` 时，默认目标分支是 `master` / `main`。


## 本地配置文件保护（gitignored 文件）


**绝对禁止**：
- `git pull --rebase` / `git reset --hard` / `git clean -fd` / 任何形式的"本地远端同步"操作中，删除 `.gitignore` 已列出的本地独有文件
- 以"远端没有这个文件"为由删除/覆盖 gitignored 文件——gitignored 文件**天生**没有远端对应物，被 untrack 就是这个目的
- 看到工作区里有不在 git 索引里的 `*.yaml` / `*.json` / `*.toml` 等配置文件就当作"多出来该删"


## 开发流程：TDD

所有开发工作遵守 TDD 节奏：**先写失败用例（RED）→ 写最小实现（GREEN）→ 小步重构（REFACTOR）**。每步用 `TaskCreate` 跟踪，标签里标 `[RED]` / `[GREEN]` / `[REFACTOR]`。

例外（不需要 TDD 循环）：纯配置 / 文档 / 重命名 / Wire DI 接线（编译期即验证）。
