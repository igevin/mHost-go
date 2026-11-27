package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
)

var workbenchTabs = []string{"Overview", "Profiles", "Snapshots", "Settings & AdBlock", "Issues"}

func (m *WorkbenchModel) View(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(12).Padding(16, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "mHost").FontSize(22).Bold()
					ui.Badge(c, "Phase 1 · Storage Workbench")
					if m.DemoMode {
						ui.Badge(c, "Demo service · no filesystem writes")
					}
				})
				ui.Text(c, "Synthetic storage compatibility validation").FontSize(12).TextColor(theme.TextMuted)
			})
			if ui.Button(c, "Open Source…").Disabled(m.busy()).Clicked() {
				m.OpenSource()
			}
		})

		if m.Workspace.ID != "" {
			m.workspaceHeader(c)
		}
		if m.Problem != nil {
			ui.Row(c).Gap(10).Padding(10, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
				ui.Text(c, errorLabel(m.Problem.Kind)+m.Problem.Message).Bold()
				if m.Problem.Path != "" {
					ui.Text(c, m.Problem.Path).FontSize(12).TextColor(theme.TextMuted)
				}
				if m.Problem.Detail != "" {
					ui.Text(c, m.Problem.Detail).FontSize(11).TextColor(theme.TextMuted)
				}
			})
		}

		if m.Workspace.ID == "" {
			m.emptyView(c)
		} else {
			ui.Row(c).Padding(12, 20, 0, 20).Children(func() {
				ui.Tabs(c, &m.SelectedTab, workbenchTabs...)
			})
			ui.Column(c).Grow(1).FillWidth().Padding(12, 20).Children(func() {
				switch m.SelectedTab {
				case 1:
					m.profilesView(c)
				case 2:
					m.snapshotsView(c)
				case 3:
					m.settingsView(c)
				case 4:
					m.issuesView(c)
				default:
					m.overviewView(c)
				}
			})
			m.actionBar(c)
		}
	})
	m.previewDialog(c)
}

func (m *WorkbenchModel) emptyView(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(32).Children(func() {
		if m.QuitRequested {
			ui.Text(c, "Quit is waiting for directory selection. Dismiss the native panel to continue.").Bold()
			if ui.Button(c, "Cancel Quit").Clicked() {
				m.CancelQuit()
			}
		}
		if m.Phase == PhaseSelecting || m.Phase == PhaseLoading {
			ui.Spinner(c)
			ui.Text(c, m.phaseText()).FontSize(18).Bold()
		} else {
			ui.Text(c, "Storage compatibility validation").FontSize(24).Bold()
			ui.Text(c, "Inspect synthetic mHost data without changing the source.").TextColor(theme.TextMuted)
			ui.Text(c, "Migration and write checks run only in a separate working copy.").TextColor(theme.TextMuted)
			if ui.PrimaryButton(c, "Open Source…").Disabled(m.busy()).Clicked() {
				m.OpenSource()
			}
			ui.Text(c, "No directory is accessed until you choose one.").FontSize(12).TextColor(theme.TextMuted)
		}
		if (m.Phase == PhaseLoading || m.Phase == PhasePreviewing) && ui.Button(c, "Cancel").Clicked() {
			m.CancelOperation()
		}
		if m.Phase == PhaseFailed && m.Problem != nil {
			ui.Row(c).Gap(10).Children(func() {
				if ui.Button(c, "Retry").Clicked() {
					m.Retry()
				}
				if ui.Button(c, "Choose Another Source…").Clicked() {
					m.OpenSource()
				}
			})
		}
	})
}

func (m *WorkbenchModel) workspaceHeader(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Gap(5).Padding(12, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Badge(c, "Read only")
			ui.Text(c, "SOURCE  "+m.Workspace.SourceRoot).FontSize(12)
			if ui.Button(c, "Copy Source Path").Clicked() {
				mygo.Clipboard.WriteText(m.Workspace.SourceRoot)
			}
		})
		if m.Workspace.Access == app.AccessWritable {
			ui.Row(c).Gap(8).Children(func() {
				ui.Badge(c, "Working copy")
				ui.Text(c, "WORK  "+m.Workspace.Root).FontSize(12)
				if ui.Button(c, "Copy Working Path").Clicked() {
					mygo.Clipboard.WriteText(m.Workspace.Root)
				}
			})
		}
		ui.Text(c, m.statusText()).FontSize(12).TextColor(theme.TextMuted)
	})
}

func (m *WorkbenchModel) overviewView(c *ui.Context) {
	theme := c.Theme()
	summary := m.Workspace.Summary
	ui.Column(c).Gap(16).Fill().Children(func() {
		ui.Text(c, "Storage overview").FontSize(20).Bold()
		if summary.Manifest != nil {
			ui.Text(c, fmt.Sprintf("Manifest v%d · app %s · updated %s", summary.Manifest.Version, summary.Manifest.AppVersion, summary.Manifest.UpdatedAt.Format("2006-01-02 15:04 UTC")))
			ui.Text(c, fmt.Sprintf("DNS enabled: %v · Original DNS: %s", boolValue(summary.Manifest.DNSEnabled), summary.Manifest.OriginalDNS)).TextColor(theme.TextMuted)
		}
		ui.Row(c).Gap(24).Children(func() {
			ui.Badge(c, fmt.Sprintf("%d Profiles", len(summary.Profiles)))
			ui.Badge(c, fmt.Sprintf("%d Snapshots", len(summary.Snapshots)))
			ui.Badge(c, fmt.Sprintf("%d Issues", len(summary.Issues)))
		})
		if m.SummaryStale {
			ui.Text(c, "May be stale. Reload successfully before any write operation.").Bold()
		}
		ui.Text(c, "Settings migration: "+summary.Settings.MigrationStatus).FontSize(12).TextColor(theme.TextMuted)
		if len(summary.Issues) != 0 {
			ui.Text(c, fmt.Sprintf("Loaded with %d issues. Valid data remains available.", len(summary.Issues))).Bold()
		} else {
			ui.Text(c, "Inspection completed without compatibility issues.")
		}
		if m.Receipt != nil {
			ui.Text(c, receiptSummary(*m.Receipt)).Bold()
			results := m.Receipt.Results
			ui.Table(c, &m.receiptRows, []ui.TableColumn{
				{Title: "Entity", Width: 100}, {Title: "Path", Width: 220}, {Title: "Status", Width: 90},
				{Title: "Readback", Width: 80}, {Title: "Issue"},
			}, len(results), func(row, col int) {
				result := results[row]
				backupID := ""
				if result.Backup != nil {
					backupID = result.Backup.ID
				}
				issueText := ""
				if result.Issue != nil {
					issueText = result.Issue.Message
				}
				values := []string{result.Entity, result.Path, string(result.Status), fmt.Sprint(result.ReadbackVerified), issueText}
				if col == 4 && backupID != "" && issueText == "" {
					values[4] = "backup " + backupID
				}
				ui.Text(c, values[col]).SingleLine()
			}).Grow(1).FillWidth()
			// 带 issue 的行逐条列出（含 Detail），回答"为什么 skipped/failed"
			// （#23：informational issue 不再只显示一个无解释的 skipped）。
			for _, result := range results {
				if result.Issue == nil {
					continue
				}
				detail := result.Issue.Message
				if result.Issue.Detail != "" && result.Issue.Detail != result.Issue.Message {
					detail += " · " + result.Issue.Detail
				}
				ui.Text(c, fmt.Sprintf("%s %s: %s", result.Entity, result.Path, detail)).FontSize(11).TextColor(theme.TextMuted)
			}
		}
	})
}

func (m *WorkbenchModel) profilesView(c *ui.Context) {
	profiles := m.Workspace.Summary.Profiles
	ui.Column(c).Gap(12).Fill().Children(func() {
		ui.Text(c, "Profiles · read only").FontSize(20).Bold()
		ui.Table(c, &m.profileRows, []ui.TableColumn{
			{Title: "Name", Width: 150}, {Title: "Mode", Width: 70}, {Title: "Rules", Width: 60},
			{Title: "Enabled", Width: 70}, {Title: "Protected", Width: 80}, {Title: "Path"},
		}, len(profiles), func(row, col int) {
			profile := profiles[row]
			values := []string{profile.Name, profile.Mode, fmt.Sprint(profile.RuleCount), fmt.Sprint(profile.Enabled), fmt.Sprint(profile.Protected), profile.Path}
			ui.Text(c, values[col]).SingleLine()
		}).Grow(1).FillWidth()
	})
}

func (m *WorkbenchModel) snapshotsView(c *ui.Context) {
	snapshots := m.Workspace.Summary.Snapshots
	ui.Column(c).Gap(12).Fill().Children(func() {
		ui.Text(c, "Snapshots and backups").FontSize(20).Bold()
		ui.Table(c, &m.snapshotRows, []ui.TableColumn{
			{Title: "Name", Width: 180}, {Title: "Profiles", Width: 70}, {Title: "Created", Width: 160}, {Title: "Path"},
		}, len(snapshots), func(row, col int) {
			snapshot := snapshots[row]
			values := []string{snapshot.Name, fmt.Sprint(snapshot.ProfileCount), snapshot.CreatedAt.Format("2006-01-02 15:04"), snapshot.Path}
			ui.Text(c, values[col]).SingleLine()
		}).Grow(1).FillWidth()
		if m.Workspace.Access == app.AccessWritable {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d registered backups", len(m.Backups))).Bold()
				if ui.Button(c, "Refresh Backups").Disabled(m.busy()).Clicked() {
					m.LoadBackups()
				}
				if ui.Button(c, "Restore Latest Backup…").Disabled(m.busy() || m.SummaryStale || len(m.Backups) == 0).Clicked() {
					m.RequestRestore(m.Backups[len(m.Backups)-1].ID)
				}
			})
			for _, backup := range m.Backups {
				ui.Text(c, fmt.Sprintf("%s · %s · %s · %s · %s", backup.ID, backup.Entity, backup.Path, backup.OperationID, backup.CreatedAt.Format("2006-01-02 15:04"))).FontSize(12).SingleLine()
			}
		}
	})
}

func (m *WorkbenchModel) settingsView(c *ui.Context) {
	theme := c.Theme()
	summary := m.Workspace.Summary
	ui.Column(c).Gap(12).Children(func() {
		ui.Text(c, "Settings & AdBlock · read only").FontSize(20).Bold()
		ui.Text(c, "Settings source: "+summary.Settings.Source)
		ui.Text(c, "Migration status: "+summary.Settings.MigrationStatus)
		ui.Text(c, "The Phase 0 synthetic settings.json is not treated as a legacy disk contract.").FontSize(12).TextColor(theme.TextMuted)
		if summary.AdBlock != nil {
			ui.Text(c, fmt.Sprintf("AdBlock enabled: %v · %d sources · %d whitelist entries", summary.AdBlock.Enabled, summary.AdBlock.SourceCount, summary.AdBlock.WhitelistSize))
		} else {
			ui.Text(c, "No AdBlock configuration in this dataset.").TextColor(theme.TextMuted)
		}
	})
}

func (m *WorkbenchModel) issuesView(c *ui.Context) {
	issues := m.Workspace.Summary.Issues
	ui.Column(c).Gap(12).Fill().Children(func() {
		ui.Text(c, fmt.Sprintf("Issues (%d)", len(issues))).FontSize(20).Bold()
		if len(issues) == 0 {
			ui.Text(c, "No compatibility issues.")
			return
		}
		ui.Table(c, &m.issueRows, []ui.TableColumn{
			{Title: "Severity", Width: 80}, {Title: "Path", Width: 330}, {Title: "Message"},
		}, len(issues), func(row, col int) {
			issue := issues[row]
			values := []string{string(issue.Severity), issue.Path, issue.Message}
			ui.Text(c, values[col]).SingleLine()
		}).Grow(1).FillWidth()
	})
}

func (m *WorkbenchModel) actionBar(c *ui.Context) {
	theme := c.Theme()
	ui.Row(c).Gap(10).Padding(12, 20).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, m.actionSafetyText()).FontSize(12).TextColor(theme.TextMuted)
		})
		if (m.Phase == PhaseLoading || m.Phase == PhasePreviewing) && ui.Button(c, "Cancel").Clicked() {
			m.CancelOperation()
		}
		if m.QuitRequested && ui.Button(c, "Cancel Quit").Clicked() {
			m.CancelQuit()
		}
		if ui.Button(c, "Reload").Disabled(m.busy()).Clicked() {
			m.Reload()
		}
		if m.Workspace.Access == app.AccessReadOnly {
			if ui.PrimaryButton(c, "Create Working Copy…").Disabled(m.busy() || m.SummaryStale).Clicked() {
				m.CreateWorkspace()
			}
		} else if m.Workspace.StorageVersion == 1 {
			if ui.PrimaryButton(c, "Preview Migration…").Disabled(m.busy() || m.SummaryStale || m.Preview != nil).Clicked() {
				m.PreviewMigration()
			}
		} else {
			if ui.PrimaryButton(c, "Preview Validation Changes…").Disabled(m.busy() || m.SummaryStale || m.Preview != nil).Clicked() {
				m.PreviewValidationChanges()
			}
		}
	})
}

func (m *WorkbenchModel) previewDialog(c *ui.Context) {
	if m.Preview == nil {
		return
	}
	open := true
	switch m.Preview.Kind {
	case PreviewMigrationKind:
		plan := m.Preview.Migration
		message := previewMessage(plan.Changes)
		if len(plan.Blockers) != 0 {
			message += fmt.Sprintf("\n%d blockers must be resolved first.", len(plan.Blockers))
			if ui.AlertDialog(c, &open, "Migration blocked", message, "Close") == 0 || !open {
				m.CancelPreview()
			}
			return
		}
		switch ui.AlertDialog(c, &open, "Preview v1 → v2 migration", message, "Apply Migration", "Cancel") {
		case 0:
			m.ConfirmPreview()
		case 1:
			m.CancelPreview()
		default:
			if !open {
				m.CancelPreview()
			}
		}
	case PreviewValidationKind:
		message := previewMessage(m.Preview.Changes.Changes)
		switch ui.AlertDialog(c, &open, "Preview validation changes", message, "Save to Working Copy", "Cancel") {
		case 0:
			m.ConfirmPreview()
		case 1:
			m.CancelPreview()
		default:
			if !open {
				m.CancelPreview()
			}
		}
	case PreviewRestoreKind:
		backup := m.Preview.Backup
		message := fmt.Sprintf("Restore %s from backup %s?\nA safety backup is created before restoring.", backup.Path, backup.ID)
		switch ui.AlertDialog(c, &open, "Restore storage backup", message, "Restore Backup", "Cancel") {
		case 0:
			m.ConfirmPreview()
		case 1:
			m.CancelPreview()
		default:
			if !open {
				m.CancelPreview()
			}
		}
	}
}

func (m *WorkbenchModel) statusText() string {
	if m.SummaryStale {
		return "May be stale · reload before write operations"
	}
	if m.Problem != nil {
		return "Operation needs attention · " + m.Problem.Message
	}
	if m.Receipt != nil {
		return receiptSummary(*m.Receipt)
	}
	return m.phaseText()
}

func (m *WorkbenchModel) phaseText() string {
	switch m.Phase {
	case PhaseSelecting:
		return "Waiting for directory selection…"
	case PhaseLoading:
		return "Inspecting storage…"
	case PhaseCopying:
		return "Creating working copy…"
	case PhasePreviewing:
		return "Preparing preview…"
	case PhaseWriting:
		return "Writing to the working copy… Quit will wait for a safe boundary."
	case PhaseFailed:
		return "Storage inspection failed"
	case PhaseReady:
		if m.Workspace.Access == app.AccessWritable {
			return fmt.Sprintf("Working copy ready · storage v%d · revision %d", m.Workspace.StorageVersion, m.Workspace.Revision)
		}
		return "Source ready · no writes allowed"
	default:
		return "No source selected"
	}
}

func (m *WorkbenchModel) actionSafetyText() string {
	if m.SummaryStale {
		return "May be stale. Write operations are disabled until Reload succeeds."
	}
	if m.QuitRequested {
		return "Quit is waiting for the current storage operation."
	}
	if m.Workspace.Access == app.AccessWritable {
		return "Writes affect only WORK. Source remains read only."
	}
	return "Read-only source. Create a working copy to test writes."
}

func previewMessage(changes []app.ChangeItem) string {
	var message strings.Builder
	message.WriteString("Only the selected working copy will be changed.\n")
	for _, change := range changes {
		fmt.Fprintf(&message, "\n%s · %s\n%s", change.Entity, change.Path, change.Summary)
	}
	return message.String()
}

func errorLabel(kind app.UIErrorKind) string {
	switch kind {
	case app.ErrorWrite, app.ErrorReadback, app.ErrorBackup:
		return "Write error: "
	case app.ErrorMigration:
		return "Migration blocked: "
	case app.ErrorInvalidJSON, app.ErrorValidation:
		return "Validation error: "
	default:
		return "Error: "
	}
}

func receiptSummary(receipt app.WriteReceipt) string {
	verified := 0
	failed := 0
	for _, result := range receipt.Results {
		if result.ReadbackVerified {
			verified++
		}
		if result.Status == app.WriteFailed {
			failed++
		}
	}
	if failed == 0 && verified == len(receipt.Results) {
		return fmt.Sprintf("All %d storage results verified · revision %d", verified, receipt.Revision)
	}
	return fmt.Sprintf("Storage operation incomplete · %d verified · %d failed · revision %d", verified, failed, receipt.Revision)
}

func boolValue(value *bool) string {
	if value == nil {
		return "not set"
	}
	return fmt.Sprint(*value)
}
