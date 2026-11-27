package demo

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
)

const (
	managedBlockHeader = "# <mhost-managed>\n"
	managedBlockFooter = "# </mhost-managed>\n"

	devContent       = "# 开发环境\n127.0.0.1 localhost # loopback\n192.168.1.10 api.dev.local db.dev.local\n# 10.0.0.9 old.dev.local\n"
	conflictContent  = "10.0.0.1 api.demo.test\n192.168.1.1 api.demo.test\n10.0.0.1 shared.demo.test\n10.0.0.1 shared.demo.test\n"
	brokenContent    = "not-an-ip broken.test\n127.0.0.1 ok.test\n"
	protectedContent = "127.0.0.1 mhost.internal\n"
	initialBlock     = managedBlockHeader + "127.0.0.1 mhost.internal\n" + managedBlockFooter
)

type hostsProfile struct {
	id        string
	name      string
	content   string
	protected bool
	revision  uint64
}

type hostsPlan struct {
	id           string
	profileID    string
	baseRevision uint64
	merged       string
	conflicts    []app.Conflict
	blockers     []app.StorageIssue
}

type hostsBackup struct {
	app.ApplyBackupRef
	prevProfileID   string
	prevProfileName string
}

// HostsService is an in-memory demo implementation of app.HostsService. It
// never touches /etc/hosts; the managed block lives in memory and can be
// failed or drifted on demand to exercise the UI.
type HostsService struct {
	mu         sync.Mutex
	sequence   int
	profiles   map[string]*hostsProfile
	order      []string
	plans      map[string]*hostsPlan
	backups    []hostsBackup
	block      string
	appliedID  string
	appliedNm  string
	drifted    bool
	failApply  bool
	failVerify bool
}

func NewHostsService() *HostsService {
	service := &HostsService{
		profiles: map[string]*hostsProfile{},
		plans:    map[string]*hostsPlan{},
	}
	service.seed("dev", "开发环境", devContent, false)
	service.seed("conflict", "冲突样本", conflictContent, false)
	service.seed("broken", "解析错误样本", brokenContent, false)
	service.seed("protected", "内置系统", protectedContent, true)
	service.block = initialBlock
	service.appliedID = "protected"
	service.appliedNm = "内置系统"
	return service
}

func (s *HostsService) seed(id, name, content string, protected bool) {
	s.sequence++
	s.profiles[id] = &hostsProfile{id: id, name: name, content: content, protected: protected, revision: 1}
	s.order = append(s.order, id)
}

func (s *HostsService) ListProfiles(ctx context.Context) ([]app.HostsProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles := make([]app.HostsProfile, 0, len(s.order))
	for _, id := range s.order {
		profile := s.profiles[id]
		profiles = append(profiles, app.HostsProfile{
			ID:        profile.id,
			Name:      profile.name,
			Enabled:   true,
			Protected: profile.protected,
			RuleCount: countRules(profile.content),
			Revision:  profile.revision,
		})
	}
	return profiles, nil
}

func (s *HostsService) LoadProfile(ctx context.Context, profileID string) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.profile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: app.HostsProfile{
			ID:        profile.id,
			Name:      profile.name,
			Enabled:   true,
			Protected: profile.protected,
			RuleCount: countRules(profile.content),
			Revision:  profile.revision,
		},
		Content: profile.content,
	}, nil
}

func (s *HostsService) SaveProfile(ctx context.Context, profileID string, content string, baseRevision uint64) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.profile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	if profile.revision != baseRevision {
		return app.HostsProfileDetail{}, app.ErrStaleOperation
	}
	profile.content = content
	profile.revision++
	return app.HostsProfileDetail{
		HostsProfile: app.HostsProfile{
			ID:        profile.id,
			Name:      profile.name,
			Enabled:   true,
			Protected: profile.protected,
			RuleCount: countRules(profile.content),
			Revision:  profile.revision,
		},
		Content: profile.content,
	}, nil
}

func (s *HostsService) PreviewApply(ctx context.Context, profileID string, resolutions map[string]string) (app.ApplyPlan, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.profile(profileID)
	if err != nil {
		return app.ApplyPlan{}, err
	}
	rules := parseRules(profile.content)
	conflicts := findConflicts(rules)
	blockers := findBlockers(rules)
	merged := managedBlockHeader + renderMerged(rules, validResolutions(conflicts, resolutions)) + managedBlockFooter
	plan := &hostsPlan{
		id:           s.nextID("plan"),
		profileID:    profile.id,
		baseRevision: profile.revision,
		merged:       merged,
		conflicts:    conflicts,
		blockers:     blockers,
	}
	s.plans[plan.id] = plan
	return app.ApplyPlan{
		ID:             plan.id,
		ProfileID:      profile.id,
		ProfileName:    profile.name,
		BaseRevision:   profile.revision,
		CurrentBlock:   s.block,
		MergedBlock:    merged,
		Conflicts:      cloneConflicts(conflicts),
		BackupRequired: true,
		Blockers:       append([]app.StorageIssue(nil), blockers...),
	}, nil
}

func (s *HostsService) Apply(ctx context.Context, planID string, resolutions map[string]string) (app.ApplyReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return app.ApplyReceipt{}, app.ErrStaleOperation
	}
	profile, err := s.profile(plan.profileID)
	if err != nil {
		return app.ApplyReceipt{}, err
	}
	if profile.revision != plan.baseRevision {
		return app.ApplyReceipt{}, app.ErrStaleOperation
	}
	if len(plan.blockers) != 0 {
		return app.ApplyReceipt{}, demoError(app.ErrorValidation, "Resolve parse errors before applying")
	}
	if err := validateResolutions(plan.conflicts, resolutions); err != nil {
		return app.ApplyReceipt{}, err
	}
	delete(s.plans, planID)

	s.sequence++
	operationID := fmt.Sprintf("op-%d", s.sequence)
	backup := hostsBackup{
		ApplyBackupRef: app.ApplyBackupRef{
			ID:           s.nextID("backup"),
			ProfileID:    profile.id,
			ProfileName:  profile.name,
			CreatedAt:    time.Now().UTC(),
			OperationID:  operationID,
			BlockContent: s.block,
		},
		prevProfileID:   s.appliedID,
		prevProfileName: s.appliedNm,
	}
	s.backups = append(s.backups, backup)
	receipt := app.ApplyReceipt{
		OperationID: operationID,
		ProfileID:   profile.id,
		Results: []app.ApplyResult{
			{Entity: "backup", Status: app.WriteWritten, Backup: &backup.ApplyBackupRef},
		},
		RollbackRef: &backup.ApplyBackupRef,
	}
	if s.failApply {
		s.failApply = false
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteFailed, Issue: blockerIssue("managed block write failed (injected failure)")},
			app.ApplyResult{Entity: "verification", Status: app.WriteSkipped},
		)
		return receipt, demoError(app.ErrorWrite, "Managed block write failed; the previous block is preserved")
	}
	s.block = managedBlockHeader + renderMerged(parseRules(profile.content), resolutions) + managedBlockFooter
	s.appliedID = profile.id
	s.appliedNm = profile.name
	s.drifted = false
	receipt.Results = append(receipt.Results,
		app.ApplyResult{Entity: "managed_block", Status: app.WriteWritten},
	)
	if s.failVerify {
		s.failVerify = false
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "verification", Status: app.WriteWritten, Issue: blockerIssue("readback does not match the merged block (injected mismatch)")},
		)
		return receipt, demoError(app.ErrorReadback, "Readback verification failed after applying")
	}
	receipt.Results = append(receipt.Results,
		app.ApplyResult{Entity: "verification", Status: app.WriteWritten, ReadbackVerified: true},
	)
	return receipt, nil
}

func (s *HostsService) Rollback(ctx context.Context, backupID string) (app.ApplyReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.backups) - 1; i >= 0; i-- {
		backup := s.backups[i]
		if backup.ID != backupID {
			continue
		}
		s.sequence++
		operationID := fmt.Sprintf("op-%d", s.sequence)
		s.block = backup.BlockContent
		s.appliedID = backup.prevProfileID
		s.appliedNm = backup.prevProfileName
		s.drifted = false
		return app.ApplyReceipt{
			OperationID: operationID,
			ProfileID:   backup.prevProfileID,
			Results: []app.ApplyResult{
				{Entity: "backup", Status: app.WriteWritten, Backup: &backup.ApplyBackupRef},
				{Entity: "managed_block", Status: app.WriteWritten},
				{Entity: "verification", Status: app.WriteWritten, ReadbackVerified: true},
			},
		}, nil
	}
	return app.ApplyReceipt{}, demoError(app.ErrorNotFound, "No apply backup with that ID")
}

func (s *HostsService) ListApplyBackups(ctx context.Context) ([]app.ApplyBackupRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backups := make([]app.ApplyBackupRef, 0, len(s.backups))
	for i := len(s.backups) - 1; i >= 0; i-- {
		backups = append(backups, s.backups[i].ApplyBackupRef)
	}
	return backups, nil
}

func (s *HostsService) AppliedState(ctx context.Context) (app.AppliedState, error) {
	if err := ctx.Err(); err != nil {
		return app.AppliedState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return app.AppliedState{ProfileID: s.appliedID, ProfileName: s.appliedNm, Drifted: s.drifted}, nil
}

// FailNextApply makes the next Apply fail at the managed-block write, after
// the backup was created.
func (s *HostsService) FailNextApply() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failApply = true
}

// FailNextReadback makes the next Apply succeed at the write but fail the
// readback verification.
func (s *HostsService) FailNextReadback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failVerify = true
}

// SetDrift marks the managed block as drifted from what mHost last wrote.
func (s *HostsService) SetDrift(drifted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drifted = drifted
}

func (s *HostsService) profile(id string) (*hostsProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return nil, demoError(app.ErrorNotFound, "No hosts profile with that ID")
	}
	return profile, nil
}

func (s *HostsService) nextID(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}

type demoRule struct {
	line    int // 1-based
	raw     string
	ip      string
	domains []string
	enabled bool
	valid   bool
}

func parseRules(content string) []demoRule {
	var rules []demoRule
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		enabled := !strings.HasPrefix(line, "#")
		rule := demoRule{line: i + 1, raw: raw, enabled: enabled}
		if !enabled {
			line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if line == "" {
				continue
			}
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		rule.ip = fields[0]
		rule.domains = fields[1:]
		if _, err := netip.ParseAddr(rule.ip); err == nil {
			rule.valid = true
		}
		rules = append(rules, rule)
	}
	return rules
}

func countRules(content string) int {
	count := 0
	for _, rule := range parseRules(content) {
		if rule.enabled && rule.valid && len(rule.domains) > 0 {
			count++
		}
	}
	return count
}

func findConflicts(rules []demoRule) []app.Conflict {
	byDomain := map[string][]demoRule{}
	var domains []string
	for _, rule := range rules {
		if !rule.enabled || !rule.valid {
			continue
		}
		for _, domain := range rule.domains {
			if _, ok := byDomain[domain]; !ok {
				domains = append(domains, domain)
			}
			byDomain[domain] = append(byDomain[domain], rule)
		}
	}
	sort.Strings(domains)
	var conflicts []app.Conflict
	for _, domain := range domains {
		occurrences := byDomain[domain]
		ips := []string{}
		for _, occurrence := range occurrences {
			if !contains(ips, occurrence.ip) {
				ips = append(ips, occurrence.ip)
			}
		}
		if len(ips) < 2 {
			continue
		}
		conflict := app.Conflict{Domain: domain, IPs: ips}
		for _, occurrence := range occurrences {
			conflict.Lines = append(conflict.Lines, occurrence.line)
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts
}

func findBlockers(rules []demoRule) []app.StorageIssue {
	var blockers []app.StorageIssue
	for _, rule := range rules {
		if rule.enabled && !rule.valid {
			blockers = append(blockers, app.StorageIssue{
				Code:        "invalid_ip",
				Severity:    app.SeverityError,
				Message:     fmt.Sprintf("Line %d: %q is not a valid IP address", rule.line, rule.ip),
				Detail:      rule.raw,
				Recoverable: true,
			})
		}
	}
	return blockers
}

func validateResolutions(conflicts []app.Conflict, resolutions map[string]string) error {
	for _, conflict := range conflicts {
		choice, ok := resolutions[conflict.Domain]
		if !ok {
			return demoError(app.ErrorValidation, fmt.Sprintf("Choose which IP to keep for %q", conflict.Domain))
		}
		if !contains(conflict.IPs, choice) {
			return demoError(app.ErrorValidation, fmt.Sprintf("Resolution for %q must be one of its conflicting IPs", conflict.Domain))
		}
	}
	return nil
}

// validResolutions keeps only entries that match a real conflict choice, so a
// preview renders pending user choices while ignoring stale or invalid ones.
func validResolutions(conflicts []app.Conflict, resolutions map[string]string) map[string]string {
	if len(resolutions) == 0 {
		return nil
	}
	valid := map[string]string{}
	for _, conflict := range conflicts {
		if choice, ok := resolutions[conflict.Domain]; ok && contains(conflict.IPs, choice) {
			valid[conflict.Domain] = choice
		}
	}
	return valid
}

// renderMerged renders the active rules of the managed block. Same-IP
// duplicates collapse to the first occurrence; conflicting domains render
// only the lines whose IP was chosen (nil resolutions keep the first IP).
func renderMerged(rules []demoRule, resolutions map[string]string) string {
	chosen := map[string]string{}
	for _, rule := range rules {
		if !rule.enabled || !rule.valid {
			continue
		}
		for _, domain := range rule.domains {
			if _, decided := chosen[domain]; decided {
				continue
			}
			keep := rule
			for _, other := range rules {
				if other.line == rule.line || !other.enabled || !other.valid || !contains(other.domains, domain) || other.ip == rule.ip {
					continue
				}
				keepIP := resolutions[domain]
				if keepIP == "" {
					keepIP = rule.ip
				}
				if other.ip == keepIP {
					keep = other
				}
			}
			chosen[domain] = keep.ip
		}
	}
	var lines []string
	seen := map[string]bool{}
	for _, rule := range rules {
		if !rule.enabled || !rule.valid {
			continue
		}
		active := make([]string, 0, len(rule.domains))
		for _, domain := range rule.domains {
			if chosen[domain] == rule.ip {
				active = append(active, domain)
			}
		}
		if len(active) == 0 {
			continue
		}
		rendered := rule.ip + " " + strings.Join(active, " ")
		if !seen[rendered] {
			seen[rendered] = true
			lines = append(lines, rendered)
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func cloneConflicts(conflicts []app.Conflict) []app.Conflict {
	cloned := make([]app.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		cloned = append(cloned, app.Conflict{
			Domain: conflict.Domain,
			Lines:  append([]int(nil), conflict.Lines...),
			IPs:    append([]string(nil), conflict.IPs...),
		})
	}
	return cloned
}

func blockerIssue(message string) *app.StorageIssue {
	return &app.StorageIssue{Code: "injected", Severity: app.SeverityError, Message: message, Recoverable: true}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
