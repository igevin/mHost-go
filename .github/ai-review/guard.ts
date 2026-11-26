/**
 * ai-review guard 扩展 —— 审查代理的文件级防线。
 *
 * Pi 的 read/grep/find/ls 工具接受任意绝对路径，内核没有文件权限系统；
 * 被审 diff 里的 prompt injection 可能诱导模型读取 /proc/self/environ，
 * 把进程环境里的 API key / GITHUB_TOKEN / GEVINYU_TOKEN 带进 PR 评论造成泄密。
 * 本扩展在 tool_call gate 上拦截对内核伪文件系统的访问。
 *
 * 加载方式（ai-review.yml）：`-e <本文件>`——`--no-extensions` 只关闭自动发现，
 * 显式 -e 仍然加载。工作流从 **base 分支 tip** 恢复本文件后再加载，
 * 绝不加载 PR 工作树里的副本（防 PR 作者在 PR 里植入弱化版 guard）。
 *
 * 本文件是 guard 的**唯一真值源**：ai-review.yml 里没有内嵌副本。
 * base 分支 tip 缺这个文件时工作流会 fail closed（拒绝在无防线状态下跑审查），
 * 而不是降级成一份需要人工同步的副本。引入本工作流的那次 PR 本身就是
 * bootstrap 场景（base 上还没有审查体系），此时审查不会触发——这是有意的取舍：
 * 审查程序本身必须来自可信的 base 分支，否则 PR 可以自带审查逻辑。
 *
 * 自检：加载时向 stderr 打印一行日志；工作流在恢复文件后校验其包含
 * "tool_call" 关键字，缺失即拒绝运行审查（防线自校验，防静默失效）。
 */

import { resolve } from "node:path";

/** 内核伪文件系统前缀，命中即拦截。 */
const BLOCKED_PREFIXES = ["/proc", "/sys", "/dev"];

/** 只读审查启用的工具集（与 ai-review.yml 的 --tools 保持一致）。 */
const GUARDED_TOOLS = new Set(["read", "grep", "find", "ls"]);

function blockedReason(resolvedPath: string): string | undefined {
	for (const prefix of BLOCKED_PREFIXES) {
		if (resolvedPath === prefix || resolvedPath.startsWith(prefix + "/")) {
			return `path ${resolvedPath} is under ${prefix}, blocked by ai-review guard`;
		}
	}
	return undefined;
}

export default function (pi: any) {
	pi.on("tool_call", (event: any, ctx: any) => {
		if (!GUARDED_TOOLS.has(event.toolName)) return;
		const input = event.input;
		if (!input || typeof input !== "object") return;

		const candidates: string[] = [];
		if (typeof input.path === "string") candidates.push(input.path);
		// find 的 glob pattern 若以 / 开头等价于绝对路径，同样纳入检查
		if (typeof input.pattern === "string" && input.pattern.startsWith("/")) {
			candidates.push(input.pattern);
		}

		for (const raw of candidates) {
			const resolved = raw.startsWith("/") ? resolve(raw) : resolve(ctx.cwd, raw);
			const reason = blockedReason(resolved);
			if (reason) {
				console.error(`[ai-review-guard] blocked ${event.toolName}: ${reason}`);
				return { block: true, reason, terminate: true };
			}
		}
	});

	console.error("[ai-review-guard] loaded, blocking reads under /proc, /sys, /dev");
}
