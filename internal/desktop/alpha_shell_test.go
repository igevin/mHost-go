package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/alphaboot"
	"github.com/igevin/mHost-go/internal/app/hostssvc"
	"github.com/igevin/mHost-go/internal/storage"
)

// newRealHostsService 用临时工作区 + 临时 hosts 文件构造真实服务（桌面测试专用）。
// 返回服务与工作区根（测试可直接造受保护 profile 等存储态）。
func newRealHostsService(t *testing.T, profileContents ...string) (*hostssvc.Service, string) {
	t.Helper()
	root := t.TempDir()
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 keep.me\n"), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	svc := hostssvc.New(root, hostsPath, filepath.Join(t.TempDir(), "backups"), storage.NewOSVolume())
	for i, content := range profileContents {
		if _, err := svc.CreateProfile(context.Background(), "profile-"+itoaSmall(i+1), content); err != nil {
			t.Fatalf("seed profile: %v", err)
		}
	}
	return svc, root
}

func itoaSmall(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoaSmall(n/10) + string(rune('0'+n%10))
}

func newAdminWorkbench(t *testing.T, svc *hostssvc.Service) *HostsWorkbenchModel {
	t.Helper()
	model := NewHostsWorkbenchModel(svc)
	model.Admin = svc
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	return model
}

func TestWorkbenchCreateProfileSelectsIt(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	if len(model.Profiles) != 1 {
		t.Fatalf("profiles = %+v", model.Profiles)
	}

	model.CreateNewProfile()
	if model.Problem != nil {
		t.Fatalf("problem = %+v", model.Problem)
	}
	if len(model.Profiles) != 2 {
		t.Fatalf("profiles = %+v", model.Profiles)
	}
	created := findProfile(model.Profiles, "New Profile")
	if created == nil {
		t.Fatalf("New Profile missing: %+v", model.Profiles)
	}
	if model.SelectedID != created.ID {
		t.Fatalf("selected = %q, want new profile", model.SelectedID)
	}

	// 再建一个：名称去重
	model.CreateNewProfile()
	second := findProfile(model.Profiles, "New Profile 2")
	if len(model.Profiles) != 3 || second == nil {
		t.Fatalf("profiles = %+v", model.Profiles)
	}
}

func findProfile(profiles []app.HostsProfile, name string) *app.HostsProfile {
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i]
		}
	}
	return nil
}

func TestWorkbenchDuplicateAndToggleEnabled(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SelectProfile(model.Profiles[0].ID)

	model.DuplicateSelected()
	if model.Problem != nil {
		t.Fatalf("problem = %+v", model.Problem)
	}
	copy := findProfile(model.Profiles, "profile-1 copy")
	if len(model.Profiles) != 2 || copy == nil {
		t.Fatalf("profiles = %+v", model.Profiles)
	}
	if model.SelectedID != copy.ID {
		t.Fatal("copy not selected")
	}

	model.ToggleSelectedEnabled()
	if model.Problem != nil {
		t.Fatalf("toggle problem = %+v", model.Problem)
	}
	copy = findProfile(model.Profiles, "profile-1 copy")
	if copy.Enabled {
		t.Fatal("profile should be disabled after toggle")
	}
}

func TestWorkbenchDeleteRequiresConfirmation(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n", "127.0.0.1 b.com\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	first := model.Profiles[0].ID

	model.RequestDelete(first)
	if model.PendingDelete != first {
		t.Fatalf("pending delete = %q", model.PendingDelete)
	}
	// 列表未变（等待确认）
	if len(model.Profiles) != 2 {
		t.Fatal("profile deleted before confirmation")
	}

	model.ConfirmDelete()
	if model.Problem != nil {
		t.Fatalf("delete problem = %+v", model.Problem)
	}
	if len(model.Profiles) != 1 {
		t.Fatalf("profiles = %+v", model.Profiles)
	}
	if model.SelectedID == first {
		t.Fatal("deleted profile still selected")
	}
}

func TestWorkbenchDeleteProtectedRefusedImmediately(t *testing.T) {
	svc, root := newRealHostsService(t, "127.0.0.1 a.com\n")
	// 直接落盘一个受保护 profile
	protected := storage.Profile{
		ID:        "44444444-4444-4444-8444-444444444444",
		Name:      "内置",
		Enabled:   true,
		Protected: true,
		Tags:      []string{},
		Rules:     []storage.HostRule{},
		Mode:      storage.ModeHosts,
		CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.MarshalIndent(protected, "", "  ")
	dir := filepath.Join(root, "profiles", "hosts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, protected.ID+".json"), data, 0o600); err != nil {
		t.Fatalf("write protected: %v", err)
	}

	model := newAdminWorkbench(t, svc)
	model.Load()
	model.RequestDelete(protected.ID)
	if model.PendingDelete != "" {
		t.Fatal("protected profile should not enter pending delete")
	}
	if model.Problem == nil {
		t.Fatal("expected visible refusal reason")
	}
}

func TestWorkbenchSearchFiltersProfiles(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n", "127.0.0.1 b.com\n")
	profiles, _ := svc.ListProfiles(context.Background())
	if _, err := svc.DuplicateProfile(context.Background(), profiles[0].ID); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SearchQuery = "copy"
	filtered := model.filteredProfiles()
	if len(filtered) != 1 || !strings.Contains(filtered[0].Name, "copy") {
		t.Fatalf("filtered = %+v", filtered)
	}
	model.SearchQuery = ""
	if len(model.filteredProfiles()) != len(model.Profiles) {
		t.Fatal("empty query should show all")
	}
}

func TestAlphaModelStartLoadsProfiles(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	boot := &alphaboot.Result{
		DataDir:   t.TempDir(),
		HostsPath: "/tmp/hosts",
		Service:   svc,
	}
	model := NewAlphaModel(boot, nil)
	model.Hosts.Async = func(fn func()) { fn() }
	model.Hosts.Dispatch = func(fn func()) { fn() }

	model.Start()
	if len(model.Hosts.Profiles) != 1 {
		t.Fatalf("profiles = %+v", model.Hosts.Profiles)
	}
	if model.Hosts.Admin == nil {
		t.Fatal("alpha model should wire ProfileAdmin")
	}
	// Start 幂等（不重复加载）
	model.Start()
}

func TestAlphaModelNavComingSoon(t *testing.T) {
	svc, _ := newRealHostsService(t)
	boot := &alphaboot.Result{Service: svc}
	model := NewAlphaModel(boot, nil)
	if model.Title() != "mHost Alpha" {
		t.Fatalf("title = %q", model.Title())
	}
	for nav := 1; nav < len(alphaNavTabs); nav++ {
		model.SelectedNav = nav
		if alphaNavTabs[nav] == "" {
			t.Fatal("empty tab label")
		}
	}
	model.SelectedNav = 0
}

func TestAlphaModelBootBannerData(t *testing.T) {
	boot := &alphaboot.Result{
		Service: newRealSvcOnly(t),
		Issues: []app.StorageIssue{{
			Code: "orphan_snapshot", Message: "snapshot file not in index",
		}},
	}
	model := NewAlphaModel(boot, nil)
	if model.Boot.Issues[0].Code != "orphan_snapshot" {
		t.Fatalf("issues = %+v", model.Boot.Issues)
	}
	if len(boot.Issues) == 0 {
		t.Fatal("banner would not render")
	}
}

func newRealSvcOnly(t *testing.T) *hostssvc.Service {
	svc, _ := newRealHostsService(t)
	return svc
}

func TestAlphaSnapshotsPageLoads(t *testing.T) {
	svc, root := newRealHostsService(t, "127.0.0.1 a.com\n")
	// alphaboot 在真实启动时会写 v2 manifest；测试补齐该前提
	manifest := `{"version":2,"app_version":"0.3.0-alpha","updated_at":"2026-11-01T00:00:00Z","dns_enabled":false,"original_dns":{"kind":"dhcp_empty"}}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// 写一个快照索引 + 一个孤儿快照文件
	snapshotDir := filepath.Join(root, "snapshots")
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	index := `{"version":2,"snapshots":[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"baseline","profile_count":1,"created_at":"2026-11-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "index.json"), []byte(index), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	kept := `{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"baseline","profile_count":1,"created_at":"2026-11-01T00:00:00Z","profiles":[]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.json"), []byte(kept), 0o600); err != nil {
		t.Fatalf("write kept: %v", err)
	}
	orphan := `{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"orphan","profile_count":0,"created_at":"2026-11-01T00:00:00Z","profiles":[]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.json"), []byte(orphan), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	model := NewAlphaModel(&alphaboot.Result{DataDir: root, Service: svc}, nil)
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	model.LoadSnapshots()
	if len(model.snapshots) != 1 || model.snapshots[0].Meta.Name != "baseline" {
		t.Fatalf("snapshots = %+v issues = %+v", model.snapshots, model.snapshotIssues)
	}
	foundOrphan := false
	for _, issue := range model.snapshotIssues {
		if issue.Code == "orphan_snapshot" {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Fatalf("orphan issue missing: %+v", model.snapshotIssues)
	}
}

func TestAlphaSystemHostsPageLoads(t *testing.T) {
	svc, _ := newRealHostsService(t)
	model := NewAlphaModel(&alphaboot.Result{Service: svc}, nil)
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	model.LoadSystemHosts(context.Background())
	if model.systemHostsErr != nil {
		t.Fatalf("err = %v", model.systemHostsErr)
	}
	if !strings.Contains(model.systemHosts, "127.0.0.1 keep.me") {
		t.Fatalf("content = %q", model.systemHosts)
	}
	if got := managedBlockBounds("# ---- mHost start ----\nx\n"); got != 0 {
		t.Fatalf("bounds = %d", got)
	}
	if got := managedBlockBounds("no block\n"); got != -1 {
		t.Fatalf("bounds = %d", got)
	}
}

type fakePicker struct {
	dir string
	err error
}

func (p fakePicker) PickDirectory(ctx context.Context, opts app.DirectoryOptions) (string, error) {
	return p.dir, p.err
}

func TestWorkbenchExportAndImportRoundTrip(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 export.me\n")
	exportDir := t.TempDir()
	importDir := t.TempDir()
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SelectProfile(model.Profiles[0].ID)
	model.Picker = fakePicker{dir: exportDir}

	model.ExportSelected()
	if model.Problem != nil {
		t.Fatalf("export problem = %+v", model.Problem)
	}
	if model.Info == "" || !strings.Contains(model.Info, exportDir) {
		t.Fatalf("info = %q", model.Info)
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".hosts") {
		t.Fatalf("exports = %v err=%v", entries, err)
	}

	// 把导出文件挪到导入目录，导入回来
	exported, _ := os.ReadFile(filepath.Join(exportDir, entries[0].Name()))
	if err := os.WriteFile(filepath.Join(importDir, entries[0].Name()), exported, 0o644); err != nil {
		t.Fatalf("copy: %v", err)
	}
	model.Picker = fakePicker{dir: importDir}
	model.ImportFromDirectory()
	if model.Problem != nil {
		t.Fatalf("import problem = %+v", model.Problem)
	}
	if len(model.Profiles) != 2 {
		t.Fatalf("profiles = %+v", model.Profiles)
	}
}

func TestStartupErrorModel(t *testing.T) {
	model := NewStartupErrorModel(errors.New("manifest corrupted"))
	if model.Title() == "" {
		t.Fatal("title missing")
	}
	if !model.RequestQuit() {
		t.Fatal("startup error should allow quit")
	}
	if err := model.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	quit := false
	model.SetOnSafeToQuit(func() { quit = true })
	if model.onQuit == nil {
		t.Fatal("onQuit not wired")
	}
	_ = quit
}

func TestToggleEnabledKeepsDraftRevisionSaveable(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SelectProfile(model.Profiles[0].ID)

	// 启停（刷新存储修订号）后必须仍能编辑并保存当前草稿
	model.ToggleSelectedEnabled()
	if model.Problem != nil {
		t.Fatalf("toggle problem = %+v", model.Problem)
	}
	model.Editor.Select(0, 0)
	model.Editor.Insert("# note\n")
	model.SyncFromEditor()
	if !model.Modified() {
		t.Fatal("draft not modified")
	}
	model.Save()
	if model.Problem != nil {
		t.Fatalf("save after toggle = %+v", model.Problem)
	}
	if model.Modified() {
		t.Fatal("draft still modified after save")
	}
}

func TestAlphaModelWiresPickerAndExposesIt(t *testing.T) {
	picker := fakePicker{dir: t.TempDir()}
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	model := NewAlphaModel(&alphaboot.Result{Service: svc}, picker)
	if model.Hosts.Picker == nil {
		t.Fatal("picker not wired into hosts workbench")
	}
	if model.DirectoryPicker() == nil {
		t.Fatal("DirectoryPicker() not exposed for App parent wiring")
	}
}

func TestExportImportBusyGuardsAndPhaseRestore(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	exportDir := t.TempDir()
	model := newAdminWorkbench(t, svc)
	model.Load()
	model.SelectProfile(model.Profiles[0].ID)
	model.Picker = fakePicker{dir: exportDir}

	// 忙碌中拒绝再进导出（busy guard 生效）
	model.Phase = HostsPhaseApplying
	model.ExportSelected()
	if model.Info != "" {
		t.Fatal("export should refuse while busy")
	}
	model.Phase = HostsPhaseIdle

	// 完成后相位恢复 Idle（busy 态曾进入）
	model.ExportSelected()
	if model.Problem != nil {
		t.Fatalf("export problem = %+v", model.Problem)
	}
	if model.busy() {
		t.Fatal("phase should restore to idle after export")
	}
	if model.Info == "" {
		t.Fatal("export info missing")
	}
}

// #35：History 页整合 Apply 备份历史，支持任意备份恢复（Restore），
// 恢复后刷新 Active 状态、System Hosts 与备份计数缓存。

func newSyncAlphaModel(t *testing.T, svc *hostssvc.Service) *AlphaModel {
	t.Helper()
	model := NewAlphaModel(&alphaboot.Result{Service: svc}, nil)
	model.Async = func(fn func()) { fn() }
	model.Dispatch = func(fn func()) { fn() }
	model.Hosts.Async = func(fn func()) { fn() }
	model.Hosts.Dispatch = func(fn func()) { fn() }
	return model
}

func applyFirstProfile(t *testing.T, svc *hostssvc.Service) app.ApplyReceipt {
	t.Helper()
	profiles, err := svc.ListProfiles(context.Background())
	if err != nil || len(profiles) == 0 {
		t.Fatalf("list profiles: %v (%d)", err, len(profiles))
	}
	plan, err := svc.PreviewApply(context.Background(), profiles[0].ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := svc.Apply(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return receipt
}

func TestAlphaHistoryTabReplacesSnapshots(t *testing.T) {
	if alphaNavTabs[1] != "History" {
		t.Fatalf("nav tabs = %v, want History at index 1", alphaNavTabs)
	}
}

func TestAlphaHistoryListsApplyBackups(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)

	model := newSyncAlphaModel(t, svc)
	model.LoadHistory(context.Background())
	if model.historyErr != nil {
		t.Fatalf("history err = %v", model.historyErr)
	}
	if len(model.history) != 1 {
		t.Fatalf("history = %+v", model.history)
	}
	entry := model.history[0]
	if entry.ProfileName != "profile-1" {
		t.Fatalf("profile = %q, want profile-1", entry.ProfileName)
	}
	if entry.Result != app.BackupResultApplied || !entry.Available {
		t.Fatalf("entry = %+v, want applied+available", entry)
	}
}

func TestAlphaRestoreFlowConfirmsThenRefreshes(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)

	model := newSyncAlphaModel(t, svc)
	model.Hosts.Load()
	if model.Hosts.Applied.ProfileID == "" {
		t.Fatal("profile should be applied before restore")
	}
	before, err := svc.SystemHostsContent(context.Background())
	if err != nil || !strings.Contains(before, "mHost start") {
		t.Fatalf("hosts before restore = %q err=%v", before, err)
	}

	model.LoadHistory(context.Background())
	if len(model.history) != 1 {
		t.Fatalf("history = %+v", model.history)
	}
	backupID := model.history[0].ID

	// 未知备份不进入确认态
	model.RequestRestore("does-not-exist")
	if model.pendingRestore != nil {
		t.Fatal("unknown backup should not arm restore")
	}
	// 确认前可取消，且系统未变
	model.RequestRestore(backupID)
	if model.pendingRestore == nil || model.pendingRestore.ID != backupID {
		t.Fatalf("pending restore = %+v", model.pendingRestore)
	}
	model.CancelRestore()
	if model.pendingRestore != nil {
		t.Fatal("cancel should clear pending restore")
	}
	content, _ := svc.SystemHostsContent(context.Background())
	if content != before {
		t.Fatal("hosts changed before confirm")
	}

	model.RequestRestore(backupID)
	model.ConfirmRestore()
	if model.restoring {
		t.Fatal("restoring flag should clear after restore")
	}
	if model.restoreProblem != nil {
		t.Fatalf("restore problem = %+v", model.restoreProblem)
	}
	if model.restoreInfo == "" {
		t.Fatal("restore success info missing")
	}

	// 系统 hosts 恢复到应用前（无管理块），Active 归零并刷新到主页面
	content, err = svc.SystemHostsContent(context.Background())
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	if strings.Contains(content, "mHost start") {
		t.Fatalf("hosts still managed after restore: %q", content)
	}
	if model.Hosts.Applied.ProfileID != "" {
		t.Fatalf("applied after restore = %+v, want empty", model.Hosts.Applied)
	}

	// 缓存失效：System Hosts / 备份计数 / 历史（安全备份已生成）
	if model.systemHostsLoaded || model.backupCountLoaded {
		t.Fatal("system hosts and backup count caches must invalidate after restore")
	}
	model.LoadHistory(context.Background())
	if len(model.history) != 2 {
		t.Fatalf("history after restore = %+v, want 2 (apply + safety)", model.history)
	}
	safety := model.history[0]
	if safety.Result != app.BackupResultApplied || !safety.Available {
		t.Fatalf("safety backup = %+v", safety)
	}
	if safety.ProfileID != "" {
		t.Fatalf("safety backup profile = %q, want empty (restored to no profile)", safety.ProfileID)
	}
}

func TestAlphaRestoreFailureKeepsSystemAndReports(t *testing.T) {
	// 自建服务以拿到备份目录：删掉备份文件后恢复必然失败
	root := t.TempDir()
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 keep.me\n"), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	backupDir := filepath.Join(t.TempDir(), "backups")
	svc := hostssvc.New(root, hostsPath, backupDir, storage.NewOSVolume())
	if _, err := svc.CreateProfile(context.Background(), "profile-1", "127.0.0.1 a.com\n"); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	receipt := applyFirstProfile(t, svc)
	backups, err := svc.ListApplyBackups(context.Background())
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v err=%v", backups, err)
	}
	if err := os.Remove(filepath.Join(backupDir, "hosts-"+backups[0].ID+".bak")); err != nil {
		t.Fatalf("remove backup file: %v", err)
	}

	model := newSyncAlphaModel(t, svc)
	model.Hosts.Load()
	model.LoadHistory(context.Background())

	model.RequestRestore(receipt.RollbackRef.ID)
	model.ConfirmRestore()
	if model.restoreProblem == nil {
		t.Fatal("restore problem missing after failed restore")
	}
	if model.restoring {
		t.Fatal("restoring flag should clear after failure")
	}
	content, _ := svc.SystemHostsContent(context.Background())
	if !strings.Contains(content, "mHost start") {
		t.Fatalf("hosts changed after failed restore: %q", content)
	}
	if model.Hosts.Applied.ProfileID == "" {
		t.Fatalf("applied changed after failed restore: %+v", model.Hosts.Applied)
	}
}

// TestAlphaHistoryPageRendersBackupsAndRestoreDialog 走渲染层：History
// 页展示备份列表，Restore… 弹出确认对话框，确认后系统恢复、Active 刷新。
func TestAlphaHistoryPageRendersBackupsAndRestoreDialog(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)
	model := newSyncAlphaModel(t, svc)
	model.Hosts.Load()

	tester := ui.NewTester(model.View, 1120, 760)
	model.SelectedNav = 1
	tester.Frame()
	for _, want := range []string{"History", "Apply backups", "profile-1", "Applied", "Restore…"} {
		if !tester.HasText(want) {
			t.Fatalf("text %q missing: %q", want, tester.Texts())
		}
	}

	if err := tester.Click("Restore…"); err != nil {
		t.Fatalf("click restore: %v (texts %q)", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("Restore backup") {
		t.Fatalf("confirm dialog missing: %q", tester.Texts())
	}
	if err := tester.Click("Restore"); err != nil {
		t.Fatalf("click confirm restore: %v (texts %q)", err, tester.Texts())
	}
	if model.restoreProblem != nil {
		t.Fatalf("restore problem = %+v", model.restoreProblem)
	}
	if model.Hosts.Applied.ProfileID != "" {
		t.Fatalf("applied after restore = %+v", model.Hosts.Applied)
	}
	content, err := svc.SystemHostsContent(context.Background())
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	if strings.Contains(content, "mHost start") {
		t.Fatalf("hosts still managed after restore: %q", content)
	}
}

// #37：辅助页面——Apply/Rollback 后缓存失效、System Hosts 只读浏览
// （行号/管理块标记/跳转/复制）、Settings 页收敛为 Data。

func TestAlphaReceiptInvalidatesAuxCaches(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)

	model := newSyncAlphaModel(t, svc)
	model.LoadSystemHosts(context.Background())
	model.LoadBackupCount(context.Background())
	if !model.systemHostsLoaded || !model.backupCountLoaded {
		t.Fatal("caches should be loaded")
	}

	// 模拟主页面写操作完成（workbench 回执 OperationID 变化）
	model.Hosts.Receipt = &app.ApplyReceipt{OperationID: "op-invalidates"}
	model.invalidateOnNewReceipt()
	if model.systemHostsLoaded || model.backupCountLoaded || model.historyLoaded {
		t.Fatal("apply/receipt must invalidate system hosts, backup count and history caches")
	}

	// 同一回执不重复失效；重新加载后保持
	model.LoadSystemHosts(context.Background())
	model.LoadBackupCount(context.Background())
	model.invalidateOnNewReceipt()
	if !model.systemHostsLoaded || !model.backupCountLoaded {
		t.Fatal("same receipt op must not invalidate again")
	}
}

func TestAlphaSystemHostsViewerMarksManagedBlock(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)

	model := newSyncAlphaModel(t, svc)
	model.LoadSystemHosts(context.Background())
	if model.systemHostsErr != nil {
		t.Fatalf("load: %v", model.systemHostsErr)
	}
	viewer := model.systemHostsViewer
	if viewer == nil {
		t.Fatal("system hosts viewer missing")
	}
	if !viewer.ReadOnly {
		t.Fatal("system hosts viewer must be read only")
	}
	if !strings.Contains(viewer.Buffer.String(), "mHost start") {
		t.Fatalf("viewer content = %q", viewer.Buffer.String())
	}
	marks := viewer.Diagnostics()
	if len(marks) != 1 || marks[0].Message != "mHost managed block" {
		t.Fatalf("managed block marks = %+v", marks)
	}
	if model.systemHostsReadAt.IsZero() {
		t.Fatal("last read time missing")
	}
}

func TestAlphaSystemHostsJumpTarget(t *testing.T) {
	content := "127.0.0.1 keep.me\n# ---- mHost start ----\n9.9.9.9 legacy.com\n# ---- mHost end ----\ntail.line\n"
	if got := managedBlockBounds(content); got != 1 {
		t.Fatalf("managed block line = %d, want 1", got)
	}
	if got := managedBlockBounds("no block\n"); got != -1 {
		t.Fatalf("managed block line = %d, want -1", got)
	}
}

func TestAlphaSystemHostsRefreshReloads(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")

	model := newSyncAlphaModel(t, svc)
	model.LoadSystemHosts(context.Background())
	if strings.Contains(model.systemHosts, "mHost start") {
		t.Fatalf("unexpected block before apply: %q", model.systemHosts)
	}

	applyFirstProfile(t, svc)
	model.RefreshSystemHosts()
	if !model.systemHostsLoaded {
		t.Fatal("refresh should reload system hosts")
	}
	if !strings.Contains(model.systemHosts, "mHost start") {
		t.Fatalf("stale content after refresh: %q", model.systemHosts)
	}
}

func TestAlphaDataTabReplacesSettings(t *testing.T) {
	if alphaNavTabs[3] != "Data" {
		t.Fatalf("nav tabs = %v, want Data at index 3", alphaNavTabs)
	}
}

// TestAlphaSystemHostsPageRendersReadOnlyViewer 渲染冒烟：只读浏览器、
// 管理块标记说明、跳转与复制入口都出帧。
func TestAlphaSystemHostsPageRendersReadOnlyViewer(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)
	model := newSyncAlphaModel(t, svc)

	tester := ui.NewTester(model.View, 1120, 760)
	model.SelectedNav = 2
	tester.Frame()
	for _, want := range []string{"System Hosts", "mHost managed block at line 2", "Jump to Managed Block", "Copy All", "Refresh"} {
		if !tester.HasText(want) {
			t.Fatalf("text %q missing: %q", want, tester.Texts())
		}
	}
}

// TestAlphaDataPageRendersPathsAndCount 渲染冒烟：Data 页展示数据目录、
// 系统 hosts 路径与备份计数（含复制入口）。
func TestAlphaDataPageRendersPathsAndCount(t *testing.T) {
	svc, _ := newRealHostsService(t, "127.0.0.1 a.com\n")
	applyFirstProfile(t, svc)
	model := newSyncAlphaModel(t, svc)
	model.Boot = &alphaboot.Result{Service: svc, DataDir: "/tmp/mhost-data", HostsPath: "/tmp/mhost-hosts"}

	tester := ui.NewTester(model.View, 1120, 760)
	model.SelectedNav = 3
	tester.Frame()
	for _, want := range []string{"Data", "Data directory", "/tmp/mhost-data", "System hosts", "/tmp/mhost-hosts", "Apply backups: 1", "Copy"} {
		if !tester.HasText(want) {
			t.Fatalf("text %q missing: %q", want, tester.Texts())
		}
	}
}
