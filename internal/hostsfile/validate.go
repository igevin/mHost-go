package hostsfile

import (
	"sort"
	"strings"
)

// IsValidDomain 逐条移植源项目 validator::is_valid_domain：
// 仅字母/数字/连字符/点；标签不得以连字符开头结尾；域名不得以点结尾；
// 不得为纯数字+点的 IP 外形 token；允许 RFC 1123 全数字标签
// （如 analytics.163.com，源项目 issue #203）。
func IsValidDomain(domain string) bool {
	if domain == "" {
		return false
	}
	if strings.HasPrefix(domain, "-") || strings.HasSuffix(domain, "-") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, c := range domain {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	allNumeric := true
	for _, c := range domain {
		if !(c >= '0' && c <= '9' || c == '.') {
			allNumeric = false
			break
		}
	}
	if allNumeric {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			return false
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

// LooksLikeIP 逐条移植源项目 validator::looks_like_ip：
// IPv4 外形（数字+点，含点、不含首尾点）；IPv6 外形（含 ':'，
// 但排除 URL scheme 与 host:port 形式）。
func LooksLikeIP(token string) bool {
	if token == "" {
		return false
	}
	if strings.Contains(token, ":") {
		if strings.Contains(token, "//") {
			return false
		}
		beforeColon := token
		if idx := strings.IndexByte(token, ':'); idx >= 0 {
			beforeColon = token[:idx]
		}
		if strings.Contains(beforeColon, ".") {
			return false
		}
		return true
	}
	if strings.HasPrefix(token, ".") || strings.HasSuffix(token, ".") || !strings.Contains(token, ".") {
		return false
	}
	hasDigit := false
	for _, c := range token {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == '.':
		default:
			return false
		}
	}
	return hasDigit
}

// DuplicateKind 与源项目 DuplicateKind 一致。
type DuplicateKind int

const (
	DuplicateSameIP DuplicateKind = iota
	DuplicateDifferentIP
)

func (k DuplicateKind) String() string {
	if k == DuplicateSameIP {
		return "same_ip"
	}
	return "different_ip"
}

// Duplicate 是一个出现在多行上的域名，附行号与 IP 是否一致。
type Duplicate struct {
	Domain string
	Lines  []int
	Kind   DuplicateKind
}

// CheckDuplicates 移植源项目 validator::check_duplicates：
// 只看启用且带 IP 的规则；按域名（BTreeMap）排序输出；
// 行号去重升序；全部 IP 相同为 SameIP，否则 DifferentIP。
func CheckDuplicates(rules []Rule) []Duplicate {
	type entry struct {
		line int
		ip   string
	}
	byDomain := map[string][]entry{}
	for _, rule := range rules {
		if !rule.Enabled || rule.IP == nil {
			continue
		}
		if rule.Line == 0 {
			continue
		}
		ip := rule.IP.String()
		for _, domain := range rule.Domains {
			byDomain[domain] = append(byDomain[domain], entry{line: rule.Line, ip: ip})
		}
	}

	domains := make([]string, 0, len(byDomain))
	for domain := range byDomain {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	var duplicates []Duplicate
	for _, domain := range domains {
		entries := byDomain[domain]
		if len(entries) < 2 {
			continue
		}
		lineSet := map[int]bool{}
		for _, e := range entries {
			lineSet[e.line] = true
		}
		lines := make([]int, 0, len(lineSet))
		for line := range lineSet {
			lines = append(lines, line)
		}
		sort.Ints(lines)

		kind := DuplicateSameIP
		for _, e := range entries[1:] {
			if e.ip != entries[0].ip {
				kind = DuplicateDifferentIP
				break
			}
		}
		duplicates = append(duplicates, Duplicate{Domain: domain, Lines: lines, Kind: kind})
	}
	return duplicates
}
