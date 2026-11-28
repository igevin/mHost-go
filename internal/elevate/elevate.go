// Package elevate 以一次 macOS 管理员授权完成受保护文件的安装：
// 通过 osascript 的 `do shell script ... with administrator privileges`
// 弹出系统密码框，在同一提权会话内完成文件落位与 DNS 缓存刷新。
// 核心逻辑（脚本构造、错误分类）与执行器解耦，可注入假执行器单测。
package elevate

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCanceled 用户在管理员授权框点了取消（osascript 返回 -128 /
// "User canceled"）。调用方应把它与"写入失败"区分：hosts 未被改动。
var ErrCanceled = errors.New("user canceled the authorization prompt")

// Runner 执行外部命令并返回合并输出，生产实现为 exec.Command。
type Runner func(name string, args ...string) ([]byte, error)

// InstallScript 构造提权安装 + DNS 刷新的 AppleScript。
// 两级转义：shell 层单引号包裹（内嵌单引号按 '\” 惯例），
// AppleScript 层反斜杠与双引号翻倍。cat > 目标 是截断写，
// 保留原 inode 与权限位。
func InstallScript(src, target string) string {
	shell := fmt.Sprintf("/bin/cat %s > %s && /usr/sbin/dscacheutil -flushcache && /usr/bin/killall -HUP mDNSResponder",
		shellQuote(src), shellQuote(target))
	return `do shell script "` + applescriptEscape(shell) + `" with administrator privileges`
}

// InstallWith 用给定执行器运行提权安装，并把结果分类：
// 成功 → nil；用户取消 → %w 包装的 ErrCanceled；其余 → 含输出的错误。
func InstallWith(runner Runner, src, target string) error {
	out, err := runner("osascript", "-e", InstallScript(src, target))
	if err == nil {
		return nil
	}
	text := string(out)
	if strings.Contains(text, "User canceled") || strings.Contains(text, "-128") {
		return fmt.Errorf("%w: %v", ErrCanceled, err)
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
