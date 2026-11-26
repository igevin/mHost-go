package demo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
)

type preview struct {
	id          string
	workspaceID string
	revision    uint64
	migration   *app.MigrationPlan
	changes     *app.ChangePreview
}

type StorageService struct {
	mu         sync.Mutex
	sequence   int
	workspaces map[string]*app.Workspace
	previews   map[string]preview
	backups    map[string][]app.BackupRef
}

func NewStorageService() *StorageService {
	return &StorageService{
		workspaces: map[string]*app.Workspace{},
		previews:   map[string]preview{},
		backups:    map[string][]app.BackupRef{},
	}
}

func (s *StorageService) Inspect(ctx context.Context, sourceRoot string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	if strings.TrimSpace(sourceRoot) == "" {
		return app.Workspace{}, demoError(app.ErrorInvalidPath, "Choose a source directory")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace := &app.Workspace{
		ID:             s.nextID("source"),
		Root:           sourceRoot,
		SourceRoot:     sourceRoot,
		Access:         app.AccessReadOnly,
		StorageVersion: 2,
		Summary:        summaryFor(sourceRoot),
	}
	workspace.StorageVersion = workspace.Summary.Manifest.Version
	s.workspaces[workspace.ID] = workspace
	return cloneWorkspace(workspace), nil
}

func (s *StorageService) Refresh(ctx context.Context, workspaceID string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, err := s.workspace(workspaceID)
	if err != nil {
		return app.Workspace{}, err
	}
	return cloneWorkspace(workspace), nil
}

func (s *StorageService) CreateWorkspace(ctx context.Context, sourceID, destinationRoot string) (app.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return app.Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.workspace(sourceID)
	if err != nil {
		return app.Workspace{}, err
	}
	if strings.TrimSpace(destinationRoot) == "" || destinationRoot == source.Root {
		return app.Workspace{}, demoError(app.ErrorInvalidPath, "Working copy must use a different directory")
	}
	workspace := &app.Workspace{
		ID:             s.nextID("workspace"),
		Root:           destinationRoot,
		SourceRoot:     source.Root,
		Access:         app.AccessWritable,
		StorageVersion: source.StorageVersion,
		Revision:       source.Revision,
		Summary:        cloneSummary(source.Summary),
	}
	s.workspaces[workspace.ID] = workspace
	return cloneWorkspace(workspace), nil
}

func (s *StorageService) PreviewMigration(ctx context.Context, workspaceID string) (app.MigrationPlan, error) {
	if err := ctx.Err(); err != nil {
		return app.MigrationPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, err := s.workspace(workspaceID)
	if err != nil {
		return app.MigrationPlan{}, err
	}
	if workspace.Access != app.AccessWritable || workspace.StorageVersion != 1 {
		return app.MigrationPlan{}, demoError(app.ErrorMigration, "Migration requires a writable v1 working copy")
	}
	plan := app.MigrationPlan{
		ID:           s.nextID("migration"),
		WorkspaceID:  workspaceID,
		BaseRevision: workspace.Revision,
		FromVersion:  1,
		ToVersion:    2,
		Changes: []app.ChangeItem{
			{Entity: "manifest", Path: "manifest.json", Summary: "Upgrade manifest from v1 to v2", RequiresBackup: true},
			{Entity: "profiles", Path: "profiles/", Summary: "Preserve profiles and materialize v2 defaults", RequiresBackup: true},
		},
	}
	s.previews[plan.ID] = preview{id: plan.ID, workspaceID: workspaceID, revision: workspace.Revision, migration: &plan}
	return plan, nil
}

func (s *StorageService) ApplyMigration(ctx context.Context, workspaceID, planID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, preview, err := s.preview(workspaceID, planID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if preview.migration == nil {
		return app.WriteReceipt{}, demoError(app.ErrorValidation, "Preview is not a migration")
	}
	base := workspace.Revision
	workspace.StorageVersion = 2
	workspace.Summary.Manifest.Version = 2
	workspace.Revision++
	receipt := s.writeReceipt(workspace, base, preview.migration.Changes)
	delete(s.previews, planID)
	return receipt, nil
}

func (s *StorageService) PreviewValidationChange(ctx context.Context, workspaceID string) (app.ChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return app.ChangePreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, err := s.workspace(workspaceID)
	if err != nil {
		return app.ChangePreview{}, err
	}
	if workspace.Access != app.AccessWritable || workspace.StorageVersion != 2 {
		return app.ChangePreview{}, demoError(app.ErrorValidation, "Validation changes require a writable v2 working copy")
	}
	changes := []app.ChangeItem{
		{Entity: "manifest", Path: "manifest.json", Summary: "Round-trip manifest validation", RequiresBackup: true},
		{Entity: "profile", Path: "profiles/hosts/demo.json", Summary: "Round-trip profile validation", RequiresBackup: true},
		{Entity: "settings", Path: "settings.json", Summary: "Save native settings validation", RequiresBackup: true},
		{Entity: "snapshot", Path: "snapshots/validation.json", Summary: "Create a validation snapshot", RequiresBackup: false},
	}
	preview := app.ChangePreview{ID: s.nextID("changes"), WorkspaceID: workspaceID, BaseRevision: workspace.Revision, Changes: changes}
	s.previews[preview.ID] = previewRecord(preview)
	return preview, nil
}

func previewRecord(changes app.ChangePreview) preview {
	copy := changes
	return preview{id: changes.ID, workspaceID: changes.WorkspaceID, revision: changes.BaseRevision, changes: &copy}
}

func (s *StorageService) SaveValidationChange(ctx context.Context, workspaceID, previewID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, preview, err := s.preview(workspaceID, previewID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	if preview.changes == nil {
		return app.WriteReceipt{}, demoError(app.ErrorValidation, "Preview is not a validation change")
	}
	base := workspace.Revision
	workspace.Revision++
	workspace.Summary.Snapshots = append(workspace.Summary.Snapshots, app.SnapshotSummary{
		ID: fmt.Sprintf("validation-%d", workspace.Revision), Name: "Phase 1 validation", ProfileCount: len(workspace.Summary.Profiles), CreatedAt: demoTime, Path: "snapshots/validation.json",
	})
	receipt := s.writeReceipt(workspace, base, preview.changes.Changes)
	delete(s.previews, previewID)
	return receipt, nil
}

func (s *StorageService) ListBackups(ctx context.Context, workspaceID string) ([]app.BackupRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.workspace(workspaceID); err != nil {
		return nil, err
	}
	items := s.backups[workspaceID]
	if len(items) == 0 {
		return []app.BackupRef{}, nil
	}
	return append([]app.BackupRef(nil), items...), nil
}

func (s *StorageService) RestoreBackup(ctx context.Context, workspaceID, backupID string) (app.WriteReceipt, error) {
	if err := ctx.Err(); err != nil {
		return app.WriteReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, err := s.workspace(workspaceID)
	if err != nil {
		return app.WriteReceipt{}, err
	}
	var backup *app.BackupRef
	for _, candidate := range s.backups[workspaceID] {
		if candidate.ID == backupID {
			copy := candidate
			backup = &copy
			break
		}
	}
	if backup == nil {
		return app.WriteReceipt{}, demoError(app.ErrorNotFound, "Backup was not found in this workspace")
	}
	base := workspace.Revision
	operationID := s.nextID("operation")
	workspace.Revision++
	safety := app.BackupRef{ID: s.nextID("backup"), WorkspaceID: workspaceID, Entity: backup.Entity, Path: backup.Path, CreatedAt: demoTime, OperationID: operationID}
	s.backups[workspaceID] = append(s.backups[workspaceID], safety)
	return app.WriteReceipt{
		OperationID: operationID, WorkspaceID: workspaceID, BaseRevision: base, Revision: workspace.Revision,
		Results: []app.WriteResult{{Entity: backup.Entity, Path: backup.Path, Status: app.WriteWritten, Backup: &safety, ReadbackVerified: true, Preservation: app.PreserveRestored}},
	}, nil
}

func (s *StorageService) writeReceipt(workspace *app.Workspace, base uint64, changes []app.ChangeItem) app.WriteReceipt {
	operationID := s.nextID("operation")
	receipt := app.WriteReceipt{OperationID: operationID, WorkspaceID: workspace.ID, BaseRevision: base, Revision: workspace.Revision, Results: make([]app.WriteResult, 0, len(changes))}
	for _, change := range changes {
		result := app.WriteResult{Entity: change.Entity, Path: change.Path, Status: app.WriteWritten, ReadbackVerified: true, Preservation: app.PreservePreserved}
		if change.RequiresBackup {
			backup := app.BackupRef{ID: s.nextID("backup"), WorkspaceID: workspace.ID, Entity: change.Entity, Path: change.Path, CreatedAt: demoTime, OperationID: operationID}
			s.backups[workspace.ID] = append(s.backups[workspace.ID], backup)
			result.Backup = &backup
		}
		receipt.Results = append(receipt.Results, result)
	}
	return receipt
}

func (s *StorageService) preview(workspaceID, previewID string) (*app.Workspace, preview, error) {
	workspace, err := s.workspace(workspaceID)
	if err != nil {
		return nil, preview{}, err
	}
	record, ok := s.previews[previewID]
	if !ok || record.workspaceID != workspaceID || record.revision != workspace.Revision {
		return nil, preview{}, &app.UIError{Kind: app.ErrorStale, Message: "Preview is stale; generate it again", Retryable: true, Cause: app.ErrStaleOperation}
	}
	return workspace, record, nil
}

func (s *StorageService) workspace(id string) (*app.Workspace, error) {
	workspace, ok := s.workspaces[id]
	if !ok {
		return nil, demoError(app.ErrorNotFound, "Workspace is no longer available")
	}
	return workspace, nil
}

func (s *StorageService) nextID(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}

func demoError(kind app.UIErrorKind, message string) *app.UIError {
	return &app.UIError{Kind: kind, Message: message, Retryable: kind != app.ErrorInvalidPath}
}

var demoTime = time.Date(2026, time.November, 26, 12, 0, 0, 0, time.UTC)

func summaryFor(root string) app.StorageSummary {
	lower := strings.ToLower(root)
	version := 2
	if strings.Contains(lower, "v1") {
		version = 1
	}
	enabled := false
	summary := app.StorageSummary{
		Manifest: &app.ManifestSummary{Version: version, AppVersion: "0.4.2", UpdatedAt: demoTime, DNSEnabled: &enabled, OriginalDNS: "DHCP / Empty"},
		Profiles: []app.ProfileSummary{
			{ID: "22222222-2222-4222-8222-222222222222", Name: "Development", Mode: "hosts", RuleCount: 4, Enabled: true, Path: "profiles/hosts/22222222-2222-4222-8222-222222222222.json"},
			{ID: "33333333-3333-4333-8333-333333333333", Name: "Local DNS", Mode: "dns", RuleCount: 2, Protected: true, Path: "profiles/dns/33333333-3333-4333-8333-333333333333.json"},
		},
		Snapshots: []app.SnapshotSummary{{ID: "3fa85f64-5717-4562-b3fc-2c963f66afa6", Name: "Before validation", ProfileCount: 2, CreatedAt: demoTime, Path: "snapshots/3fa85f64-5717-4562-b3fc-2c963f66afa6.json"}},
		Settings:  app.SettingsSummary{Source: "Native settings (in-memory demo)", MigrationStatus: "Compatible"},
		AdBlock:   &app.AdBlockSummary{Enabled: true, SourceCount: 2, WhitelistSize: 3},
		Issues:    []app.StorageIssue{},
	}
	if version == 1 {
		summary.Profiles = summary.Profiles[:1]
		summary.Snapshots = []app.SnapshotSummary{}
		summary.AdBlock = nil
	}
	if strings.Contains(lower, "mixed-invalid") {
		summary.Profiles = summary.Profiles[:1]
		summary.Issues = []app.StorageIssue{
			{Code: "invalid_json", Severity: app.SeverityError, Path: "profiles/hosts/broken.json", Message: "Invalid JSON", Detail: "unexpected end of JSON input", Recoverable: true},
			{Code: "missing_fields", Severity: app.SeverityError, Path: "profiles/hosts/missing-fields.json", Message: "Required profile fields are missing"},
			{Code: "wrong_type", Severity: app.SeverityError, Path: "profiles/dns/wrong-type.json", Message: "Profile has an invalid field type"},
		}
	}
	return summary
}

func cloneWorkspace(workspace *app.Workspace) app.Workspace {
	copy := *workspace
	copy.Summary = cloneSummary(workspace.Summary)
	return copy
}

func cloneSummary(summary app.StorageSummary) app.StorageSummary {
	copy := summary
	if summary.Manifest != nil {
		manifest := *summary.Manifest
		copy.Manifest = &manifest
	}
	if summary.AdBlock != nil {
		adblock := *summary.AdBlock
		copy.AdBlock = &adblock
	}
	copy.Profiles = append([]app.ProfileSummary{}, summary.Profiles...)
	copy.Snapshots = append([]app.SnapshotSummary{}, summary.Snapshots...)
	copy.Issues = append([]app.StorageIssue{}, summary.Issues...)
	return copy
}
