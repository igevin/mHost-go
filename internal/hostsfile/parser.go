// Package hostsfile 是 Hosts 核心包：parser、formatter、validator、
// 合并/冲突/diff、managed block 处理与系统 hosts 写入器。语义逐条移植自
// 源项目 mHost v0.4.2（commit ef1502e）的 mhost-hosts 与 mhost-apply
// crate，行为以 Rust/Go 差分测试（fixtures/hosts + tools/rust-harness）
// 锁定。管理块标记与源项目保持一致，保证新旧版本读写同一份 /etc/hosts。
package hostsfile

import (
	"fmt"
	"net/netip"
	"strings"
)

// 管理块标记，与源项目 MANAGED_START/MANAGED_END 字面量一致。
const (
	ManagedStart = "# ---- mHost start ----"
	ManagedEnd   = "# ---- mHost end ----"
)

// Rule 是解析出的 hosts 规则。IP 为 nil 表示注释占位规则
// （对应源项目 HostRule::is_comment_only）。
type Rule struct {
	Line        int // 1-based 行号；无来源时为 0
	IP          *netip.Addr
	Domains     []string
	Enabled     bool
	Comment     *string // 行内注释（'#' 后文本，已 trim）；注释占位规则存整行文本
	CommentOnly bool
}

// ParseErrorKind 分类与源项目 ParseError 一致。
type ParseErrorKind int

const (
	ErrInvalidIP ParseErrorKind = iota
	ErrMalformedLine
	ErrInvalidDomain
)

func (k ParseErrorKind) String() string {
	switch k {
	case ErrInvalidIP:
		return "invalid_ip"
	case ErrMalformedLine:
		return "malformed_line"
	case ErrInvalidDomain:
		return "invalid_domain"
	}
	return "unknown"
}

// ParseError 是带 1-based 行号的解析错误。
type ParseError struct {
	Line  int
	Kind  ParseErrorKind
	Token string // InvalidIP/InvalidDomain 的问题 token，或 MalformedLine 的原始行
}

func (e ParseError) Error() string {
	switch e.Kind {
	case ErrInvalidIP:
		return fmt.Sprintf("line %d: invalid ip %q", e.Line, e.Token)
	case ErrInvalidDomain:
		return fmt.Sprintf("line %d: invalid domain %q", e.Line, e.Token)
	case ErrMalformedLine:
		return fmt.Sprintf("line %d: malformed line %q", e.Line, e.Token)
	}
	return fmt.Sprintf("line %d: parse error", e.Line)
}

// ParseResult 是 Parse 的结构化输出。
type ParseResult struct {
	Rules      []Rule
	Errors     []ParseError
	Duplicates []Duplicate
}

// Parse 解析 hosts 文本。规则含注释占位规则；错误带行号；重复检测
// 只看启用且带 IP 的规则（与源项目 parse_with_lines 一致）。
func Parse(input string) ParseResult {
	result := ParseResult{}
	for idx, line := range strings.Split(input, "\n") {
		line = strings.TrimSuffix(line, "\r") // 与 Rust str::lines() 一致
		rule, err := parseLine(line)
		if err != nil {
			err.Line = idx + 1
			if err.Kind == ErrMalformedLine {
				err.Token = line
			}
			result.Errors = append(result.Errors, *err)
			continue
		}
		if rule != nil {
			rule.Line = idx + 1
			result.Rules = append(result.Rules, *rule)
		}
	}
	result.Duplicates = CheckDuplicates(result.Rules)
	return result
}

// parseLine 逐行语义与源项目 Parser::parse_line 一致。
func parseLine(line string) (*Rule, *ParseError) {
	trimmed := strings.TrimSpace(line)

	// 空行 -> 无规则
	if trimmed == "" {
		return nil, nil
	}

	// 独立注释行 -> 注释占位规则，保留 trim 后的整行文本。
	// 源项目 HostRule::comment_only 的 enabled = false。
	if strings.HasPrefix(trimmed, "#") {
		comment := trimmed
		return &Rule{Enabled: false, Comment: &comment, CommentOnly: true}, nil
	}

	// 去掉行内注释（第一个 '#' 之后）
	content := trimmed
	var comment *string
	if sharp := strings.IndexByte(trimmed, '#'); sharp >= 0 {
		content = strings.TrimSpace(trimmed[:sharp])
		if text := strings.TrimSpace(trimmed[sharp+1:]); text != "" {
			comment = &text
		}
	}
	if content == "" {
		return nil, nil
	}

	fields := strings.Fields(content)
	if len(fields) == 0 {
		return nil, nil
	}

	ip, err := netip.ParseAddr(fields[0])
	if err != nil {
		// 区分「像是 IP 但非法」与「完全无关的内容」
		if LooksLikeIP(fields[0]) {
			return nil, &ParseError{Kind: ErrInvalidIP, Token: fields[0]}
		}
		return nil, &ParseError{Kind: ErrMalformedLine, Token: line}
	}

	if len(fields) < 2 {
		return nil, &ParseError{Kind: ErrMalformedLine, Token: line}
	}

	domains := make([]string, 0, len(fields)-1)
	for _, domain := range fields[1:] {
		if !IsValidDomain(domain) {
			return nil, &ParseError{Kind: ErrInvalidDomain, Token: domain}
		}
		domains = append(domains, domain)
	}

	rule := &Rule{IP: &ip, Domains: domains, Enabled: true, Comment: comment}
	return rule, nil
}

// ExtractManagedBlock 返回管理块的 0-based 闭区间行号
// [startLine, endLine]。标记缺失、重复或顺序颠倒时返回 ok=false
// （与源项目 extract_managed_block 一致）。
func ExtractManagedBlock(input string) (startLine, endLine int, ok bool) {
	start, end := -1, -1
	for idx, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case ManagedStart:
			if start >= 0 {
				return 0, 0, false
			}
			start = idx
		case ManagedEnd:
			if end >= 0 {
				return 0, 0, false
			}
			end = idx
		}
	}
	if start >= 0 && end >= 0 && start <= end {
		return start, end, true
	}
	return 0, 0, false
}

// ExtractManagedBlockBytes 返回管理块的字节区间 [start, end)：
// start 指向起始标记行行首，end 为结束标记行（含其换行符）之后的偏移。
// 单次 split_inclusive 扫描，CRLF 安全（源项目 extract_managed_block_bytes）。
func ExtractManagedBlockBytes(input string) (start, end int, ok bool) {
	start, end = -1, -1
	pos := 0
	for _, line := range splitInclusive(input, '\n') {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		switch trimmed {
		case ManagedStart:
			if start >= 0 {
				return 0, 0, false
			}
			start = pos
		case ManagedEnd:
			if end >= 0 {
				return 0, 0, false
			}
			end = pos + len(line)
		}
		pos += len(line)
	}
	if start >= 0 && end >= 0 && start <= end {
		return start, end, true
	}
	return 0, 0, false
}

// ExtractManagedBlockContent 返回管理块标记之间的内容（不含标记行）。
// 空块返回 ("", true)；无有效块返回 ok=false。
func ExtractManagedBlockContent(input string) (string, bool) {
	start, end, ok := ExtractManagedBlock(input)
	if !ok {
		return "", false
	}
	lines := strings.Split(input, "\n")
	if end <= start+1 {
		return "", true
	}
	return strings.Join(lines[start+1:end], "\n"), true
}

// splitInclusive 按 sep 切分且保留分隔符（Go 标准库无此函数）。
func splitInclusive(s string, sep byte) []string {
	var parts []string
	for {
		idx := strings.IndexByte(s, sep)
		if idx < 0 {
			if s != "" {
				parts = append(parts, s)
			}
			return parts
		}
		parts = append(parts, s[:idx+1])
		s = s[idx+1:]
	}
}
