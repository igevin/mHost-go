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

	PendingDelete string
	PendingQuit   bool
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
	savedForPreview   bool // Apply… 刚自动保存了草稿；取消预览时需说明系统未变
	quitDiscard       bool // 用户明确选择不保存退出；允许框架后续 quit 事件
	quitAfterSaveAll  bool
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
	// 草稿按 Profile 保存在内存中，切换本身不丢数据；不需要确认打断。
	m.switchToProfile(profileID)
}

func (m *HostsWorkbenchModel) switchToProfile(profileID string) {
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
	m.savedForPreview = false
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

// StartApply 是 Apply… 按钮入口：未修改时直接预览；有草稿时先保存，
// 保存成功后再进入同一个预览流程。系统改写仍必须经过预览和最终确认。
func (m *HostsWorkbenchModel) StartApply() {
	if m.busy() || m.SelectedID == "" {
		return
	}
	if !m.Modified() {
		m.PreviewApply()
		return
	}
	m.saveDraftThenPreviewApply()
}

func (m *HostsWorkbenchModel) saveDraftThenPreviewApply() {
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
		})
		m.refreshProfilesThenPreview(id, ctx, profileID)
	})
}

// refreshProfilesThenPreview 在 worker 侧刷新列表，再在主线程生成预览；
// 刷新失败不得继续预览。
func (m *HostsWorkbenchModel) refreshProfilesThenPreview(id uint64, ctx context.Context, profileID string) {
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
		if m.SelectedID == profileID {
			m.previewApply(true)
		}
	})
}

func (m *HostsWorkbenchModel) PreviewApply() {
	m.previewApply(false)
}

func (m *HostsWorkbenchModel) previewApply(savedForPreview bool) {
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
				m.savedForPreview = false
				m.setError("preview_apply", err)
				return
			}
			m.Staged = &plan
			m.PlanStale = false
			m.savedForPreview = savedForPreview
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
	if m.savedForPreview {
		m.Info = "Profile saved · system hosts unchanged"
	}
	m.Staged = nil
	m.PlanStale = false
	m.Resolutions = map[string]string{}
	m.savedForPreview = false
}

func (m *HostsWorkbenchModel) ChooseResolution(domain, ip string) {
	if m.Staged == nil {
		return
	}
	m.Resolutions[domain] = ip
	// Re-render the preview so the staged diff matches what Apply will write.
	// Keep the auto-save marker: canceling after a resolution choice must still
	// explain that the profile was saved while system hosts stayed unchanged.
	if !m.busy() && !m.Modified() {
		m.previewApply(m.savedForPreview)
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
	m.savedForPreview = false
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

// RequestQuit 在真正可能丢失草稿时改为展示选择框。写入中的 Apply 仍按
// 原安全门禁等待；写入完成后若仍有未保存草稿，再展示选择而不是自动退出。
func (m *HostsWorkbenchModel) RequestQuit() bool {
	if m.quitDiscard {
		return true
	}
	if m.PendingQuit {
		return false
	}
	if m.Phase == HostsPhaseApplying {
		m.QuitRequested = true
		return false
	}
	if m.hasUnsavedDrafts() {
		m.PendingQuit = true
		if m.cancel != nil {
			m.cancel()
		}
		return false
	}
	if m.cancel != nil {
		m.cancel()
	}
	return true
}

func (m *HostsWorkbenchModel) CancelQuit() {
	m.PendingQuit = false
	m.QuitRequested = false
	m.quitAfterSaveAll = false
}

// QuitWithoutSaving 仅由退出确认框调用：用户明确放弃内存草稿后允许框架
// 继续分发 quit 事件。
func (m *HostsWorkbenchModel) QuitWithoutSaving() {
	if m.busy() {
		return
	}
	m.PendingQuit = false
	m.QuitRequested = false
	m.quitAfterSaveAll = false
	m.quitDiscard = true
	m.completeSafeQuit()
}

// SaveAllAndQuit 保存全部草稿；任一保存失败都保持应用打开并显示原因。
func (m *HostsWorkbenchModel) SaveAllAndQuit() {
	m.PendingQuit = false
	m.quitAfterSaveAll = true
	m.saveAllDrafts()
}

func (m *HostsWorkbenchModel) saveAllDrafts() {
	if m.busy() || m.Service == nil {
		m.quitAfterSaveAll = false
		return
	}
	id, ctx := m.beginOperation()
	m.Phase = HostsPhaseApplying
	m.Problem = nil
	m.saveNextDraft(id, ctx)
}

func (m *HostsWorkbenchModel) saveNextDraft(id uint64, ctx context.Context) {
	for _, profile := range m.Profiles {
		draft := m.drafts[profile.ID]
		if draft == nil || draft.content == draft.savedContent {
			continue
		}
		profileID := profile.ID
		content := draft.content
		revision := draft.revision
		m.runAsync(func() {
			detail, err := m.Service.SaveProfile(ctx, profileID, content, revision)
			if err != nil {
				m.dispatch(func() {
					if m.current(id) {
						m.quitAfterSaveAll = false
						m.setError("save_all", err)
					}
				})
				return
			}
			m.dispatch(func() {
				if !m.current(id) {
					return
				}
				draft.savedContent = detail.Content
				draft.revision = detail.Revision
			})
			m.saveNextDraft(id, ctx)
		})
		return
	}
	m.dispatch(func() {
		if !m.current(id) {
			return
		}
		m.Phase = HostsPhaseIdle
		m.finishOperation()
		if m.quitAfterSaveAll {
			m.quitAfterSaveAll = false
			m.completeSafeQuit()
		}
	})
}

func (m *HostsWorkbenchModel) hasUnsavedDrafts() bool {
	for _, draft := range m.drafts {
		if draft.content != draft.savedContent {
			return true
		}
	}
	return false
}

func (m *HostsWorkbenchModel) unsavedDraftCount() int {
	count := 0
	for _, draft := range m.drafts {
		if draft.content != draft.savedContent {
			count++
		}
	}
	return count
}

func (m *HostsWorkbenchModel) completeSafeQuit() {
	if m.OnSafeToQuit != nil {
		m.OnSafeToQuit()
	}
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
		if m.hasUnsavedDrafts() {
			// 写入已经到达安全边界，但退出仍可能丢弃其他 Profile 的草稿。
			m.PendingQuit = true
			return
		}
		m.completeSafeQuit()
	}
}
