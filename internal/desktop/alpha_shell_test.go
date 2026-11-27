package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	model := NewAlphaModel(boot)
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
	model := NewAlphaModel(boot)
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
	model := NewAlphaModel(boot)
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

	model := NewAlphaModel(&alphaboot.Result{DataDir: root, Service: svc})
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
	model := NewAlphaModel(&alphaboot.Result{Service: svc})
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
