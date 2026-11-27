package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
)

// PR #22 review 回归测试。

// 恢复内容与当前文件相同时（例如同一份备份恢复两次）必须成功返回，
// 且绝不能产生 typed-nil error 让 UI 层空指针解引用。
func TestRestoreSameBackupTwiceSucceeds(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v2-full"))

	preview, err := service.PreviewValidationChange(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID); err != nil {
		t.Fatalf("save: %v", err)
	}
	backups, err := service.ListBackups(context.Background(), workspace.ID)
	if err != nil || len(backups) == 0 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	// 直接恢复第一个备份；不做任何外部修改，内容与工作副本一致。
	receipt, err := service.RestoreBackup(context.Background(), workspace.ID, backups[len(backups)-1].ID)
	if err != nil {
		t.Fatalf("restore identical content must succeed: %v", err)
	}
	// 第二次恢复：当前文件与备份字节相同，走 unchanged 分支，
	// 同样必须成功且不能产生 typed-nil error。
	receipt, err = service.RestoreBackup(context.Background(), workspace.ID, backups[len(backups)-1].ID)
	if err != nil {
		t.Fatalf("second restore of identical content must succeed: %v", err)
	}
	if err != nil {
		var uiErr *app.UIError
		if errors.As(err, &uiErr) && uiErr == nil {
			t.Fatal("typed-nil UIError leaked")
		}
	}
	if len(receipt.Results) == 0 || receipt.Results[0].Preservation != app.PreserveRestored {
		t.Fatalf("restore receipt = %+v", receipt.Results)
	}
}

// v1 profile 没有 mode 字段时，按所在目录推断模式：profiles/dns/ 下的
// 文件必须是 dns，迁移 round-trip 后磁盘上写回的也是 dns。
func TestV1ProfileModeInferredFromDirectory(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-full"))

	scan, err := service.store.Scan(workspace.Root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	modes := map[string]ProfileMode{}
	for _, entry := range scan.Profiles {
		modes[entry.Profile.Name] = entry.Profile.Mode
	}
	if modes["Legacy DNS"] != ModeDNS {
		t.Fatalf("dns-dir profile mode = %q", modes["Legacy DNS"])
	}
	if modes["Legacy development"] != ModeHosts {
		t.Fatalf("hosts-dir profile mode = %q", modes["Legacy development"])
	}

	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// round-trip 后磁盘上的 mode 与目录语义一致，没有被改写成 hosts。
	data, err := os.ReadFile(filepath.Join(workspace.Root, "profiles", "dns", "33333333-3333-4333-8333-333333333333.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"mode": "dns"`) {
		t.Fatalf("dns profile lost its mode after migration:\n%s", data)
	}
}

// 目录不可读（权限错误）必须报告 permission_denied，而不是 validation_failed。
func TestInspectPermissionDeniedStaysPermission(t *testing.T) {
	service := newTestService(t)
	dir := copyFixture(t, "v1-hosts")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := service.Inspect(context.Background(), dir)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) {
		t.Fatalf("err = %v, want UIError", err)
	}
	if uiErr.Kind != app.ErrorPermission {
		t.Fatalf("kind = %q, want permission_denied", uiErr.Kind)
	}
}

// CreateWorkspace 必须使用校验后的绝对路径，而不是用户原始输入。
func TestCreateWorkspaceUsesValidatedAbsolutePath(t *testing.T) {
	service := newTestService(t)
	base := t.TempDir()
	sourceRoot := copyFixture(t, "v1-hosts")
	source, err := service.Inspect(context.Background(), sourceRoot)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	// 用相对路径（含 . 段）作目标；workspace.Root 必须是清洗后的绝对路径。
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relDest, err := filepath.Rel(cwd, filepath.Join(base, "work"))
	if err != nil {
		t.Fatal(err)
	}
	relDest = "./" + relDest + string(filepath.Separator) + "." // 制造冗余路径段
	workspace, err := service.CreateWorkspace(context.Background(), source.ID, relDest)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	want := filepath.Join(base, "work")
	if workspace.Root != want {
		t.Fatalf("root = %q, want cleaned absolute %q", workspace.Root, want)
	}
}

// TestBackupContentRejectsBadID 确保 backupID 不能越界读取。
func TestBackupContentRejectsBadID(t *testing.T) {
	store := newTestStore()
	if _, err := store.BackupContent(t.TempDir(), "../../etc/passwd"); err == nil {
		t.Fatal("path-traversal backupID must be rejected")
	}
	if _, err := store.BackupContent(t.TempDir(), "not-a-uuid"); err == nil {
		t.Fatal("non-uuid backupID must be rejected")
	}
}

// 构造带路径穿越记录的备份索引：恢复不得逃逸出工作副本。
func TestBackupIndexTraversalRecordIsRejected(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v2-full"))

	// 写入恶意备份登记：path 指向工作副本外的文件。
	outside := filepath.Join(t.TempDir(), "evil.txt")
	if err := os.WriteFile(outside, []byte("innocent"), 0o600); err != nil {
		t.Fatal(err)
	}
	escapePath, err := filepath.Rel(workspace.Root, outside)
	if err != nil {
		t.Fatal(err)
	}
	backupsDir := filepath.Join(workspace.Root, DirBackups)
	if err := os.MkdirAll(backupsDir, DirPerm); err != nil {
		t.Fatal(err)
	}
	malicious := `{"backups":[{"id":"99999999-9999-4999-8999-999999999999","entity":"manifest","path":"` + filepath.ToSlash(escapePath) + `","created_at":"2026-11-27T00:00:00Z","operation_id":"op-evil"}]}`
	if err := os.WriteFile(filepath.Join(backupsDir, "index.json"), []byte(malicious), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupsDir, "99999999-9999-4999-8999-999999999999.json"), []byte("pwned"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 登记里的恶意记录必须被丢弃。
	backups, err := service.ListBackups(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	for _, backup := range backups {
		if backup.ID == "99999999-9999-4999-8999-999999999999" {
			t.Fatal("traversal backup record must be dropped from listing")
		}
	}
	// 直接请求恢复也必须失败，且工作副本外的文件不被改动。
	if _, err := service.RestoreBackup(context.Background(), workspace.ID, "99999999-9999-4999-8999-999999999999"); err == nil {
		t.Fatal("restore of traversal record must fail")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "innocent" {
		t.Fatalf("outside file overwritten: %q", data)
	}
}

// 恢复失败时结果不得标记为 PreserveRestored。
func TestRestoreFailureNotMarkedRestored(t *testing.T) {
	restoreOnly := false
	volume := NewFaultyVolume(NewOSVolume(), Faults{
		FailAtomicWrite: func(path string) bool {
			return restoreOnly && strings.HasSuffix(path, FileManifest)
		},
	})
	service := newTestService(t, WithVolume(volume))
	workspace := inspectAndCopy(t, service, copyFixture(t, "v2-full"))

	preview, err := service.PreviewValidationChange(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID); err != nil {
		t.Fatalf("save: %v", err)
	}
	backups, err := service.ListBackups(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var manifestBackup *app.BackupRef
	for i := range backups {
		if backups[i].Entity == "manifest" {
			manifestBackup = &backups[i]
		}
	}
	if manifestBackup == nil {
		t.Fatalf("no manifest backup: %v", backups)
	}
	restoreOnly = true
	receipt, err := service.RestoreBackup(context.Background(), workspace.ID, manifestBackup.ID)
	if err == nil {
		t.Fatal("expected restore failure")
	}
	result := receipt.Results[0]
	if result.Status != app.WriteFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Preservation == app.PreserveRestored {
		t.Fatalf("failed restore must not be marked restored: %+v", result)
	}
}
