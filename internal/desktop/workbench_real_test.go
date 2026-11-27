package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/storage"
)

// 真实 StorageService 驱动的 Workbench 契约测试：与 demo 版
// workbench_test.go 的关键场景一一对应，确保 #13 的 UI 流程在真实
// 磁盘服务下行为一致。所有写入都发生在 t.TempDir() 的隔离副本里。

func copyStorageFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "fixtures", "storage", name)
	dst := filepath.Join(t.TempDir(), name)
	if err := storage.NewStore(storage.NewOSVolume()).CopyTree(src, dst); err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

func newRealWorkbench(t *testing.T) (*WorkbenchModel, *stubDirectoryPicker) {
	t.Helper()
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(storage.NewService(), picker)
	return model, picker
}

func TestRealWorkbenchOpenV1Source(t *testing.T) {
	model, picker := newRealWorkbench(t)
	picker.path = copyStorageFixture(t, "v1-hosts")
	model.OpenSource()

	if model.Phase != PhaseReady || model.Workspace.Access != app.AccessReadOnly {
		t.Fatalf("phase/access = %v/%v, problem = %+v", model.Phase, model.Workspace.Access, model.Problem)
	}
	if model.Workspace.StorageVersion != 1 || len(model.Workspace.Summary.Profiles) != 1 {
		t.Fatalf("workspace = %+v", model.Workspace)
	}
	if !strings.Contains(model.Workspace.Summary.Settings.Source, "localStorage") {
		t.Fatalf("v1 settings source = %q", model.Workspace.Summary.Settings.Source)
	}
}

func TestRealWorkbenchPartialIssuesFromCorruptFiles(t *testing.T) {
	model, picker := newRealWorkbench(t)
	picker.path = copyStorageFixture(t, "mixed-invalid")
	model.OpenSource()

	if model.Phase != PhaseReady {
		t.Fatalf("phase = %v, problem = %+v", model.Phase, model.Problem)
	}
	if len(model.Workspace.Summary.Issues) != 3 || len(model.Workspace.Summary.Profiles) != 1 {
		t.Fatalf("summary = %+v", model.Workspace.Summary)
	}
	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("Issues") || !tester.HasText("3 Issues") {
		t.Fatalf("issues not visible: %q", tester.Texts())
	}
}

func TestRealWorkbenchMigrationFlow(t *testing.T) {
	model, picker := newRealWorkbench(t)
	source := copyStorageFixture(t, "v1-hosts")
	picker.path = source
	model.OpenSource()
	destination := filepath.Join(t.TempDir(), "work")
	picker.path = destination
	model.CreateWorkspace()

	if model.Workspace.Access != app.AccessWritable || model.Workspace.StorageVersion != 1 {
		t.Fatalf("workspace = %+v", model.Workspace)
	}
	// 源目录未被改动。
	if entries, err := os.ReadDir(source); err != nil || len(entries) != 2 {
		t.Fatalf("source mutated: %v %d entries", err, len(entries))
	}

	model.PreviewMigration()
	if model.Preview == nil || model.Preview.Migration == nil {
		t.Fatal("migration preview missing")
	}
	model.ConfirmPreview()

	if model.Workspace.StorageVersion != 2 || model.Receipt == nil {
		t.Fatalf("after migration: version=%d receipt=%+v problem=%+v", model.Workspace.StorageVersion, model.Receipt, model.Problem)
	}
	for _, result := range model.Receipt.Results {
		if result.Status == app.WriteFailed || !result.ReadbackVerified {
			t.Fatalf("unverified result: %+v", result)
		}
	}
	// 磁盘上的 manifest 确实升级了。
	scan, err := storage.NewStore(storage.NewOSVolume()).Scan(destination)
	if err != nil || scan.Version != 2 {
		t.Fatalf("destination scan: %v %+v", err, scan.Manifest)
	}
}

func TestRealWorkbenchValidationBackupAndRestore(t *testing.T) {
	model, picker := newRealWorkbench(t)
	picker.path = copyStorageFixture(t, "v2-full")
	model.OpenSource()
	destination := filepath.Join(t.TempDir(), "work")
	picker.path = destination
	model.CreateWorkspace()

	model.PreviewValidationChanges()
	if model.Preview == nil || model.Preview.Changes == nil {
		t.Fatal("validation preview missing")
	}
	model.ConfirmPreview()
	if model.Receipt == nil {
		t.Fatalf("receipt missing, problem = %+v", model.Problem)
	}
	for _, result := range model.Receipt.Results {
		if result.Status == app.WriteFailed {
			t.Fatalf("failed result: %+v", result)
		}
	}

	// settings.json 物化 + 快照登记。
	scan, err := storage.NewStore(storage.NewOSVolume()).Scan(destination)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if scan.Settings == nil || len(scan.Snapshots) != 2 {
		t.Fatalf("settings/snapshots = %+v/%d", scan.Settings, len(scan.Snapshots))
	}

	// 备份列表非空，恢复第一个 manifest 备份。
	model.LoadBackups()
	if len(model.Backups) == 0 {
		t.Fatal("no backups listed")
	}
	var manifestBackup *app.BackupRef
	for i := range model.Backups {
		if model.Backups[i].Entity == "manifest" {
			manifestBackup = &model.Backups[i]
		}
	}
	if manifestBackup == nil {
		t.Fatalf("no manifest backup in %v", model.Backups)
	}
	model.RequestRestore(manifestBackup.ID)
	if model.Preview == nil || model.Preview.Backup == nil {
		t.Fatal("restore preview missing")
	}
	model.ConfirmPreview()
	if model.Receipt == nil || model.Problem != nil {
		t.Fatalf("restore receipt/problem = %+v/%+v", model.Receipt, model.Problem)
	}
	result := model.Receipt.Results[0]
	if result.Preservation != app.PreserveRestored || !result.ReadbackVerified {
		t.Fatalf("restore result = %+v", result)
	}
}

func TestRealWorkbenchWriteFailureSurfacesAndKeepsData(t *testing.T) {
	volume := storage.NewFaultyVolume(storage.NewOSVolume(), storage.Faults{
		FailAtomicWrite: func(path string) bool {
			return strings.Contains(path, string(filepath.Join("profiles", "hosts")))
		},
	})
	picker := &stubDirectoryPicker{}
	model := newTestWorkbench(storage.NewService(storage.WithVolume(volume)), picker)
	picker.path = copyStorageFixture(t, "v1-hosts")
	model.OpenSource()
	picker.path = filepath.Join(t.TempDir(), "work")
	model.CreateWorkspace()

	model.PreviewMigration()
	model.ConfirmPreview()

	// 失败必须可见，且不能显示全局成功。
	if model.Problem == nil {
		t.Fatal("expected visible write failure")
	}
	if model.Receipt == nil {
		t.Fatal("partial receipt must be preserved")
	}
	failed := 0
	for _, result := range model.Receipt.Results {
		if result.Status == app.WriteFailed {
			failed++
		}
	}
	if failed == 0 {
		t.Fatalf("no failed result in %+v", model.Receipt.Results)
	}
}

func TestRealWorkbenchViewRendersWorkspace(t *testing.T) {
	model, picker := newRealWorkbench(t)
	picker.path = copyStorageFixture(t, "v2-full")
	model.OpenSource()

	tester := ui.NewTester(model.View, 1120, 760)
	tester.Frame()
	for _, want := range []string{"Read only", "2 Profiles", "1 Snapshots"} {
		if !tester.HasText(want) {
			t.Fatalf("missing %q in view: %q", want, tester.Texts())
		}
	}
	if err := tester.Click("Profiles"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("Local services") {
		t.Fatalf("profile rows missing: %q", tester.Texts())
	}
}
