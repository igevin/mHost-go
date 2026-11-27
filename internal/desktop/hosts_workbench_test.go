package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/demo"
)

func newTestHostsWorkbench(service *demo.HostsService) *HostsWorkbenchModel {
	model := NewHostsWorkbenchModel(service)
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	model.DemoMode = true
	return model
}

func loadAndSelect(t *testing.T, model *HostsWorkbenchModel, id string) {
	t.Helper()
	model.Load()
	if model.Problem != nil {
		t.Fatalf("load problem = %+v", model.Problem)
	}
	model.SelectProfile(id)
	model.SyncFromEditor()
	if model.SelectedID != id {
		t.Fatalf("selected = %q", model.SelectedID)
	}
}

func TestHostsWorkbenchLoadSelectsFirstProfile(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	if len(model.Profiles) != 0 {
		t.Fatalf("profiles loaded before Load: %+v", model.Profiles)
	}
	model.Load()
	if model.Problem != nil || len(model.Profiles) != 4 {
		t.Fatalf("profiles/problem = %+v/%+v", model.Profiles, model.Problem)
	}
	if model.SelectedID != "dev" {
		t.Fatalf("selected = %q", model.SelectedID)
	}
	if !strings.Contains(model.Editor.Buffer.String(), "api.dev.local") {
		t.Fatalf("editor content = %q", model.Editor.Buffer.String())
	}
	if model.Applied.ProfileID != "protected" {
		t.Fatalf("applied = %+v", model.Applied)
	}
}

func TestHostsWorkbenchEditSaveMarksStoredState(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	if model.Modified() {
		t.Fatal("fresh draft reported modified")
	}
	model.Editor.Select(0, 0)
	model.Editor.Insert("# draft note\n")
	model.SyncFromEditor()
	if !model.Modified() {
		t.Fatal("edited draft not modified")
	}
	model.Save()
	if model.Problem != nil {
		t.Fatalf("save problem = %+v", model.Problem)
	}
	if model.Modified() {
		t.Fatal("draft still modified after save")
	}
	var dev *app.HostsProfile
	for i := range model.Profiles {
		if model.Profiles[i].ID == "dev" {
			dev = &model.Profiles[i]
		}
	}
	if dev == nil || !strings.Contains(model.editorContent(), "# draft note") {
		t.Fatalf("saved content missing edit: %q", model.editorContent())
	}
}

func TestHostsWorkbenchDirtySwitchRequiresConfirmation(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# draft note\n")
	model.SyncFromEditor()

	model.SelectProfile("conflict")
	if model.PendingSwitch != "conflict" || model.SelectedID != "dev" {
		t.Fatalf("pending/selected = %q/%q", model.PendingSwitch, model.SelectedID)
	}
	model.CancelSwitch()
	if model.PendingSwitch != "" {
		t.Fatalf("pending = %q", model.PendingSwitch)
	}
	model.SelectProfile("conflict")
	model.ConfirmSwitch()
	model.SyncFromEditor()
	if model.SelectedID != "conflict" {
		t.Fatalf("selected = %q", model.SelectedID)
	}
	if !strings.Contains(model.Editor.Buffer.String(), "api.demo.test") {
		t.Fatalf("editor content = %q", model.Editor.Buffer.String())
	}
}

func TestHostsWorkbenchDraftPreservedAcrossSwitch(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# draft note\n")
	model.SyncFromEditor()

	model.switchToProfile("conflict")
	model.SyncFromEditor()
	model.switchToProfile("dev")
	model.SyncFromEditor()
	if !strings.Contains(model.Editor.Buffer.String(), "# draft note") {
		t.Fatalf("draft lost after switch: %q", model.Editor.Buffer.String())
	}
	if !model.Modified() {
		t.Fatal("draft no longer marked modified after switch")
	}
}

func TestHostsWorkbenchEditingInvalidatesStagedPlan(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Save()
	model.PreviewApply()
	if model.Staged == nil || model.PlanStale {
		t.Fatalf("staged/stale = %+v/%v", model.Staged, model.PlanStale)
	}
	model.Editor.Select(0, 0)
	model.Editor.Insert("# edit\n")
	model.SyncFromEditor()
	if !model.PlanStale {
		t.Fatal("staged plan not marked stale after edit")
	}
	if model.ApplyBlockedReason() == "" {
		t.Fatal("apply should be blocked with a visible reason")
	}
}

func TestHostsWorkbenchApplyCleanProfile(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.PreviewApply()
	if model.Staged == nil || len(model.Staged.Blockers) != 0 || len(model.Staged.Conflicts) != 0 {
		t.Fatalf("staged = %+v", model.Staged)
	}
	quitted := false
	model.OnSafeToQuit = func() { quitted = true }
	if !model.RequestQuit() {
		t.Fatal("quit with only a staged preview should proceed")
	}
	if model.QuitRequested {
		t.Fatal("quit requested without an in-flight write")
	}
	model.Apply()
	if model.Problem != nil {
		t.Fatalf("apply problem = %+v", model.Problem)
	}
	if model.Receipt == nil || model.Receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", model.Receipt)
	}
	if model.Applied.ProfileID != "dev" || model.Applied.Drifted {
		t.Fatalf("applied = %+v", model.Applied)
	}
	if quitted || model.QuitRequested {
		t.Fatalf("no quit should be pending: %v/%v", quitted, model.QuitRequested)
	}
}

func TestHostsWorkbenchConflictsBlockApplyUntilResolved(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "conflict")
	model.PreviewApply()
	if model.Staged == nil || len(model.Staged.Conflicts) != 1 {
		t.Fatalf("staged = %+v", model.Staged)
	}
	if model.ApplyBlockedReason() == "" {
		t.Fatal("unresolved conflicts should block apply with a reason")
	}
	model.ChooseResolution("api.demo.test", "192.168.1.1")
	if model.Staged == nil || !strings.Contains(model.Staged.MergedBlock, "192.168.1.1 api.demo.test") {
		t.Fatalf("staged preview did not re-render with the chosen IP: %+v", model.Staged)
	}
	if model.ApplyBlockedReason() != "" {
		t.Fatalf("apply still blocked: %q", model.ApplyBlockedReason())
	}
	model.Apply()
	if model.Problem != nil {
		t.Fatalf("apply problem = %+v", model.Problem)
	}
	if model.Applied.ProfileID != "conflict" {
		t.Fatalf("applied = %+v", model.Applied)
	}
}

func TestHostsWorkbenchBlockersPreventStagedApply(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "broken")
	model.PreviewApply()
	if model.Staged == nil || len(model.Staged.Blockers) != 1 {
		t.Fatalf("staged = %+v", model.Staged)
	}
	if model.ApplyBlockedReason() == "" {
		t.Fatal("blockers should disable apply with a reason")
	}
	model.Apply()
	if model.Problem != nil || model.Receipt != nil {
		t.Fatalf("blocked apply must not run: problem=%+v receipt=%+v", model.Problem, model.Receipt)
	}
}

func TestHostsWorkbenchApplyFailureOffersRollback(t *testing.T) {
	service := demo.NewHostsService()
	model := newTestHostsWorkbench(service)
	loadAndSelect(t, model, "dev")
	model.PreviewApply()
	service.FailNextApply()
	model.Apply()
	if model.Problem == nil {
		t.Fatal("expected apply failure problem")
	}
	if model.Receipt == nil || model.Receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", model.Receipt)
	}
	if model.Applied.ProfileID != "protected" {
		t.Fatalf("applied changed despite failure: %+v", model.Applied)
	}
	backupID := model.Receipt.RollbackRef.ID
	model.Rollback(backupID)
	if model.Problem != nil {
		t.Fatalf("rollback problem = %+v", model.Problem)
	}
	if model.Applied.ProfileID != "protected" || model.Applied.Drifted {
		t.Fatalf("applied after rollback = %+v", model.Applied)
	}
	if model.Receipt == nil {
		t.Fatal("rollback receipt missing")
	}
}

type blockingHostsService struct {
	*demo.HostsService
	applyStarted chan struct{}
	release      chan struct{}
}

func (b *blockingHostsService) Apply(ctx context.Context, planID string, resolutions map[string]string) (app.ApplyReceipt, error) {
	close(b.applyStarted)
	<-b.release
	return b.HostsService.Apply(ctx, planID, resolutions)
}

func TestHostsWorkbenchQuitGatedDuringApply(t *testing.T) {
	inner := demo.NewHostsService()
	service := &blockingHostsService{HostsService: inner, applyStarted: make(chan struct{}), release: make(chan struct{})}
	model := NewHostsWorkbenchModel(service)
	model.Async = func(fn func()) { fn() }
	dispatch := make(chan func(), 64)
	model.Dispatch = func(fn func()) { dispatch <- fn }
	go func() {
		for fn := range dispatch {
			fn()
		}
	}()
	run := func(fn func()) {
		done := make(chan struct{})
		dispatch <- func() { fn(); close(done) }
		<-done
	}
	model.DemoMode = true
	run(func() { model.Load() })
	run(func() { model.SelectProfile("dev") })
	run(func() { model.SyncFromEditor() })
	run(func() { model.PreviewApply() })
	quitted := false
	run(func() { model.OnSafeToQuit = func() { quitted = true } })
	model.Async = func(fn func()) { go fn() }
	done := make(chan struct{})
	go func() { model.Apply(); close(done) }()
	<-service.applyStarted
	if deferred := func() bool { ch := make(chan bool, 1); run(func() { ch <- model.RequestQuit() }); return <-ch }(); deferred {
		t.Fatal("quit during an in-flight write must be deferred")
	}
	close(service.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		phase := func() HostsPhase { ch := make(chan HostsPhase, 1); run(func() { ch <- model.Phase }); return <-ch }()
		if phase != HostsPhaseApplying || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	<-done
	quitReq := func() bool { ch := make(chan bool, 1); run(func() { ch <- model.QuitRequested }); return <-ch }()
	if quitReq || !quitted {
		t.Fatalf("deferred quit did not continue after the safe boundary: %v/%v", quitReq, quitted)
	}
}

func TestHostsWorkbenchInitialViewShowsSafetyCopy(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("No profile is loaded until you start the demo") {
		t.Fatalf("safety copy missing: %q", tester.Texts())
	}
	if !tester.HasText("Demo service · no /etc/hosts writes") {
		t.Fatalf("demo badge missing: %q", tester.Texts())
	}
}

func TestHostsWorkbenchViewShowsInlineDiagnosticsAndProblemsPanel(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "conflict")
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("Problems (4)") {
		t.Fatalf("problems panel missing: %q", tester.Texts())
	}
	if !tester.HasText("Line 2: duplicate domain \"api.demo.test\" (different IP)") {
		t.Fatalf("different-IP diagnostic missing: %q", tester.Texts())
	}
	if err := tester.Click("Line 1: duplicate domain \"api.demo.test\""); err != nil {
		t.Fatalf("click problem row: %v (texts %q)", err, tester.Texts())
	}
	if pos := model.Editor.CursorPos(); pos.Line != 0 {
		t.Fatalf("cursor line = %d, want 0", pos.Line)
	}
}

func TestHostsWorkbenchStagedPreviewConflictsAndResolution(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "conflict")
	tester := ui.NewTester(model.View, 1120, 760)
	if err := tester.Click("Apply…"); err != nil {
		t.Fatalf("click apply: %v (texts %q)", err, tester.Texts())
	}
	if model.Staged == nil {
		t.Fatal("no staged plan after Apply…")
	}
	tester.Frame()
	if !tester.HasText("Apply preview") {
		t.Fatalf("staged preview missing: %q", tester.Texts())
	}
	if !tester.HasText("Choose which IP to keep for \"api.demo.test\"") {
		t.Fatalf("blocking reason missing: %q", tester.Texts())
	}
	if err := tester.Click("Keep 10.0.0.1"); err != nil {
		t.Fatalf("click resolution: %v (texts %q)", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("Apply Changes"); err != nil {
		t.Fatalf("click apply changes: %v (texts %q)", err, tester.Texts())
	}
	if model.Applied.ProfileID != "conflict" {
		t.Fatalf("applied = %+v", model.Applied)
	}
	if model.Receipt == nil {
		t.Fatal("receipt missing after apply")
	}
}

func TestHostsWorkbenchFailureReceiptOffersRollback(t *testing.T) {
	service := demo.NewHostsService()
	model := newTestHostsWorkbench(service)
	loadAndSelect(t, model, "dev")
	service.FailNextApply()
	model.PreviewApply()
	model.Apply()
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("Apply incomplete") {
		t.Fatalf("failure summary missing: %q", tester.Texts())
	}
	if !tester.HasText("Write error") {
		t.Fatalf("write error banner missing: %q", tester.Texts())
	}
	if err := tester.Click("Rollback to Backup"); err != nil {
		t.Fatalf("click rollback: %v (texts %q)", err, tester.Texts())
	}
	if model.Problem != nil || model.Applied.ProfileID != "protected" {
		t.Fatalf("rollback problem/applied = %+v/%+v", model.Problem, model.Applied)
	}
	tester.Frame()
	if !tester.HasText("Apply verified") {
		t.Fatalf("rollback receipt missing: %q", tester.Texts())
	}
}

func TestHostsWorkbenchDirtySwitchDialogFromView(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# note\n")
	model.SyncFromEditor()
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if err := tester.Click("冲突样本"); err != nil {
		t.Fatalf("click profile: %v (texts %q)", err, tester.Texts())
	}
	if model.PendingSwitch != "conflict" || model.SelectedID != "dev" {
		t.Fatalf("pending/selected = %q/%q", model.PendingSwitch, model.SelectedID)
	}
	tester.Frame()
	if err := tester.Click("Stay"); err != nil {
		t.Fatalf("click stay: %v (texts %q)", err, tester.Texts())
	}
	if model.PendingSwitch != "" || model.SelectedID != "dev" {
		t.Fatalf("after stay: pending/selected = %q/%q", model.PendingSwitch, model.SelectedID)
	}
	tester.Frame()
	if err := tester.Click("冲突样本"); err != nil {
		t.Fatalf("click profile again: %v", err)
	}
	tester.Frame()
	if err := tester.Click("Switch"); err != nil {
		t.Fatalf("click switch: %v (texts %q)", err, tester.Texts())
	}
	if model.SelectedID != "conflict" {
		t.Fatalf("selected = %q", model.SelectedID)
	}
	model.switchToProfile("dev")
	model.SyncFromEditor()
	if !strings.Contains(model.Editor.Buffer.String(), "# note") {
		t.Fatalf("draft lost: %q", model.Editor.Buffer.String())
	}
}
