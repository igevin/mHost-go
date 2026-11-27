package app

import (
	"context"
	"errors"
	"time"
)

var (
	ErrPickerCancelled = errors.New("directory picker cancelled")
	ErrStaleOperation  = errors.New("stale storage operation")
)

type DirectoryPicker interface {
	PickDirectory(ctx context.Context, opts DirectoryOptions) (string, error)
}

type DirectoryOptions struct {
	Title             string
	DefaultPath       string
	ButtonLabel       string
	Message           string
	CreateDirectories bool
}

type StorageService interface {
	Inspect(ctx context.Context, sourceRoot string) (Workspace, error)
	Refresh(ctx context.Context, workspaceID string) (Workspace, error)
	CreateWorkspace(ctx context.Context, sourceID, destinationRoot string) (Workspace, error)
	PreviewMigration(ctx context.Context, workspaceID string) (MigrationPlan, error)
	ApplyMigration(ctx context.Context, workspaceID, planID string) (WriteReceipt, error)
	PreviewValidationChange(ctx context.Context, workspaceID string) (ChangePreview, error)
	SaveValidationChange(ctx context.Context, workspaceID, previewID string) (WriteReceipt, error)
	ListBackups(ctx context.Context, workspaceID string) ([]BackupRef, error)
	RestoreBackup(ctx context.Context, workspaceID, backupID string) (WriteReceipt, error)
}

type AccessMode string

const (
	AccessReadOnly AccessMode = "read_only"
	AccessWritable AccessMode = "writable"
)

type Workspace struct {
	ID             string
	Root           string
	SourceRoot     string
	Access         AccessMode
	StorageVersion int
	Revision       uint64
	Summary        StorageSummary
}

type StorageSummary struct {
	Manifest  *ManifestSummary
	Profiles  []ProfileSummary
	Snapshots []SnapshotSummary
	Settings  SettingsSummary
	AdBlock   *AdBlockSummary
	Issues    []StorageIssue
}

type ManifestSummary struct {
	Version     int
	AppVersion  string
	UpdatedAt   time.Time
	DNSEnabled  *bool
	OriginalDNS string
}

type ProfileSummary struct {
	ID        string
	Name      string
	Mode      string
	RuleCount int
	Enabled   bool
	Protected bool
	Path      string
}

type SnapshotSummary struct {
	ID           string
	Name         string
	ProfileCount int
	CreatedAt    time.Time
	Path         string
	Pending      bool
}

type SettingsSummary struct {
	Source          string
	MigrationStatus string
}

type AdBlockSummary struct {
	Enabled       bool
	SourceCount   int
	WhitelistSize int
}

type IssueSeverity string

const (
	SeverityWarning IssueSeverity = "warning"
	SeverityError   IssueSeverity = "error"
)

type StorageIssue struct {
	Code        string
	Severity    IssueSeverity
	Path        string
	Message     string
	Detail      string
	Recoverable bool
}

type MigrationPlan struct {
	ID           string
	WorkspaceID  string
	BaseRevision uint64
	FromVersion  int
	ToVersion    int
	Changes      []ChangeItem
	Blockers     []StorageIssue
}

type ChangePreview struct {
	ID           string
	WorkspaceID  string
	BaseRevision uint64
	Changes      []ChangeItem
}

type ChangeItem struct {
	Entity         string
	Path           string
	Summary        string
	RequiresBackup bool
}

type BackupRef struct {
	ID          string
	WorkspaceID string
	Entity      string
	Path        string
	CreatedAt   time.Time
	OperationID string
}

type WriteReceipt struct {
	OperationID  string
	WorkspaceID  string
	BaseRevision uint64
	Revision     uint64
	Results      []WriteResult
}

type WriteStatus string

const (
	WriteWritten   WriteStatus = "written"
	WriteUnchanged WriteStatus = "unchanged"
	WriteSkipped   WriteStatus = "skipped"
	WriteFailed    WriteStatus = "failed"
)

type PreservationStatus string

const (
	PreservePreserved     PreservationStatus = "preserved"
	PreserveRestored      PreservationStatus = "restored"
	PreserveUnknown       PreservationStatus = "unknown"
	PreserveNotApplicable PreservationStatus = "not_applicable"
)

type WriteResult struct {
	Entity           string
	Path             string
	Status           WriteStatus
	Backup           *BackupRef
	ReadbackVerified bool
	Preservation     PreservationStatus
	Issue            *StorageIssue
}

type UIErrorKind string

const (
	ErrorPickerCancelled UIErrorKind = "picker_cancelled"
	ErrorPickerFailed    UIErrorKind = "picker_failed"
	ErrorInvalidPath     UIErrorKind = "invalid_path"
	ErrorPermission      UIErrorKind = "permission_denied"
	ErrorNotFound        UIErrorKind = "not_found"
	ErrorInvalidJSON     UIErrorKind = "invalid_json"
	ErrorValidation      UIErrorKind = "validation_failed"
	ErrorMigration       UIErrorKind = "migration_blocked"
	ErrorStale           UIErrorKind = "stale_operation"
	ErrorBackup          UIErrorKind = "backup_failed"
	ErrorWrite           UIErrorKind = "write_failed"
	ErrorReadback        UIErrorKind = "readback_mismatch"
	ErrorCanceled        UIErrorKind = "canceled"
	ErrorInternal        UIErrorKind = "internal"
)

type UIError struct {
	Kind         UIErrorKind
	Operation    string
	Path         string
	Message      string
	Detail       string
	Retryable    bool
	Preservation PreservationStatus
	Cause        error
}

func (e *UIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Kind)
}

func (e *UIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
