package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/egoist/mygo/ui/editor"
	app "github.com/igevin/mHost-go/internal/app"
)

type HostsPhase string

const (
	HostsPhaseIdle       HostsPhase = "idle"
	HostsPhaseLoading    HostsPhase = "loading"
	HostsPhasePreviewing HostsPhase = "previewing"
	HostsPhaseApplying   HostsPhase = "applying"
)

type hostsDraft struct {
	content      string
	savedContent string
	revision     uint64
}

// HostsWorkbenchModel owns the hosts rewrite workbench state on the MyGo main
// thread. Worker results return through Dispatch; App completes Close on that
// same thread. One editor surface is always editable; unsaved drafts live in
// drafts and survive profile switches.
type HostsWorkbenchModel struct {
	Service  app.HostsService
	Async    func(func())
	Dispatch func(func())

	Profiles   []app.HostsProfile
	Applied    app.AppliedState
	SelectedID string
	Editor     *editor.State

	Phase       HostsPhase
	Staged      *app.ApplyPlan
	PlanStale   bool
	Resolutions map[string]string
	Receipt     *app.ApplyReceipt
	Problem     *app.UIError

	PendingSwitch string
	PendingDelete string
	QuitRequested bool
	DemoMode      bool
	Chromeless    bool // Alpha 壳嵌入时隐藏自身标题栏（壳已有导航头）
	Admin         ProfileAdmin
	SearchQuery   string
	Picker        app.DirectoryPicker
	Info          string // 一次性操作提示（导出/导入结果）
	OnSafeToQuit  func()

	// ProblemsSplit is the editor's share (percent) of the vertical split
	// between the editor and the problems panel; the user drags the divider
	// to resize, and the value persists across frames and profile switches.
	ProblemsSplit float32

	drafts            map[string]*hostsDraft
	operation         uint64
	cancel            context.CancelFunc
	closed            atomic.Bool
	findBar           *editor.FindBar
	diagnosticVersion uint64
}

func NewHostsWorkbenchModel(service app.HostsService) *HostsWorkbenchModel {
	buffer := editor.NewBuffer("")
	buffer.SetLanguage(HostsLanguage())
	return &HostsWorkbenchModel{
		Service:  service,
		Async:    func(fn func()) { go fn() },
		Dispatch: func(fn func()) { fn() },
		Phase:    HostsPhaseIdle,
		Editor: &editor.State{
			Buffer:         buffer,
			Label:          "Hosts rules",
			KeepScrollbars: true,
			LineNumbers:    editor.LineNumbersOn,
		},
		drafts:        map[string]*hostsDraft{},
		Resolutions:   map[string]string{},
		ProblemsSplit: 70,
	}
}

// Title 是窗口与托盘标题。
func (m *HostsWorkbenchModel) Title() string { return "mHost Phase 2 · Hosts Rewrite Workbench" }

// SetDispatch, SetOnSafeToQuit and SetProblem wire the model into App; they
// satisfy desktop.AppModel together with View/RequestQuit/CancelQuit/Close.
func (m *HostsWorkbenchModel) SetDispatch(fn func(func()))     { m.Dispatch = fn }
func (m *HostsWorkbenchModel) SetOnSafeToQuit(fn func())       { m.OnSafeToQuit = fn }
func (m *HostsWorkbenchModel) SetProblem(problem *app.UIError) { m.Problem = problem }

func (m *HostsWorkbenchModel) Load() {
	if m.busy() || m.Service == nil {
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseLoading
	m.Problem = nil
	m.runAsync(func() {
		profiles, err := m.Service.ListProfiles(ctx)
		if err != nil {
			m.dispatchError(id, "load", err)
			return
		}
		applied, err := m.Service.AppliedState(ctx)
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("load", err)
				return
			}
			m.Profiles = profiles
			m.Applied = applied
			m.Phase = HostsPhaseIdle
			m.finishOperation()
			if m.SelectedID == "" && len(profiles) > 0 {
				m.switchToProfile(profiles[0].ID)
			}
		})
	})
}

func (m *HostsWorkbenchModel) SelectProfile(profileID string) {
	if m.busy() || profileID == "" || profileID == m.SelectedID {
		return
	}
	if m.Modified() {
		m.PendingSwitch = profileID
		return
	}
	m.switchToProfile(profileID)
}

func (m *HostsWorkbenchModel) ConfirmSwitch() {
	if m.PendingSwitch == "" {
		return
	}
	target := m.PendingSwitch
	m.PendingSwitch = ""
	m.switchToProfile(target)
}

func (m *HostsWorkbenchModel) CancelSwitch() {
	m.PendingSwitch = ""
}

func (m *HostsWorkbenchModel) switchToProfile(profileID string) {
	m.PendingSwitch = ""
	if draft, ok := m.drafts[profileID]; ok {
		m.presentDraft(profileID, draft)
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseLoading
	m.Problem = nil
	m.runAsync(func() {
		detail, err := m.Service.LoadProfile(ctx, profileID)
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("load_profile", err)
				return
			}
			draft := &hostsDraft{content: detail.Content, savedContent: detail.Content, revision: detail.Revision}
			m.drafts[profileID] = draft
			m.presentDraft(profileID, draft)
			m.Phase = HostsPhaseIdle
			m.finishOperation()
		})
	})
}

func (m *HostsWorkbenchModel) presentDraft(profileID string, draft *hostsDraft) {
	m.SelectedID = profileID
	m.Staged = nil
	m.PlanStale = false
	m.Resolutions = map[string]string{}
	m.Editor.ReadOnly = m.profileProtected(profileID)
	m.Editor.Buffer.Reset(draft.content)
	m.diagnosticVersion = 0
}

func (m *HostsWorkbenchModel) profileProtected(profileID string) bool {
	for _, profile := range m.Profiles {
		if profile.ID == profileID {
			return profile.Protected
		}
	}
	return false
}

func (m *HostsWorkbenchModel) currentDraft() *hostsDraft {
	return m.drafts[m.SelectedID]
}

func (m *HostsWorkbenchModel) editorContent() string {
	return m.Editor.Buffer.String()
}

// SyncFromEditor folds editor changes back into the draft. The view calls it
// once per frame; tests call it directly. Editing invalidates a staged plan.
func (m *HostsWorkbenchModel) SyncFromEditor() {
	draft := m.currentDraft()
	if draft == nil {
		return
	}
	text := m.editorContent()
	if text == draft.content {
		return
	}
	draft.content = text
	if m.Staged != nil && !m.PlanStale {
		m.PlanStale = true
	}
}

func (m *HostsWorkbenchModel) Modified() bool {
	draft := m.currentDraft()
	return draft != nil && draft.content != draft.savedContent
}

func (m *HostsWorkbenchModel) Save() {
	draft := m.currentDraft()
	if m.busy() || draft == nil || !m.Modified() {
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseApplying
	m.Problem = nil
	profileID := m.SelectedID
	content := draft.content
	revision := draft.revision
	m.runAsync(func() {
		detail, err := m.Service.SaveProfile(ctx, profileID, content, revision)
		if err != nil {
			m.dispatchError(id, "save", err)
			return
		}
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			draft.savedContent = detail.Content
			draft.revision = detail.Revision
			m.Staged = nil
			m.PlanStale = false
		})
		m.refreshProfiles(id, ctx)
	})
}

func (m *HostsWorkbenchModel) PreviewApply() {
	if m.busy() || m.SelectedID == "" || m.Modified() {
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhasePreviewing
	m.Problem = nil
	profileID := m.SelectedID
	resolutions := map[string]string{}
	for domain, ip := range m.Resolutions {
		resolutions[domain] = ip
	}
	m.runAsync(func() {
		plan, err := m.Service.PreviewApply(ctx, profileID, resolutions)
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			if err != nil {
				m.setError("preview_apply", err)
				return
			}
			m.Staged = &plan
			m.PlanStale = false
			// Keep choices made while this preview was in flight: only fill
			// domains the user has not chosen since.
			for domain, ip := range resolutions {
				if _, chosen := m.Resolutions[domain]; !chosen {
					m.Resolutions[domain] = ip
				}
			}
			m.Phase = HostsPhaseIdle
			m.finishOperation()
		})
	})
}

func (m *HostsWorkbenchModel) CancelStaged() {
	if m.busy() {
		return
	}
	m.Staged = nil
	m.PlanStale = false
	m.Resolutions = map[string]string{}
}

func (m *HostsWorkbenchModel) ChooseResolution(domain, ip string) {
	if m.Staged == nil {
		return
	}
	m.Resolutions[domain] = ip
	// Re-render the preview so the staged diff matches what Apply will write.
	if !m.busy() && !m.Modified() {
		m.PreviewApply()
	}
}

// ApplyBlockedReason returns why Apply is disabled, or "" when it is allowed.
func (m *HostsWorkbenchModel) ApplyBlockedReason() string {
	if m.busy() {
		return "Wait for the current operation"
	}
	if m.Staged == nil {
		return "Preview the rewrite first"
	}
	if m.PlanStale {
		return "The draft changed after this preview · preview again"
	}
	if len(m.Staged.Blockers) != 0 {
		return fmt.Sprintf("Fix %d parse error(s) in the editor first", len(m.Staged.Blockers))
	}
	for _, conflict := range m.Staged.Conflicts {
		if _, ok := m.Resolutions[conflict.Domain]; !ok {
			return fmt.Sprintf("Choose which IP to keep for %q", conflict.Domain)
		}
	}
	return ""
}

func (m *HostsWorkbenchModel) Apply() {
	if m.Staged == nil || m.ApplyBlockedReason() != "" {
		return
	}
	planID := m.Staged.ID
	resolutions := map[string]string{}
	for domain, ip := range m.Resolutions {
		resolutions[domain] = ip
	}
	m.Staged = nil
	m.PlanStale = false
	m.Resolutions = map[string]string{}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseApplying
	m.Problem = nil
	m.runAsync(func() {
		receipt, err := m.Service.Apply(ctx, planID, resolutions)
		if receipt.OperationID != "" || len(receipt.Results) != 0 {
			m.dispatch(func() {
				if m.current(id) {
					m.Receipt = &receipt
				}
			})
		}
		if err != nil {
			// Keep the conflict choices for the next preview; only the plan ID
			// is consumed by a failed apply.
			m.dispatch(func() {
				if m.current(id) {
					m.Resolutions = resolutions
				}
			})
			m.dispatchError(id, "apply", err)
			return
		}
		m.refreshApplied(id, ctx)
	})
}

func (m *HostsWorkbenchModel) Rollback(backupID string) {
	if m.busy() || backupID == "" {
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseApplying
	m.Problem = nil
	m.runAsync(func() {
		receipt, err := m.Service.Rollback(ctx, backupID)
		if receipt.OperationID != "" || len(receipt.Results) != 0 {
			m.dispatch(func() {
				if m.current(id) {
					m.Receipt = &receipt
				}
			})
		}
		if err != nil {
			m.dispatchError(id, "rollback", err)
			return
		}
		m.refreshApplied(id, ctx)
	})
}

// refreshProfiles and refreshApplied run service reads on the worker side and
// merge results on the main thread; service I/O never runs inside Dispatch.
func (m *HostsWorkbenchModel) refreshProfiles(id uint64, ctx context.Context) {
	profiles, err := m.Service.ListProfiles(ctx)
	m.dispatch(func() {
		if !m.current(id) {
			return
		}
		if err != nil {
			m.setError("refresh_profiles", err)
			return
		}
		m.Profiles = profiles
		m.Phase = HostsPhaseIdle
		m.finishOperation()
	})
}

func (m *HostsWorkbenchModel) refreshApplied(id uint64, ctx context.Context) {
	applied, err := m.Service.AppliedState(ctx)
	if err != nil {
		m.dispatch(func() {
			if m.current(id) {
				m.setError("refresh_applied", err)
			}
		})
		return
	}
	m.dispatch(func() {
		if !m.current(id) {
			return
		}
		m.Applied = applied
	})
	m.refreshProfiles(id, ctx)
}

func (m *HostsWorkbenchModel) RequestQuit() bool {
	if m.Phase == HostsPhaseApplying {
		m.QuitRequested = true
		return false
	}
	if m.cancel != nil {
		m.cancel()
	}
	return true
}

func (m *HostsWorkbenchModel) CancelQuit() {
	m.QuitRequested = false
}

func (m *HostsWorkbenchModel) Close() error {
	m.closed.Store(true)
	m.operation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	return nil
}

func (m *HostsWorkbenchModel) busy() bool {
	switch m.Phase {
	case HostsPhaseLoading, HostsPhasePreviewing, HostsPhaseApplying:
		return true
	default:
		return false
	}
}

func (m *HostsWorkbenchModel) beginOperation() (uint64, context.Context) {
	if m.cancel != nil {
		m.cancel()
	}
	m.operation++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return m.operation, ctx
}

func (m *HostsWorkbenchModel) current(id uint64) bool {
	return !m.closed.Load() && id == m.operation
}

func (m *HostsWorkbenchModel) runAsync(fn func()) {
	if m.closed.Load() {
		return
	}
	m.Async(fn)
}

func (m *HostsWorkbenchModel) dispatch(fn func()) {
	if m.closed.Load() {
		return
	}
	m.Dispatch(fn)
}

func (m *HostsWorkbenchModel) dispatchError(id uint64, operation string, err error) {
	m.dispatch(func() {
		if m.current(id) {
			m.setError(operation, err)
		}
	})
}

func (m *HostsWorkbenchModel) setError(operation string, err error) {
	if errors.Is(err, context.Canceled) {
		m.Problem = nil
		m.Phase = HostsPhaseIdle
		m.finishOperation()
		return
	}
	m.Problem = classifyUIError(operation, err)
	m.QuitRequested = false
	m.Phase = HostsPhaseIdle
	m.finishOperation()
}

func (m *HostsWorkbenchModel) finishOperation() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.QuitRequested && m.Phase != HostsPhaseApplying {
		m.QuitRequested = false
		if m.OnSafeToQuit != nil {
			m.OnSafeToQuit()
		}
	}
}
