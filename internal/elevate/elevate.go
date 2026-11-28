// Package elevate 以一次 macOS 管理员授权完成受保护文件的安装：
// 通过 osascript 的 `do shell script ... with administrator privileges`
// 弹出系统密码框，在同一提权会话内完成原子落位与 DNS 缓存刷新。
// 核心逻辑（脚本构造、错误分类）与执行器解耦，可注入假执行器单测。
package elevate

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

var (
	// ErrCanceled 用户在管理员授权框点了取消（osascript 返回 -128 /
	// "User canceled"）。调用方应把它与"写入失败"区分：hosts 未被改动。
	ErrCanceled = errors.New("user canceled the authorization prompt")
	// ErrAuthFailed 管理员授权失败（密码错误等，osascript -60005）。
	// hosts 未被改动，用户修正后可重试。
	ErrAuthFailed = errors.New("administrator authorization failed")
)

// Runner 执行外部命令并返回合并输出，生产实现为 exec.Command。
type Runner func(name string, args ...string) ([]byte, error)

// InstallScript 构造提权安装的 AppleScript。三段式保证安全语义：
//  1. root 在目标同目录写 target.mhost-new（目标全程不被触碰，
//     绝无半截文件窗口）；
//  2. chmod 保留原权限位后 mv 原子覆盖；
//  3. DNS 刷新只在落位成功分支执行，且失败不影响退出码
//     （与 writer 的 Flusher 语义一致：刷新尽力而为）；
//     落位失败则清理暂存文件并以非零退出，交由调用方走错误路径。
//
// 两级转义：shell 层单引号包裹（内嵌单引号按 '\” 惯例），
// AppleScript 层反斜杠与双引号翻倍。
func InstallScript(src, target string, mode fs.FileMode) string {
	staged := target + ".mhost-new"
	shell := fmt.Sprintf("if /bin/cat %s > %s && /bin/chmod %o %s && /bin/mv -f %s %s; then\n"+
		"dscacheutil -flushcache || true\n"+
		"killall -HUP mDNSResponder || true\n"+
		"exit 0\n"+
		"else\n"+
		"/bin/rm -f %s\n"+
		"exit 1\nfi",
		shellQuote(src), shellQuote(staged), mode.Perm(), shellQuote(staged),
		shellQuote(staged), shellQuote(target), shellQuote(staged))
	return `do shell script "` + applescriptEscape(shell) + `" with administrator privileges`
}

// InstallWith 用给定执行器运行提权安装，并把结果分类：
// 成功 → nil；用户取消 → %w 包装的 ErrCanceled；密码错误/授权失败 →
// %w 包装的 ErrAuthFailed；其余 → 含输出的普通错误。
func InstallWith(runner Runner, src, target string, mode fs.FileMode) error {
	out, err := runner("/usr/bin/osascript", "-e", InstallScript(src, target, mode))
	if err == nil {
		return nil
	}
	text := string(out)
	switch {
	case strings.Contains(text, "User canceled") || strings.Contains(text, "-128"):
		return fmt.Errorf("%w: %v", ErrCanceled, err)
	case strings.Contains(text, "-60005") || strings.Contains(text, "authorization failed"):
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}
	return fmt.Errorf("privileged install failed: %v: %s", err, strings.TrimSpace(text))
}

// shellQuote 按 POSIX shell 单引号惯例包裹路径。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// applescriptEscape 转义 AppleScript 字符串字面量中的 \ 与 "。
func applescriptEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
