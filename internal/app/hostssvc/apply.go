package hostssvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/elevate"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

// applyPlan 是 PreviewApply 产出的计划（内存态，进程重启即失效）。
type applyPlan struct {
	id           string
	profileID    string
	baseRevision uint64
	mergedBlock  string
	resolved     []hostsfile.ResolvedRule
	conflicts    []app.Conflict
	blockers     []app.StorageIssue
}

// appliedStateRecord 记录当前管理块归属（applied-state.json，
// 存于备份目录）。Drift = 当前管理块内容哈希 ≠ 记录哈希。
type appliedStateRecord struct {
	ProfileID   string    `json:"profile_id"`
	ProfileName string    `json:"profile_name"`
	OperationID string    `json:"operation_id"`
	AppliedAt   time.Time `json:"applied_at"`
	BlockSHA256 string    `json:"block_sha256"`
}

const appliedStateFile = "apply-state.json"

// maxPlans 限制内存中未消费计划数量，防泄漏。
const maxPlans = 256

// PreviewApply 生成改写计划：解析 profile → 冲突检测 → 渲染合并块。
// resolutions 携带待定的保留选择；未解决的冲突按首个出现 IP 渲染
// （与 demo 语义一致）。Blockers 来自解析错误——SaveProfile 已拒绝
// 错误文本，这里是防御性兜底。
func (s *Service) PreviewApply(ctx context.Context, profileID string, resolutions map[string]string) (app.ApplyPlan, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyPlan{}, err
	}
	profile, err := s.loadProfile(profileID)
	if err != nil {
		return app.ApplyPlan{}, err
	}
	// 与源项目合并器语义一致：禁用的 profile 不进入系统 hosts。
	// 存储态 Enable ≠ 已应用；停用后须先启用才能预览/应用。
	if !profile.Enabled {
		return app.ApplyPlan{}, &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Profile %q is disabled; enable it before applying", profile.Name)}
	}

	parsed, blockers := parseProfileContent(profile)
	conflicts := findConflicts(parsed.Rules)
	resolved := resolveRules(parsed.Rules, conflicts, resolutions)
	mergedBlock := hostsfile.FormatManagedBlock(rulesFromResolved(resolved))

	s.mu.Lock()
	plan := &applyPlan{
		id:           s.newID(),
		profileID:    profile.ID,
		baseRevision: profileRevision(profile),
		mergedBlock:  mergedBlock,
		resolved:     resolved,
		conflicts:    cloneConflicts(conflicts),
		blockers:     blockers,
	}
	if len(s.plans) >= maxPlans {
		for key := range s.plans {
			delete(s.plans, key)
			break
		}
	}
	s.plans[plan.id] = plan
	s.mu.Unlock()

	return app.ApplyPlan{
		ID:             plan.id,
		ProfileID:      profile.ID,
		ProfileName:    profile.Name,
		BaseRevision:   plan.baseRevision,
		CurrentBlock:   s.currentBlockText(),
		MergedBlock:    mergedBlock,
		Conflicts:      cloneConflicts(conflicts),
		BackupRequired: true,
		Blockers:       append([]app.StorageIssue(nil), blockers...),
	}, nil
}

// Apply 执行计划：Apply Lock 串行化 → 校验计划/修订/冲突选择 →
// 备份 + 原子写 + 读回验证（验证失败自动回滚）→ 记录 applied-state。
// 回执形状与 demo 实现一致。
func (s *Service) Apply(ctx context.Context, planID string, resolutions map[string]string) (app.ApplyReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyReceipt{}, err
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	s.mu.Lock()
	plan, ok := s.plans[planID]
	s.mu.Unlock()
	if !ok {
		return app.ApplyReceipt{}, app.ErrStaleOperation
	}
	profile, err := s.loadProfile(plan.profileID)
	if err != nil {
		return app.ApplyReceipt{}, err
	}
	if profileRevision(profile) != plan.baseRevision {
		s.dropPlan(planID)
		return app.ApplyReceipt{}, app.ErrStaleOperation
	}
	if len(plan.blockers) > 0 {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorValidation, Message: "Resolve parse errors before applying"}
	}
	if !profile.Enabled {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Profile %q is disabled; enable it before applying", profile.Name)}
	}
	if err := validateResolutions(plan.conflicts, resolutions); err != nil {
		return app.ApplyReceipt{}, err
	}
	s.dropPlan(planID)

	// 用调用方给出的保留选择重新计算解析结果。修订号校验保证
	// profile 自预览以来未变，重算结果与预览时一致（默认选择除外）。
	parsed, blockers := parseProfileContent(profile)
	if len(blockers) > 0 {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorValidation, Message: "Resolve parse errors before applying"}
	}
	resolved := resolveRules(parsed.Rules, plan.conflicts, resolutions)
	mergedBlock := hostsfile.FormatManagedBlock(rulesFromResolved(resolved))

	operationID := s.operation.new()
	prevState, _ := s.readAppliedState()

	meta := hostsfile.BackupRecord{
		ProfileID:       profile.ID,
		ProfileName:     profile.Name,
		PrevProfileID:   prevState.ProfileID,
		PrevProfileName: prevState.ProfileName,
		OperationID:     operationID,
	}
	backup, prevContent, writeErr := s.writer.Apply(resolved, true, meta)
	if backup != nil {
		// #35：补标操作结果供历史页展示；补标失败不影响主流程。
		_ = s.writer.MarkBackupResult(backup.ID, backupResultForError(writeErr))
	}
	receipt := app.ApplyReceipt{OperationID: operationID, ProfileID: profile.ID}
	if backup != nil {
		ref := s.backupRef(backup, prevContent, storageProfileRef{ID: profile.ID, Name: profile.Name})
		receipt.Results = append(receipt.Results, app.ApplyResult{Entity: "backup", Status: app.WriteWritten, Backup: ref})
		receipt.RollbackRef = ref
	}

	if writeErr != nil {
		return s.applyFailureReceipt(receipt, writeErr)
	}

	newState := appliedStateRecord{
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		OperationID: operationID,
		AppliedAt:   s.now(),
		BlockSHA256: sha256HexString(mergedBlock),
	}
	if err := s.writeAppliedState(newState); err != nil {
		// 管理块已写入但状态记录失败：写入本身是成功的，报告 internal
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteWritten},
			app.ApplyResult{Entity: "verification", Status: app.WriteWritten, ReadbackVerified: true},
		)
		return receipt, &app.UIError{Kind: app.ErrorInternal, Message: "Applied, but failed to record applied state", Cause: err}
	}

	receipt.Results = append(receipt.Results,
		app.ApplyResult{Entity: "managed_block", Status: app.WriteWritten},
		app.ApplyResult{Entity: "verification", Status: app.WriteWritten, ReadbackVerified: true},
	)
	return receipt, nil
}

// applyFailureReceipt 把 writer 错误按哨兵类型分类为回执 + UIError。
// 授权取消：未触碰系统；写入失败：原块保留；验证失败：writer 已自动
// 回滚；备份失败：未触碰系统。
func (s *Service) applyFailureReceipt(receipt app.ApplyReceipt, err error) (app.ApplyReceipt, error) {
	message := err.Error()
	switch {
	case errors.Is(err, hostsfile.ErrReadback):
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteWritten},
			app.ApplyResult{Entity: "verification", Status: app.WriteWritten, Issue: &app.StorageIssue{
				Code: "readback_mismatch", Severity: app.SeverityError,
				Message: "Readback does not match the merged block", Detail: message, Recoverable: true,
			}},
		)
		return receipt, &app.UIError{Kind: app.ErrorReadback, Message: "Readback verification failed after applying", Detail: message, Cause: err}
	case errors.Is(err, elevate.ErrCanceled):
		// 用户在管理员授权框取消：系统 hosts 未被触碰，与写失败区分。
		// 写前备份虽已生成，但内容与原文件一致，回执不再给 RollbackRef，
		// 避免 UI 引导一次无意义（且会再次索要密码）的回写。
		receipt.RollbackRef = nil
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteSkipped, Issue: &app.StorageIssue{
				Code: "authorization_canceled", Severity: app.SeverityWarning,
				Message: "Administrator authorization was canceled; the system hosts was not changed", Recoverable: true,
			}},
			app.ApplyResult{Entity: "verification", Status: app.WriteSkipped},
		)
		return receipt, &app.UIError{Kind: app.ErrorPermission, Message: "Administrator authorization was canceled; the system hosts was not changed", Cause: err}
	case errors.Is(err, elevate.ErrAuthFailed):
		// 密码错误等授权失败：hosts 未被触碰，修正密码后可重试。
		// 同取消分支，不再给 RollbackRef。
		receipt.RollbackRef = nil
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteSkipped, Issue: &app.StorageIssue{
				Code: "authorization_failed", Severity: app.SeverityWarning,
				Message: "Administrator authorization failed; the system hosts was not changed", Recoverable: true,
			}},
			app.ApplyResult{Entity: "verification", Status: app.WriteSkipped},
		)
		return receipt, &app.UIError{Kind: app.ErrorPermission, Message: "Administrator authorization failed; the system hosts was not changed. Check the password and try again", Retryable: true, Cause: err}
	case errors.Is(err, hostsfile.ErrBackup):
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "backup", Status: app.WriteFailed, Issue: &app.StorageIssue{
				Code: "backup_failed", Severity: app.SeverityError,
				Message: "Backup failed; the system hosts was not touched", Detail: message, Recoverable: true,
			}},
			app.ApplyResult{Entity: "managed_block", Status: app.WriteSkipped},
			app.ApplyResult{Entity: "verification", Status: app.WriteSkipped},
		)
		return receipt, &app.UIError{Kind: app.ErrorBackup, Message: "Backup failed; nothing was written", Detail: message, Retryable: true, Cause: err}
	default:
		receipt.Results = append(receipt.Results,
			app.ApplyResult{Entity: "managed_block", Status: app.WriteFailed, Issue: &app.StorageIssue{
				Code: "write_failed", Severity: app.SeverityError,
				Message: "Managed block write failed; the previous block is preserved", Detail: message, Recoverable: true,
			}},
			app.ApplyResult{Entity: "verification", Status: app.WriteSkipped},
		)
		return receipt, &app.UIError{Kind: app.ErrorWrite, Message: "Managed block write failed; the previous block is preserved", Detail: message, Retryable: true, Cause: err}
	}
}

func (s *Service) dropPlan(planID string) {
	s.mu.Lock()
	delete(s.plans, planID)
	s.mu.Unlock()
}

// Rollback 按备份 ID 恢复系统 hosts，并把 applied-state 还给备份时的
// 上一个 profile。回执与 demo 形状一致。
//
// #35：恢复不再限于最近回执——任意历史备份都可恢复。写前生成安全
// 备份（内容 = 当前系统 hosts，归属恢复后的 profile，Prev = 恢复前
// 的 applied），恢复本身可经回执 RollbackRef 撤销；安全备份结果随
// 恢复成败补标。原有语义保留：管理员授权、原子写、读回验证。
func (s *Service) Rollback(ctx context.Context, backupID string) (app.ApplyReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.ApplyReceipt{}, err
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	operationID := s.operation.new()

	record, err := s.findBackupRecord(backupID)
	if err != nil {
		return app.ApplyReceipt{}, err
	}
	current, err := s.writer.CurrentContent()
	if err != nil {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorPermission, Message: "Cannot read the system hosts file before restoring", Detail: err.Error(), Cause: err}
	}
	prevState, _ := s.readAppliedState()

	// 写前安全备份：恢复失败或后悔时系统状态可寻回。
	safety, serr := s.writer.CreateBackupForProfile(current, hostsfile.BackupRecord{
		ProfileID:       record.PrevProfileID,
		ProfileName:     record.PrevProfileName,
		PrevProfileID:   prevState.ProfileID,
		PrevProfileName: prevState.ProfileName,
		OperationID:     operationID,
	})
	if serr != nil {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorBackup, Message: "Backup failed; nothing was restored", Detail: serr.Error(), Retryable: true, Cause: serr}
	}

	record, err = s.writer.RollbackTo(backupID)
	if err != nil {
		// 恢复未完成：系统 hosts 保持原样（读回失败除外，见 writer）。
		// 安全备份照旧保留并补标，历史页可见这次未完成的恢复。
		_ = s.writer.MarkBackupResult(safety.ID, backupResultForError(err))
		var uiErr *app.UIError
		if errors.As(err, &uiErr) {
			return app.ApplyReceipt{}, uiErr
		}
		// 恢复同样可能弹管理员授权框：取消/密码错误与"恢复失败"区分，
		// 语义与 Apply 的失败分类一致（hosts 未被改动）。
		if errors.Is(err, elevate.ErrCanceled) {
			return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorPermission, Message: "Administrator authorization was canceled; the system hosts was not changed", Cause: err}
		}
		if errors.Is(err, elevate.ErrAuthFailed) {
			return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorPermission, Message: "Administrator authorization failed; the system hosts was not changed. Check the password and try again", Retryable: true, Cause: err}
		}
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorBackup, Message: "Restore failed", Detail: err.Error(), Retryable: true, Cause: err}
	}
	_ = s.writer.MarkBackupResult(safety.ID, app.BackupResultApplied)

	// 恢复 applied-state：归属备份时记录的前一个 profile；
	// 备份时无前置应用（PrevProfileID 为空且恢复后无块）则清零状态。
	restored, rerr := s.writer.CurrentContent()
	state := appliedStateRecord{
		ProfileID:   record.PrevProfileID,
		ProfileName: record.PrevProfileName,
		OperationID: operationID,
		AppliedAt:   s.now(),
	}
	if rerr == nil {
		if block, ok := managedBlockText(string(restored)); ok {
			state.BlockSHA256 = sha256HexString(block)
		}
	}
	if state.ProfileID == "" && state.BlockSHA256 == "" {
		state = appliedStateRecord{}
	}
	if werr := s.writeAppliedState(state); werr != nil {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorInternal, Message: "Rolled back, but failed to record applied state", Cause: werr}
	} else if rerr != nil {
		return app.ApplyReceipt{}, &app.UIError{Kind: app.ErrorInternal, Message: "Rolled back, but failed to read applied state", Cause: rerr}
	}

	ref := s.backupRef(record, restored, storageProfileRef{ID: record.ProfileID, Name: record.ProfileName})
	safetyRef := s.backupRef(safety, current, storageProfileRef{ID: safety.ProfileID, Name: safety.ProfileName})
	return app.ApplyReceipt{
		OperationID: operationID,
		ProfileID:   record.PrevProfileID,
		Results: []app.ApplyResult{
			{Entity: "backup", Status: app.WriteWritten, Backup: ref},
			{Entity: "managed_block", Status: app.WriteWritten},
			{Entity: "verification", Status: app.WriteWritten, ReadbackVerified: true},
		},
		RollbackRef: safetyRef, // 撤销本次恢复的安全备份
	}, nil
}

// findBackupRecord 按 ID 定位索引中的备份记录；找不到时返回 ErrorNotFound
// （与 demo 语义一致），索引不可读归为 ErrorBackup。
func (s *Service) findBackupRecord(backupID string) (*hostsfile.BackupRecord, error) {
	records, err := s.writer.ListBackups()
	if err != nil {
		return nil, &app.UIError{Kind: app.ErrorBackup, Message: "Cannot list apply backups", Detail: err.Error(), Cause: err}
	}
	for i := range records {
		if records[i].ID == backupID {
			return &records[i], nil
		}
	}
	return nil, &app.UIError{Kind: app.ErrorNotFound, Message: fmt.Sprintf("No apply backup with ID %q", backupID)}
}

// backupResultForError 把一次写入/恢复的结果映射为历史页操作标记：
// 成功 applied；管理员授权取消 canceled；其余（写失败、读回失败、
// 授权失败等）failed。
func backupResultForError(writeErr error) string {
	switch {
	case writeErr == nil:
		return app.BackupResultApplied
	case errors.Is(writeErr, elevate.ErrCanceled):
		return app.BackupResultCanceled
	default:
		return app.BackupResultFailed
	}
}

// ListApplyBackups 返回备份引用（含旧管理块内容），最新在前。
func (s *Service) ListApplyBackups(ctx context.Context) ([]app.ApplyBackupRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := s.writer.ListBackups()
	if err != nil {
		return nil, err
	}
	refs := make([]app.ApplyBackupRef, 0, len(records))
	for i := range records {
		refs = append(refs, *s.backupRef(&records[i], nil, storageProfileRef{ID: records[i].ProfileID, Name: records[i].ProfileName}))
	}
	return refs, nil
}

// AppliedState 报告管理块当前归属与 drift：
// 读系统 hosts → 提取管理块 → 哈希与 applied-state 比对。
// 「无块」按空串参与哈希比较：应用空 profile（无管理块写入）后
// 哈希相等不报 drift；记录非空但块被外部删除时哈希不等仍报 drift
// （review 建议项：应用空规则集后不再误报漂移）。
func (s *Service) AppliedState(ctx context.Context) (app.AppliedState, error) {
	if err := ctx.Err(); err != nil {
		return app.AppliedState{}, err
	}
	content, err := s.writer.CurrentContent()
	if err != nil {
		return app.AppliedState{}, &app.UIError{Kind: app.ErrorPermission, Message: "Cannot read system hosts file", Detail: err.Error(), Cause: err}
	}
	block, hasBlock := managedBlockText(string(content))
	if !hasBlock {
		block = ""
	}

	record, recErr := s.readAppliedState()
	if recErr != nil || record.ProfileID == "" {
		// 本服务无记录：块存在即 drift（外部/旧版应用写入）
		return app.AppliedState{Drifted: hasBlock}, nil
	}
	drifted := sha256HexString(block) != record.BlockSHA256
	return app.AppliedState{
		ProfileID:   record.ProfileID,
		ProfileName: record.ProfileName,
		Drifted:     drifted,
	}, nil
}

// --- 解析 / 冲突 / 解决 ---

// parseProfileContent 把存储规则渲染回文本再解析，得到行号相关的
// 结构化结果（冲突行号、阻断清单）。渲染是确定性的，行号可复现。
func parseProfileContent(profile storage.Profile) (hostsfile.ParseResult, []app.StorageIssue) {
	parsed := hostsfile.Parse(profileContent(profile))
	var blockers []app.StorageIssue
	for _, err := range parsed.Errors {
		blockers = append(blockers, parseErrorIssue(err))
	}
	return parsed, blockers
}

func parseErrorIssue(err hostsfile.ParseError) app.StorageIssue {
	issue := app.StorageIssue{Severity: app.SeverityError, Recoverable: true}
	switch err.Kind {
	case hostsfile.ErrInvalidIP:
		issue.Code = "invalid_ip"
		issue.Message = fmt.Sprintf("Line %d: %q is not a valid IP address", err.Line, err.Token)
	case hostsfile.ErrMalformedLine:
		issue.Code = "malformed_line"
		issue.Message = fmt.Sprintf("Line %d: %q is not a valid hosts rule", err.Line, err.Token)
	case hostsfile.ErrInvalidDomain:
		issue.Code = "invalid_domain"
		issue.Message = fmt.Sprintf("Line %d: %q is not a valid domain", err.Line, err.Token)
	}
	issue.Detail = err.Token
	return issue
}

// findConflicts 把 DifferentIP 重复升级为冲突对象（与 demo 语义一致）。
func findConflicts(rules []hostsfile.Rule) []app.Conflict {
	duplicates := hostsfile.CheckDuplicates(rules)
	var conflicts []app.Conflict
	for _, dup := range duplicates {
		if dup.Kind != hostsfile.DuplicateDifferentIP {
			continue
		}
		ips := conflictingIPs(rules, dup.Domain)
		conflicts = append(conflicts, app.Conflict{Domain: dup.Domain, Lines: dup.Lines, IPs: ips})
	}
	return conflicts
}

// conflictingIPs 收集某域名在启用规则中出现过的全部 IP（排序去重）。
func conflictingIPs(rules []hostsfile.Rule, domain string) []string {
	set := map[string]bool{}
	for _, rule := range rules {
		if !rule.Enabled || rule.IP == nil {
			continue
		}
		for _, d := range rule.Domains {
			if d == domain {
				set[rule.IP.String()] = true
			}
		}
	}
	ips := make([]string, 0, len(set))
	for ip := range set {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	return ips
}

// resolveRules 计算应用后的扁平规则：每个域名保留一个 IP
// （resolutions 有效选择优先，默认首个出现的行），输出按 (domain, ip) 排序。
func resolveRules(rules []hostsfile.Rule, conflicts []app.Conflict, resolutions map[string]string) []hostsfile.ResolvedRule {
	validChoice := map[string]string{}
	for _, conflict := range conflicts {
		if choice, ok := resolutions[conflict.Domain]; ok && contains(conflict.IPs, choice) {
			validChoice[conflict.Domain] = choice
		}
	}

	chosen := map[string]string{}
	for _, rule := range rules {
		if !rule.Enabled || rule.IP == nil {
			continue
		}
		for _, domain := range rule.Domains {
			if _, decided := chosen[domain]; decided {
				continue
			}
			if choice, ok := validChoice[domain]; ok {
				chosen[domain] = choice
			} else {
				chosen[domain] = rule.IP.String()
			}
		}
	}

	var resolved []hostsfile.ResolvedRule
	for domain, ip := range chosen {
		parsed, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		resolved = append(resolved, hostsfile.ResolvedRule{IP: parsed, Domain: domain})
	}
	sort.Slice(resolved, func(i, j int) bool {
		if resolved[i].Domain != resolved[j].Domain {
			return resolved[i].Domain < resolved[j].Domain
		}
		return resolved[i].IP.Less(resolved[j].IP)
	})
	return resolved
}

func validateResolutions(conflicts []app.Conflict, resolutions map[string]string) error {
	for _, conflict := range conflicts {
		choice, ok := resolutions[conflict.Domain]
		if !ok {
			return &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Choose which IP to keep for %q", conflict.Domain)}
		}
		if !contains(conflict.IPs, choice) {
			return &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Resolution for %q must be one of its conflicting IPs", conflict.Domain)}
		}
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func rulesFromResolved(resolved []hostsfile.ResolvedRule) []hostsfile.Rule {
	rules := make([]hostsfile.Rule, 0, len(resolved))
	for _, r := range resolved {
		ip := r.IP
		rules = append(rules, hostsfile.Rule{IP: &ip, Domains: []string{r.Domain}, Enabled: true})
	}
	return rules
}

// --- applied-state 读写 ---

func (s *Service) statePath() string {
	return filepath.Join(s.writer.BackupDir(), appliedStateFile)
}

func (s *Service) readAppliedState() (appliedStateRecord, error) {
	var record appliedStateRecord
	data, err := s.volume.ReadFile(s.statePath())
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return appliedStateRecord{}, err
	}
	return record, nil
}

func (s *Service) writeAppliedState(record appliedStateRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return s.volume.WriteAtomic(s.statePath(), data, 0o600)
}

// --- 备份引用 ---

type storageProfileRef struct {
	ID   string
	Name string
}

// backupRef 把 writer 的备份记录映射为契约引用；BlockContent 是
// 写入前的管理块全量文本（含标记），从备份内容中提取。Available
// 报告备份文件可读性（#35 历史页"可恢复"判断）；Result 透传操作
// 结果标记。
func (s *Service) backupRef(record *hostsfile.BackupRecord, prevContent []byte, profile storageProfileRef) *app.ApplyBackupRef {
	block := ""
	available := prevContent != nil
	if prevContent != nil {
		if text, ok := managedBlockText(string(prevContent)); ok {
			block = text
		}
	} else if data, err := s.volume.ReadFile(filepath.Join(s.writer.BackupDir(), record.File)); err == nil {
		available = true
		if text, ok := managedBlockText(string(data)); ok {
			block = text
		}
	}
	return &app.ApplyBackupRef{
		ID:           record.ID,
		ProfileID:    profile.ID,
		ProfileName:  profile.Name,
		CreatedAt:    record.CreatedAt,
		OperationID:  record.OperationID,
		BlockContent: block,
		Result:       record.Result,
		Available:    available,
	}
}

// managedBlockText 提取含标记的管理块全量文本；无块返回 ok=false。
func managedBlockText(content string) (string, bool) {
	start, end, ok := hostsfile.ExtractManagedBlockBytes(content)
	if !ok {
		return "", false
	}
	return content[start:end], true
}

func (s *Service) currentBlockText() string {
	content, err := s.writer.CurrentContent()
	if err != nil {
		return ""
	}
	block, ok := managedBlockText(string(content))
	if !ok {
		return ""
	}
	return block
}

func sha256HexString(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
