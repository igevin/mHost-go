package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/hostssvc"
)

// 本文件是 #25 Alpha 产品化对 HostsWorkbenchModel 的扩展：
// Profile 管理（新建/复制/启停/删除）与列表搜索。Admin 为 nil
// 时（demo 服务）相关入口隐藏，模型行为与 #16 验证时一致。

// ProfileAdmin 是 workbench 对服务的管理面要求；由 hostssvc 实现。
type ProfileAdmin = hostssvc.ProfileAdmin

// filteredProfiles 应用搜索过滤（大小写不敏感，匹配名称）。
func (m *HostsWorkbenchModel) filteredProfiles() []app.HostsProfile {
	query := m.SearchQuery
	if query == "" {
		return m.Profiles
	}
	var out []app.HostsProfile
	for _, profile := range m.Profiles {
		if containsFold(profile.Name, query) {
			out = append(out, profile)
		}
	}
	return out
}

func containsFold(text, query string) bool {
	return len(query) == 0 || indexFold(text, query) >= 0
}

func indexFold(text, query string) int {
	for i := 0; i+len(query) <= len(text); i++ {
		if equalFoldASCII(text[i:i+len(query)], query) {
			return i
		}
	}
	return -1
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// CreateNewProfile 新建 profile 并选中。名称为 "New Profile" 系列去重。
func (m *HostsWorkbenchModel) CreateNewProfile() {
	if m.busy() || m.Admin == nil {
		return
	}
	id, ctx := m.beginOperation()
	m.Problem = nil
	name := uniqueProfileName("New Profile", m.profileNames())
	m.runAsync(func() {
		detail, err := m.Admin.CreateProfile(ctx, name, "# New profile\n")
		if err != nil {
			m.dispatchError(id, "create_profile", err)
			return
		}
		m.dispatch(func() {
			if m.current(id) {
				m.Phase = HostsPhaseIdle
			}
		})
		// 列表刷新留在 worker 侧执行（service I/O 不进 Dispatch；
		// review 建议项），合并回主线程时 operation id 仍有效。
		m.refreshAfterAdminChange(id, ctx, detail.ID)
	})
}

// DuplicateSelected 复制当前 profile 并选中副本。
func (m *HostsWorkbenchModel) DuplicateSelected() {
	if m.busy() || m.Admin == nil || m.SelectedID == "" {
		return
	}
	id, ctx := m.beginOperation()
	m.Problem = nil
	profileID := m.SelectedID
	m.runAsync(func() {
		detail, err := m.Admin.DuplicateProfile(ctx, profileID)
		if err != nil {
			m.dispatchError(id, "duplicate_profile", err)
			return
		}
		m.dispatch(func() {
			if m.current(id) {
				m.Phase = HostsPhaseIdle
			}
		})
		// 列表刷新留在 worker 侧执行（service I/O 不进 Dispatch；
		// review 建议项），合并回主线程时 operation id 仍有效。
		m.refreshAfterAdminChange(id, ctx, detail.ID)
	})
}

// ToggleSelectedEnabled 切换当前 profile 的启用标记（存储态）。
func (m *HostsWorkbenchModel) ToggleSelectedEnabled() {
	if m.busy() || m.Admin == nil || m.SelectedID == "" {
		return
	}
	profile := m.selectedProfile()
	if profile == nil {
		return
	}
	id, ctx := m.beginOperation()
	m.Problem = nil
	profileID, enabled := profile.ID, !profile.Enabled
	m.runAsync(func() {
		detail, err := m.Admin.SetProfileEnabled(ctx, profileID, enabled)
		if err != nil {
			m.dispatchError(id, "set_enabled", err)
			return
		}
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			// 启停会刷新 UpdatedAt（即修订号）：同步当前草稿的 revision，
			// 否则此后 Save 的 baseRevision 校验必然失败、草稿永远存不下
			// （review 建议项）。
			if draft := m.drafts[profileID]; draft != nil {
				draft.revision = detail.Revision
			}
		})
		m.refreshProfiles(id, ctx)
	})
}

// RequestDelete 弹确认后删除；ConfirmDelete/CancelDelete 由对话框驱动。
func (m *HostsWorkbenchModel) RequestDelete(profileID string) {
	if m.busy() || m.Admin == nil || profileID == "" {
		return
	}
	profile := m.profileByID(profileID)
	if profile == nil {
		return
	}
	if profile.Protected {
		m.Problem = &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Profile %q is protected and cannot be deleted", profile.Name)}
		return
	}
	m.PendingDelete = profileID
}

func (m *HostsWorkbenchModel) CancelDelete() { m.PendingDelete = "" }

func (m *HostsWorkbenchModel) ConfirmDelete() {
	profileID := m.PendingDelete
	if m.busy() || m.Admin == nil || profileID == "" {
		return
	}
	m.PendingDelete = ""
	id, ctx := m.beginOperation()
	m.Problem = nil
	m.runAsync(func() {
		if err := m.Admin.DeleteProfile(ctx, profileID); err != nil {
			m.dispatchError(id, "delete_profile", err)
			return
		}
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			delete(m.drafts, profileID)
			if m.SelectedID == profileID {
				m.SelectedID = ""
			}
		})
		m.refreshAfterAdminChange(id, ctx, "")
	})
}

// refreshAfterAdminChange 刷新列表；selectID 非空时选中该项。
// 调用方须在 worker 侧执行（ListProfiles I/O 不得跑在 Dispatch 里）。
func (m *HostsWorkbenchModel) refreshAfterAdminChange(id uint64, ctx context.Context, selectID string) {
	m.refreshProfiles(id, ctx)
	if selectID != "" {
		// 选中走新一轮操作：等刷新合并 dispatch 入队后再开始，
		// 保证两个 dispatch 按序执行且刷新的 operation 判活有效。
		m.dispatch(func() {
			if m.current(id) {
				m.switchToProfile(selectID)
			}
		})
	}
}

func (m *HostsWorkbenchModel) profileNames() []string {
	names := make([]string, 0, len(m.Profiles))
	for _, profile := range m.Profiles {
		names = append(names, profile.Name)
	}
	return names
}

func uniqueProfileName(base string, existing []string) string {
	taken := map[string]bool{}
	for _, name := range existing {
		taken[name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// ExportSelected 把当前 profile 导出到用户选择的目录（.hosts 文本）。
func (m *HostsWorkbenchModel) ExportSelected() {
	if m.busy() || m.Admin == nil || m.Picker == nil || m.SelectedID == "" {
		return
	}
	id, ctx := m.beginOperation()
	m.Problem = nil
	profileID := m.SelectedID
	m.runAsync(func() {
		dir, err := m.Picker.PickDirectory(ctx, app.DirectoryOptions{
			Title:       "Export Profile",
			ButtonLabel: "Export Here",
			Message:     "Choose the directory for the .hosts file",
		})
		if err != nil {
			if errors.Is(err, app.ErrPickerCancelled) {
				m.dispatch(func() {
					if m.current(id) {
						m.Phase = HostsPhaseIdle
						m.finishOperation()
					}
				})
				return
			}
			m.dispatchError(id, "export_profile", err)
			return
		}
		path, err := m.Admin.ExportProfile(ctx, profileID, dir)
		if err != nil {
			m.dispatchError(id, "export_profile", err)
			return
		}
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			m.Info = "Exported to " + path
			m.Phase = HostsPhaseIdle
			m.finishOperation()
		})
	})
}

// ImportFromDirectory 让用户选目录，导入其中全部 .hosts 文件。
func (m *HostsWorkbenchModel) ImportFromDirectory() {
	if m.busy() || m.Admin == nil || m.Picker == nil {
		return
	}
	id, ctx := m.beginOperation()
	m.Problem = nil
	m.runAsync(func() {
		dir, err := m.Picker.PickDirectory(ctx, app.DirectoryOptions{
			Title:       "Import Profiles",
			ButtonLabel: "Import from Here",
			Message:     "Choose the directory that contains .hosts files",
		})
		if err != nil {
			if errors.Is(err, app.ErrPickerCancelled) {
				m.dispatch(func() {
					if m.current(id) {
						m.Phase = HostsPhaseIdle
						m.finishOperation()
					}
				})
				return
			}
			m.dispatchError(id, "import_profiles", err)
			return
		}
		files, err := hostsFilesInDir(dir)
		if err != nil {
			m.dispatchError(id, "import_profiles", err)
			return
		}
		if len(files) == 0 {
			m.dispatch(func() {
				if m.current(id) {
					m.Info = "No .hosts files found in " + dir
					m.Phase = HostsPhaseIdle
					m.finishOperation()
				}
			})
			return
		}
		imported := 0
		var lastErr error
		for _, path := range files {
			if _, err := m.Admin.ImportProfileFile(ctx, path); err != nil {
				lastErr = err
				continue
			}
			imported++
		}
		m.dispatch(func() {
			if !m.current(id) {
				return
			}
			if lastErr != nil && imported == 0 {
				m.setError("import_profiles", lastErr)
				return
			}
			m.Info = fmt.Sprintf("Imported %d profile(s) from %s", imported, dir)
			if failed := len(files) - imported; failed > 0 {
				m.Info += fmt.Sprintf(" · %d failed (invalid content or unreadable)", failed)
			}
		})
		// 列表刷新留在 worker 侧（service I/O 不进 Dispatch）。
		m.refreshProfiles(id, ctx)
	})
}

// hostsFilesInDir 列出目录下全部 .hosts 文件（排序，绝对路径）。
func hostsFilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".hosts") {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(out)
	return out, nil
}
