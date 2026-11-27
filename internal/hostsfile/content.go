package hostsfile

import (
	"fmt"
	"strings"
)

// BuildHostsContent 移植源项目 mhost-apply 的 content::build_hosts_content：
// 已有管理块则按字节区间原位替换（保留块外内容与格式）；
// 没有则在文件末尾追加；managedBlock 为空串时移除块不写新块。
func BuildHostsContent(current, managedBlock string) string {
	if start, end, ok := ExtractManagedBlockBytes(current); ok {
		var out strings.Builder
		out.Grow(len(current) + len(managedBlock))
		out.WriteString(current[:start])
		out.WriteString(managedBlock)
		out.WriteString(current[end:])
		return out.String()
	}

	var out strings.Builder
	out.Grow(len(current) + len(managedBlock) + 1)
	out.WriteString(current)
	if current != "" && !strings.HasSuffix(current, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString(managedBlock)
	return out.String()
}

// Verify 移植源项目 mhost-apply 的 verification::verify：
// 规则为空时期望没有管理块；否则管理块必须存在且包含每个 "ip domain" 行。
// 这是存在性检查，不拒绝块内多余行（与源项目行为一致）。
func Verify(written string, rules []ResolvedRule) error {
	if len(rules) == 0 {
		if _, _, ok := ExtractManagedBlock(written); ok {
			return fmt.Errorf("expected no managed block but found one")
		}
		return nil
	}

	if _, _, ok := ExtractManagedBlock(written); !ok {
		return fmt.Errorf("managed block missing")
	}
	content, _ := ExtractManagedBlockContent(written)
	writtenLines := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		writtenLines[line] = true
	}

	for _, rule := range rules {
		line := fmt.Sprintf("%s %s", rule.IP.String(), rule.Domain)
		if !writtenLines[line] {
			return fmt.Errorf("expected rule %q not found", line)
		}
	}
	return nil
}
