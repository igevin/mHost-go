package desktop

import (
	"context"
	"fmt"
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

func TestHostsWorkbenchDirtySwitchSwitchesImmediately(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# draft note\n")
	model.SyncFromEditor()

	model.SelectProfile("conflict")
	model.SyncFromEditor()
	if model.SelectedID != "conflict" {
		t.Fatalf("selected = %q", model.SelectedID)
	}
	if !strings.Contains(model.Editor.Buffer.String(), "api.demo.test") {
		t.Fatalf("editor content = %q", model.Editor.Buffer.String())
	}

	model.switchToProfile("dev")
	model.SyncFromEditor()
	if !strings.Contains(model.Editor.Buffer.String(), "# draft note") || !model.Modified() {
		t.Fatalf("draft not preserved after immediate switch: %q modified=%v", model.Editor.Buffer.String(), model.Modified())
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

func TestHostsWorkbenchApplySavesModifiedDraftBeforePreview(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# draft note\n")
	model.SyncFromEditor()

	model.StartApply()
	if model.Problem != nil {
		t.Fatalf("save-and-preview problem = %+v", model.Problem)
	}
	if model.Modified() {
		t.Fatal("draft still modified after Apply… saved it")
	}
	if model.Staged == nil || len(model.Staged.Blockers) != 0 {
		t.Fatalf("staged plan missing after save: %+v", model.Staged)
	}
	detail, err := model.Service.LoadProfile(context.Background(), "dev")
	if err != nil {
		t.Fatalf("load saved profile: %v", err)
	}
	if !strings.Contains(detail.Content, "# draft note") {
		t.Fatalf("profile was not saved before preview: %q", detail.Content)
	}

	model.CancelStaged()
	if model.Info == "" || !strings.Contains(model.Info, "Profile saved") || !strings.Contains(model.Info, "system hosts unchanged") {
		t.Fatalf("cancel did not explain saved-but-not-applied state: %q", model.Info)
	}
}

func TestHostsWorkbenchResolutionKeepsAutoSavedCancelNotice(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "conflict")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# resolution note\n")
	model.SyncFromEditor()

	model.StartApply()
	if model.Staged == nil || model.Modified() || model.Problem != nil {
		t.Fatalf("save-and-preview state: staged=%+v modified=%v problem=%+v", model.Staged, model.Modified(), model.Problem)
	}
	model.ChooseResolution("api.demo.test", "192.168.1.1")
	if model.Staged == nil || model.Problem != nil {
		t.Fatalf("resolution preview failed: staged=%+v problem=%+v", model.Staged, model.Problem)
	}
	model.CancelStaged()
	if model.Info == "" || !strings.Contains(model.Info, "Profile saved") || !strings.Contains(model.Info, "system hosts unchanged") {
		t.Fatalf("auto-saved notice lost after resolution: %q", model.Info)
	}
}

func TestHostsWorkbenchApplyWithInvalidDraftDoesNotPreview(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SelectProfile(model.Profiles[0].ID)
	model.Editor.Select(0, 0)
	model.Editor.Insert("999.999.999.999 bad.invalid\n")
	model.SyncFromEditor()

	model.StartApply()
	if model.Problem == nil {
		t.Fatal("invalid draft should fail before preview")
	}
	if model.Staged != nil {
		t.Fatalf("invalid draft must not stage an apply plan: %+v", model.Staged)
	}
	if !model.Modified() {
		t.Fatal("failed save-and-preview must keep the editable draft")
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

func TestHostsWorkbenchQuitWithUnsavedDraftOffersSaveAll(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# quit note\n")
	model.SyncFromEditor()
	quitted := false
	model.OnSafeToQuit = func() { quitted = true }

	if model.RequestQuit() {
		t.Fatal("quit with an unsaved draft should require a choice")
	}
	if !model.PendingQuit || model.QuitRequested {
		t.Fatalf("quit state = pending %v requested %v", model.PendingQuit, model.QuitRequested)
	}
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if err := tester.Click("Save All"); err != nil {
		t.Fatalf("click save all: %v (texts %q)", err, tester.Texts())
	}
	if model.Problem != nil || model.Modified() || model.PendingQuit {
		t.Fatalf("save-all quit result: problem=%+v modified=%v pending=%v", model.Problem, model.Modified(), model.PendingQuit)
	}
	if !quitted {
		t.Fatal("safe quit was not triggered after Save All")
	}
}

func TestHostsWorkbenchQuitWithUnsavedDraftCanCancel(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# quit note\n")
	model.SyncFromEditor()

	if model.RequestQuit() {
		t.Fatal("quit with an unsaved draft should require a choice")
	}
	model.CancelQuit()
	if model.PendingQuit || model.QuitRequested {
		t.Fatalf("cancel did not clear quit state: pending=%v requested=%v", model.PendingQuit, model.QuitRequested)
	}
	allowed := model.RequestQuit()
	if allowed || !model.PendingQuit {
		t.Fatalf("a new quit request should offer the choice again: allowed=%v pending=%v", allowed, model.PendingQuit)
	}
	model.CancelQuit()
	if !model.Modified() {
		t.Fatal("canceling quit must not discard the draft")
	}
}

func TestHostsWorkbenchQuitWithoutSavingDiscardsDrafts(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	model.Editor.Select(0, 0)
	model.Editor.Insert("# quit note\n")
	model.SyncFromEditor()
	quitted := false
	model.OnSafeToQuit = func() { quitted = true }

	if model.RequestQuit() {
		t.Fatal("quit with an unsaved draft should require a choice")
	}
	model.QuitWithoutSaving()
	if !quitted {
		t.Fatal("discard choice did not trigger safe quit")
	}
	if !model.RequestQuit() {
		t.Fatal("discard latch should allow the framework quit to proceed")
	}
}

func TestHostsWorkbenchSaveAllAndQuitSavesEveryDraft(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 one.test\n", "127.0.0.1 two.test\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	firstID := model.Profiles[0].ID
	secondID := model.Profiles[1].ID
	model.Editor.Select(0, 0)
	model.Editor.Insert("# first note\n")
	model.SyncFromEditor()
	model.switchToProfile(secondID)
	model.SyncFromEditor()
	model.Editor.Select(0, 0)
	model.Editor.Insert("# second note\n")
	model.SyncFromEditor()
	quitted := false
	model.OnSafeToQuit = func() { quitted = true }

	if model.RequestQuit() {
		t.Fatal("quit with unsaved drafts should require a choice")
	}
	model.SaveAllAndQuit()
	if model.Problem != nil || !quitted {
		t.Fatalf("save-all quit: problem=%+v quitted=%v", model.Problem, quitted)
	}
	for id, note := range map[string]string{firstID: "# first note", secondID: "# second note"} {
		detail, err := svc.LoadProfile(context.Background(), id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if !strings.Contains(detail.Content, note) {
			t.Fatalf("profile %s was not saved: %q", id, detail.Content)
		}
	}
	if model.SelectedID != secondID {
		t.Fatalf("save-all changed the selected profile: %q", model.SelectedID)
	}
}

func TestHostsWorkbenchSaveAllFailureKeepsApplicationOpen(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 one.test\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.Editor.Select(0, 0)
	model.Editor.Insert("999.999.999.999 bad.invalid\n")
	model.SyncFromEditor()
	quitted := false
	model.OnSafeToQuit = func() { quitted = true }

	if model.RequestQuit() {
		t.Fatal("quit with an unsaved draft should require a choice")
	}
	model.SaveAllAndQuit()
	if model.Problem == nil {
		t.Fatal("invalid draft should surface a save error")
	}
	if quitted || model.PendingQuit || !model.Modified() {
		t.Fatalf("failed save-all state: quitted=%v pending=%v modified=%v", quitted, model.PendingQuit, model.Modified())
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

func TestHostsWorkbenchDeferredQuitWithDirtyDraftShowsChoiceAfterApply(t *testing.T) {
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
	// 在另一个 Profile 上保留未保存草稿；当前要 Apply 的 dev 保持已保存。
	run(func() { model.switchToProfile("conflict") })
	run(func() { model.SyncFromEditor() })
	run(func() {
		model.Editor.Select(0, 0)
		model.Editor.Insert("# deferred quit note\n")
		model.SyncFromEditor()
	})
	run(func() { model.switchToProfile("dev") })
	run(func() { model.SyncFromEditor() })
	run(func() { model.PreviewApply() })
	quitted := false
	run(func() { model.OnSafeToQuit = func() { quitted = true } })
	model.Async = func(fn func()) { go fn() }
	done := make(chan struct{})
	go func() { model.Apply(); close(done) }()
	<-service.applyStarted
	if allowed := func() bool { ch := make(chan bool, 1); run(func() { ch <- model.RequestQuit() }); return <-ch }(); allowed {
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
	pending := func() bool { ch := make(chan bool, 1); run(func() { ch <- model.PendingQuit }); return <-ch }()
	quitRequested := func() bool { ch := make(chan bool, 1); run(func() { ch <- model.QuitRequested }); return <-ch }()
	if !pending || quitRequested || quitted {
		t.Fatalf("deferred quit with a dirty draft = pending %v requested %v quitted %v", pending, quitRequested, quitted)
	}
}

func TestHostsWorkbenchSidebarRendersSelectedProfileOnce(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "conflict")
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	occurrences := 0
	for _, text := range tester.Texts() {
		if text == "冲突样本" {
			occurrences++
		}
	}
	// One sidebar button + one header name; the sidebar must not double-render
	// the selected profile.
	if occurrences != 2 {
		t.Fatalf("selected profile rendered %d times, want 2: %q", occurrences, tester.Texts())
	}
}

func TestHostsWorkbenchProblemsPanelWithManyDiagnostics(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	var lines strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&lines, "10.0.0.%d crowd.demo.test\n", i)
	}
	model.Editor.Select(0, 0)
	model.Editor.Insert(lines.String())
	model.SyncFromEditor()
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	// The editor status row and the problems header must both stay visible
	// regardless of how many diagnostics exist.
	if !tester.HasText("Cmd-F find · edits invalidate a staged preview") {
		t.Fatalf("editor status row squeezed out: %q", tester.Texts())
	}
	if !tester.HasText("Problems (30)") {
		t.Fatalf("problems panel missing: %q", tester.Texts())
	}
}

func TestHostsWorkbenchProblemsSplitGivesEditorTheMajorShare(t *testing.T) {
	model := newTestHostsWorkbench(demo.NewHostsService())
	loadAndSelect(t, model, "dev")
	var lines strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&lines, "10.0.0.%d crowd.demo.test\n", i)
	}
	model.Editor.Select(0, 0)
	model.Editor.Insert(lines.String())
	model.SyncFromEditor()
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	statusRect, ok := tester.Find("Cmd-F find · edits invalidate a staged preview")
	if !ok {
		t.Fatalf("editor status row missing: %q", tester.Texts())
	}
	problemsRect, ok := tester.Find("Problems (30)")
	if !ok {
		t.Fatalf("problems panel missing: %q", tester.Texts())
	}
	t.Logf("status y=%v problems y=%v", statusRect.Y, problemsRect.Y)
	// SplitVerticalPercent takes the first pane's share of the room in
	// percent (0-100), so ProblemsSplit=70 puts the editor on top with 70%
	// and the problems panel in the bottom 30%. Lock that geometry: the
	// problems title sits in the bottom third of the window, and the editor
	// status row at the bottom of the top pane stays well above it. If the
	// percentage were read as the second pane's share, or the range were
	// 0-1, the divider would land near the top and the problems title would
	// move up into the top half.
	if problemsRect.Y < 760*0.6 {
		t.Fatalf("problems panel top y=%v, want bottom third (split share semantics changed?)", problemsRect.Y)
	}
	if statusRect.Y+statusRect.H >= problemsRect.Y {
		t.Fatalf("editor status row (y=%v..%v) should end above the problems panel (y=%v)",
			statusRect.Y, statusRect.Y+statusRect.H, problemsRect.Y)
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

func TestHostsWorkbenchDirtySwitchFromViewSwitchesImmediately(t *testing.T) {
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
	if model.SelectedID != "conflict" {
		t.Fatalf("selected = %q", model.SelectedID)
	}
	model.switchToProfile("dev")
	model.SyncFromEditor()
	if !strings.Contains(model.Editor.Buffer.String(), "# note") || !model.Modified() {
		t.Fatalf("draft lost after view switch: %q modified=%v", model.Editor.Buffer.String(), model.Modified())
	}
}
