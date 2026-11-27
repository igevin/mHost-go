package hostsfile

import (
	"fmt"
	"sort"
	"strings"
)

// HostsDiff 是管理块 before/after 的行级差分，行均升序
// （源项目 BTreeSet 语义）。
type HostsDiff struct {
	Added     []string
	Removed   []string
	Unchanged []string
}

// CalculateDiff 移植源项目 mhost-apply 的 diff::calculate_diff：
// 只比较当前 /etc/hosts 管理块内的行与目标规则行（集合运算，
// 不做序列对齐）；块外用户内容不参与。
func CalculateDiff(currentHosts string, resolved []ResolvedRule) HostsDiff {
	existing := extractExistingLines(currentHosts)

	targetSet := map[string]bool{}
	for _, rule := range resolved {
		targetSet[fmt.Sprintf("%s %s", rule.IP.String(), rule.Domain)] = true
	}
	existingSet := map[string]bool{}
	for _, line := range existing {
		existingSet[line] = true
	}

	diff := HostsDiff{}
	for line := range targetSet {
		if existingSet[line] {
			diff.Unchanged = append(diff.Unchanged, line)
		} else {
			diff.Added = append(diff.Added, line)
		}
	}
	for line := range existingSet {
		if !targetSet[line] {
			diff.Removed = append(diff.Removed, line)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Unchanged)
	return diff
}

// extractExistingLines 取当前 hosts 管理块内部的行：
// 规则展开为一行一域名，注释占位行原样保留（源项目 diff::extract_existing_lines）。
func extractExistingLines(currentHosts string) []string {
	start, end, ok := ExtractManagedBlock(currentHosts)
	if !ok || start+1 >= end {
		return nil
	}
	// 与源项目一致：按字节切出块内部原文再解析（CRLF 安全，parse 会 trim \r）
	lines := splitInclusive(currentHosts, '\n')
	var interior []string
	for i, line := range lines {
		if i >= start+1 && i <= end-1 {
			interior = append(interior, line)
		}
	}
	contentToParse := strings.Join(interior, "")

	parsed := Parse(contentToParse)
	var out []string
	for _, rule := range parsed.Rules {
		if rule.IP != nil {
			for _, domain := range rule.Domains {
				out = append(out, fmt.Sprintf("%s %s", rule.IP.String(), domain))
			}
		} else if rule.Comment != nil {
			out = append(out, *rule.Comment)
		}
	}
	return out
}
