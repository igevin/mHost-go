package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore() *Store {
	return NewStore(NewOSVolume())
}

func TestScanV2FullFixture(t *testing.T) {
	result, err := newTestStore().Scan(fixturePath(t, "v2-full"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Version != 2 {
		t.Fatalf("version = %d", result.Version)
	}
	if result.Manifest == nil || result.Manifest.AppVersion != "0.4.2" {
		t.Fatalf("manifest: %+v", result.Manifest)
	}
	if len(result.Profiles) != 2 {
		t.Fatalf("profiles = %d", len(result.Profiles))
	}
	if len(result.Snapshots) != 1 {
		t.Fatalf("snapshots = %d", len(result.Snapshots))
	}
	if result.AdBlock == nil || len(result.AdBlock.Sources) != 1 {
		t.Fatalf("adblock: %+v", result.AdBlock)
	}
	if result.Settings == nil || result.Settings.QuickApplyOnToggle != true {
		t.Fatalf("settings: %+v", result.Settings)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("issues = %v", result.Issues)
	}
}

func TestScanV1HostsFixture(t *testing.T) {
	result, err := newTestStore().Scan(fixturePath(t, "v1-hosts"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Version != 1 {
		t.Fatalf("version = %d", result.Version)
	}
	if result.Manifest.DNSEnabled != nil || result.Manifest.OriginalDNS != nil {
		t.Fatalf("v1 manifest invented v2 fields: %+v", result.Manifest)
	}
	if len(result.Profiles) != 1 {
		t.Fatalf("profiles = %d", len(result.Profiles))
	}
	if result.Settings != nil {
		t.Fatalf("v1 has no native settings: %+v", result.Settings)
	}
}

func TestScanMixedInvalidReportsPartialIssues(t *testing.T) {
	result, err := newTestStore().Scan(fixturePath(t, "mixed-invalid"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Profiles) != 1 {
		t.Fatalf("valid profiles = %d", len(result.Profiles))
	}
	if len(result.Issues) != 3 {
		t.Fatalf("issues = %v", result.Issues)
	}
	// 问题按相对路径稳定排序。
	paths := []string{result.Issues[0].Path, result.Issues[1].Path, result.Issues[2].Path}
	want := []string{
		filepath.Join("profiles", "dns", "wrong-type.json"),
		filepath.Join("profiles", "hosts", "broken.json"),
		filepath.Join("profiles", "hosts", "missing-fields.json"),
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("issue paths = %v, want %v", paths, want)
		}
	}
}

func TestScanMissingManifestIsFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles", "hosts"), DirPerm); err != nil {
		t.Fatal(err)
	}
	_, err := newTestStore().Scan(dir)
	if err == nil {
		t.Fatal("expected fatal error for missing manifest")
	}
}

func TestScanUnsupportedVersionIsFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":3,"app_version":"9","updated_at":"2024-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newTestStore().Scan(dir)
	if err == nil {
		t.Fatal("expected fatal error for unsupported version")
	}
}

func TestScanMissingRoot(t *testing.T) {
	_, err := newTestStore().Scan(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestCopyTreeCreatesOwnerOnlyWorkingCopy(t *testing.T) {
	store := newTestStore()
	src := fixturePath(t, "v2-full")
	dst := filepath.Join(t.TempDir(), "work")

	if err := store.CopyTree(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}

	compare, err := store.Scan(dst)
	if err != nil {
		t.Fatalf("scan copy: %v", err)
	}
	if len(compare.Profiles) != 2 || len(compare.Snapshots) != 1 || compare.Settings == nil {
		t.Fatalf("copy incomplete: %+v", compare)
	}
	info, err := os.Stat(filepath.Join(dst, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != FilePerm {
		t.Fatalf("copied file perm = %o, want %o", info.Mode().Perm(), FilePerm)
	}
}

func TestCopyTreeRejectsNonEmptyDestination(t *testing.T) {
	store := newTestStore()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyTree(fixturePath(t, "v1-hosts"), dst); err == nil {
		t.Fatal("expected rejection of non-empty destination")
	}
}

func TestValidateDestinationBoundaries(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.MkdirAll(source, DirPerm); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		dest string
		ok   bool
	}{
		{"same", source, false},
		{"child of source", filepath.Join(source, "work"), false},
		{"parent of source", base, false},
		{"sibling", filepath.Join(base, "work"), true},
		{"empty", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateDestination(source, tc.dest)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected boundary rejection")
			}
		})
	}
}

func TestRecordBackupAndList(t *testing.T) {
	store := newTestStore()
	root := t.TempDir()
	profilePath := filepath.Join(root, "profiles", "hosts")
	if err := os.MkdirAll(profilePath, DirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "p.json"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	ref, err := store.RecordBackup(root, filepath.Join("profiles", "hosts", "p.json"), "profile", "op-1", now, "11111111-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatalf("record backup: %v", err)
	}
	if ref.ID != "11111111-2222-4222-8222-222222222222" || ref.OperationID != "op-1" {
		t.Fatalf("unexpected ref: %+v", ref)
	}

	content, err := store.BackupContent(root, "11111111-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatalf("backup content: %v", err)
	}
	if string(content) != "original" {
		t.Fatalf("backup content = %q", content)
	}

	backups, err := store.Backups(root)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 || backups[0].Path != filepath.Join("profiles", "hosts", "p.json") {
		t.Fatalf("backups = %+v", backups)
	}

	// 登记信息必须原子落盘且 owner-only。
	info, err := os.Stat(filepath.Join(root, FileBackupIndex))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != FilePerm {
		t.Fatalf("backup index perm = %o", info.Mode().Perm())
	}
}

func TestRecordBackupFailsForMissingSource(t *testing.T) {
	store := newTestStore()
	_, err := store.RecordBackup(t.TempDir(), "missing.json", "profile", "op", time.Now(), "11111111-2222-4222-8222-222222222299")
	if err == nil {
		t.Fatal("expected error for missing backup source")
	}
}

func TestScanReportsOrphanSnapshotFiles(t *testing.T) {
	root := t.TempDir()
	snapshotDir := filepath.Join(root, DirSnapshots)
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	index := `{"version":2,"snapshots":[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"kept","profile_count":1,"created_at":"2026-11-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "index.json"), []byte(index), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	kept := `{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"kept","profile_count":1,"created_at":"2026-11-01T00:00:00Z","profiles":[]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.json"), []byte(kept), 0o600); err != nil {
		t.Fatalf("write kept: %v", err)
	}
	orphan := `{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"orphan","profile_count":0,"created_at":"2026-11-01T00:00:00Z","profiles":[]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.json"), []byte(orphan), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	manifest := `{"version":2,"app_version":"0.4.2","updated_at":"2026-11-01T00:00:00Z","dns_enabled":false,"original_dns":{"kind":"dhcp_empty"}}`
	if err := os.WriteFile(filepath.Join(root, FileManifest), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	result, err := newTestStore().Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1 (orphan not registered)", len(result.Snapshots))
	}
	var orphanIssues []ScanIssue
	for _, issue := range result.Issues {
		if issue.Code == "orphan_snapshot" {
			orphanIssues = append(orphanIssues, issue)
		}
	}
	if len(orphanIssues) != 1 {
		t.Fatalf("orphan issues = %v, all issues = %v", orphanIssues, result.Issues)
	}
	if orphanIssues[0].Severity != "warning" || !orphanIssues[0].Recoverable {
		t.Fatalf("issue = %+v", orphanIssues[0])
	}
}

func TestScanReportsOrphanSnapshotsWithoutIndex(t *testing.T) {
	root := t.TempDir()
	snapshotDir := filepath.Join(root, DirSnapshots)
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 无 index.json：典型孤儿场景（登记/写出失败）
	orphan := `{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"orphan","profile_count":0,"created_at":"2026-11-01T00:00:00Z","profiles":[]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.json"), []byte(orphan), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	manifest := `{"version":2,"app_version":"0.4.2","updated_at":"2026-11-01T00:00:00Z","dns_enabled":false,"original_dns":{"kind":"dhcp_empty"}}`
	if err := os.WriteFile(filepath.Join(root, FileManifest), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	result, err := newTestStore().Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var orphanIssues []ScanIssue
	for _, issue := range result.Issues {
		if issue.Code == "orphan_snapshot" {
			orphanIssues = append(orphanIssues, issue)
		}
	}
	if len(orphanIssues) != 1 {
		t.Fatalf("orphan issues = %v, all = %v", orphanIssues, result.Issues)
	}
}
