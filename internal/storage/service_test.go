package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
)

// copyFixture 把 fixture 复制到隔离临时目录，测试从不写仓库内的 fixture。
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := fixturePath(t, name)
	dst := filepath.Join(t.TempDir(), name)
	if err := NewStore(NewOSVolume()).CopyTree(src, dst); err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	return NewService(opts...)
}

func TestInspectV1SourceIsReadOnly(t *testing.T) {
	service := newTestService(t)
	workspace, err := service.Inspect(context.Background(), copyFixture(t, "v1-hosts"))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if workspace.Access != app.AccessReadOnly || workspace.StorageVersion != 1 {
		t.Fatalf("workspace: %+v", workspace)
	}
	if len(workspace.Summary.Profiles) != 1 {
		t.Fatalf("profiles = %d", len(workspace.Summary.Profiles))
	}
	if !strings.Contains(workspace.Summary.Settings.Source, "localStorage") {
		t.Fatalf("v1 settings source = %q", workspace.Summary.Settings.Source)
	}
}

func TestInspectRejectsEmptyAndMissingRoot(t *testing.T) {
	service := newTestService(t)
	if _, err := service.Inspect(context.Background(), "  "); err == nil {
		t.Fatal("empty root must fail")
	}
	if _, err := service.Inspect(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root must fail")
	}
}

func TestCreateWorkspaceCopiesSourceAndValidatesTarget(t *testing.T) {
	service := newTestService(t)
	sourceRoot := copyFixture(t, "v1-hosts")
	source, err := service.Inspect(context.Background(), sourceRoot)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	workspace, err := service.CreateWorkspace(context.Background(), source.ID, filepath.Join(t.TempDir(), "work"))
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if workspace.Access != app.AccessWritable || workspace.StorageVersion != 1 || workspace.SourceRoot != sourceRoot {
		t.Fatalf("workspace: %+v", workspace)
	}
	// 源目录不被改动：没有 backups 目录产生。
	if entries, _ := os.ReadDir(sourceRoot); len(entries) != 2 {
		t.Fatalf("source tree mutated: %d entries", len(entries))
	}
	// 非法目标：相同、嵌套、非空。
	if _, err := service.CreateWorkspace(context.Background(), source.ID, sourceRoot); err == nil {
		t.Fatal("same-dir target must fail")
	}
	if _, err := service.CreateWorkspace(context.Background(), source.ID, filepath.Join(sourceRoot, "nested")); err == nil {
		t.Fatal("nested target must fail")
	}
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateWorkspace(context.Background(), source.ID, occupied); err == nil {
		t.Fatal("non-empty target must fail")
	}
}

func TestMigrationFlowOnV1WorkingCopy(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))

	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview migration: %v", err)
	}
	if plan.FromVersion != 1 || plan.ToVersion != 2 || len(plan.Blockers) != 0 {
		t.Fatalf("plan: %+v", plan)
	}
	if len(plan.Changes) != 2 { // manifest + 1 profile
		t.Fatalf("changes = %v", plan.Changes)
	}

	receipt, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID)
	if err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	for _, result := range receipt.Results {
		if result.Status != app.WriteWritten || !result.ReadbackVerified || result.Backup == nil {
			t.Fatalf("result not fully verified: %+v", result)
		}
	}
	if receipt.Revision != receipt.BaseRevision+1 {
		t.Fatalf("revision = %d, base = %d", receipt.Revision, receipt.BaseRevision)
	}

	// 迁移结果：manifest 升到 v2 并物化 DNS 默认值；profile 内容保留。
	scan, err := service.store.Scan(workspace.Root)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if scan.Version != 2 || scan.Manifest.DNSEnabled == nil || *scan.Manifest.DNSEnabled {
		t.Fatalf("manifest after migration: %+v", scan.Manifest)
	}
	if scan.Manifest.OriginalDNS == nil || scan.Manifest.OriginalDNS.Kind != OriginalDNSDHCPEmpty {
		t.Fatalf("original_dns after migration: %+v", scan.Manifest.OriginalDNS)
	}
	if len(scan.Profiles) != 1 || len(scan.Profiles[0].Profile.Rules) == 0 {
		t.Fatalf("profiles after migration: %+v", scan.Profiles)
	}
	// 备份可定位。
	backups, err := service.ListBackups(context.Background(), workspace.ID)
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	// 迁移后的副本不再可预览迁移。
	if _, err := service.PreviewMigration(context.Background(), workspace.ID); err == nil {
		t.Fatal("v2 workspace must not preview migration")
	}
}

func TestMigrationBlockedByDamagedProfile(t *testing.T) {
	service := newTestService(t)
	root := copyFixture(t, "v1-hosts")
	broken := filepath.Join(root, "profiles", "hosts", "broken.json")
	if err := os.WriteFile(broken, []byte(`{"id": `), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := inspectAndCopy(t, service, root)
	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(plan.Blockers) != 1 {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
	if _, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID); err == nil {
		t.Fatal("blocked migration must not apply")
	}
	// 副本保持 v1 原样。
	scan, err := service.store.Scan(workspace.Root)
	if err != nil || scan.Version != 1 {
		t.Fatalf("workspace mutated: %v %+v", err, scan.Manifest)
	}
}

func TestMigrationPreservesSnapshotsAndAdBlock(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-full"))

	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(plan.Changes) != 3 { // manifest + 2 profiles
		t.Fatalf("changes = %v", plan.Changes)
	}
	if _, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID); err != nil {
		t.Fatalf("apply: %v", err)
	}

	scan, err := service.store.Scan(workspace.Root)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if scan.Version != 2 {
		t.Fatalf("version = %d", scan.Version)
	}
	// 快照与广告配置不是迁移对象：原样保留。
	if len(scan.Snapshots) != 1 || scan.Snapshots[0].Meta.Name != "Before cleanup" {
		t.Fatalf("snapshots after migration: %+v", scan.Snapshots)
	}
	if scan.AdBlock == nil || len(scan.AdBlock.Sources) != 1 || scan.AdBlock.Whitelist[0] != "allowed.legacy.test" {
		t.Fatalf("adblock after migration: %+v", scan.AdBlock)
	}
	// 两个 profile 的内容语义保留（规则数不变）。
	byRules := map[string]int{}
	for _, entry := range scan.Profiles {
		byRules[entry.Profile.Name] = len(entry.Profile.Rules)
	}
	if byRules["Legacy development"] != 2 || byRules["Legacy DNS"] != 1 {
		t.Fatalf("rules after migration: %v", byRules)
	}
}

func TestStalePreviewRejectedAfterWrite(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))
	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// 旧 plan 已被消费；再生成一个并模拟过期。
	workspace = refreshWorkspace(t, service, workspace.ID)
	if workspace.StorageVersion != 2 {
		t.Fatalf("version = %d", workspace.StorageVersion)
	}
	preview, err := service.PreviewValidationChange(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview validation: %v", err)
	}
	receipt, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID)
	if err != nil {
		t.Fatalf("save validation: %v", err)
	}
	_ = receipt
	// 同一个 preview ID 在 revision 变化后必须过期。
	if _, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID); err == nil {
		t.Fatal("stale preview must be rejected")
	} else {
		var uiErr *app.UIError
		if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorStale {
			t.Fatalf("err kind = %v", err)
		}
	}
}

func TestValidationChangeMaterializesSettingsAndSnapshot(t *testing.T) {
	service := newTestService(t)
	root := copyFixture(t, "v2-full")
	workspace := inspectAndCopy(t, service, root)

	preview, err := service.PreviewValidationChange(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	entities := map[string]bool{}
	for _, change := range preview.Changes {
		entities[change.Entity] = true
	}
	for _, want := range []string{"manifest", "profile", "settings", "snapshot", "snapshot_index"} {
		if !entities[want] {
			t.Fatalf("change %s missing: %v", want, preview.Changes)
		}
	}

	receipt, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	statuses := map[string]app.WriteStatus{}
	for _, result := range receipt.Results {
		statuses[result.Entity+" "+result.Path] = result.Status
		if result.Status == app.WriteFailed {
			t.Fatalf("failed result: %+v", result)
		}
	}
	// 磁盘结果：settings.json 物化、快照文件与索引登记。
	scan, err := service.store.Scan(workspace.Root)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if scan.Settings == nil {
		t.Fatal("settings.json not materialized")
	}
	if len(scan.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2 (fixture + validation)", len(scan.Snapshots))
	}
	// 至少 manifest/profile 有备份；快照是新建不需要备份。
	backups, err := service.ListBackups(context.Background(), workspace.ID)
	if err != nil || len(backups) < 2 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
}

func TestRestoreBackupRoundTrip(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v2-full"))

	// 先产生一次备份：修改 manifest 需要可写 v2 副本上的验证变更。
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
	var manifestBackup *app.BackupRef
	for i := range backups {
		if backups[i].Entity == "manifest" {
			manifestBackup = &backups[i]
		}
	}
	if manifestBackup == nil {
		t.Fatalf("no manifest backup in %v", backups)
	}

	// 外部改动 manifest 后恢复；期望恢复到备份时的内容（备份保存的是
	// 原始字节，语义上与验证变更后的文件一致）。
	if err := os.WriteFile(filepath.Join(workspace.Root, FileManifest), []byte(`{"corrupted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.RestoreBackup(context.Background(), workspace.ID, manifestBackup.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	result := receipt.Results[0]
	if result.Status != app.WriteWritten || !result.ReadbackVerified || result.Preservation != app.PreserveRestored || result.Backup == nil {
		t.Fatalf("restore result: %+v", result)
	}
	restored, err := os.ReadFile(filepath.Join(workspace.Root, FileManifest))
	if err != nil {
		t.Fatal(err)
	}
	var restoredManifest, backupManifest Manifest
	if err := unmarshalModel(restored, &restoredManifest); err != nil {
		t.Fatalf("restored manifest unparsable: %v\n%s", err, restored)
	}
	backupContent, err := service.store.BackupContent(workspace.Root, manifestBackup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := unmarshalModel(backupContent, &backupManifest); err != nil {
		t.Fatal(err)
	}
	if restoredManifest.Version != backupManifest.Version ||
		restoredManifest.AppVersion != backupManifest.AppVersion ||
		restoredManifest.DNSEnabled == nil || backupManifest.DNSEnabled == nil ||
		*restoredManifest.DNSEnabled != *backupManifest.DNSEnabled {
		t.Fatalf("restored manifest differs from backup:\n%s\n%s", restored, backupContent)
	}
	// 恢复前产生了安全备份。
	backups, err = service.ListBackups(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundSafety := false
	for _, backup := range backups {
		if backup.OperationID == receipt.OperationID {
			foundSafety = true
		}
	}
	if !foundSafety {
		t.Fatalf("safety backup missing after restore: %v", backups)
	}
}

func TestWriteFailureInjectionKeepsOriginalAndReportsPartial(t *testing.T) {
	volume := NewFaultyVolume(NewOSVolume(), Faults{
		FailAtomicWrite: func(path string) bool {
			return strings.HasSuffix(path, ".json") && strings.Contains(path, string(filepath.Join("profiles", "hosts")))
		},
	})
	service := newTestService(t, WithVolume(volume))
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))

	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID)
	if err == nil {
		t.Fatal("expected partial failure error")
	}
	var failed, skipped int
	var manifestResult *app.WriteResult
	for i := range receipt.Results {
		result := receipt.Results[i]
		switch result.Status {
		case app.WriteFailed:
			failed++
			if result.Issue == nil {
				t.Fatalf("failed result without issue: %+v", result)
			}
		case app.WriteSkipped:
			skipped++
			if result.Entity == "manifest" {
				manifestResult = &receipt.Results[i]
			}
		}
	}
	if failed == 0 || skipped == 0 || manifestResult == nil {
		t.Fatalf("receipt = %+v", receipt.Results)
	}
	// profile 原文件未被破坏（内容为 fixture 原样）。
	data, err := os.ReadFile(filepath.Join(workspace.Root, "profiles", "hosts", "11111111-1111-4111-8111-111111111111.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "11111111-1111-4111-8111-111111111111") {
		t.Fatalf("profile mutated: %s", data)
	}
}

func TestBackupFailureAbortsTargetWrite(t *testing.T) {
	volume := NewFaultyVolume(NewOSVolume(), Faults{
		FailCopy: func(dst, _ string) bool {
			// profile 先于 manifest 处理，其备份复制最先命中故障。
			return strings.Contains(dst, DirBackups)
		},
	})
	service := newTestService(t, WithVolume(volume))
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))

	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID)
	if err == nil {
		t.Fatal("expected backup failure error")
	}
	// 备份故障先作用于第一个被处理的实体（profile）；manifest 因
	// 依赖失败被跳过，副本保持 v1。
	var profileFailed bool
	var manifestSkipped bool
	for _, result := range receipt.Results {
		switch result.Entity {
		case "profile":
			if result.Status == app.WriteFailed && result.Issue != nil && result.Issue.Code == CodeBackupFailed {
				profileFailed = true
			}
		case "manifest":
			if result.Status == app.WriteSkipped {
				manifestSkipped = true
			}
		}
	}
	if !profileFailed || !manifestSkipped {
		t.Fatalf("receipt = %+v", receipt.Results)
	}
	// manifest 原文件未被覆盖。
	scan, err := service.store.Scan(workspace.Root)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if scan.Version != 1 {
		t.Fatalf("manifest overwritten despite backup failure: %+v", scan.Manifest)
	}
}

func TestReadbackMismatchReported(t *testing.T) {
	service := newTestService(t, WithVerifyFault(func(rel string) bool {
		return rel == FileManifest
	}))
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))
	plan, err := service.PreviewMigration(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := service.ApplyMigration(context.Background(), workspace.ID, plan.ID)
	if err == nil {
		t.Fatal("expected readback mismatch error")
	}
	for _, result := range receipt.Results {
		if result.Entity == "manifest" && (result.ReadbackVerified || result.Issue == nil || result.Issue.Code != CodeReadbackFailed) {
			t.Fatalf("manifest result = %+v", result)
		}
	}
}

func TestRefreshPicksUpExternalChanges(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v1-hosts"))
	if err := os.Remove(filepath.Join(workspace.Root, "profiles", "hosts", "11111111-1111-4111-8111-111111111111.json")); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Refresh(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(updated.Summary.Profiles) != 0 {
		t.Fatalf("profiles = %d", len(updated.Summary.Profiles))
	}
	if updated.Revision != workspace.Revision {
		t.Fatalf("refresh must not bump revision: %d vs %d", updated.Revision, workspace.Revision)
	}
}

func TestServiceSerializesConcurrentWrites(t *testing.T) {
	service := newTestService(t)
	workspace := inspectAndCopy(t, service, copyFixture(t, "v2-full"))

	var wg sync.WaitGroup
	revisions := make(chan uint64, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 并发下预览随时可能过期；过期后重新预览是契约内行为。
			for attempt := 0; attempt < 200; attempt++ {
				preview, err := service.PreviewValidationChange(context.Background(), workspace.ID)
				if err != nil {
					t.Errorf("preview: %v", err)
					return
				}
				receipt, err := service.SaveValidationChange(context.Background(), workspace.ID, preview.ID)
				if err == nil {
					revisions <- receipt.Revision
					return
				}
				var uiErr *app.UIError
				if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorStale {
					t.Errorf("save: %v", err)
					return
				}
			}
			t.Errorf("could not land a write after retries")
		}()
	}
	wg.Wait()
	close(revisions)
	seen := map[uint64]bool{}
	for revision := range revisions {
		if seen[revision] {
			t.Fatalf("duplicate revision %d", revision)
		}
		seen[revision] = true
	}
	if len(seen) != 16 {
		t.Fatalf("revisions = %d, want 16", len(seen))
	}
}

func inspectAndCopy(t *testing.T, service *Service, sourceRoot string) app.Workspace {
	t.Helper()
	source, err := service.Inspect(context.Background(), sourceRoot)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	workspace, err := service.CreateWorkspace(context.Background(), source.ID, filepath.Join(t.TempDir(), "work"))
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return workspace
}

func refreshWorkspace(t *testing.T, service *Service, id string) app.Workspace {
	t.Helper()
	workspace, err := service.Refresh(context.Background(), id)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return workspace
}
