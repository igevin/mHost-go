package hostsfile

import (
	"net/netip"
	"strings"
	"testing"
)

func addrPtr(t *testing.T, s string) *netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad test ip %q: %v", s, err)
	}
	return &a
}

func TestParseStandardLines(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ip    string
		doms  []string
	}{
		{"ipv4_single", "127.0.0.1 example.com", "127.0.0.1", []string{"example.com"}},
		{"ipv6_single", "::1 localhost", "::1", []string{"localhost"}},
		{"ipv6_full", "2001:db8::1 example.com", "2001:db8::1", []string{"example.com"}},
		{"multi_domain", "127.0.0.1 a.com b.com", "127.0.0.1", []string{"a.com", "b.com"}},
	}
	for _, tc := range cases {
		result := Parse(tc.input)
		if len(result.Errors) != 0 {
			t.Fatalf("%s: unexpected errors %v", tc.name, result.Errors)
		}
		if len(result.Rules) != 1 {
			t.Fatalf("%s: expected 1 rule, got %d", tc.name, len(result.Rules))
		}
		rule := result.Rules[0]
		if rule.IP == nil || rule.IP.String() != tc.ip {
			t.Fatalf("%s: ip = %v, want %s", tc.name, rule.IP, tc.ip)
		}
		if strings.Join(rule.Domains, ",") != strings.Join(tc.doms, ",") {
			t.Fatalf("%s: domains = %v, want %v", tc.name, rule.Domains, tc.doms)
		}
		if !rule.Enabled {
			t.Fatalf("%s: rule should be enabled", tc.name)
		}
	}
}

func TestParseWithInlineComment(t *testing.T) {
	result := Parse("127.0.0.1 example.com # dev")
	if len(result.Rules) != 1 || result.Rules[0].Comment == nil || *result.Rules[0].Comment != "dev" {
		t.Fatalf("comment = %+v", result.Rules)
	}

	// 注释只有 '#' 时 comment 为 nil
	result = Parse("127.0.0.1 example.com #")
	if len(result.Rules) != 1 || result.Rules[0].Comment != nil {
		t.Fatalf("empty comment should be nil: %+v", result.Rules)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		kind  ParseErrorKind
		token string
	}{
		{"invalid_ip", "999.999.999.999 x.com", ErrInvalidIP, "999.999.999.999"},
		{"invalid_domain", "127.0.0.1 -bad.com", ErrInvalidDomain, "-bad.com"},
		{"malformed", "example.com 127.0.0.1", ErrMalformedLine, "example.com 127.0.0.1"},
	}
	for _, tc := range cases {
		result := Parse(tc.input)
		if len(result.Rules) != 0 {
			t.Fatalf("%s: expected no rules, got %v", tc.name, result.Rules)
		}
		if len(result.Errors) != 1 {
			t.Fatalf("%s: expected 1 error, got %v", tc.name, result.Errors)
		}
		err := result.Errors[0]
		if err.Kind != tc.kind || err.Token != tc.token {
			t.Fatalf("%s: error = %+v, want kind %v token %q", tc.name, err, tc.kind, tc.token)
		}
		if err.Line != 1 {
			t.Fatalf("%s: line = %d, want 1", tc.name, err.Line)
		}
	}
}

func TestParseCommentOnlyAndEmpty(t *testing.T) {
	result := Parse("# this is a comment")
	if len(result.Rules) != 1 || !result.Rules[0].CommentOnly {
		t.Fatalf("comment-only rule expected: %+v", result.Rules)
	}
	if result.Rules[0].Comment == nil || *result.Rules[0].Comment != "# this is a comment" {
		t.Fatalf("comment text: %+v", result.Rules)
	}
	if result.Rules[0].IP != nil {
		t.Fatalf("comment-only rule should have no ip")
	}

	for _, input := range []string{"", "   ", "\n\n"} {
		if rules := Parse(input).Rules; len(rules) != 0 {
			t.Fatalf("input %q: expected no rules, got %v", input, rules)
		}
	}
}

func TestParseLineNumbersAndMultipleErrors(t *testing.T) {
	result := Parse("127.0.0.1 ok.com\nbad_line\n127.0.0.1 ok2.com\n999.999.999.999 bad.com")
	if len(result.Errors) != 2 {
		t.Fatalf("errors = %v", result.Errors)
	}
	if result.Errors[0].Line != 2 || result.Errors[1].Line != 4 {
		t.Fatalf("error lines = %v", result.Errors)
	}
}

func TestParseLeadingTrailingWhitespaceAndCRLF(t *testing.T) {
	result := Parse("  127.0.0.1   example.com  \r\n\t::1 localhost\r\n")
	if len(result.Errors) != 0 || len(result.Rules) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExtractManagedBlock(t *testing.T) {
	input := "# line 1\n# ---- mHost start ----\n127.0.0.1 x.com\n# ---- mHost end ----\n# line 5"
	start, end, ok := ExtractManagedBlock(input)
	if !ok || start != 1 || end != 3 {
		t.Fatalf("range = %d,%d ok=%v", start, end, ok)
	}

	cases := []string{
		"# no markers\n127.0.0.1 a.com",
		"# ---- mHost start ----\n127.0.0.1 a.com",
		"127.0.0.1 a.com\n# ---- mHost end ----",
		"# ---- mHost end ----\n127.0.0.1 a.com\n# ---- mHost start ----",
		"# ---- mHost start ----\n# ---- mHost start ----\n# ---- mHost end ----",
		"# ---- mHost start ----\n# ---- mHost end ----\n# ---- mHost end ----",
	}
	for _, c := range cases {
		if _, _, ok := ExtractManagedBlock(c); ok {
			t.Fatalf("expected no block for %q", c)
		}
	}
}

func TestExtractManagedBlockBytes(t *testing.T) {
	input := "# line 1\n# ---- mHost start ----\n127.0.0.1 x.com\n# ---- mHost end ----\n# line 5"
	start, end, ok := ExtractManagedBlockBytes(input)
	if !ok {
		t.Fatal("block not found")
	}
	want := "# ---- mHost start ----\n127.0.0.1 x.com\n# ---- mHost end ----\n"
	if input[start:end] != want {
		t.Fatalf("block bytes = %q, want %q", input[start:end], want)
	}
	if input[end:] != "# line 5" || input[:start] != "# line 1\n" {
		t.Fatalf("surrounding bytes wrong: after=%q before=%q", input[end:], input[:start])
	}

	crlf := "# hdr\r\n# ---- mHost start ----\r\n127.0.0.1 x.com\r\n# ---- mHost end ----\r\n# tail\r\n"
	_, end, ok = ExtractManagedBlockBytes(crlf)
	if !ok || crlf[end:] != "# tail\r\n" {
		t.Fatalf("crlf block end = %d ok=%v after=%q", end, ok, crlf[end:])
	}
}

func TestExtractManagedBlockContent(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"# some comment\n# ---- mHost start ----\n127.0.0.1 example.com\n# ---- mHost end ----\n# tail", "127.0.0.1 example.com", true},
		{"# ---- mHost start ----\n# ---- mHost end ----", "", true},
		{"# ---- mHost start ----\n127.0.0.1 a.com\n192.168.1.1 b.com\n# ---- mHost end ----", "127.0.0.1 a.com\n192.168.1.1 b.com", true},
		{"127.0.0.1 pre-existing.com\n# ---- mHost start ----\n::1 managed.local\n# ---- mHost end ----\n192.168.1.1 post-existing.com", "::1 managed.local", true},
		{"# no block here\n127.0.0.1 example.com", "", false},
	}
	for _, tc := range cases {
		got, ok := ExtractManagedBlockContent(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("input %q: got %q ok=%v, want %q ok=%v", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFormatRoundTrip(t *testing.T) {
	input := "# header comment\n127.0.0.1 example.com\n# footer comment\n"
	result := Parse(input)
	if len(result.Rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(result.Rules))
	}
	formatted := FormatRules(result.Rules)
	if formatted != input {
		t.Fatalf("formatted = %q, want %q", formatted, input)
	}
	reparsed := Parse(formatted)
	if len(reparsed.Rules) != 3 {
		t.Fatalf("reparsed rules = %d", len(reparsed.Rules))
	}
}

func TestFormatMultiDomainExpansionAndComment(t *testing.T) {
	ip := addrPtr(t, "127.0.0.1")
	comment := "dev"
	rule := Rule{IP: ip, Domains: []string{"a.com", "b.com"}, Enabled: true, Comment: &comment}
	if got, want := FormatRules([]Rule{rule}), "127.0.0.1 a.com # dev\n127.0.0.1 b.com # dev\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatManagedBlockEmpty(t *testing.T) {
	if FormatManagedBlock(nil) != "" {
		t.Fatal("empty rules should produce empty block")
	}
	block := FormatManagedBlock([]Rule{{IP: addrPtr(t, "127.0.0.1"), Domains: []string{"x.com"}, Enabled: true}})
	for _, marker := range []string{ManagedStart, "127.0.0.1 x.com", ManagedEnd} {
		if !strings.Contains(block, marker) {
			t.Fatalf("block missing %q: %q", marker, block)
		}
	}
}

func TestIsValidDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"my-site.com", true},
		{"localhost", true},
		{"-bad.com", false},
		{"bad-.com", false},
		{"", false},
		{"bad@com", false},
		{"bad_com", false},
		{"example.com.", false},
		{"1.2.3.4", false},
		{"1.2", false},
		{"12345", false},
		{"0.0.0.0", false},
		{"analytics.163.com", true},
		{"163.com", true},
		{"0.ackzany.com", true},
		{"0.0.0.0.creative.hpyrdr.com", true},
		{"123.example.com", true},
	}
	for _, tc := range cases {
		if got := IsValidDomain(tc.domain); got != tc.want {
			t.Fatalf("IsValidDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestLooksLikeIP(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"127.0.0.1", true},
		{"999.999.999.999", true},
		{"::1", true},
		{"2001:db8::1", true},
		{"::ffff:192.0.2.1", true},
		{"example.com", false},
		{"abc", false},
		{"", false},
		{".", false},
		{"1.", false},
		{"::", true},
		{"abc:def", true},
		{"example.com:8080", false},
		{"http://example.com", false},
		{"2001:0db8:85a3:0000:0000:8a2e:0370:7334", true},
	}
	for _, tc := range cases {
		if got := LooksLikeIP(tc.token); got != tc.want {
			t.Fatalf("LooksLikeIP(%q) = %v, want %v", tc.token, got, tc.want)
		}
	}
}

func TestCheckDuplicates(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
		kind  DuplicateKind
		lines []int
	}{
		{"unique", "127.0.0.1 a.com\n127.0.0.1 b.com", 0, 0, nil},
		{"same_ip", "127.0.0.1 a.com\n127.0.0.1 a.com", 1, DuplicateSameIP, []int{1, 2}},
		{"different_ip", "127.0.0.1 a.com\n192.168.1.1 a.com", 1, DuplicateDifferentIP, []int{1, 2}},
		{"disabled_not_checked", "127.0.0.1 a.com\n# 127.0.0.1 a.com", 0, 0, nil},
		{"comment_only_not_checked", "127.0.0.1 a.com\n# just a comment", 0, 0, nil},
		{"across_multi_domain_rule", "127.0.0.1 a.com b.com\n127.0.0.1 a.com", 1, DuplicateSameIP, []int{1, 2}},
	}
	for _, tc := range cases {
		result := Parse(tc.input)
		if len(result.Duplicates) != tc.want {
			t.Fatalf("%s: duplicates = %v, want %d", tc.name, result.Duplicates, tc.want)
		}
		if tc.want == 1 {
			dup := result.Duplicates[0]
			if dup.Domain != "a.com" || dup.Kind != tc.kind {
				t.Fatalf("%s: dup = %+v", tc.name, dup)
			}
			if len(dup.Lines) != len(tc.lines) {
				t.Fatalf("%s: lines = %v, want %v", tc.name, dup.Lines, tc.lines)
			}
			for i, line := range tc.lines {
				if dup.Lines[i] != line {
					t.Fatalf("%s: lines = %v, want %v", tc.name, dup.Lines, tc.lines)
				}
			}
		}
	}
}
