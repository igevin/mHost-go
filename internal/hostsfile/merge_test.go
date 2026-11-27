package hostsfile

import (
	"strings"
	"testing"
)

func resolved(ip, domain, profile string) ResolvedRule {
	return ResolvedRule{IP: *addrPtr(tTesting, ip), Domain: domain, SourceProfileID: profile, SourceProfileName: profile}
}

// 测试辅助：避免在每个 helper 里传 t。
var tTesting *testing.T

func profileInput(id, name string, enabled bool, rules ...Rule) ProfileInput {
	return ProfileInput{ID: id, Name: name, Enabled: enabled, Rules: rules}
}

func hostRule(ip string, domains ...string) Rule {
	return Rule{IP: addrPtr(tTesting, ip), Domains: domains, Enabled: true}
}

func TestMergeSingleProfileExpandsDomains(t *testing.T) {
	tTesting = t
	p := profileInput("p1", "dev", true, hostRule("127.0.0.1", "a.com", "b.com"))
	result := Merge([]ProfileInput{p})
	if len(result.Rules) != 2 || len(result.Conflicts) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.Rules[0].Domain != "a.com" || result.Rules[1].Domain != "b.com" {
		t.Fatalf("domains = %+v", result.Rules)
	}
	if result.Rules[0].SourceProfileName != "dev" {
		t.Fatalf("source profile = %q", result.Rules[0].SourceProfileName)
	}
}

func TestMergeSameDomainSameIPCollapses(t *testing.T) {
	tTesting = t
	p1 := profileInput("p1", "p1", true, hostRule("127.0.0.1", "x.com"))
	p2 := profileInput("p2", "p2", true, hostRule("127.0.0.1", "x.com"))
	result := Merge([]ProfileInput{p1, p2})
	if len(result.Rules) != 1 || len(result.Conflicts) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMergeConflictExcludesDomainFromRules(t *testing.T) {
	tTesting = t
	p1 := profileInput("p1", "p1", true, hostRule("127.0.0.1", "x.com"))
	p2 := profileInput("p2", "p2", true, hostRule("192.168.1.1", "x.com"))
	p3 := profileInput("p3", "p3", true, hostRule("10.0.0.1", "x.com"))
	result := Merge([]ProfileInput{p1, p2, p3})
	if len(result.Rules) != 0 || len(result.Conflicts) != 1 {
		t.Fatalf("result = %+v", result)
	}
	conflict := result.Conflicts[0]
	if conflict.Domain != "x.com" || len(conflict.Rules) != 3 {
		t.Fatalf("conflict = %+v", conflict)
	}
	// 按 IP 排序
	ips := []string{conflict.Rules[0].IP.String(), conflict.Rules[1].IP.String(), conflict.Rules[2].IP.String()}
	if ips[0] != "10.0.0.1" || ips[1] != "127.0.0.1" || ips[2] != "192.168.1.1" {
		t.Fatalf("ips = %v", ips)
	}
}

func TestMergeDisabledProfileAndRuleIgnored(t *testing.T) {
	tTesting = t
	disabledProfile := profileInput("p1", "p1", false, hostRule("127.0.0.1", "a.com"))
	enabledProfile := profileInput("p2", "p2", true, hostRule("127.0.0.1", "b.com"))
	result := Merge([]ProfileInput{disabledProfile, enabledProfile})
	if len(result.Rules) != 1 || result.Rules[0].Domain != "b.com" {
		t.Fatalf("result = %+v", result)
	}

	disabledRule := Rule{IP: addrPtr(t, "127.0.0.1"), Domains: []string{"a.com"}, Enabled: false}
	p1 := profileInput("p1", "p1", true, disabledRule)
	p2 := profileInput("p2", "p2", true, hostRule("127.0.0.1", "b.com"))
	result = Merge([]ProfileInput{p1, p2})
	if len(result.Rules) != 1 || result.Rules[0].Domain != "b.com" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMergeCommentOnlyRuleIgnored(t *testing.T) {
	tTesting = t
	comment := "# a comment"
	p := profileInput("p1", "p1", true,
		hostRule("127.0.0.1", "a.com"),
		Rule{Enabled: false, Comment: &comment, CommentOnly: true},
	)
	result := Merge([]ProfileInput{p})
	if len(result.Rules) != 1 || result.Rules[0].Domain != "a.com" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMergeEmpty(t *testing.T) {
	result := Merge(nil)
	if len(result.Rules) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMergeDeterministicSort(t *testing.T) {
	tTesting = t
	p := profileInput("p1", "p1", true,
		hostRule("10.0.0.1", "z.com"),
		hostRule("127.0.0.1", "a.com"),
		hostRule("10.0.0.2", "m.com"),
	)
	result := Merge([]ProfileInput{p})
	want := []string{"a.com", "m.com", "z.com"}
	for i, domain := range want {
		if result.Rules[i].Domain != domain {
			t.Fatalf("rules = %+v", result.Rules)
		}
	}
}

func TestCalculateDiffAllNew(t *testing.T) {
	tTesting = t
	current := "# Some existing comment\n"
	rules := []ResolvedRule{resolved("127.0.0.1", "a.com", "p1"), resolved("127.0.0.1", "b.com", "p1")}
	diff := CalculateDiff(current, rules)
	if len(diff.Added) != 2 || len(diff.Removed) != 0 || len(diff.Unchanged) != 0 {
		t.Fatalf("diff = %+v", diff)
	}
}

func TestCalculateDiffNoChangeInBlock(t *testing.T) {
	tTesting = t
	current := "# ---- mHost start ----\n127.0.0.1 a.com\n127.0.0.1 b.com\n# ---- mHost end ----\n"
	rules := []ResolvedRule{resolved("127.0.0.1", "a.com", "p1"), resolved("127.0.0.1", "b.com", "p1")}
	diff := CalculateDiff(current, rules)
	if len(diff.Added) != 0 || len(diff.Removed) != 0 || len(diff.Unchanged) != 2 {
		t.Fatalf("diff = %+v", diff)
	}
}

func TestCalculateDiffIgnoresContentOutsideBlock(t *testing.T) {
	tTesting = t
	current := "127.0.0.1 unmanaged.com\n# ---- mHost start ----\n127.0.0.1 a.com\n# ---- mHost end ----\n"
	rules := []ResolvedRule{resolved("127.0.0.1", "a.com", "p1"), resolved("127.0.0.1", "b.com", "p1")}
	diff := CalculateDiff(current, rules)
	if len(diff.Added) != 1 || diff.Added[0] != "127.0.0.1 b.com" {
		t.Fatalf("added = %v", diff.Added)
	}
	if len(diff.Removed) != 0 {
		t.Fatalf("unmanaged line must not appear in removed: %v", diff.Removed)
	}
}

func TestCalculateDiffPreservesCommentLines(t *testing.T) {
	tTesting = t
	current := "# ---- mHost start ----\n# a comment\n127.0.0.1 a.com\n# ---- mHost end ----\n"
	diff := CalculateDiff(current, nil)
	if len(diff.Removed) != 2 { // 注释行 + 规则行
		t.Fatalf("removed = %v", diff.Removed)
	}
}

func TestBuildHostsContentReplacesBlockPreservingSurroundings(t *testing.T) {
	current := "127.0.0.1 pre-existing.com\n# ---- mHost start ----\n1.1.1.1 old.com\n# ---- mHost end ----\n192.168.1.1 post-existing.com\n"
	block := FormatManagedBlock([]Rule{{IP: addrPtr(t, "2.2.2.2"), Domains: []string{"new.com"}, Enabled: true}})
	got := BuildHostsContent(current, block)
	want := "127.0.0.1 pre-existing.com\n" + block + "192.168.1.1 post-existing.com\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildHostsContentAppendsWhenNoBlock(t *testing.T) {
	block := FormatManagedBlock([]Rule{{IP: addrPtr(t, "2.2.2.2"), Domains: []string{"new.com"}, Enabled: true}})

	got := BuildHostsContent("127.0.0.1 keep.me", block)
	if !strings.HasSuffix(got, "\n"+block) {
		t.Fatalf("no newline inserted: %q", got)
	}

	got = BuildHostsContent("127.0.0.1 keep.me\n", block)
	if !strings.HasSuffix(got, block) {
		t.Fatalf("extra newline inserted: %q", got)
	}
}

func TestBuildHostsContentRemovesBlock(t *testing.T) {
	current := "127.0.0.1 keep.me\n# ---- mHost start ----\n1.1.1.1 old.com\n# ---- mHost end ----\n"
	got := BuildHostsContent(current, "")
	if got != "127.0.0.1 keep.me\n" {
		t.Fatalf("got %q", got)
	}
}

func TestVerify(t *testing.T) {
	tTesting = t
	managed := "# ---- mHost start ----\n127.0.0.1 a.com\n192.168.1.1 b.com\n# ---- mHost end ----"
	rules := []ResolvedRule{resolved("127.0.0.1", "a.com", "p1"), resolved("192.168.1.1", "b.com", "p1")}
	if err := Verify(managed, rules); err != nil {
		t.Fatalf("verify: %v", err)
	}

	missing := []ResolvedRule{resolved("127.0.0.1", "a.com", "p1"), resolved("192.168.1.1", "missing.com", "p1")}
	err := Verify(managed, missing)
	if err == nil || !strings.Contains(err.Error(), "missing.com") {
		t.Fatalf("expected missing.com error, got %v", err)
	}

	if err := Verify("127.0.0.1 unmanaged.com", nil); err != nil {
		t.Fatalf("empty plan on block-free file: %v", err)
	}
	if err := Verify("# ---- mHost start ----\n# ---- mHost end ----", nil); err == nil {
		t.Fatal("empty plan with existing block should fail")
	}

	ipv6 := []ResolvedRule{resolved("::1", "localhost", "p1")}
	if err := Verify("# ---- mHost start ----\n::1 localhost\n# ---- mHost end ----", ipv6); err != nil {
		t.Fatalf("ipv6 verify: %v", err)
	}
}
