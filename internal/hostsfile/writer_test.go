package hostsfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igevin/mHost-go/internal/storage"
)

type failMover struct{ err error }

func (m failMover) Move(_, _ string) error { return m.err }

// corruptFirstMoveMover 第一次 Move 先把临时文件正常落位、再把目标改写成
// 坏内容（模拟"写入未按预期生效"，触发读回验证失败）；后续 Move 正常。
// 回滚也走同一 Mover，因此坏写入只注入一次，自动回滚才能成功——
// 这与源项目一致（apply 与 rollback 共用 elevated_move）。
type corruptFirstMoveMover struct {
	mu    sync.Mutex
	calls int
}

func (m *corruptFirstMoveMover) Move(tempPath, target string) error {
	m.mu.Lock()
	m.calls++
	first := m.calls == 1
	m.mu.Unlock()
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	if first {
		return os.WriteFile(target, []byte("# ---- mHost start ----\n# ---- mHost end ----\n"), 0o644)
	}
	return nil
}

// corruptThenFailMover 第一次 Move 坏写入，第二次（回滚）失败。
type corruptThenFailMover struct {
	mu    sync.Mutex
	calls int
}

func (m *corruptThenFailMover) Move(tempPath, target string) error {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n > 1 {
		return errors.New("move failed (injected)")
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	return os.WriteFile(target, []byte("garbage\n"), 0o644)
}

func testWriter(t *testing.T, hostsPath, backupDir string, vol Volume, opts ...WriterOption) *SystemWriter {
	t.Helper()
	if vol == nil {
		vol = storage.NewOSVolume()
	}
	return NewSystemWriter(hostsPath, backupDir, vol, opts...)
}

func writeHostsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
}

func TestApplyReplacesBlockPreservingUnmanaged(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	backupDir := filepath.Join(dir, "backups")
	current := "127.0.0.1 pre-existing.com\n# ---- mHost start ----\n1.1.1.1 old.com\n# ---- mHost end ----\n192.168.1.1 post-existing.com\n"
	writeHostsFile(t, hostsPath, current)

	w := testWriter(t, hostsPath, backupDir, nil)
	rules := []ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}
	backup, prev, err := w.Apply(rules, true, BackupRecord{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if backup == nil {
		t.Fatal("expected backup record")
	}
	if string(prev) != current {
		t.Fatalf("prev content mismatch")
	}

	got, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := "127.0.0.1 pre-existing.com\n" + FormatManagedBlock([]Rule{{IP: addrPtr(t, "2.2.2.2"), Domains: []string{"new.com"}, Enabled: true}}) + "192.168.1.1 post-existing.com\n"
	if string(got) != want {
		t.Fatalf("hosts content:\n%q\nwant:\n%q", got, want)
	}
	if backup.ContentSHA256 == "" {
		t.Fatal("backup record missing content hash")
	}

	// 备份文件权限 0600（源项目 H2 安全修复）
	info, err := os.Stat(filepath.Join(backupDir, backup.File))
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup perm = %o, want 600", info.Mode().Perm())
	}
	backupContent, err := os.ReadFile(filepath.Join(backupDir, backup.File))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backupContent) != current {
		t.Fatalf("backup content = %q, want %q", backupContent, current)
	}
}

func TestApplyAppendsBlockWhenMissing(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me")

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	_, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := os.ReadFile(hostsPath)
	if !strings.Contains(string(got), "127.0.0.1 keep.me\n# ---- mHost start ----") {
		t.Fatalf("block not appended: %q", got)
	}
}

func TestApplyEmptyRulesRemovesBlock(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me\n# ---- mHost start ----\n1.1.1.1 old.com\n# ---- mHost end ----\n")

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	_, _, err := w.Apply(nil, true, BackupRecord{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := os.ReadFile(hostsPath)
	if string(got) != "127.0.0.1 keep.me\n" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyPreservesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me\n")
	if err := os.Chmod(hostsPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	if _, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	info, err := os.Stat(hostsPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("perm = %o, want 644", info.Mode().Perm())
	}
}

func TestApplyWriteFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	current := "127.0.0.1 keep.me\n"
	writeHostsFile(t, hostsPath, current)

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	w.SetMover(failMover{err: errors.New("disk full (injected)")})
	backup, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if backup == nil {
		t.Fatal("backup should exist before write")
	}
	got, _ := os.ReadFile(hostsPath)
	if string(got) != current {
		t.Fatalf("original content changed: %q", got)
	}
}

func TestApplyVerifyFailureAutoRollsBack(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	current := "127.0.0.1 keep.me\n"
	writeHostsFile(t, hostsPath, current)

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	w.SetMover(&corruptFirstMoveMover{})
	backup, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{})
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if !strings.Contains(err.Error(), "verify") {
		t.Fatalf("error should mention verify: %v", err)
	}
	if backup == nil {
		t.Fatal("backup record expected for rollback")
	}
	got, _ := os.ReadFile(hostsPath)
	if string(got) != current {
		t.Fatalf("auto rollback failed, content = %q", got)
	}
}

func TestApplyVerifyFailureRollbackAlsoFails(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me\n")

	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	w.SetMover(&corruptThenFailMover{}) // 第一次坏写入成功，回滚 Move 失败
	_, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{})
	if err == nil || !strings.Contains(err.Error(), "rollback also failed") {
		t.Fatalf("expected combined error, got %v", err)
	}
}

func TestRollbackToRestoresSpecificBackup(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	backupDir := filepath.Join(dir, "backups")
	first := "127.0.0.1 first.com\n"
	writeHostsFile(t, hostsPath, first)

	w := testWriter(t, hostsPath, backupDir, nil)
	backup1, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "second.com")}, true, BackupRecord{})
	if err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	backup2, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "3.3.3.3", "third.com")}, true, BackupRecord{})
	if err != nil {
		t.Fatalf("apply 2: %v", err)
	}

	// 回滚到第一份备份（first 内容）
	record, err := w.RollbackTo(backup1.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if record.ID != backup1.ID {
		t.Fatalf("record = %s, want %s", record.ID, backup1.ID)
	}
	got, _ := os.ReadFile(hostsPath)
	if string(got) != first {
		t.Fatalf("content = %q, want %q", got, first)
	}

	// 再回滚到第二份备份
	if _, err := w.RollbackTo(backup2.ID); err != nil {
		t.Fatalf("rollback 2: %v", err)
	}
	got, _ = os.ReadFile(hostsPath)
	if !strings.Contains(string(got), "2.2.2.2 second.com") {
		t.Fatalf("content = %q", got)
	}
}

func TestRollbackToUnknownID(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me\n")
	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil)
	if _, err := w.RollbackTo("nope"); err == nil {
		t.Fatal("expected error for unknown backup")
	}
}

func TestRefusesSymlinkHostsPath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-hosts")
	writeHostsFile(t, real, "127.0.0.1 keep.me\n")
	link := filepath.Join(dir, "hosts")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	w := testWriter(t, link, filepath.Join(dir, "backups"), nil)
	if _, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
	if _, err := w.RollbackTo("x"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink refusal on rollback, got %v", err)
	}
	got, _ := os.ReadFile(real)
	if string(got) != "127.0.0.1 keep.me\n" {
		t.Fatalf("target of symlink was modified: %q", got)
	}
}

func TestMissingHostsFile(t *testing.T) {
	dir := t.TempDir()
	w := testWriter(t, filepath.Join(dir, "hosts"), filepath.Join(dir, "backups"), nil)
	if _, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{}); err == nil {
		t.Fatal("expected error for missing hosts file")
	}
}

func TestListBackupsNewestFirstAndPrune(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	writeHostsFile(t, hostsPath, "127.0.0.1 keep.me\n")

	var tick time.Time
	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), nil,
		WithWriterClock(func() time.Time { tick = tick.Add(time.Second); return tick }),
		WithWriterIDGenerator(newSequentialID()),
		WithWriterMaxBackups(3),
	)

	for i := 0; i < 5; i++ {
		if _, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{}); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	backups, err := w.ListBackups()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("backups = %d, want 3 (pruned)", len(backups))
	}
	for i := 1; i < len(backups); i++ {
		if !backups[i-1].CreatedAt.After(backups[i].CreatedAt) {
			t.Fatalf("backups not sorted newest first: %+v", backups)
		}
	}
	// 被裁剪的备份文件已从磁盘删除
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	bakCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			bakCount++
		}
	}
	if bakCount != 3 {
		t.Fatalf("backup files on disk = %d, want 3", bakCount)
	}
}

func newSequentialID() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return filepath.Base(strings.Repeat("0", 0) + itoa(n))
	}
}

func itoa(n int) string {
	digits := "0123456789"
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return string(out)
}

func resolvedTest(t *testing.T, ip, domain string) ResolvedRule {
	t.Helper()
	return ResolvedRule{IP: *addrPtr(t, ip), Domain: domain}
}

// FaultyVolume 注入：备份写入失败时 apply 必须中止且 hosts 不变。
func TestApplyBackupFailureAbortsBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	current := "127.0.0.1 keep.me\n"
	writeHostsFile(t, hostsPath, current)

	base := storage.NewOSVolume()
	vol := storage.NewFaultyVolume(base, storage.Faults{
		FailAtomicWrite: func(path string) bool {
			return filepath.Dir(path) == filepath.Join(dir, "backups")
		},
	})
	w := testWriter(t, hostsPath, filepath.Join(dir, "backups"), vol)
	_, _, err := w.Apply([]ResolvedRule{resolvedTest(t, "2.2.2.2", "new.com")}, true, BackupRecord{})
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("expected backup failure, got %v", err)
	}
	got, _ := os.ReadFile(hostsPath)
	if string(got) != current {
		t.Fatalf("hosts changed despite backup failure: %q", got)
	}
}
