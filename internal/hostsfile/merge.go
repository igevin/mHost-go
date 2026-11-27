package hostsfile

import (
	"net/netip"
	"sort"
)

// ResolvedRule 是合并后的扁平规则（一域名一规则），与源项目一致。
type ResolvedRule struct {
	IP                netip.Addr
	Domain            string
	SourceProfileID   string
	SourceProfileName string
}

// RuleConflict 是同一域名映射到多个 IP 的冲突，Rules 按 IP 排序。
type RuleConflict struct {
	Domain string
	Rules  []ResolvedRule
}

// MergeResult 是合并结果：无冲突的扁平规则 + 冲突清单。
type MergeResult struct {
	Rules     []ResolvedRule
	Conflicts []RuleConflict
}

// ProfileInput 是合并器的最小输入：一个启用的 hosts profile。
type ProfileInput struct {
	ID      string
	Name    string
	Enabled bool
	Rules   []Rule
}

// Merge 移植源项目 mhost-apply 的 Merger::merge：
// 展开多域名规则；按 (domain, ip) 去重（保留首个来源）；
// 同域名多 IP 进入冲突（不进 Rules）；输出按域名/ (domain, ip) 排序，
// 保证确定性。
func Merge(profiles []ProfileInput) MergeResult {
	var all []ResolvedRule
	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}
		for _, rule := range profile.Rules {
			if !rule.Enabled || rule.IP == nil {
				continue
			}
			for _, domain := range rule.Domains {
				all = append(all, ResolvedRule{
					IP:                *rule.IP,
					Domain:            domain,
					SourceProfileID:   profile.ID,
					SourceProfileName: profile.Name,
				})
			}
		}
	}

	type domainIP struct {
		domain string
		ip     netip.Addr
	}
	byDomainIP := map[domainIP]ResolvedRule{}
	domainIPs := map[string]map[netip.Addr]bool{}
	for _, rule := range all {
		if _, ok := domainIPs[rule.Domain]; !ok {
			domainIPs[rule.Domain] = map[netip.Addr]bool{}
		}
		domainIPs[rule.Domain][rule.IP] = true
		key := domainIP{domain: rule.Domain, ip: rule.IP}
		if _, exists := byDomainIP[key]; !exists {
			byDomainIP[key] = rule
		}
	}

	// 冲突域名排序（源项目 HashMap 收集后 sort）
	var conflictDomains []string
	for domain, ips := range domainIPs {
		if len(ips) > 1 {
			conflictDomains = append(conflictDomains, domain)
		}
	}
	sort.Strings(conflictDomains)

	conflicts := make([]RuleConflict, 0, len(conflictDomains))
	conflicted := map[string]bool{}
	for _, domain := range conflictDomains {
		conflicted[domain] = true
		var rules []ResolvedRule
		for ip := range domainIPs[domain] {
			rules = append(rules, byDomainIP[domainIP{domain: domain, ip: ip}])
		}
		sort.Slice(rules, func(i, j int) bool { return rules[i].IP.Less(rules[j].IP) })
		conflicts = append(conflicts, RuleConflict{Domain: domain, Rules: rules})
	}

	var rules []ResolvedRule
	for key, rule := range byDomainIP {
		if conflicted[key.domain] {
			continue
		}
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Domain != rules[j].Domain {
			return rules[i].Domain < rules[j].Domain
		}
		return rules[i].IP.Less(rules[j].IP)
	})

	return MergeResult{Rules: rules, Conflicts: conflicts}
}

// ConflictDomains 返回排序去重的冲突域名（源项目 conflict::conflict_domains）。
func ConflictDomains(conflicts []RuleConflict) []string {
	domains := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		domains = append(domains, conflict.Domain)
	}
	sort.Strings(domains)
	out := domains[:0]
	for i, domain := range domains {
		if i == 0 || domain != domains[i-1] {
			out = append(out, domain)
		}
	}
	return out
}

// IsConflicted 报告域名是否处于冲突中（源项目 conflict::is_conflicted）。
func IsConflicted(domain string, conflicts []RuleConflict) bool {
	for _, conflict := range conflicts {
		if conflict.Domain == domain {
			return true
		}
	}
	return false
}
