package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	app "github.com/igevin/mHost-go/internal/app"
)

func (m *HostsWorkbenchModel) View(c *ui.Context) {
	theme := c.Theme()
	m.Editor.RenderFindBar = func(c *ui.Context, bar ui.Element, find *editor.FindBar) {
		m.findBar = find
		editor.DefaultFindBar(c, bar, find)
	}
	m.SyncFromEditor()
	m.updateHostsDiagnostics()
	ui.Column(c).Fill().Children(func() {
		if !m.Chromeless {
			m.chromeHeader(c, theme)
		}

		if m.Problem != nil {
			ui.Row(c).Gap(10).Padding(10, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
				ui.Text(c, hostsErrorLabel(m.Problem.Kind)+m.Problem.Message).Bold()
				if m.Problem.Detail != "" {
					ui.Text(c, m.Problem.Detail).FontSize(11).TextColor(theme.TextMuted)
				}
			})
		}
		if m.Info != "" {
			ui.Row(c).Gap(10).Padding(8, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
				ui.Text(c, m.Info).FontSize(12)
				if ui.Button(c, "Dismiss").Clicked() {
					m.Info = ""
				}
			})
		}

		if len(m.Profiles) == 0 {
			m.hostsEmptyView(c)
			return
		}

		ui.Row(c).Grow(1).FillWidth().Children(func() {
			m.profileSidebar(c)
			ui.Column(c).Grow(1).FillHeight().Padding(12, 16).Gap(10).Children(func() {
				if m.SelectedID == "" {
					ui.Text(c, "Choose a profile on the left.").TextColor(theme.TextMuted)
				} else {
					m.profileHeader(c)
					m.toolbar(c)
					if len(m.Editor.Diagnostics()) == 0 {
						m.editorOrPreview(c)
					} else {
						// The problems panel scrolls inside its split section and
						// never stretches with the diagnostic count; the divider
						// is draggable to resize manually.
						ui.SplitVerticalPercent(c, &m.ProblemsSplit, func() {
							m.editorOrPreview(c)
						}, func() {
							m.problemsPanel(c)
						}).Grow(1).FillWidth()
					}
					m.receiptArea(c)
				}
			})
		})
	})
	m.pendingSwitchDialog(c)
	m.pendingDeleteDialog(c)
}

func (m *HostsWorkbenchModel) pendingDeleteDialog(c *ui.Context) {
	if m.PendingDelete == "" {
		return
	}
	open := true
	name := m.PendingDelete
	if profile := m.profileByID(m.PendingDelete); profile != nil {
		name = profile.Name
	}
	message := fmt.Sprintf("Delete profile %q? This cannot be undone.", name)
	switch ui.AlertDialog(c, &open, "Delete profile", message, "Delete", "Cancel") {
	case 0:
		m.ConfirmDelete()
	case 1:
		m.CancelDelete()
	default:
		if !open {
			m.CancelDelete()
		}
	}
}

func (m *HostsWorkbenchModel) chromeHeader(c *ui.Context, theme *ui.Theme) {
	ui.Row(c).Gap(12).Padding(16, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "mHost").FontSize(22).Bold()
				ui.Badge(c, "Phase 2 · Hosts Rewrite Workbench")
				if m.DemoMode {
					ui.Badge(c, "Demo service · no /etc/hosts writes")
				}
			})
			ui.Text(c, "Edit hosts profiles and stage the managed-block rewrite").FontSize(12).TextColor(theme.TextMuted)
		})
		if ui.Button(c, "Reload Profiles").Disabled(m.busy()).Clicked() {
			m.Load()
		}
	})
}

func (m *HostsWorkbenchModel) hostsEmptyView(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(32).Children(func() {
		if m.QuitRequested {
			ui.Text(c, "Quit is waiting for the current operation to reach a safe boundary.").Bold()
			if ui.Button(c, "Cancel Quit").Clicked() {
				m.CancelQuit()
			}
		}
		if m.Phase == HostsPhaseLoading {
			ui.Spinner(c)
			ui.Text(c, "Loading profiles…").FontSize(18).Bold()
		} else if m.DemoMode {
			ui.Text(c, "Hosts rewrite workbench").FontSize(24).Bold()
			ui.Text(c, "No profile is loaded until you start the demo.").TextColor(theme.TextMuted)
			ui.Text(c, "Nothing touches /etc/hosts; the managed block is simulated in memory.").FontSize(12).TextColor(theme.TextMuted)
			if ui.PrimaryButton(c, "Load Demo Profiles").Disabled(m.busy()).Clicked() {
				m.Load()
			}
		} else {
			ui.Text(c, "No profiles").FontSize(24).Bold()
			ui.Text(c, "Create one with ＋ New in the sidebar.").TextColor(theme.TextMuted)
			if m.Admin != nil && ui.PrimaryButton(c, "＋ New Profile").Disabled(m.busy()).Clicked() {
				m.CreateNewProfile()
			}
		}
	})
}

func (m *HostsWorkbenchModel) profileSidebar(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Width(230).FillHeight().Padding(10, 12).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "PROFILES").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			if m.Admin != nil && ui.Button(c, "＋ New").Disabled(m.busy()).Clicked() {
				m.CreateNewProfile()
			}
		})
		if len(m.Profiles) > 4 {
			ui.TextInput(c, &m.SearchQuery)
		}
		profiles := m.filteredProfiles()
		if len(profiles) == 0 && m.SearchQuery != "" {
			ui.Text(c, "No profiles match").FontSize(12).TextColor(theme.TextMuted)
		}
		for _, profile := range profiles {
			label := profile.Name
			if !profile.Enabled {
				label += "  ⏸"
			}
			if draft := m.drafts[profile.ID]; draft != nil && draft.content != draft.savedContent {
				label += " ●"
			}
			if profile.Protected {
				label += "  🔒"
			}
			clicked := false
			if profile.ID == m.SelectedID {
				clicked = ui.PrimaryButton(c, label).Disabled(m.busy()).Clicked()
			} else {
				clicked = ui.Button(c, label).Disabled(m.busy()).Clicked()
			}
			if clicked {
				m.SelectProfile(profile.ID)
			}
		}
		ui.Spacer(c)
		ui.Text(c, m.appliedStatusText()).FontSize(11).TextColor(theme.TextMuted)
	})
}

func (m *HostsWorkbenchModel) appliedStatusText() string {
	if m.Applied.ProfileID == "" {
		return "Managed block: none applied"
	}
	status := "Managed block: " + m.Applied.ProfileName
	if m.Applied.Drifted {
		status += " · drifted"
	}
	return status
}

func (m *HostsWorkbenchModel) profileHeader(c *ui.Context) {
	theme := c.Theme()
	profile := m.selectedProfile()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if profile != nil {
			ui.Text(c, profile.Name).FontSize(20).Bold()
		}
		if m.Modified() {
			ui.Badge(c, "Modified")
		} else {
			ui.Badge(c, "Stored")
		}
		if profile != nil && profile.Protected {
			ui.Badge(c, "Read only")
		}
		if m.Applied.ProfileID == m.SelectedID && m.SelectedID != "" {
			if m.Applied.Drifted {
				ui.Badge(c, "Drifted")
			} else {
				ui.Badge(c, "Applied")
			}
		}
		if profile != nil {
			ui.Text(c, fmt.Sprintf("%d rules", profile.RuleCount)).FontSize(12).TextColor(theme.TextMuted)
		}
		if m.Admin != nil && profile != nil {
			enableLabel := "Disable"
			if !profile.Enabled {
				enableLabel = "Enable"
			}
			if ui.Button(c, enableLabel).Disabled(m.busy()).Clicked() {
				m.ToggleSelectedEnabled()
			}
			if ui.Button(c, "Duplicate").Disabled(m.busy()).Clicked() {
				m.DuplicateSelected()
			}
			if ui.Button(c, "Delete").Disabled(m.busy() || profile.Protected).Clicked() {
				m.RequestDelete(profile.ID)
			}
			exportDisabled := m.busy() || m.Picker == nil || m.Modified()
			if ui.Button(c, "Export…").Disabled(exportDisabled).Clicked() {
				m.ExportSelected()
			}
			if m.Modified() {
				ui.Text(c, "Export stores the saved profile · save first").FontSize(11).TextColor(theme.TextMuted)
			}
		}
		if m.Admin != nil && ui.Button(c, "Import…").Disabled(m.busy() || m.Picker == nil).Clicked() {
			m.ImportFromDirectory()
		}
		ui.Spacer(c)
		ui.Text(c, m.appliedStatusText()).FontSize(12).TextColor(theme.TextMuted)
	})
}

func (m *HostsWorkbenchModel) toolbar(c *ui.Context) {
	theme := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, "Save").Disabled(m.busy() || !m.Modified()).Clicked() {
			m.Save()
		}
		if !m.Modified() {
			ui.Text(c, "Draft matches the stored profile").FontSize(11).TextColor(theme.TextMuted)
		} else {
			ui.Text(c, "Unsaved changes · Save before Apply").FontSize(11).TextColor(theme.TextMuted)
		}
		ui.Spacer(c)
		if m.Staged == nil {
			if ui.PrimaryButton(c, "Apply…").Disabled(m.busy() || m.Modified() || m.SelectedID == "").Clicked() {
				m.PreviewApply()
			}
			if m.Modified() {
				ui.Text(c, "Apply needs a saved draft").FontSize(11).TextColor(theme.TextMuted)
			}
		} else {
			if ui.Button(c, "Cancel Preview").Disabled(m.busy()).Clicked() {
				m.CancelStaged()
			}
			if reason := m.ApplyBlockedReason(); reason != "" {
				ui.Text(c, reason).FontSize(11).TextColor(theme.TextMuted)
			}
			if ui.PrimaryButton(c, "Apply Changes").Disabled(m.ApplyBlockedReason() != "").Clicked() {
				m.Apply()
			}
		}
	})
}

func (m *HostsWorkbenchModel) editorOrPreview(c *ui.Context) {
	if m.Staged != nil {
		m.stagedPreview(c)
	} else {
		m.editorSurface(c)
	}
}

func (m *HostsWorkbenchModel) editorSurface(c *ui.Context) {
	theme := c.Theme()
	editor.View(c, m.Editor).Grow(1).FillWidth()
	position := m.Editor.CursorPos()
	ui.Row(c).Gap(16).Padding(6, 0).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Text(c, fmt.Sprintf("Ln %d, Col %d", position.Line+1, position.Col+1)).FontSize(12).TextColor(theme.TextMuted)
		ui.Text(c, fmt.Sprintf("%d lines", m.Editor.Buffer.LineCount())).FontSize(12).TextColor(theme.TextMuted)
		ui.Text(c, fmt.Sprintf("%d diagnostics", len(m.Editor.Diagnostics()))).FontSize(12).TextColor(theme.TextMuted)
		ui.Text(c, "Cmd-F find · edits invalidate a staged preview").FontSize(12).TextColor(theme.TextMuted)
	})
}

func (m *HostsWorkbenchModel) stagedPreview(c *ui.Context) {
	theme := c.Theme()
	plan := m.Staged
	ui.Column(c).Grow(1).FillWidth().Gap(8).Padding(10).BorderWidth(1).BorderColor(theme.Border).Children(func() {
		ui.Text(c, "Apply preview · replaces the mHost managed block in /etc/hosts (simulated)").Bold()
		ui.Text(c, fmt.Sprintf("From %d lines to %d lines. A backup of the current block is created before writing.", lineCount(plan.CurrentBlock), lineCount(plan.MergedBlock))).FontSize(12).TextColor(theme.TextMuted)
		if m.PlanStale {
			ui.Text(c, "Stale · the draft changed after this preview").Bold()
		}
		for _, blocker := range plan.Blockers {
			ui.Text(c, blocker.Message).TextColor(theme.TextMuted)
		}
		for _, conflict := range plan.Conflicts {
			m.conflictRow(c, conflict)
		}
		diff := editor.DiffState{
			Old:          editor.NewBuffer(plan.CurrentBlock),
			New:          editor.NewBuffer(plan.MergedBlock),
			ContextLines: 3,
		}
		editor.DiffView(c, &diff).Grow(1).FillWidth()
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Cancel keeps the current block unchanged.").FontSize(11).TextColor(theme.TextMuted)
			ui.Spacer(c)
			if ui.Button(c, "Cancel").Clicked() {
				m.CancelStaged()
			}
			if ui.PrimaryButton(c, "Apply Changes").Disabled(m.ApplyBlockedReason() != "").Clicked() {
				m.Apply()
			}
		})
	})
}

func (m *HostsWorkbenchModel) conflictRow(c *ui.Context, conflict app.Conflict) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%s on lines %v maps to different IPs", conflict.Domain, conflict.Lines)).FontSize(12)
		for _, ip := range conflict.IPs {
			label := "Keep " + ip
			if m.Resolutions[conflict.Domain] == ip {
				label = "✓ " + label
			}
			if ui.Button(c, label).Disabled(m.busy()).Clicked() {
				m.ChooseResolution(conflict.Domain, ip)
			}
		}
	})
}

func (m *HostsWorkbenchModel) problemsPanel(c *ui.Context) {
	diagnostics := m.Editor.Diagnostics()
	ui.Column(c).Fill().Gap(4).Padding(8).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
		ui.Text(c, fmt.Sprintf("Problems (%d)", len(diagnostics))).FontSize(12).Bold()
		ui.Scroll(c).Fill().Children(func() {
			for _, diagnostic := range diagnostics {
				pos := m.Editor.Buffer.Pos(diagnostic.Start)
				label := fmt.Sprintf("Line %d: %s", pos.Line+1, diagnostic.Message)
				if ui.Button(c, label).Clicked() {
					m.Editor.GoTo(editor.Pos{Line: pos.Line})
				}
			}
		})
	})
}

func (m *HostsWorkbenchModel) receiptArea(c *ui.Context) {
	receipt := m.Receipt
	if receipt == nil {
		return
	}
	theme := c.Theme()
	ui.Column(c).Gap(4).Padding(8).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Text(c, applyReceiptSummary(*receipt)).Bold()
		ui.Row(c).Gap(16).Children(func() {
			for _, result := range receipt.Results {
				ui.Text(c, fmt.Sprintf("%s: %s (readback %v)", result.Entity, result.Status, result.ReadbackVerified)).FontSize(12).TextColor(theme.TextMuted)
			}
		})
		if receipt.RollbackRef != nil && ui.Button(c, "Rollback to Backup").Disabled(m.busy()).Clicked() {
			m.Rollback(receipt.RollbackRef.ID)
		}
	})
}

func (m *HostsWorkbenchModel) pendingSwitchDialog(c *ui.Context) {
	if m.PendingSwitch == "" {
		return
	}
	open := true
	name := m.PendingSwitch
	if profile := m.profileByID(m.PendingSwitch); profile != nil {
		name = profile.Name
	}
	message := fmt.Sprintf("Switch to %q and keep the unsaved draft? You can come back to it later.", name)
	switch ui.AlertDialog(c, &open, "Unsaved changes", message, "Switch", "Stay") {
	case 0:
		m.ConfirmSwitch()
	case 1:
		m.CancelSwitch()
	default:
		if !open {
			m.CancelSwitch()
		}
	}
}

func (m *HostsWorkbenchModel) updateHostsDiagnostics() {
	version := m.Editor.Buffer.Version()
	if version == m.diagnosticVersion {
		return
	}
	m.Editor.SetDiagnostics(version, HostsDiagnostics(m.Editor.Buffer))
	m.diagnosticVersion = version
}

func (m *HostsWorkbenchModel) selectedProfile() *app.HostsProfile {
	for i := range m.Profiles {
		if m.Profiles[i].ID == m.SelectedID {
			return &m.Profiles[i]
		}
	}
	return nil
}

func (m *HostsWorkbenchModel) profileByID(id string) *app.HostsProfile {
	for i := range m.Profiles {
		if m.Profiles[i].ID == id {
			return &m.Profiles[i]
		}
	}
	return nil
}

func lineCount(text string) int {
	count := 1
	for _, r := range text {
		if r == '\n' {
			count++
		}
	}
	return count
}

func hostsErrorLabel(kind app.UIErrorKind) string {
	switch kind {
	case app.ErrorWrite:
		return "Write error: "
	case app.ErrorReadback:
		return "Verification error: "
	case app.ErrorValidation:
		return "Validation error: "
	case app.ErrorStale:
		return "Stale operation: "
	default:
		return "Error: "
	}
}

func applyReceiptSummary(receipt app.ApplyReceipt) string {
	failed := 0
	verified := 0
	for _, result := range receipt.Results {
		if result.Status == app.WriteFailed {
			failed++
		}
		if result.ReadbackVerified {
			verified++
		}
	}
	switch {
	case failed > 0 && receipt.RollbackRef != nil:
		return fmt.Sprintf("Apply incomplete · %d failed · backup %s available", failed, receipt.RollbackRef.ID)
	case failed > 0:
		return fmt.Sprintf("Apply incomplete · %d failed · no backup available", failed)
	case verified > 0:
		return fmt.Sprintf("Apply verified · %d checked", verified)
	default:
		return "Apply finished without verification"
	}
}
