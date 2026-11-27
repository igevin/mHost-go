package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
)

// Service 是 app.StorageService 的真实磁盘实现：源目录只读检查、
// 工作副本复制、v1→v2 迁移、验证变更、备份与恢复。所有写操作
// 串行（单互斥锁），Preview 与 Apply/Save 通过 ID + Base Revision
// 配对防过期。
type Service struct {
	mu       sync.Mutex
	store    *Store
	now      func() time.Time
	newID    func() string
	verify   func(rel string) bool // 测试注入读回失败
	entries  map[string]*workspaceEntry
	previews map[string]previewEntry
}

type workspaceEntry struct {
	workspace app.Workspace
}

type previewKind int

const (
	previewMigration previewKind = iota
	previewValidation
)

type previewEntry struct {
	kind         previewKind
	workspaceID  string
	baseRevision uint64
	migration    *app.MigrationPlan
	changes      *app.ChangePreview
}

// Option 定制 Service，主要用于测试注入。
type Option func(*Service)

// WithVolume 替换文件系统实现（测试注入故障）。
func WithVolume(vol Volume) Option {
	return func(s *Service) { s.store = NewStore(vol) }
}

// WithClock 替换时间源。
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithIDGenerator 替换 ID 生成器。
func WithIDGenerator(fn func() string) Option {
	return func(s *Service) { s.newID = fn }
}

// WithVerifyFault 注入读回校验失败（仅测试使用）。
func WithVerifyFault(fn func(rel string) bool) Option {
	return func(s *Service) { s.verify = fn }
}

func NewService(opts ...Option) *Service {
	service := &Service{
		store:    NewStore(NewOSVolume()),
		now:      time.Now,
		newID:    newUUID,
		entries:  map[string]*workspaceEntry{},
		previews: map[string]previewEntry{},
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

var _ app.StorageService = (*Service)(nil)

func (s *Service) Inspect(ctx context.Context, sourceRoot string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	if strings.TrimSpace(sourceRoot) == "" {
		return app.Workspace{}, &app.UIError{Kind: app.ErrorInvalidPath, Message: "Choose a source directory"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scan, err := s.store.Scan(sourceRoot)
	if err != nil {
		return app.Workspace{}, s.mapScanError("inspect", sourceRoot, err)
	}
	workspace := app.Workspace{
		ID:             s.newID(),
		Root:           sourceRoot,
		SourceRoot:     sourceRoot,
		Access:         app.AccessReadOnly,
		StorageVersion: scan.Version,
		Revision:       0,
		Summary:        buildSummary(scan),
	}
	s.entries[workspace.ID] = &workspaceEntry{workspace: cloneWorkspace(workspace)}
	return workspace, nil
}

func (s *Service) Refresh(ctx context.Context, workspaceID string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return app.Workspace{}, err
	}
	scan, err := s.store.Scan(entry.workspace.Root)
	if err != nil {
		return app.Workspace{}, s.mapScanError("refresh", entry.workspace.Root, err)
	}
	updated := cloneWorkspace(entry.workspace)
	updated.StorageVersion = scan.Version
	updated.Summary = buildSummary(scan)
	entry.workspace = cloneWorkspace(updated)
	return updated, nil
}

func (s *Service) CreateWorkspace(ctx context.Context, sourceID, destinationRoot string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.workspace(sourceID)
	if err != nil {
		return app.Workspace{}, err
	}
	if strings.TrimSpace(destinationRoot) == "" {
		return app.Workspace{}, &app.UIError{Kind: app.ErrorInvalidPath, Message: "Choose a working-copy directory"}
	}
	destination, err := ValidateDestination(source.workspace.Root, destinationRoot)
	if err != nil {
		return app.Workspace{}, &app.UIError{Kind: app.ErrorInvalidPath, Message: err.Error(), Retryable: true}
	}
	// 复制前重新验证源仍然可读（不信任 picker 或先前校验）。
	if _, err := s.store.Scan(source.workspace.Root); err != nil {
		return app.Workspace{}, s.mapScanError("create workspace", source.workspace.Root, err)
	}
	if err := s.store.CopyTree(source.workspace.Root, destination); err != nil {
		kind := app.ErrorInvalidPath
		if !IsNotExist(err) && !strings.Contains(err.Error(), "not empty") && !strings.Contains(err.Error(), "not a directory") {
			kind = app.ErrorWrite
		}
		return app.Workspace{}, &app.UIError{Kind: kind, Operation: "create workspace", Path: destination, Message: err.Error(), Retryable: kind == app.ErrorWrite}
	}
	scan, err := s.store.Scan(destination)
	if err != nil {
		return app.Workspace{}, s.mapScanError("create workspace", destination, err)
	}
	workspace := app.Workspace{
		ID:             s.newID(),
		Root:           destination,
		SourceRoot:     source.workspace.Root,
		Access:         app.AccessWritable,
		StorageVersion: scan.Version,
		Revision:       0,
		Summary:        buildSummary(scan),
	}
	s.entries[workspace.ID] = &workspaceEntry{workspace: cloneWorkspace(workspace)}
	return workspace, nil
}

func (s *Service) PreviewMigration(ctx context.Context, workspaceID string) (app.MigrationPlan, error) {
	if err := ctx.Err(); err != nil {
		return app.MigrationPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return app.MigrationPlan{}, err
	}
	if entry.workspace.Access != app.AccessWritable {
		return app.MigrationPlan{}, migrationTargetError("Migration requires a writable working copy")
	}
	if entry.workspace.StorageVersion != 1 {
		return app.MigrationPlan{}, migrationTargetError("Migration requires a v1 working copy")
	}
	scan, err := s.store.Scan(entry.workspace.Root)
	if err != nil {
		return app.MigrationPlan{}, s.mapScanError("preview migration", entry.workspace.Root, err)
	}
	plan := app.MigrationPlan{
		ID:           s.newID(),
		WorkspaceID:  workspaceID,
		BaseRevision: entry.workspace.Revision,
		FromVersion:  1,
		ToVersion:    2,
		Changes:      []app.ChangeItem{},
		Blockers:     []app.StorageIssue{},
	}
	plan.Changes = append(plan.Changes, app.ChangeItem{
		Entity: "manifest", Path: FileManifest,
		Summary:        "Upgrade manifest from v1 to v2: materialize dns_enabled=false and original_dns=dhcp_empty",
		RequiresBackup: true,
	})
	profilePaths := sortedProfilePaths(scan)
	for _, rel := range profilePaths {
		plan.Changes = append(plan.Changes, app.ChangeItem{
			Entity: "profile", Path: rel,
			Summary:        "Round-trip profile to materialize v2 defaults; content preserved",
			RequiresBackup: true,
		})
	}
	for _, issue := range scan.Issues {
		if issue.Path == FileManifest {
			continue
		}
		if strings.HasPrefix(issue.Path, DirProfiles) {
			// Profile 数据语义不明确：属于阻塞项，不能静默跳过。
			plan.Blockers = append(plan.Blockers, issue)
			continue
		}
		// 可选组件（快照/广告/设置）损坏：原样保留，不阻断迁移。
		plan.Changes = append(plan.Changes, app.ChangeItem{
			Entity: entityForPath(issue.Path), Path: issue.Path,
			Summary:        "Damaged optional component left as-is; not touched by migration",
			RequiresBackup: false,
		})
	}
	s.previews[plan.ID] = previewEntry{kind: previewMigration, workspaceID: workspaceID, baseRevision: entry.workspace.Revision, migration: &plan}
	return plan, nil
}

func (s *Service) ApplyMigration(ctx context.Context, workspaceID, planID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, record, err := s.preview(workspaceID, planID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if record.kind != previewMigration || record.migration == nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorValidation, Message: "Preview is not a migration"}
	}
	plan := record.migration
	if len(plan.Blockers) > 0 {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorMigration, Message: "Migration is blocked; resolve blockers first"}
	}
	operationID := s.newID()
	root := entry.workspace.Root
	results := []app.WriteResult{}
	firstFailure := (*app.UIError)(nil)

	// 先迁移 profile，最后写 manifest（版本升级标志）：任何 profile
	// 失败都跳过 manifest 写入，副本保持 v1，迁移入口保留；重试时已
	// 完成的项按 unchanged 跳过。
	profileFailures := 0
	for _, change := range plan.Changes {
		if change.Entity != "profile" {
			continue
		}
		result := s.roundTripProfile(root, change.Path, operationID)
		results = append(results, result)
		firstFailure = firstFailureOr(firstFailure, result)
		if result.Status == app.WriteFailed {
			profileFailures++
		}
	}
	manifest, err := s.readManifestForWrite(root)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if profileFailures > 0 {
		results = append(results, app.WriteResult{
			Entity: "manifest", Path: FileManifest, Status: app.WriteSkipped, Preservation: app.PreservePreserved,
			Issue: &app.StorageIssue{Code: "skipped_dependency_failed", Severity: app.SeverityWarning, Path: FileManifest,
				Message: "Manifest upgrade skipped because profiles failed; the working copy stays v1 so migration can be retried", Recoverable: true},
		})
	} else {
		target := Manifest{
			Version:     2,
			AppVersion:  manifest.AppVersion,
			UpdatedAt:   s.now().UTC(),
			DNSEnabled:  boolPtr(false),
			OriginalDNS: &OriginalDNS{Kind: OriginalDNSDHCPEmpty},
		}
		result := s.writeEntity(root, FileManifest, "manifest", operationID, target)
		results = append(results, result)
		firstFailure = firstFailureOr(firstFailure, result)
	}
	for _, change := range plan.Changes {
		if change.Entity == "manifest" || change.Entity == "profile" {
			continue
		}
		// 损坏的可选组件：原样保留，回执中显式标记。
		results = append(results, app.WriteResult{
			Entity: change.Entity, Path: change.Path, Status: app.WriteSkipped,
			ReadbackVerified: false, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: "preserved_damaged", Severity: app.SeverityWarning, Path: change.Path, Message: change.Summary, Recoverable: true},
		})
	}

	receipt := s.commit(entry, operationID, plan.BaseRevision, results)
	delete(s.previews, planID)
	if firstFailure != nil {
		return receipt, firstFailure
	}
	return receipt, nil
}

func (s *Service) PreviewValidationChange(ctx context.Context, workspaceID string) (app.ChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return app.ChangePreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return app.ChangePreview{}, err
	}
	if entry.workspace.Access != app.AccessWritable {
		return app.ChangePreview{}, &app.UIError{Kind: app.ErrorValidation, Message: "Validation changes require a writable working copy"}
	}
	if entry.workspace.StorageVersion != 2 {
		return app.ChangePreview{}, &app.UIError{Kind: app.ErrorValidation, Message: "Validation changes require a v2 working copy"}
	}
	if _, err := s.store.Scan(entry.workspace.Root); err != nil {
		return app.ChangePreview{}, s.mapScanError("preview validation", entry.workspace.Root, err)
	}
	changes := []app.ChangeItem{
		{Entity: "manifest", Path: FileManifest, Summary: "Round-trip manifest validation", RequiresBackup: true},
	}
	if first := s.firstProfilePath(entry.workspace.Root); first != "" {
		changes = append(changes, app.ChangeItem{Entity: "profile", Path: first, Summary: "Round-trip profile validation", RequiresBackup: true})
	}
	changes = append(changes,
		app.ChangeItem{Entity: "settings", Path: FileSettings, Summary: "Materialize native v2 settings", RequiresBackup: true},
		app.ChangeItem{Entity: "snapshot", Path: filepath.Join(DirSnapshots, "<new>"), Summary: "Create a validation snapshot of current profiles", RequiresBackup: false},
		app.ChangeItem{Entity: "snapshot_index", Path: FileSnapshotIndex, Summary: "Register the validation snapshot in the index", RequiresBackup: true},
	)
	preview := app.ChangePreview{ID: s.newID(), WorkspaceID: workspaceID, BaseRevision: entry.workspace.Revision, Changes: changes}
	record := previewEntry{kind: previewValidation, workspaceID: workspaceID, baseRevision: entry.workspace.Revision, changes: &preview}
	s.previews[preview.ID] = record
	return preview, nil
}

func (s *Service) SaveValidationChange(ctx context.Context, workspaceID, previewID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, record, err := s.preview(workspaceID, previewID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if record.kind != previewValidation || record.changes == nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorValidation, Message: "Preview is not a validation change"}
	}
	operationID := s.newID()
	root := entry.workspace.Root
	results := []app.WriteResult{}
	firstFailure := (*app.UIError)(nil)

	for _, change := range record.changes.Changes {
		var result app.WriteResult
		switch change.Entity {
		case "manifest":
			manifest, err := s.readManifestForWrite(root)
			if err != nil {
				result = failedResult("manifest", FileManifest, err)
				break
			}
			result = s.writeEntity(root, FileManifest, "manifest", operationID, *manifest)
		case "profile":
			result = s.roundTripProfile(root, change.Path, operationID)
		case "settings":
			settings := s.readSettingsOrDefault(root)
			result = s.writeEntity(root, FileSettings, "settings", operationID, settings)
		case "snapshot":
			result = s.createValidationSnapshot(root, operationID)
		case "snapshot_index":
			// 索引在创建快照时一并更新；此处读回验证。
			result = s.verifyEntity(root, FileSnapshotIndex, "snapshot_index")
			if result.Status == app.WriteWritten {
				result.Status = app.WriteUnchanged
			}
		}
		results = append(results, result)
		firstFailure = firstFailureOr(firstFailure, result)
	}

	receipt := s.commit(entry, operationID, record.changes.BaseRevision, results)
	delete(s.previews, previewID)
	if firstFailure != nil {
		return receipt, firstFailure
	}
	return receipt, nil
}

func (s *Service) ListBackups(ctx context.Context, workspaceID string) ([]app.BackupRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return nil, err
	}
	refs, err := s.store.Backups(entry.workspace.Root)
	if err != nil {
		return nil, &app.UIError{Kind: app.ErrorReadback, Operation: "list backups", Path: entry.workspace.Root, Message: err.Error(), Cause: err}
	}
	for i := range refs {
		refs[i].WorkspaceID = workspaceID
	}
	return refs, nil
}

func (s *Service) RestoreBackup(ctx context.Context, workspaceID, backupID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if entry.workspace.Access != app.AccessWritable {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorPermission, Message: "Restore requires a writable working copy"}
	}
	backups, err := s.store.Backups(entry.workspace.Root)
	if err != nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorReadback, Operation: "restore", Message: err.Error(), Cause: err}
	}
	var target *app.BackupRef
	for i := range backups {
		if backups[i].ID == backupID {
			target = &backups[i]
			break
		}
	}
	if target == nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorNotFound, Message: "Backup was not found in this workspace", Retryable: true}
	}
	root := entry.workspace.Root
	// 纵深防御：即使登记被绕过，恢复目标也必须在根目录内。
	if target.Path == "" || filepath.IsAbs(target.Path) {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorInvalidPath, Path: target.Path, Message: "Backup path is invalid", Retryable: false}
	}
	cleaned := filepath.Clean(target.Path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorInvalidPath, Path: target.Path, Message: "Backup path escapes the workspace", Retryable: false}
	}
	if _, err := os.Stat(filepath.Join(root, target.Path)); err != nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorNotFound, Path: target.Path, Message: "Current file is missing; refusing to restore over nothing", Cause: err}
	}
	content, err := s.store.BackupContent(root, backupID)
	if err != nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorNotFound, Path: target.Path, Message: "Backup content is missing", Cause: err}
	}
	operationID := s.newID()
	// 恢复前先生成安全备份，失败则中止。
	safety, err := s.store.RecordBackup(root, target.Path, target.Entity, operationID, s.now().UTC(), s.newID())
	if err != nil {
		return app.WriteReceipt{}, &app.UIError{Kind: app.ErrorBackup, Operation: "restore", Path: target.Path, Message: "Safety backup failed; restore aborted", Cause: err}
	}
	safety.WorkspaceID = workspaceID
	result := s.writeContent(root, target.Path, target.Entity, operationID, content)
	result.Backup = &safety
	// 只有实际写入或确认一致才标记 restored；失败结果保留写入路径
	// 给出的 Preservation，避免与 Status/Issue 矛盾。
	if result.Status == app.WriteWritten || result.Status == app.WriteUnchanged {
		result.Preservation = app.PreserveRestored
	}
	receipt := s.commit(entry, operationID, entry.workspace.Revision, []app.WriteResult{result})
	// 内容与备份相同（WriteUnchanged）视为恢复成功：文件本就处于
	// 备份所记录的状态。
	if result.Status != app.WriteWritten && result.Status != app.WriteUnchanged {
		if uiErr := resultIssueError("restore", result); uiErr != nil {
			return receipt, uiErr
		}
		return receipt, &app.UIError{Kind: app.ErrorWrite, Operation: "restore", Path: target.Path, Message: "Restore did not complete", Retryable: true}
	}
	if !result.ReadbackVerified {
		return receipt, &app.UIError{Kind: app.ErrorReadback, Operation: "restore", Path: target.Path, Message: "Restore verification failed", Retryable: true}
	}
	return receipt, nil
}

// writeEntity 把模型序列化后走统一写入路径。
func (s *Service) writeEntity(root, rel, entity, operationID string, model any) app.WriteResult {
	data, err := marshalModel(model)
	if err != nil {
		return failedResult(entity, rel, fmt.Errorf("encode %s: %w", rel, err))
	}
	return s.writeContent(root, rel, entity, operationID, data)
}

// writeContent 统一写入路径：内容未变则跳过；有变化先备份再原子
// 替换并读回验证。任何一步失败都保证原文件不被破坏。
func (s *Service) writeContent(root, rel, entity, operationID string, data []byte) app.WriteResult {
	target := filepath.Join(root, rel)
	existing, err := s.store.vol.ReadFile(target)
	exists := err == nil
	if err != nil && !IsNotExist(err) {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: CodeReadFailed, Severity: app.SeverityError, Path: rel, Message: err.Error(), Detail: err.Error(), Recoverable: true}}
	}
	if exists && bytes.Equal(existing, data) {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteUnchanged, ReadbackVerified: true, Preservation: app.PreservePreserved}
	}
	var backup *app.BackupRef
	if exists {
		ref, err := s.store.RecordBackup(root, rel, entity, operationID, s.now().UTC(), s.newID())
		if err != nil {
			return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Preservation: app.PreservePreserved,
				Issue: &app.StorageIssue{Code: CodeBackupFailed, Severity: app.SeverityError, Path: rel, Message: "Backup failed; write aborted", Detail: err.Error(), Recoverable: true}}
		}
		backup = &ref
	}
	if err := s.store.vol.WriteAtomic(target, data, FilePerm); err != nil {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Backup: backup, Preservation: app.PreservePreserved,
			Issue: &app.StorageIssue{Code: CodeWriteFailed, Severity: app.SeverityError, Path: rel, Message: err.Error(), Detail: err.Error(), Recoverable: true}}
	}
	if s.verify != nil && s.verify(rel) {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Backup: backup, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: CodeReadbackFailed, Severity: app.SeverityError, Path: rel, Message: "Readback verification failed", Recoverable: true}}
	}
	readback, err := s.store.vol.ReadFile(target)
	if err != nil || !bytes.Equal(readback, data) {
		detail := "readback read failed"
		if err != nil {
			detail = err.Error()
		}
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Backup: backup, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: CodeReadbackFailed, Severity: app.SeverityError, Path: rel, Message: "Readback verification failed", Detail: detail, Recoverable: true}}
	}
	return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteWritten, Backup: backup, ReadbackVerified: true, Preservation: app.PreservePreserved}
}

// roundTripProfile 解析后重新序列化，物化 v2 字段默认值。
// mode 缺失时按所在目录推断，保持目录语义一致。
func (s *Service) roundTripProfile(root, rel, operationID string) app.WriteResult {
	data, err := s.store.vol.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return failedResult("profile", rel, err)
	}
	profile, err := parseProfile(data, directoryModeForProfile(rel))
	if err != nil {
		return failedResult("profile", rel, err)
	}
	return s.writeEntity(root, rel, "profile", operationID, profile)
}

func (s *Service) createValidationSnapshot(root, operationID string) app.WriteResult {
	scan, err := s.store.Scan(root)
	if err != nil {
		return failedResult("snapshot", DirSnapshots, err)
	}
	profiles := make([]Profile, 0, len(scan.Profiles))
	for _, entry := range scan.Profiles {
		profiles = append(profiles, entry.Profile)
	}
	id := s.newID()
	rel := filepath.Join(DirSnapshots, id+".json")
	snapshot := Snapshot{ID: id, Name: "Validation snapshot", Profiles: profiles, CreatedAt: s.now().UTC()}
	result := s.writeEntity(root, rel, "snapshot", operationID, snapshot)
	if result.Status != app.WriteWritten && result.Status != app.WriteUnchanged {
		return result
	}
	// 登记索引；先备份现有索引。
	index := SnapshotIndex{Version: 1, Snapshots: []SnapshotMeta{}}
	if data, err := s.store.vol.ReadFile(filepath.Join(root, FileSnapshotIndex)); err == nil {
		if err := unmarshalModel(data, &index); err != nil {
			return failedResult("snapshot_index", FileSnapshotIndex, err)
		}
	} else if !IsNotExist(err) {
		return failedResult("snapshot_index", FileSnapshotIndex, err)
	}
	index.Snapshots = append(index.Snapshots, SnapshotMeta{ID: id, Name: snapshot.Name, ProfileCount: len(profiles), CreatedAt: snapshot.CreatedAt})
	indexResult := s.writeContent(root, FileSnapshotIndex, "snapshot_index", operationID, mustMarshal(index))
	if indexResult.Status == app.WriteFailed {
		// 索引登记失败必须对用户可见：快照文件虽在，但没有索引它永远不会
		// 出现在 UI 里；按失败上报，禁止显示全局成功。
		return app.WriteResult{Entity: "snapshot", Path: rel, Status: app.WriteFailed, ReadbackVerified: true,
			Issue: &app.StorageIssue{Code: "index_update_failed", Severity: app.SeverityError, Path: FileSnapshotIndex, Message: "Snapshot written but index update failed", Detail: "snapshot is pending until the index is rebuilt", Recoverable: true}}
	}
	return result
}

func (s *Service) verifyEntity(root, rel, entity string) app.WriteResult {
	if _, err := s.store.vol.ReadFile(filepath.Join(root, rel)); err != nil {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: CodeReadFailed, Severity: app.SeverityError, Path: rel, Message: err.Error(), Detail: err.Error(), Recoverable: true}}
	}
	if s.verify != nil && s.verify(rel) {
		return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Preservation: app.PreserveUnknown,
			Issue: &app.StorageIssue{Code: CodeReadbackFailed, Severity: app.SeverityError, Path: rel, Message: "Readback verification failed", Recoverable: true}}
	}
	return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteWritten, ReadbackVerified: true, Preservation: app.PreservePreserved}
}

func (s *Service) readManifestForWrite(root string) (*Manifest, error) {
	data, err := s.store.vol.ReadFile(filepath.Join(root, FileManifest))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := unmarshalModel(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (s *Service) readSettingsOrDefault(root string) Settings {
	data, err := s.store.vol.ReadFile(filepath.Join(root, FileSettings))
	if err != nil {
		return DefaultSettings()
	}
	var settings Settings
	if err := unmarshalModel(data, &settings); err != nil {
		return DefaultSettings()
	}
	return settings
}

func (s *Service) firstProfilePath(root string) string {
	scan, err := s.store.Scan(root)
	if err != nil || len(scan.Profiles) == 0 {
		return ""
	}
	return sortedProfilePaths(scan)[0]
}

func (s *Service) commit(entry *workspaceEntry, operationID string, base uint64, results []app.WriteResult) app.WriteReceipt {
	entry.workspace.Revision = base + 1
	if scan, err := s.store.Scan(entry.workspace.Root); err == nil {
		entry.workspace.StorageVersion = scan.Version
		entry.workspace.Summary = buildSummary(scan)
	}
	for i := range results {
		if results[i].Backup != nil && results[i].Backup.WorkspaceID == "" {
			results[i].Backup.WorkspaceID = entry.workspace.ID
		}
	}
	return app.WriteReceipt{
		OperationID:  operationID,
		WorkspaceID:  entry.workspace.ID,
		BaseRevision: base,
		Revision:     entry.workspace.Revision,
		Results:      results,
	}
}

func (s *Service) workspace(id string) (*workspaceEntry, error) {
	entry, ok := s.entries[id]
	if !ok {
		return nil, &app.UIError{Kind: app.ErrorNotFound, Message: "Workspace is no longer available"}
	}
	return entry, nil
}

func (s *Service) preview(workspaceID, previewID string) (*workspaceEntry, previewEntry, error) {
	entry, err := s.workspace(workspaceID)
	if err != nil {
		return nil, previewEntry{}, err
	}
	record, ok := s.previews[previewID]
	if !ok || record.workspaceID != workspaceID || record.baseRevision != entry.workspace.Revision {
		return nil, previewEntry{}, &app.UIError{Kind: app.ErrorStale, Message: "Preview is stale; generate it again", Retryable: true, Cause: app.ErrStaleOperation}
	}
	return entry, record, nil
}

func (s *Service) mapScanError(operation, root string, err error) error {
	var uiErr *app.UIError
	if errors.As(err, &uiErr) {
		uiErr.Operation = operation
		return uiErr
	}
	if IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
		return &app.UIError{Kind: app.ErrorNotFound, Operation: operation, Path: root, Message: "Directory is not available", Detail: err.Error(), Cause: err, Retryable: true}
	}
	kind := app.ErrorPermission
	if strings.Contains(err.Error(), "not a directory") || strings.Contains(err.Error(), "not an mHost storage") {
		kind = app.ErrorInvalidPath
	}
	// 只有确属模型解析失败时才改判；OS 错误（权限等）保持原分类。
	switch ClassifyModelError(err) {
	case ModelErrSyntax:
		kind = app.ErrorInvalidJSON
	case ModelErrValidation:
		if kind == app.ErrorPermission {
			kind = app.ErrorValidation
		}
	}
	return &app.UIError{Kind: kind, Operation: operation, Path: root, Message: err.Error(), Detail: err.Error(), Cause: err}
}

func buildSummary(scan ScanResult) app.StorageSummary {
	summary := app.StorageSummary{
		Profiles:  []app.ProfileSummary{},
		Snapshots: []app.SnapshotSummary{},
		Issues:    append([]app.StorageIssue{}, scan.Issues...),
	}
	if scan.Manifest != nil {
		enabled := false
		if scan.Manifest.DNSEnabled != nil {
			enabled = *scan.Manifest.DNSEnabled
		}
		summary.Manifest = &app.ManifestSummary{
			Version:     scan.Version,
			AppVersion:  scan.Manifest.AppVersion,
			UpdatedAt:   scan.Manifest.UpdatedAt,
			DNSEnabled:  &enabled,
			OriginalDNS: originalDNSSummary(scan.Manifest.OriginalDNS),
		}
	}
	for _, entry := range scan.Profiles {
		summary.Profiles = append(summary.Profiles, app.ProfileSummary{
			ID:        entry.Profile.ID,
			Name:      entry.Profile.Name,
			Mode:      string(entry.Profile.Mode),
			RuleCount: len(entry.Profile.Rules),
			Enabled:   entry.Profile.Enabled,
			Protected: entry.Profile.Protected,
			Path:      entry.RelPath,
		})
	}
	for _, entry := range scan.Snapshots {
		summary.Snapshots = append(summary.Snapshots, app.SnapshotSummary{
			ID:           entry.Meta.ID,
			Name:         entry.Meta.Name,
			ProfileCount: entry.Meta.ProfileCount,
			CreatedAt:    entry.Meta.CreatedAt,
			Path:         entry.RelPath,
		})
	}
	summary.Settings = settingsSummary(scan)
	if scan.AdBlock != nil {
		summary.AdBlock = &app.AdBlockSummary{Enabled: scan.AdBlock.Enabled, SourceCount: len(scan.AdBlock.Sources), WhitelistSize: len(scan.AdBlock.Whitelist)}
	}
	return summary
}

func settingsSummary(scan ScanResult) app.SettingsSummary {
	if scan.Settings != nil {
		return app.SettingsSummary{Source: "Native settings (v2)", MigrationStatus: "Native"}
	}
	if scan.Version == 1 {
		// 源项目 v1 的设置存放在应用内 localStorage，没有磁盘契约；
		// 迁移时按 v2 默认值物化 settings.json。
		return app.SettingsSummary{Source: "Legacy settings (localStorage; no disk contract)", MigrationStatus: "Pending v1 → v2 migration"}
	}
	return app.SettingsSummary{Source: "Native settings (v2)", MigrationStatus: "Defaults not yet materialized"}
}

func originalDNSSummary(dns *OriginalDNS) string {
	if dns == nil {
		return "Not recorded"
	}
	if dns.Kind == OriginalDNSDHCPEmpty {
		return "DHCP / Empty"
	}
	return strings.Join(dns.Servers, ", ")
}

func sortedProfilePaths(scan ScanResult) []string {
	paths := make([]string, 0, len(scan.Profiles))
	for _, entry := range scan.Profiles {
		paths = append(paths, entry.RelPath)
	}
	sort.Strings(paths)
	return paths
}

func entityForPath(rel string) string {
	switch {
	case strings.HasPrefix(rel, DirSnapshots):
		return "snapshot"
	case rel == FileAdBlock:
		return "adblock"
	case rel == FileSettings:
		return "settings"
	default:
		return "component"
	}
}

func failedResult(entity, rel string, err error) app.WriteResult {
	code := CodeWriteFailed
	kind := ClassifyModelError(err)
	if kind == ModelErrSyntax {
		code = CodeInvalidJSON
	} else if IsNotExist(err) {
		code = "not_found"
	}
	return app.WriteResult{Entity: entity, Path: rel, Status: app.WriteFailed, Preservation: app.PreserveUnknown,
		Issue: &app.StorageIssue{Code: code, Severity: app.SeverityError, Path: rel, Message: err.Error(), Detail: err.Error(), Recoverable: true}}
}

func firstFailureOr(current *app.UIError, result app.WriteResult) *app.UIError {
	if current != nil || result.Issue == nil || result.Status != app.WriteFailed {
		return current
	}
	kind := app.ErrorWrite
	switch result.Issue.Code {
	case CodeBackupFailed:
		kind = app.ErrorBackup
	case CodeReadbackFailed:
		kind = app.ErrorReadback
	case CodeInvalidJSON:
		kind = app.ErrorInvalidJSON
	case "not_found":
		kind = app.ErrorNotFound
	}
	return &app.UIError{Kind: kind, Path: result.Path, Message: result.Issue.Message, Detail: result.Issue.Detail, Retryable: result.Issue.Recoverable}
}

func resultIssueError(operation string, result app.WriteResult) error {
	uiErr := firstFailureOr(nil, result)
	if uiErr == nil {
		return nil
	}
	uiErr.Operation = operation
	return uiErr
}

func migrationTargetError(message string) error {
	return &app.UIError{Kind: app.ErrorMigration, Message: message, Retryable: false}
}

func cloneWorkspace(workspace app.Workspace) app.Workspace {
	cloned := workspace
	cloned.Summary = cloneSummary(workspace.Summary)
	return cloned
}

func cloneSummary(summary app.StorageSummary) app.StorageSummary {
	cloned := summary
	if summary.Manifest != nil {
		manifest := *summary.Manifest
		cloned.Manifest = &manifest
	}
	if summary.AdBlock != nil {
		adblock := *summary.AdBlock
		cloned.AdBlock = &adblock
	}
	cloned.Profiles = append([]app.ProfileSummary{}, summary.Profiles...)
	cloned.Snapshots = append([]app.SnapshotSummary{}, summary.Snapshots...)
	cloned.Issues = append([]app.StorageIssue{}, summary.Issues...)
	return cloned
}

func boolPtr(v bool) *bool { return &v }

func mustMarshal(value any) []byte {
	data, err := marshalModel(value)
	if err != nil {
		panic(err)
	}
	return data
}

func newUUID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], id[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id[10:16])
	return string(encoded[:])
}
