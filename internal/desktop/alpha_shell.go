package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/alphaboot"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

// Alpha 导航页：Hosts 为主链路；其余页面 #26 实现，本阶段标记未开放。
// #35：Snapshots 页整合为 History（Apply 备份可恢复历史）。
// #37：Settings 收敛为 Data（无可修改设置，只读数据信息）。
var alphaNavTabs = []string{"Hosts", "History", "System Hosts", "Data"}

// AlphaModel 是 Hosts-only Alpha 的应用壳（#25/#26）：导航 +
// Hosts 主链路 + 快照/系统 Hosts/设置页 + 启动信息展示。
// 生命周期语义委托给内部 HostsWorkbenchModel（退出门禁、
// 写不可取消等沿用 #13 安全语义）。
type AlphaModel struct {
	Hosts *HostsWorkbenchModel

	Async    func(func()) // worker 侧执行（默认 goroutine）
	Dispatch func(func()) // 主线程合并（App 注入 window.Update）

	SelectedNav int
	Boot        *alphaboot.Result
	bannerShown bool
	started     bool
	closed      atomic.Bool

	// 历史页（#35）：Apply 备份列表（懒加载，读盘在 worker 侧执行）
	historyLoaded  bool
	historyLoading bool
	history        []app.ApplyBackupRef
	historyErr     error

	// 恢复确认与执行（pendingRestore 非 nil 时展示确认对话框）
	pendingRestore *app.ApplyBackupRef
	restoring      bool
	restoreInfo    string
	restoreProblem *app.UIError

	// 存储快照子区（旧存储模型，与 Apply 备份分开展示）
	snapshotsLoaded  bool
	snapshotsLoading bool
	snapshots        []storage.SnapshotEntry
	snapshotIssues   []app.StorageIssue

	// 系统 Hosts 页（懒加载；#37 只读浏览器：行号、管理块标记、跳转、复制）
	systemHostsLoaded  bool
	systemHostsLoading bool
	systemHosts        string
	systemHostsErr     error
	systemHostsReadAt  time.Time
	systemHostsViewer  *editor.State

	// 数据页（#37：Settings 收敛为 Data，展示数据目录/系统 hosts 路径/备份计数）
	backupCountLoaded  bool
	backupCountLoading bool
	backupCount        int
	backupCountErr     error

	// 主页面回执监听（#37：Apply/Rollback/Undo 后辅助页缓存失效）
	lastReceiptOp string
}

// NewAlphaModel 构造 Alpha 壳。picker 为目录选择器（导入导出用）；
// 生产传 &NativeDirectoryPicker{}，nil 时导入导出入口禁用。
func NewAlphaModel(boot *alphaboot.Result, picker app.DirectoryPicker) *AlphaModel {
	hosts := NewHostsWorkbenchModel(boot.Service)
	hosts.Admin = boot.Service
	hosts.Picker = picker
	hosts.Chromeless = true
	return &AlphaModel{
		Hosts:    hosts,
		Boot:     boot,
		Async:    func(fn func()) { go fn() },
		Dispatch: func(fn func()) { fn() },
	}
}

// DirectoryPicker 让 App 给原生选择器挂父窗口（与存储 Workbench 同机制）。
func (m *AlphaModel) DirectoryPicker() app.DirectoryPicker { return m.Hosts.Picker }

func (m *AlphaModel) Title() string { return "mHost Alpha" }

// Start 在窗口就绪后自动加载 profile（Alpha 是产品形态，无手动 Load 按钮）。
func (m *AlphaModel) Start() {
	if m.started {
		return
	}
	m.started = true
	m.Hosts.Load()
}

func (m *AlphaModel) View(c *ui.Context) {
	theme := c.Theme()
	// #37：主页面 Apply/Rollback/Undo 完成后，辅助页缓存同步失效。
	m.invalidateOnNewReceipt()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(12).Padding(10, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			ui.Text(c, "mHost").FontSize(20).Bold()
			ui.Badge(c, "Alpha · Hosts only")
			ui.Spacer(c)
			if m.Hosts.busy() {
				ui.Text(c, "working…").FontSize(12).TextColor(theme.TextMuted)
			}
		})
		m.bootBanner(c)
		ui.Row(c).Padding(8, 20, 0, 20).Children(func() {
			ui.Tabs(c, &m.SelectedNav, alphaNavTabs...)
		})
		switch m.SelectedNav {
		case 0:
			m.Hosts.View(c)
		case 1:
			m.historyView(c)
		case 2:
			m.systemHostsView(c)
		default:
			m.dataView(c)
		}
	})
}

// bootBanner 一次性展示启动迁移回执与扫描 issue；可关闭。
func (m *AlphaModel) bootBanner(c *ui.Context) {
	if m.Boot == nil || m.bannerShown {
		return
	}
	theme := c.Theme()
	migrationDone := m.Boot.Migration != nil
	if !migrationDone && len(m.Boot.Issues) == 0 {
		return
	}
	ui.Row(c).Gap(10).Padding(8, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			if migrationDone {
				ui.Text(c, "Data upgraded from v1 to v2 on this launch.").Bold()
				ui.Text(c, receiptSummaryLine(*m.Boot.Migration)).FontSize(11).TextColor(theme.TextMuted)
			}
			for _, issue := range m.Boot.Issues {
				ui.Text(c, fmt.Sprintf("%s: %s", issue.Code, issue.Message)).FontSize(11).TextColor(theme.TextMuted)
			}
		})
		if ui.Button(c, "Dismiss").Clicked() {
			m.bannerShown = true
		}
	})
}

func receiptSummaryLine(receipt app.WriteReceipt) string {
	written, skipped, failed := 0, 0, 0
	for _, result := range receipt.Results {
		switch result.Status {
		case app.WriteWritten, app.WriteUnchanged:
			written++
		case app.WriteSkipped:
			skipped++
		case app.WriteFailed:
			failed++
		}
	}
	return fmt.Sprintf("Migration receipt · %d written · %d skipped · %d failed", written, skipped, failed)
}

// comingSoonView 保留给真正未开放的入口（DNS/AdBlock 在 Alpha 全程隐藏）。
func (m *AlphaModel) comingSoonView(c *ui.Context, name string) {
	theme := c.Theme()
	ui.Column(c).Fill().Center().Gap(8).Padding(32).Children(func() {
		ui.Text(c, name).FontSize(22).Bold()
		ui.Text(c, "This page ships in a later Alpha slice.").TextColor(theme.TextMuted)
		ui.Text(c, "DNS and AdBlock stay hidden in the Hosts-only Alpha.").FontSize(12).TextColor(theme.TextMuted)
	})
}

// LoadSnapshots 扫描数据目录的快照（含孤儿快照 issue，#23）。
// 磁盘扫描在 worker 侧执行（快照页 I/O 不进渲染线程），结果合并回主线程。
func (m *AlphaModel) LoadSnapshots() {
	if m.snapshotsLoaded || m.snapshotsLoading || m.Boot == nil {
		return
	}
	m.snapshotsLoading = true
	m.Async(func() {
		result, err := storage.NewStore(storage.NewOSVolume()).Scan(m.Boot.DataDir)
		m.Dispatch(func() {
			if m.closed.Load() {
				return
			}
			m.snapshotsLoading = false
			m.snapshotsLoaded = true
			if err != nil {
				m.snapshots = nil
				m.snapshotIssues = []app.StorageIssue{{Code: "scan_failed", Severity: app.SeverityError, Message: err.Error(), Recoverable: true}}
				return
			}
			m.snapshots = result.Snapshots
			m.snapshotIssues = result.Issues
		})
	})
}

// LoadHistory 读取 Apply 备份历史（#35）。磁盘读取在 worker 侧执行，
// 结果合并回主线程。
func (m *AlphaModel) LoadHistory(ctx context.Context) {
	if m.historyLoaded || m.historyLoading || m.Hosts.Service == nil {
		return
	}
	m.historyLoading = true
	m.Async(func() {
		backups, err := m.Hosts.Service.ListApplyBackups(ctx)
		m.Dispatch(func() {
			if m.closed.Load() {
				return
			}
			m.historyLoading = false
			m.historyLoaded = true
			m.history = backups
			if err != nil {
				m.history = nil
				m.historyErr = err
				return
			}
			m.historyErr = nil
		})
	})
}

// RequestRestore 把指定备份置为待确认恢复目标；未知 ID 忽略。
func (m *AlphaModel) RequestRestore(backupID string) {
	if m.restoring {
		return
	}
	for i := range m.history {
		if m.history[i].ID == backupID {
			target := m.history[i]
			m.pendingRestore = &target
			m.restoreProblem = nil
			return
		}
	}
}

func (m *AlphaModel) CancelRestore() { m.pendingRestore = nil }

// ConfirmRestore 执行恢复：与最近回执的 Undo This Apply 同一服务语义
// （Service.Rollback，含管理员授权、写前安全备份、原子写、读回验证）。
// 成功后刷新 Active 状态、System Hosts 与备份计数缓存，并重新加载历史。
func (m *AlphaModel) ConfirmRestore() {
	if m.pendingRestore == nil || m.restoring || m.Hosts.Service == nil {
		return
	}
	target := *m.pendingRestore
	m.pendingRestore = nil
	m.restoring = true
	m.restoreInfo = ""
	m.restoreProblem = nil
	m.Async(func() {
		_, err := m.Hosts.Service.Rollback(context.Background(), target.ID)
		m.Dispatch(func() {
			if m.closed.Load() {
				return
			}
			m.restoring = false
			// 无论成败都刷新历史：写前安全备份已生成并补标结果。
			m.historyLoaded = false
			if err != nil {
				var uiErr *app.UIError
				if errors.As(err, &uiErr) {
					m.restoreProblem = uiErr
				} else {
					m.restoreProblem = &app.UIError{Kind: app.ErrorInternal, Message: err.Error()}
				}
				return
			}
			m.restoreInfo = fmt.Sprintf("Restored backup from %s · %s",
				target.CreatedAt.Format("2006-01-02 15:04"), historyProfileLabel(target))
			m.systemHostsLoaded = false
			m.backupCountLoaded = false
			m.Hosts.ReloadApplied()
		})
	})
}

// historyProfileLabel 给无归属（恢复到无 Active profile）的备份一个
// 可读的占位名。
func historyProfileLabel(ref app.ApplyBackupRef) string {
	if ref.ProfileName == "" {
		return "no active profile"
	}
	return ref.ProfileName
}

// backupResultLabel 把结果标记映射为历史页可读文案。
func backupResultLabel(result string) string {
	switch result {
	case app.BackupResultApplied:
		return "Applied"
	case app.BackupResultFailed:
		return "Failed"
	case app.BackupResultCanceled:
		return "Canceled"
	default:
		return "Result unknown"
	}
}

func (m *AlphaModel) historyView(c *ui.Context) {
	theme := c.Theme()
	m.LoadHistory(context.Background())
	m.LoadSnapshots()
	ui.Column(c).Fill().Gap(12).Padding(16, 20).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "History").FontSize(20).Bold().Grow(1)
			if ui.Button(c, "Refresh").Disabled(m.historyLoading || m.restoring).Clicked() {
				// 顶部 Refresh 同时重扫存储快照子区。
				m.historyLoaded = false
				m.snapshotsLoaded = false
				m.LoadHistory(context.Background())
			}
		})

		if m.restoring {
			ui.Text(c, "Restoring… administrator authorization may be required.").TextColor(theme.TextMuted)
		}
		if m.restoreProblem != nil {
			ui.Text(c, "Restore failed: "+m.restoreProblem.Message).TextColor(theme.TextMuted)
			ui.Text(c, "The system hosts was not changed unless stated above. A safety backup is created before every restore; press Refresh and try again.").
				FontSize(11).TextColor(theme.TextMuted)
		}
		if m.restoreInfo != "" {
			ui.Text(c, m.restoreInfo).Bold()
		}

		m.restoreConfirmDialog(c)

		ui.Text(c, "Apply backups").FontSize(14).Bold()
		if m.historyLoading && !m.historyLoaded {
			ui.Text(c, "Loading…").TextColor(theme.TextMuted)
		}
		if m.historyErr != nil {
			ui.Text(c, "Cannot list apply backups: "+m.historyErr.Error()).TextColor(theme.TextMuted)
		}
		if m.historyLoaded && len(m.history) == 0 && m.historyErr == nil {
			ui.Text(c, "No apply backups yet.").TextColor(theme.TextMuted)
		}
		for i := range m.history {
			m.historyRow(c, &m.history[i])
		}

		ui.Text(c, "Storage snapshots").FontSize(14).Bold()
		if len(m.snapshots) == 0 {
			ui.Text(c, "No storage snapshots.").FontSize(12).TextColor(theme.TextMuted)
		}
		for _, snapshot := range m.snapshots {
			ui.Text(c, fmt.Sprintf("%s · %d profiles · %s",
				snapshot.Meta.Name, snapshot.Meta.ProfileCount,
				snapshot.Meta.CreatedAt.Format("2006-01-02 15:04"))).SingleLine()
		}
		for _, issue := range m.snapshotIssues {
			ui.Text(c, fmt.Sprintf("%s: %s (%s)", issue.Code, issue.Message, issue.Path)).
				FontSize(12).TextColor(theme.TextMuted)
		}
	})
}

// historyRow 渲染一条备份记录：时间、Profile、结果、可恢复性、恢复入口。
func (m *AlphaModel) historyRow(c *ui.Context, backup *app.ApplyBackupRef) {
	theme := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, backup.CreatedAt.Format("2006-01-02 15:04")).SingleLine()
		ui.Text(c, historyProfileLabel(*backup)).SingleLine().Grow(1)
		ui.Text(c, backupResultLabel(backup.Result)).FontSize(12).TextColor(theme.TextMuted)
		if !backup.Available {
			ui.Text(c, "backup file missing").FontSize(12).TextColor(theme.TextMuted)
			return
		}
		if ui.Button(c, "Restore…").Disabled(m.restoring).Clicked() {
			m.RequestRestore(backup.ID)
		}
	})
}

// restoreConfirmDialog 恢复前确认：来源时间、原 Profile 与系统影响。
func (m *AlphaModel) restoreConfirmDialog(c *ui.Context) {
	if m.pendingRestore == nil {
		return
	}
	open := true
	target := *m.pendingRestore
	message := fmt.Sprintf("Restore the backup from %s (profile: %s)?\nThis modifies the system hosts file. A safety backup of the current state is created first.",
		target.CreatedAt.Format("2006-01-02 15:04"), historyProfileLabel(target))
	switch ui.AlertDialog(c, &open, "Restore backup", message, "Restore", "Cancel") {
	case 0:
		m.ConfirmRestore()
	case 1:
		m.CancelRestore()
	default:
		if !open {
			m.CancelRestore()
		}
	}
}

// LoadSystemHosts 读系统 hosts（只读视图）。读文件在 worker 侧执行；
// 成功后填充只读浏览器并标记管理块（#37）。
func (m *AlphaModel) LoadSystemHosts(ctx context.Context) {
	if m.systemHostsLoaded || m.systemHostsLoading || m.Hosts.Admin == nil {
		return
	}
	m.systemHostsLoading = true
	m.Async(func() {
		content, err := m.Hosts.Admin.SystemHostsContent(ctx)
		m.Dispatch(func() {
			if m.closed.Load() {
				return
			}
			m.systemHostsLoading = false
			m.systemHosts, m.systemHostsErr, m.systemHostsLoaded = content, err, true
			if err != nil {
				return
			}
			m.systemHostsReadAt = time.Now()
			m.presentSystemHosts()
		})
	})
}

// RefreshSystemHosts 手动重读系统 hosts（#37：外部修改后的刷新入口）。
func (m *AlphaModel) RefreshSystemHosts() {
	if m.systemHostsLoading {
		return
	}
	m.systemHostsLoaded = false
	m.LoadSystemHosts(context.Background())
}

// invalidateOnNewReceipt 在主页面写操作完成（回执 OperationID 变化）后
// 失效辅助页缓存（#37）：System Hosts、备份计数与历史。每次 View 调用。
func (m *AlphaModel) invalidateOnNewReceipt() {
	if m.Hosts.Receipt == nil {
		return
	}
	op := m.Hosts.Receipt.OperationID
	if op == "" || op == m.lastReceiptOp {
		return
	}
	m.lastReceiptOp = op
	m.systemHostsLoaded = false
	m.backupCountLoaded = false
	m.historyLoaded = false
}

// systemHostsViewerState 惰性构造只读浏览器：等宽字体、行号、可选中
// 复制，Cmd+F 查找条（MyGo editor 内建）。
func (m *AlphaModel) systemHostsViewerState() *editor.State {
	if m.systemHostsViewer == nil {
		m.systemHostsViewer = &editor.State{
			Buffer:          editor.NewBuffer(""),
			Label:           "System hosts",
			ReadOnly:        true,
			KeepScrollbars:  true,
			LineNumbers:     editor.LineNumbersOn,
			NoScrollPastEnd: true,
			RenderFindBar: func(c *ui.Context, bar ui.Element, find *editor.FindBar) {
				editor.DefaultFindBar(c, bar, find)
			},
		}
	}
	return m.systemHostsViewer
}

// presentSystemHosts 把读到的系统 hosts 装进只读浏览器，并用 Info 级
// 诊断标记管理块（下划线 + 悬停说明 + 滚动条标记，即 MyGo 现有的
// 高亮机制）。
func (m *AlphaModel) presentSystemHosts() {
	viewer := m.systemHostsViewerState()
	viewer.Buffer.Set(m.systemHosts)
	var marks []editor.Diagnostic
	if start, end, ok := hostsfile.ExtractManagedBlockBytes(m.systemHosts); ok {
		marks = append(marks, editor.Diagnostic{
			Start: start, End: end,
			Severity: editor.SeverityInfo,
			Message:  "mHost managed block",
			Source:   "mHost",
		})
	}
	viewer.SetDiagnostics(viewer.Buffer.Version(), marks)
}

func (m *AlphaModel) systemHostsView(c *ui.Context) {
	theme := c.Theme()
	m.LoadSystemHosts(context.Background())
	ui.Column(c).Fill().Gap(12).Padding(16, 20).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "System Hosts · read only").FontSize(20).Bold().Grow(1)
			if ui.Button(c, "Refresh").Disabled(m.systemHostsLoading).Clicked() {
				m.RefreshSystemHosts()
			}
			if m.systemHostsLoaded && m.systemHostsErr == nil && m.systemHosts != "" {
				if ui.Button(c, "Copy All").Clicked() {
					c.WriteClipboard(m.systemHosts)
				}
			}
		})
		if m.systemHostsLoading && !m.systemHostsLoaded {
			ui.Text(c, "Loading…").TextColor(theme.TextMuted)
		}
		if m.systemHostsErr != nil {
			// 读取失败不拿旧内容冒充最新：只展示错误与上次成功读取时间。
			ui.Text(c, "Cannot read the system hosts file: "+m.systemHostsErr.Error()).TextColor(theme.TextMuted)
			if !m.systemHostsReadAt.IsZero() {
				ui.Text(c, "Last successful read: "+m.systemHostsReadAt.Format("2006-01-02 15:04")).
					FontSize(12).TextColor(theme.TextMuted)
			}
			return
		}
		if !m.systemHostsLoaded {
			return
		}
		if blockLine := managedBlockBounds(m.systemHosts); blockLine >= 0 {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("mHost managed block at line %d", blockLine+1)).FontSize(12).TextColor(theme.TextMuted)
				if ui.Button(c, "Jump to Managed Block").Clicked() {
					m.systemHostsViewerState().GoTo(editor.Pos{Line: blockLine})
				}
			})
		} else {
			ui.Text(c, "No mHost managed block yet.").FontSize(12).TextColor(theme.TextMuted)
		}
		if m.systemHostsLoading {
			ui.Text(c, "Refreshing…").FontSize(12).TextColor(theme.TextMuted)
		}
		editor.View(c, m.systemHostsViewerState()).Grow(1).FillWidth()
	})
}

// managedBlockBounds 返回管理块起始行号（0-based），无块返回 -1。
func managedBlockBounds(content string) int {
	for i, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == hostsfile.ManagedStart {
			return i
		}
	}
	return -1
}

// LoadBackupCount 数据页展示 apply 备份数量。读盘在 worker 侧执行；
// 失败保留旧计数并展示错误（#37：不把旧计数伪装成最新）。
func (m *AlphaModel) LoadBackupCount(ctx context.Context) {
	if m.backupCountLoaded || m.backupCountLoading || m.Hosts.Service == nil {
		return
	}
	m.backupCountLoading = true
	m.Async(func() {
		backups, err := m.Hosts.Service.ListApplyBackups(ctx)
		m.Dispatch(func() {
			if m.closed.Load() {
				return
			}
			m.backupCountLoading = false
			m.backupCountLoaded = true
			if err != nil {
				m.backupCountErr = err
				return
			}
			m.backupCountErr = nil
			m.backupCount = len(backups)
		})
	})
}

// dataView 是 Settings 的收敛形态（#37）：无可修改设置，只读展示
// 数据目录、系统 hosts 路径与 Apply 备份计数，路径可复制。
func (m *AlphaModel) dataView(c *ui.Context) {
	theme := c.Theme()
	m.LoadBackupCount(context.Background())
	ui.Column(c).Gap(12).Padding(16, 20).Children(func() {
		ui.Text(c, "Data").FontSize(20).Bold()
		if m.Boot != nil {
			m.dataPathRow(c, "Data directory", m.Boot.DataDir)
			m.dataPathRow(c, "System hosts", m.Boot.HostsPath)
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := fmt.Sprintf("Apply backups: %d", m.backupCount)
			if m.backupCountLoading && !m.backupCountLoaded {
				label = "Apply backups: loading…"
			}
			ui.Text(c, label).SingleLine().Grow(1)
			if m.backupCountErr != nil {
				ui.Text(c, "count unavailable: "+m.backupCountErr.Error()).FontSize(12).TextColor(theme.TextMuted)
			}
			if ui.Button(c, "Refresh").Disabled(m.backupCountLoading).Clicked() {
				m.backupCountLoaded = false
				m.LoadBackupCount(context.Background())
			}
		})
	})
}

// dataPathRow 展示一条长路径：单行截断 + 复制入口（#37）。
func (m *AlphaModel) dataPathRow(c *ui.Context, label, path string) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label+":").SingleLine()
		ui.Text(c, path).SingleLine().Grow(1)
		if path != "" && ui.Button(c, "Copy").Clicked() {
			c.WriteClipboard(path)
		}
	})
}

// 生命周期与 App 接线：全部委托给 Hosts 主链路。
func (m *AlphaModel) RequestQuit() bool { return m.Hosts.RequestQuit() }
func (m *AlphaModel) CancelQuit()       { m.Hosts.CancelQuit() }
func (m *AlphaModel) Close() error {
	m.closed.Store(true)
	return m.Hosts.Close()
}
func (m *AlphaModel) SetDispatch(fn func(func())) {
	m.Dispatch = fn
	m.Hosts.SetDispatch(fn)
}
func (m *AlphaModel) SetOnSafeToQuit(fn func()) { m.Hosts.SetOnSafeToQuit(fn) }
func (m *AlphaModel) SetProblem(p *app.UIError) { m.Hosts.SetProblem(p) }
