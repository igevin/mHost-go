package hostssvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/elevate"
	"github.com/igevin/mHost-go/internal/hostsfile"
)

// #35：Apply 备份历史。备份在写入前创建，操作结果（applied/failed/
// canceled）在写入完成后补标；Restore 在写前生成安全备份，使恢复
// 本身可被撤销。

func TestApplyMarksBackupResultApplied(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	plan, err := env.service.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	backups, err := env.service.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1", len(backups))
	}
	if backups[0].Result != app.BackupResultApplied {
		t.Fatalf("result = %q, want %q", backups[0].Result, app.BackupResultApplied)
	}
	if !backups[0].Available {
		t.Fatal("fresh backup should be available")
	}
}

func TestApplyCanceledMarksBackupResultCanceled(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	writer := hostsfile.NewSystemWriter(env.hostsPath, env.backupDir, env.volume)
	writer.SetMover(failAlwaysMover{err: fmt.Errorf("%w: osascript exit status 1", elevate.ErrCanceled)})
	svc := New(env.root, env.hostsPath, env.backupDir, env.volume, WithWriter(writer))

	plan, err := svc.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := svc.Apply(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("expected canceled apply error")
	}

	backups, err := svc.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1", len(backups))
	}
	if backups[0].Result != app.BackupResultCanceled {
		t.Fatalf("result = %q, want %q", backups[0].Result, app.BackupResultCanceled)
	}
}

func TestApplyWriteFailureMarksBackupResultFailed(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	writer := hostsfile.NewSystemWriter(env.hostsPath, env.backupDir, env.volume)
	writer.SetMover(failAlwaysMover{err: errors.New("move failed (injected)")})
	svc := New(env.root, env.hostsPath, env.backupDir, env.volume, WithWriter(writer))

	plan, err := svc.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := svc.Apply(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("expected write failure")
	}

	backups, err := svc.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1", len(backups))
	}
	if backups[0].Result != app.BackupResultFailed {
		t.Fatalf("result = %q, want %q", backups[0].Result, app.BackupResultFailed)
	}
}

func TestListApplyBackupsMarksUnavailableWhenBackupFileMissing(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	plan, err := env.service.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	backups, err := env.service.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 || !backups[0].Available {
		t.Fatalf("backups = %+v", backups)
	}

	if err := os.Remove(filepath.Join(env.backupDir, "hosts-"+backups[0].ID+".bak")); err != nil {
		t.Fatalf("remove backup file: %v", err)
	}

	backups, err = env.service.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups after remove: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1 (index intact)", len(backups))
	}
	if backups[0].Available {
		t.Fatal("missing backup file should be reported unavailable")
	}
}

func TestRollbackCreatesSafetyBackup(t *testing.T) {
	profileA := cleanProfile()
	profileB := conflictProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profileA, profileB)

	planA, _ := env.service.PreviewApply(context.Background(), profileA.ID, nil)
	receiptA, err := env.service.Apply(context.Background(), planA.ID, nil)
	if err != nil {
		t.Fatalf("apply A: %v", err)
	}
	planB, _ := env.service.PreviewApply(context.Background(), profileB.ID, nil)
	receiptB, err := env.service.Apply(context.Background(), planB.ID, map[string]string{"api.demo.test": "192.168.1.1"})
	if err != nil {
		t.Fatalf("apply B: %v", err)
	}

	// 恢复到 A 应用之前的状态：写前应生成安全备份（内容 = 当前 B 块）。
	receipt, err := env.service.Rollback(context.Background(), receiptA.RollbackRef.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if receipt.RollbackRef == nil {
		t.Fatal("restore receipt must offer the safety backup as undo")
	}

	backups, err := env.service.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("backups = %d, want 3 (A, B, safety)", len(backups))
	}
	safety := backups[0]
	if safety.ID == receiptA.RollbackRef.ID || safety.ID == receiptB.RollbackRef.ID {
		t.Fatalf("newest backup should be the safety backup, got %s", safety.ID)
	}
	if safety.ID != receipt.RollbackRef.ID {
		t.Fatalf("receipt undo ref = %s, want safety backup %s", receipt.RollbackRef.ID, safety.ID)
	}
	if safety.ProfileID != "" || safety.ProfileName != "" {
		t.Fatalf("safety backup profile = %q/%q, want empty (restored to no profile)", safety.ProfileID, safety.ProfileName)
	}
	if !strings.Contains(safety.BlockContent, "# ---- mHost start ----") ||
		!strings.Contains(safety.BlockContent, "192.168.1.1 api.demo.test") {
		t.Fatalf("safety backup should hold the pre-restore block, got %q", safety.BlockContent)
	}
	if safety.Result != app.BackupResultApplied {
		t.Fatalf("safety backup result = %q, want %q", safety.Result, app.BackupResultApplied)
	}

	// 恢复后 Active 归零、系统 hosts 无管理块
	state, err := env.service.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("applied state: %v", err)
	}
	if state.ProfileID != "" || state.Drifted {
		t.Fatalf("state after restore = %+v, want empty", state)
	}

	// 安全备份可撤销这次恢复：系统回到 B 的状态
	undo, err := env.service.Rollback(context.Background(), receipt.RollbackRef.ID)
	if err != nil {
		t.Fatalf("undo restore: %v", err)
	}
	if undo.RollbackRef == nil {
		t.Fatal("undo restore should itself offer a safety backup")
	}
	state, err = env.service.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("applied state after undo: %v", err)
	}
	if state.ProfileID != profileB.ID || state.Drifted {
		t.Fatalf("state after undo = %+v, want B", state)
	}
	content := readHosts(t, env)
	if !strings.Contains(content, "192.168.1.1 api.demo.test") {
		t.Fatalf("hosts after undo = %q", content)
	}
}

func TestRollbackCanceledMarksSafetyBackupCanceled(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	plan, err := env.service.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := env.service.Apply(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	env.service.writer.SetMover(failAlwaysMover{err: fmt.Errorf("%w: osascript exit status 1", elevate.ErrCanceled)})
	if _, err := env.service.Rollback(context.Background(), receipt.RollbackRef.ID); err == nil {
		t.Fatal("expected canceled rollback error")
	}

	backups, err := env.service.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("backups = %d, want 2 (apply + safety)", len(backups))
	}
	if backups[0].Result != app.BackupResultCanceled {
		t.Fatalf("safety backup result = %q, want %q", backups[0].Result, app.BackupResultCanceled)
	}
	if got := readHosts(t, env); !strings.Contains(got, "api.dev.local") {
		t.Fatalf("hosts changed after canceled restore: %q", got)
	}
}

// TestListApplyBackupsVisibleAfterRestart 验证 #35 验收项"重启应用后仍
// 可看到并恢复历史 Apply 备份"：备份索引与内容都在磁盘上，新服务
// 实例（模拟重启进程）直接可见、可恢复。
func TestListApplyBackupsVisibleAfterRestart(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	plan, err := env.service.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 新服务实例指向同一工作区/系统 hosts/备份目录（进程重启语义）
	restarted := New(env.root, env.hostsPath, env.backupDir, env.volume)
	backups, err := restarted.ListApplyBackups(context.Background())
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(backups) != 1 || backups[0].Result != app.BackupResultApplied || !backups[0].Available {
		t.Fatalf("backups after restart = %+v", backups)
	}

	if _, err := restarted.Rollback(context.Background(), backups[0].ID); err != nil {
		t.Fatalf("restore after restart: %v", err)
	}
	if got := readHosts(t, env); strings.Contains(got, "mHost start") {
		t.Fatalf("hosts still managed after restore: %q", got)
	}
	state, err := restarted.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("applied state: %v", err)
	}
	if state.ProfileID != "" || state.Drifted {
		t.Fatalf("state after restart-restore = %+v", state)
	}
}
