package desktop

import (
	"context"
	"errors"
	"fmt"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
)

type Phase string

const (
	PhaseEmpty      Phase = "empty"
	PhaseSelecting  Phase = "selecting"
	PhaseLoading    Phase = "loading"
	PhaseReady      Phase = "ready"
	PhaseCopying    Phase = "copying"
	PhasePreviewing Phase = "previewing"
	PhaseWriting    Phase = "writing"
	PhaseFailed     Phase = "failed"
)

type PreviewKind string

const (
	PreviewMigrationKind  PreviewKind = "migration"
	PreviewValidationKind PreviewKind = "validation"
	PreviewRestoreKind    PreviewKind = "restore"
)

type PreviewState struct {
	Kind      PreviewKind
	Migration *app.MigrationPlan
	Changes   *app.ChangePreview
	Backup    *app.BackupRef
}

// WorkbenchModel state is owned by the MyGo main thread. Worker results must
// return through Dispatch; App completes Close on that same thread.
type WorkbenchModel struct {
	Service  app.StorageService
	Picker   app.DirectoryPicker
	Async    func(func())
	Dispatch func(func())

	Phase         Phase
	Workspace     app.Workspace
	SummaryStale  bool
	SelectedTab   int
	Preview       *PreviewState
	Receipt       *app.WriteReceipt
	Backups       []app.BackupRef
	Problem       *app.UIError
	DemoMode      bool
	QuitRequested bool
	OnSafeToQuit  func()

	profileRows    ui.ListState
	snapshotRows   ui.ListState
	issueRows      ui.ListState
	receiptRows    ui.ListState
	lastSourceRoot string
	operation      uint64
	cancel         context.CancelFunc
	closed         bool
}

func NewWorkbenchModel(service app.StorageService, picker app.DirectoryPicker) *WorkbenchModel {
	return &WorkbenchModel{
		Service:  service,
		Picker:   picker,
		Async:    func(fn func()) { go fn() },
		Dispatch: func(fn func()) { fn() },
		Phase:    PhaseEmpty,
		Backups:  []app.BackupRef{},
	}
}

func (m *WorkbenchModel) OpenSource() {
	if m.busy() || m.Service == nil || m.Picker == nil {
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = PhaseSelecting
	m.Problem = nil
	m.Async(func() {
		path, err := m.Picker.PickDirectory(ctx, app.DirectoryOptions{
			Title: "Choose Synthetic Source Data", ButtonLabel: "Open Source", Message: "The selected source remains read only.",
		})
		if err != nil {
			m.dispatchError(id, "open_source", err)
			return
		}
		m.Dispatch(func() {
			if m.current(id) {
				m.lastSourceRoot = path
				m.Workspace = app.Workspace{}
				m.SummaryStale = false
				m.Receipt = nil
				m.Backups = []app.BackupRef{}
				m.Phase = PhaseLoading
			}
		})
		m.inspect(ctx, id, path)
	})
}

func (m *WorkbenchModel) inspect(ctx context.Context, id uint64, sourceRoot string) {
	workspace, err := m.Service.Inspect(ctx, sourceRoot)
	m.Dispatch(func() {
		if !m.current(id) {
			return
		}
		if err != nil {
			m.setError("inspect", err)
			return
		}
		m.Workspace = workspace
		m.SummaryStale = false
		m.Phase = PhaseReady
		m.SelectedTab = 0
		m.Receipt = nil
		m.Backups = []app.BackupRef{}
		m.finishOperation()
	})
}

func (m *WorkbenchModel) Reload() {
	if m.busy() || m.Workspace.ID == "" {
		return
	}
	id, ctx := m.beginOperation()
	workspaceID := m.Workspace.ID
	m.Phase = PhaseLoading
	m.Problem = nil
	m.Async(func() {
		workspace, err := m.Service.Refresh(ctx, workspaceID)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("reload", err)
				return
			}
			m.Workspace = workspace
			m.SummaryStale = false
			m.Phase = PhaseReady
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) CancelOperation() {
	if m.Phase != PhaseLoading && m.Phase != PhasePreviewing {
		return
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.operation++
	m.Problem = nil
	if m.Workspace.ID == "" {
		m.Phase = PhaseEmpty
	} else {
		m.Phase = PhaseReady
	}
	m.finishOperation()
}

func (m *WorkbenchModel) Retry() {
	if m.Problem == nil || m.busy() {
		return
	}
	if m.Workspace.ID != "" {
		m.Reload()
		return
	}
	if m.lastSourceRoot == "" {
		m.OpenSource()
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = PhaseLoading
	m.Problem = nil
	sourceRoot := m.lastSourceRoot
	m.Async(func() { m.inspect(ctx, id, sourceRoot) })
}

func (m *WorkbenchModel) CreateWorkspace() {
	if m.busy() || m.SummaryStale || m.Workspace.ID == "" || m.Workspace.Access != app.AccessReadOnly {
		return
	}
	id, ctx := m.beginOperation()
	sourceID := m.Workspace.ID
	m.Phase = PhaseSelecting
	m.Problem = nil
	m.Async(func() {
		path, err := m.Picker.PickDirectory(ctx, app.DirectoryOptions{
			Title: "Choose an Empty Working-Copy Directory", ButtonLabel: "Create Working Copy", Message: "Only this working copy can be changed.", CreateDirectories: true,
		})
		if err != nil {
			m.dispatchError(id, "create_workspace", err)
			return
		}
		m.Dispatch(func() {
			if m.current(id) {
				m.Phase = PhaseCopying
			}
		})
		workspace, err := m.Service.CreateWorkspace(ctx, sourceID, path)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("create_workspace", err)
				return
			}
			m.Workspace = workspace
			m.SummaryStale = false
			m.Phase = PhaseReady
			m.Receipt = nil
			m.Backups = []app.BackupRef{}
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) PreviewMigration() {
	if m.busy() || !m.writable() || m.Workspace.StorageVersion != 1 {
		return
	}
	id, ctx := m.beginOperation()
	workspaceID := m.Workspace.ID
	m.Phase = PhasePreviewing
	m.Problem = nil
	m.Async(func() {
		plan, err := m.Service.PreviewMigration(ctx, workspaceID)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("preview_migration", err)
				return
			}
			m.Preview = &PreviewState{Kind: PreviewMigrationKind, Migration: &plan}
			m.Phase = PhaseReady
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) PreviewValidationChanges() {
	if m.busy() || !m.writable() || m.Workspace.StorageVersion != 2 {
		return
	}
	id, ctx := m.beginOperation()
	workspaceID := m.Workspace.ID
	m.Phase = PhasePreviewing
	m.Problem = nil
	m.Async(func() {
		changes, err := m.Service.PreviewValidationChange(ctx, workspaceID)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("preview_validation", err)
				return
			}
			m.Preview = &PreviewState{Kind: PreviewValidationKind, Changes: &changes}
			m.Phase = PhaseReady
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) CancelPreview() {
	if m.Preview == nil {
		return
	}
	m.Preview = nil
	m.Phase = PhaseReady
}

func (m *WorkbenchModel) ConfirmPreview() {
	if m.Preview == nil || m.busy() {
		return
	}
	preview := *m.Preview
	m.Preview = nil
	id, ctx := m.beginOperation()
	workspaceID := m.Workspace.ID
	m.Receipt = nil
	m.Phase = PhaseWriting
	m.Problem = nil
	m.Async(func() {
		var receipt app.WriteReceipt
		var err error
		switch preview.Kind {
		case PreviewMigrationKind:
			receipt, err = m.Service.ApplyMigration(ctx, workspaceID, preview.Migration.ID)
		case PreviewValidationKind:
			receipt, err = m.Service.SaveValidationChange(ctx, workspaceID, preview.Changes.ID)
		case PreviewRestoreKind:
			receipt, err = m.Service.RestoreBackup(ctx, workspaceID, preview.Backup.ID)
		default:
			err = errors.New("unknown preview kind")
		}
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if receipt.OperationID != "" || len(receipt.Results) != 0 {
				m.Receipt = &receipt
			}
			if err != nil {
				m.setError("write", err)
				return
			}
			m.refreshAfterWrite(id, ctx, workspaceID)
		})
	})
}

func (m *WorkbenchModel) refreshAfterWrite(id uint64, ctx context.Context, workspaceID string) {
	m.Async(func() {
		workspace, err := m.Service.Refresh(ctx, workspaceID)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("refresh_after_write", err)
				return
			}
			m.Workspace = workspace
			m.SummaryStale = false
			m.Phase = PhaseReady
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) LoadBackups() {
	if m.busy() || !m.writable() {
		return
	}
	id, ctx := m.beginOperation()
	workspaceID := m.Workspace.ID
	m.Phase = PhaseLoading
	m.Problem = nil
	m.Async(func() {
		backups, err := m.Service.ListBackups(ctx, workspaceID)
		m.Dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("list_backups", err)
				return
			}
			m.Backups = backups
			m.SelectedTab = 2
			m.Phase = PhaseReady
			m.finishOperation()
		})
	})
}

func (m *WorkbenchModel) RequestRestore(backupID string) {
	if m.busy() || !m.writable() {
		return
	}
	for _, backup := range m.Backups {
		if backup.ID == backupID {
			copy := backup
			m.Preview = &PreviewState{Kind: PreviewRestoreKind, Backup: &copy}
			return
		}
	}
}

func (m *WorkbenchModel) RestoreSelectedBackup() {
	if len(m.Backups) == 0 {
		return
	}
	m.RequestRestore(m.Backups[0].ID)
}

func (m *WorkbenchModel) RequestQuit() bool {
	if m.Phase == PhaseWriting || m.Phase == PhaseCopying {
		m.QuitRequested = true
		return false
	}
	if m.cancel != nil {
		m.cancel()
	}
	return true
}

func (m *WorkbenchModel) CancelQuit() {
	m.QuitRequested = false
}

func (m *WorkbenchModel) Close() error {
	m.closed = true
	m.operation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.Dispatch = func(func()) {}
	m.Async = func(func()) {}
	m.OnSafeToQuit = nil
	return nil
}

func (m *WorkbenchModel) busy() bool {
	switch m.Phase {
	case PhaseSelecting, PhaseLoading, PhaseCopying, PhasePreviewing, PhaseWriting:
		return true
	default:
		return false
	}
}

func (m *WorkbenchModel) writable() bool {
	return m.Phase == PhaseReady && !m.SummaryStale && m.Workspace.ID != "" && m.Workspace.Access == app.AccessWritable
}

func (m *WorkbenchModel) beginOperation() (uint64, context.Context) {
	if m.cancel != nil {
		m.cancel()
	}
	m.operation++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return m.operation, ctx
}

func (m *WorkbenchModel) current(id uint64) bool {
	return !m.closed && id == m.operation
}

func (m *WorkbenchModel) dispatchError(id uint64, operation string, err error) {
	m.Dispatch(func() {
		if m.current(id) {
			m.setError(operation, err)
		}
	})
}

func (m *WorkbenchModel) setError(operation string, err error) {
	if errors.Is(err, app.ErrPickerCancelled) || errors.Is(err, context.Canceled) {
		m.Problem = nil
		if m.Workspace.ID == "" {
			m.Phase = PhaseEmpty
		} else {
			m.Phase = PhaseReady
		}
		m.finishOperation()
		return
	}
	m.Problem = classifyUIError(operation, err)
	if m.Workspace.ID != "" && (operation == "reload" || operation == "write" || operation == "refresh_after_write") {
		m.SummaryStale = true
	}
	if m.Workspace.ID == "" {
		m.Phase = PhaseFailed
	} else {
		m.Phase = PhaseReady
	}
	m.finishOperation()
}

func classifyUIError(operation string, err error) *app.UIError {
	var uiErr *app.UIError
	if errors.As(err, &uiErr) {
		copy := *uiErr
		copy.Operation = operation
		if copy.Detail == "" && err != nil && err.Error() != copy.Message {
			copy.Detail = err.Error()
		}
		return &copy
	}
	kind := app.ErrorInternal
	if errors.Is(err, app.ErrStaleOperation) {
		kind = app.ErrorStale
	}
	return &app.UIError{Kind: kind, Operation: operation, Message: "Storage operation failed", Detail: fmt.Sprint(err), Retryable: true, Preservation: app.PreserveUnknown}
}

func (m *WorkbenchModel) finishOperation() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.QuitRequested && m.Phase != PhaseWriting && m.Phase != PhaseCopying {
		m.QuitRequested = false
		if m.OnSafeToQuit != nil {
			m.OnSafeToQuit()
		}
	}
}
