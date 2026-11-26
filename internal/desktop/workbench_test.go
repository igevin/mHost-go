package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/demo"
)

type stubDirectoryPicker struct {
	path  string
	err   error
	calls int
}

func (p *stubDirectoryPicker) PickDirectory(context.Context, app.DirectoryOptions) (string, error) {
	p.calls++
	return p.path, p.err
}

func newTestWorkbench(service app.StorageService, picker app.DirectoryPicker) *WorkbenchModel {
	model := NewWorkbenchModel(service, picker)
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	return model
}

func openTestWorkspace(t *testing.T, model *WorkbenchModel, picker *stubDirectoryPicker, source, destination string) {
	t.Helper()
	picker.path = source
	model.OpenSource()
	if model.Phase != PhaseReady || model.Workspace.Access != app.AccessReadOnly {
		t.Fatalf("open source phase/access = %v/%v, problem = %+v", model.Phase, model.Workspace.Access, model.Problem)
	}
	picker.path = destination
	model.CreateWorkspace()
	if model.Phase != PhaseReady || model.Workspace.Access != app.AccessWritable {
		t.Fatalf("create workspace phase/access = %v/%v, problem = %+v", model.Phase, model.Workspace.Access, model.Problem)
	}
}

// UI-001.
func TestWorkbenchInitialStateDoesNotAccessStorage(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	model.DemoMode = true
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()

	if model.Phase != PhaseEmpty || picker.calls != 0 {
		t.Fatalf("phase/picker calls = %v/%d", model.Phase, picker.calls)
	}
	if !tester.HasText("No directory is accessed until you choose one") || !tester.HasText("Demo service") {
		t.Fatalf("initial view is missing safety copy: %q", tester.Texts())
	}
}

// UI-003.
func TestWorkbenchOpenSourceFromView(t *testing.T) {
	picker := &stubDirectoryPicker{path: "/fixtures/v2-full"}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	tester := ui.NewTester(model.View, 1120, 760)
	if err := tester.Click("Open Source…"); err != nil {
		t.Fatal(err)
	}

	if model.Workspace.Root != "/fixtures/v2-full" || model.Workspace.StorageVersion != 2 {
		t.Fatalf("workspace = %+v", model.Workspace)
	}
	if len(model.Workspace.Summary.Profiles) != 2 || model.Problem != nil {
		t.Fatalf("summary/problem = %+v/%+v", model.Workspace.Summary, model.Problem)
	}
}

// UI-002 and UI-004.
func TestWorkbenchPickerCancelAndPartialIssues(t *testing.T) {
	picker := &stubDirectoryPicker{err: app.ErrPickerCancelled}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	model.OpenSource()
	if model.Phase != PhaseEmpty || model.Problem != nil {
		t.Fatalf("cancel phase/problem = %v/%+v", model.Phase, model.Problem)
	}

	picker.err = nil
	picker.path = "/fixtures/mixed-invalid"
	model.OpenSource()
	if len(model.Workspace.Summary.Issues) == 0 || len(model.Workspace.Summary.Profiles) != 1 {
		t.Fatalf("partial summary = %+v", model.Workspace.Summary)
	}
}

// UI-012 and UI-014.
func TestWorkbenchMigrationPreviewCancelAndApply(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v1-hosts", "/tmp/work-v1")
	revision := model.Workspace.Revision

	model.PreviewMigration()
	if model.Preview == nil || model.Preview.Migration == nil {
		t.Fatal("migration preview was not opened")
	}
	model.CancelPreview()
	if model.Workspace.StorageVersion != 1 || model.Workspace.Revision != revision {
		t.Fatalf("cancel mutated workspace = %+v", model.Workspace)
	}

	model.PreviewMigration()
	model.ConfirmPreview()
	if model.Workspace.StorageVersion != 2 || model.Receipt == nil {
		t.Fatalf("migration workspace/receipt = %+v/%+v", model.Workspace, model.Receipt)
	}
	if len(model.Workspace.Summary.Profiles) != 1 || model.Workspace.Summary.Settings.MigrationStatus != "Migrated v1 → v2; profiles preserved" {
		t.Fatalf("migration did not preserve and explain the v1 dataset: %+v", model.Workspace.Summary)
	}
	for _, result := range model.Receipt.Results {
		if result.Status == app.WriteFailed || !result.ReadbackVerified {
			t.Fatalf("unverified migration result = %+v", result)
		}
	}
}

// UI-018, UI-022 and UI-024.
func TestWorkbenchValidationWriteBackupAndRestore(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-v2")

	model.PreviewValidationChanges()
	model.ConfirmPreview()
	if model.Receipt == nil || len(model.Receipt.Results) != 4 || model.Problem != nil {
		t.Fatalf("write receipt/problem = %+v/%+v", model.Receipt, model.Problem)
	}
	model.LoadBackups()
	if len(model.Backups) == 0 {
		t.Fatal("write did not expose backups")
	}
	revision := model.Workspace.Revision
	model.RequestRestore(model.Backups[0].ID)
	model.CancelPreview()
	if model.Workspace.Revision != revision {
		t.Fatal("cancel restore changed revision")
	}
	model.RequestRestore(model.Backups[0].ID)
	model.ConfirmPreview()
	if model.Receipt == nil || len(model.Receipt.Results) != 1 || !model.Receipt.Results[0].ReadbackVerified {
		t.Fatalf("restore receipt = %+v", model.Receipt)
	}
}

type toggleInspectService struct {
	app.StorageService
	fail bool
}

func (s *toggleInspectService) Inspect(ctx context.Context, root string) (app.Workspace, error) {
	if s.fail {
		return app.Workspace{}, errors.New("replacement source failed")
	}
	return s.StorageService.Inspect(ctx, root)
}

// UI-007.
func TestWorkbenchReplacementSourceFailureClearsOldWorkspace(t *testing.T) {
	service := &toggleInspectService{StorageService: demo.NewStorageService()}
	picker := &stubDirectoryPicker{path: "/fixtures/v2-full"}
	model := newTestWorkbench(service, picker)
	model.OpenSource()
	service.fail = true
	picker.path = "/fixtures/v1-hosts"
	model.OpenSource()
	if model.Phase != PhaseFailed || model.Workspace.ID != "" || model.SummaryStale {
		t.Fatalf("replacement failure retained old workspace: phase/workspace/stale = %v/%+v/%v", model.Phase, model.Workspace, model.SummaryStale)
	}
}

type refreshFailureService struct {
	app.StorageService
	err error
}

func (s *refreshFailureService) Refresh(ctx context.Context, id string) (app.Workspace, error) {
	if s.err != nil {
		return app.Workspace{}, s.err
	}
	return s.StorageService.Refresh(ctx, id)
}

// UI-007.
func TestWorkbenchReloadFailureMarksStaleAndBlocksWrites(t *testing.T) {
	base := demo.NewStorageService()
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(base, picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-stale")
	service := &refreshFailureService{StorageService: base, err: errors.New("refresh failed")}
	model.Service = service
	model.Reload()
	if !model.SummaryStale || model.writable() {
		t.Fatalf("reload failure did not block stale workspace: %+v", model)
	}
	model.PreviewValidationChanges()
	if model.Preview != nil {
		t.Fatal("stale workspace opened a write preview")
	}
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("May be stale") {
		t.Fatalf("stale marker is not visible: %q", tester.Texts())
	}
	service.err = nil
	model.Reload()
	if model.SummaryStale || !model.writable() {
		t.Fatal("successful reload did not restore the workspace")
	}
}

type saveFailureService struct {
	app.StorageService
	receipt app.WriteReceipt
	err     error
}

func (s saveFailureService) SaveValidationChange(context.Context, string, string) (app.WriteReceipt, error) {
	return s.receipt, s.err
}

// UI-020.
func TestWorkbenchWriteFailureKeepsPartialReceipt(t *testing.T) {
	base := demo.NewStorageService()
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(base, picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-before-failure")
	preview, err := base.PreviewValidationChange(context.Background(), model.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := saveFailureService{
		StorageService: base,
		receipt: app.WriteReceipt{WorkspaceID: model.Workspace.ID, Results: []app.WriteResult{{
			Entity: "manifest", Status: app.WriteFailed, Preservation: app.PreservePreserved,
		}}},
		err: &app.UIError{Kind: app.ErrorWrite, Message: "Injected write failure"},
	}
	model.Service = failure
	model.PreviewValidationChanges()
	model.ConfirmPreview()

	if model.Problem == nil || model.Receipt == nil || model.Workspace.Revision != preview.BaseRevision {
		t.Fatalf("failure problem/receipt/workspace = %+v/%+v/%+v", model.Problem, model.Receipt, model.Workspace)
	}
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("Write error:") || !tester.HasText("May be stale") {
		t.Fatalf("write failure is not explicit: %q", tester.Texts())
	}
}

// UI-006.
func TestWorkbenchCancelReloadIgnoresLateResult(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-cancel")
	var tasks []func()
	model.Async = func(fn func()) { tasks = append(tasks, fn) }
	model.Reload()
	if model.Phase != PhaseLoading || len(tasks) != 1 {
		t.Fatalf("reload phase/tasks = %v/%d", model.Phase, len(tasks))
	}
	model.CancelOperation()
	if model.Phase != PhaseReady {
		t.Fatalf("cancel phase = %v", model.Phase)
	}
	tasks[0]()
	if model.Phase != PhaseReady || model.Problem != nil {
		t.Fatalf("late result changed canceled state: %v/%+v", model.Phase, model.Problem)
	}
}

// UI-006.
func TestWorkbenchCancelPreviewIgnoresLateResult(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-cancel-preview")
	var tasks []func()
	model.Async = func(fn func()) { tasks = append(tasks, fn) }
	model.PreviewValidationChanges()
	if model.Phase != PhasePreviewing || len(tasks) != 1 {
		t.Fatalf("preview phase/tasks = %v/%d", model.Phase, len(tasks))
	}
	model.CancelOperation()
	tasks[0]()
	if model.Phase != PhaseReady || model.Preview != nil || model.Problem != nil {
		t.Fatalf("late preview changed canceled state: %v/%+v/%+v", model.Phase, model.Preview, model.Problem)
	}
}

type retryInspectService struct {
	app.StorageService
	calls int
}

func (s *retryInspectService) Inspect(ctx context.Context, root string) (app.Workspace, error) {
	s.calls++
	if s.calls == 1 {
		return app.Workspace{}, errors.New("temporary inspect failure")
	}
	return s.StorageService.Inspect(ctx, root)
}

// UI-005.
func TestWorkbenchRetryFailedInspection(t *testing.T) {
	service := &retryInspectService{StorageService: demo.NewStorageService()}
	picker := &stubDirectoryPicker{path: "/fixtures/v2-full"}
	model := newTestWorkbench(service, picker)
	model.OpenSource()
	if model.Phase != PhaseFailed {
		t.Fatalf("initial failure phase = %v", model.Phase)
	}
	model.Retry()
	if model.Phase != PhaseReady || service.calls != 2 || picker.calls != 1 {
		t.Fatalf("retry phase/service calls/picker calls = %v/%d/%d", model.Phase, service.calls, picker.calls)
	}
}

// UI-018.
func TestWorkbenchReceiptRowsAreVisible(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-receipt")
	model.PreviewValidationChanges()
	model.ConfirmPreview()
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("settings.json") || !tester.HasText("written") {
		t.Fatalf("receipt rows are not rendered: %q", tester.Texts())
	}
	backupID := model.Receipt.Results[0].Backup.ID
	if !tester.HasText(backupID) {
		t.Fatalf("receipt backup %q is not rendered", backupID)
	}
}

// Demo paths only select special datasets by exact fixture directory name.
func TestWorkbenchDemoPathDoesNotInferVersionFromSubstring(t *testing.T) {
	picker := &stubDirectoryPicker{path: "/tmp/v1-copy"}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	model.OpenSource()
	if model.Workspace.StorageVersion != 2 || len(model.Workspace.Summary.Issues) != 0 {
		t.Fatalf("substring path produced misleading demo data: %+v", model.Workspace)
	}
}

// UI-020.
func TestWorkbenchWriteFailureClearsPreviousReceipt(t *testing.T) {
	base := demo.NewStorageService()
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(base, picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-old-receipt")
	model.PreviewValidationChanges()
	model.ConfirmPreview()
	if model.Receipt == nil {
		t.Fatal("successful write did not create a receipt")
	}
	model.Service = saveFailureService{StorageService: base, err: &app.UIError{Kind: app.ErrorStale, Message: "Preview expired"}}
	model.PreviewValidationChanges()
	model.ConfirmPreview()
	if model.Receipt != nil {
		t.Fatalf("failed write kept the previous successful receipt: %+v", model.Receipt)
	}
}

// UI-012 and UI-017.
func TestWorkbenchPreviewCancelButtonCanReopen(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-preview-cancel")
	model.PreviewValidationChanges()
	tester := ui.NewTester(model.View, 1120, 760)
	if err := tester.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if model.Preview != nil {
		t.Fatal("cancel button left the preview open")
	}
	model.PreviewValidationChanges()
	if model.Preview == nil {
		t.Fatal("preview could not be reopened after cancel")
	}
}

// UI-029.
func TestWorkbenchCloseSuppressesPickerCompletion(t *testing.T) {
	picker := &stubDirectoryPicker{path: "/fixtures/v2-full"}
	model := NewWorkbenchModel(demo.NewStorageService(), picker)
	var tasks []func()
	dispatchCalls := 0
	model.Async = func(fn func()) { tasks = append(tasks, fn) }
	model.Dispatch = func(fn func()) { dispatchCalls++; fn() }
	model.OpenSource()
	if len(tasks) != 1 {
		t.Fatalf("picker tasks = %d", len(tasks))
	}
	if err := model.Close(); err != nil {
		t.Fatal(err)
	}
	tasks[0]()
	if dispatchCalls != 0 || model.Workspace.ID != "" {
		t.Fatalf("closed model dispatched picker completion: calls/workspace = %d/%+v", dispatchCalls, model.Workspace)
	}
}

// UI-012 and UI-017.
func TestWorkbenchOpenSourceInvalidatesPreview(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-old-preview")
	model.PreviewValidationChanges()
	if model.Preview == nil {
		t.Fatal("preview was not opened")
	}
	picker.path = "/fixtures/v1-hosts"
	model.OpenSource()
	if model.Preview != nil {
		t.Fatal("changing source retained a preview for the old workspace")
	}
	model.ConfirmPreview()
	if model.Problem != nil || model.Receipt != nil {
		t.Fatalf("invalid preview attempted a write: %+v/%+v", model.Problem, model.Receipt)
	}
}

// UI-031.
func TestWorkbenchWriteFailureCancelsDeferredQuit(t *testing.T) {
	base := demo.NewStorageService()
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(base, picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-failed-quit")
	model.Service = saveFailureService{StorageService: base, err: &app.UIError{Kind: app.ErrorWrite, Message: "Write failed"}}
	quitCalls := 0
	model.OnSafeToQuit = func() { quitCalls++ }
	model.PreviewValidationChanges()
	model.QuitRequested = true
	model.ConfirmPreview()
	if model.QuitRequested || quitCalls != 0 || model.Problem == nil {
		t.Fatalf("failed write continued quit: requested/calls/problem = %v/%d/%+v", model.QuitRequested, quitCalls, model.Problem)
	}
}

// UI-030.
func TestWorkbenchInitialPickerQuitHasVisibleState(t *testing.T) {
	model := newTestWorkbench(demo.NewStorageService(), &stubDirectoryPicker{})
	model.Phase = PhaseSelecting
	if model.RequestQuit() {
		t.Fatal("picker did not defer quit")
	}
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("Quit is waiting for directory selection") {
		t.Fatalf("picker quit state is not visible: %q", tester.Texts())
	}
	if err := tester.Click("Cancel Quit"); err != nil {
		t.Fatal(err)
	}
	if model.QuitRequested {
		t.Fatal("cancel quit did not clear picker quit request")
	}
}

// UI-030.
func TestWorkbenchQuitWaitsForWriting(t *testing.T) {
	model := newTestWorkbench(demo.NewStorageService(), &stubDirectoryPicker{})
	model.Phase = PhaseWriting
	if model.RequestQuit() || !model.QuitRequested {
		t.Fatal("writing did not defer quit")
	}
	model.CancelQuit()
	if model.QuitRequested {
		t.Fatal("cancel quit left request set")
	}
	model.Phase = PhaseReady
	if !model.RequestQuit() {
		t.Fatal("idle model refused quit")
	}
	model.Phase = PhaseSelecting
	if model.RequestQuit() || !model.QuitRequested {
		t.Fatal("directory selection did not defer quit until the native picker dismisses")
	}
}

// UI-027.
func TestWorkbenchHiddenWindowRetainsWorkspace(t *testing.T) {
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(demo.NewStorageService(), picker)
	openTestWorkspace(t, model, picker, "/fixtures/v2-full", "/tmp/work-hidden")
	tester := ui.NewTester(model.View, 1120, 760)
	model.SelectedTab = 3
	tester.SetWindowHidden(true)
	tester.SetWindowHidden(false)
	tester.Frame()

	if model.SelectedTab != 3 || model.Workspace.Root != "/tmp/work-hidden" {
		t.Fatalf("hidden window lost state: tab/root = %d/%q", model.SelectedTab, model.Workspace.Root)
	}
}
