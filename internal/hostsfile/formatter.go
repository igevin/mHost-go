package hostsfile

import (
	"strings"
)

// FormatRules 移植源项目 formatter::format_rules：
// 多域名规则展开为一行一域名；行内注释渲染为 "ip domain # comment"；
// 注释占位规则原样输出整行文本。
func FormatRules(rules []Rule) string {
	var out strings.Builder
	for _, rule := range rules {
		if rule.CommentOnly {
			if rule.Comment != nil {
				out.WriteString(*rule.Comment)
				out.WriteByte('\n')
			}
			continue
		}
		if rule.IP == nil {
			continue
		}
		for _, domain := range rule.Domains {
			out.WriteString(rule.IP.String())
			out.WriteByte(' ')
			out.WriteString(domain)
			if rule.Comment != nil {
				out.WriteString(" # ")
				out.WriteString(*rule.Comment)
			}
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// FormatManagedBlock 把规则包进管理块标记。空规则返回 ""
// （与源项目 format_managed_block 一致：无规则不写空块）。
func FormatManagedBlock(rules []Rule) string {
	if len(rules) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(ManagedStart)
	out.WriteByte('\n')
	out.WriteString(FormatRules(rules))
	out.WriteString(ManagedEnd)
	out.WriteByte('\n')
	return out.String()
}
