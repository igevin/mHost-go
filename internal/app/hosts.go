package app

import (
	"context"
	"time"
)

// HostsService drives the hosts rewrite workbench: editing profiles, planning
// the managed-block rewrite of /etc/hosts, applying with a backup, and
// rolling back. Phase 2 ships an in-memory demo implementation; issue #4
// implements the real service against this contract and must not weaken its
// semantics.
type HostsService interface {
	// ListProfiles returns the available hosts profiles.
	ListProfiles(ctx context.Context) ([]HostsProfile, error)
	// LoadProfile returns the profile content and its current revision.
	LoadProfile(ctx context.Context, profileID string) (HostsProfileDetail, error)
	// SaveProfile persists edited content. baseRevision must match the
	// revision the caller loaded, otherwise ErrStaleOperation is returned.
	SaveProfile(ctx context.Context, profileID string, content string, baseRevision uint64) (HostsProfileDetail, error)

	// PreviewApply builds the plan that would rewrite the managed block to
	// this profile's content. Blockers (unparseable rules) come back in the
	// plan with Blockers set; cross-IP conflicts come back with Conflicts set.
	// resolutions carries pending keep-choices (domain -> IP) so the preview
	// renders the merged block as it would be written; unresolved conflicts
	// render with the first IP kept.
	PreviewApply(ctx context.Context, profileID string, resolutions map[string]string) (ApplyPlan, error)
	// Apply executes a previously previewed plan. Plans expire with the
	// profile revision. Conflicts must be resolved: resolutions maps each
	// conflicting domain to the IP to keep; missing or invalid entries fail
	// with ErrorValidation.
	Apply(ctx context.Context, planID string, resolutions map[string]string) (ApplyReceipt, error)
	// Rollback restores the managed block from a backup taken by Apply.
	Rollback(ctx context.Context, backupID string) (ApplyReceipt, error)
	// ListApplyBackups lists backups created by Apply, newest first.
	ListApplyBackups(ctx context.Context) ([]ApplyBackupRef, error)
	// AppliedState reports which profile currently owns the managed block
	// and whether the block drifted from what mHost last wrote.
	AppliedState(ctx context.Context) (AppliedState, error)
}

type HostsProfile struct {
	ID        string
	Name      string
	Enabled   bool
	Protected bool
	RuleCount int
	Revision  uint64
}

type HostsProfileDetail struct {
	HostsProfile
	Content string
}

type ApplyPlan struct {
	ID             string
	ProfileID      string
	ProfileName    string
	BaseRevision   uint64
	CurrentBlock   string
	MergedBlock    string
	Conflicts      []Conflict
	BackupRequired bool
	Blockers       []StorageIssue
}

// Conflict is a domain that maps to different IPs on several lines. The user
// chooses which IP to keep; Apply validates the choice.
type Conflict struct {
	Domain string
	Lines  []int // 1-based line numbers in the profile content
	IPs    []string
}

type ApplyBackupRef struct {
	ID           string
	ProfileID    string
	ProfileName  string
	CreatedAt    time.Time
	OperationID  string
	BlockContent string
}

type ApplyReceipt struct {
	OperationID string
	ProfileID   string
	Results     []ApplyResult
	RollbackRef *ApplyBackupRef
}

type ApplyResult struct {
	Entity           string // managed_block / backup / verification
	Status           WriteStatus
	Backup           *ApplyBackupRef
	ReadbackVerified bool
	Issue            *StorageIssue
}

type AppliedState struct {
	ProfileID   string
	ProfileName string
	Drifted     bool
}
