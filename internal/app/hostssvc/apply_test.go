package hostssvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"os"
	"strings"
	"sync"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/elevate"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

const cleanContent = "127.0.0.1 localhost # loopback\n192.168.1.10 api.dev.local\n"

func cleanProfile() storage.Profile {
	return testProfile("11111111-1111-4111-8111-111111111111", "开发环境", cleanContent)
}

func conflictProfile() storage.Profile {
	return testProfile("22222222-2222-4222-8222-222222222222", "冲突样本",
		"10.0.0.1 api.demo.test\n192.168.1.1 api.demo.test\n10.0.0.1 shared.demo.test\n10.0.0.1 shared.demo.test\n")
}

func readHosts(t *testing.T, env *testEnv) string {
	t.Helper()
	data, err := os.ReadFile(env.hostsPath)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	return string(data)
}

func TestPreviewApplyCleanProfile(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	plan, err := env.service.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if plan.ID == "" || plan.ProfileID != cleanProfile().ID || !plan.BackupRequired {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Conflicts) != 0 || len(plan.Blockers) != 0 {
		t.Fatalf("plan issues: conflicts=%v blockers=%v", plan.Conflicts, plan.Blockers)
	}
	// 合并输出按 (domain, ip) 排序（源项目 Merger 语义）
	if !strings.Contains(plan.MergedBlock, "# ---- mHost start ----\n192.168.1.10 api.dev.local\n127.0.0.1 localhost\n# ---- mHost end ----\n") {
		t.Fatalf("merged block = %q", plan.MergedBlock)
	}
	if plan.CurrentBlock != "" {
		t.Fatalf("current block = %q, want empty", plan.CurrentBlock)
	}
}

func TestPreviewApplyConflictsAndResolution(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", conflictProfile())

	plan, err := env.service.PreviewApply(context.Background(), conflictProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("conflicts = %v", plan.Conflicts)
	}
	conflict := plan.Conflicts[0]
	if conflict.Domain != "api.demo.test" {
		t.Fatalf("conflict domain = %q", conflict.Domain)
	}
	if len(conflict.IPs) != 2 || conflict.IPs[0] != "10.0.0.1" || conflict.IPs[1] != "192.168.1.1" {
		t.Fatalf("ips = %v", conflict.IPs)
	}
	if len(conflict.Lines) != 2 || conflict.Lines[0] != 1 || conflict.Lines[1] != 2 {
		t.Fatalf("lines = %v", conflict.Lines)
	}

	// 默认（未解决）保留首个出现的 IP
	if !strings.Contains(plan.MergedBlock, "10.0.0.1 api.demo.test") ||
		strings.Contains(plan.MergedBlock, "192.168.1.1 api.demo.test") {
		t.Fatalf("default resolution wrong: %q", plan.MergedBlock)
	}
	// 同 IP 重复折叠
	if strings.Count(plan.MergedBlock, "10.0.0.1 shared.demo.test") != 1 {
		t.Fatalf("same-ip duplicate not collapsed: %q", plan.MergedBlock)
	}

	// 带保留选择的预览渲染选择结果
	plan2, err := env.service.PreviewApply(context.Background(), conflictProfile().ID, map[string]string{"api.demo.test": "192.168.1.1"})
	if err != nil {
		t.Fatalf("preview 2: %v", err)
	}
	if !strings.Contains(plan2.MergedBlock, "192.168.1.1 api.demo.test") {
		t.Fatalf("resolution not applied: %q", plan2.MergedBlock)
	}
}

func TestApplyCleanProfileEndToEnd(t *testing.T) {
	profile := cleanProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n# existing tail\n", profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := env.service.Apply(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if receipt.OperationID == "" || receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", receipt)
	}
	var backupDone, blockDone, verifyDone bool
	for _, result := range receipt.Results {
		switch result.Entity {
		case "backup":
			backupDone = result.Status == app.WriteWritten && result.Backup != nil
		case "managed_block":
			blockDone = result.Status == app.WriteWritten
		case "verification":
			verifyDone = result.Status == app.WriteWritten && result.ReadbackVerified
		}
	}
	if !backupDone || !blockDone || !verifyDone {
		t.Fatalf("receipt results incomplete: %+v", receipt.Results)
	}

	// 块外内容保留；块内按 (domain, ip) 排序
	content := readHosts(t, env)
	if !strings.HasPrefix(content, "127.0.0.1 keep.me\n# existing tail\n# ---- mHost start ----\n") {
		t.Fatalf("hosts = %q", content)
	}
	if !strings.Contains(content, "192.168.1.10 api.dev.local\n127.0.0.1 localhost\n") {
		t.Fatalf("managed rules missing: %q", content)
	}

	// applied-state 指向本 profile 且不 drift
	state, err := env.service.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("applied state: %v", err)
	}
	if state.ProfileID != profile.ID || state.Drifted {
		t.Fatalf("state = %+v", state)
	}

	// 备份可列出且带旧块内容（写入前无块 → 空串）
	backups, err := env.service.ListApplyBackups(context.Background())
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v err=%v", backups, err)
	}
	if backups[0].BlockContent != "" {
		t.Fatalf("prev block = %q, want empty", backups[0].BlockContent)
	}
}

func TestApplyConflictsRequireResolution(t *testing.T) {
	profile := conflictProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// 未解决 → ErrorValidation
	_, err = env.service.Apply(context.Background(), plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("expected validation error, got %v", err)
	}

	// 计划仍可消费（校验失败不消耗计划）
	_, err = env.service.Apply(context.Background(), plan.ID, map[string]string{"api.demo.test": "not-an-ip"})
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("expected validation error for bogus ip, got %v", err)
	}

	receipt, err := env.service.Apply(context.Background(), plan.ID, map[string]string{"api.demo.test": "192.168.1.1"})
	if err != nil {
		t.Fatalf("apply with resolution: %v", err)
	}
	if receipt.RollbackRef == nil {
		t.Fatal("rollback ref expected")
	}
	content := readHosts(t, env)
	if !strings.Contains(content, "192.168.1.1 api.demo.test") ||
		strings.Contains(content, "10.0.0.1 api.demo.test") {
		t.Fatalf("resolved block wrong: %q", content)
	}
}

func TestApplyPlanExpiry(t *testing.T) {
	profile := cleanProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// 保存使修订号变化 → 计划过期
	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := env.service.SaveProfile(context.Background(), profile.ID, detail.Content, detail.Revision); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := env.service.Apply(context.Background(), plan.ID, nil); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("expected stale plan, got %v", err)
	}
	if _, err := env.service.Apply(context.Background(), "no-such-plan", nil); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("expected stale for unknown plan, got %v", err)
	}
}

func TestApplyWriteFailureKeepsPreviousBlock(t *testing.T) {
	profile := cleanProfile()
	original := "127.0.0.1 keep.me\n"
	env := newTestEnv(t, original, profile)
	env.service.writer.SetMover(&failOnceMover{})

	plan, _ := env.service.PreviewApply(context.Background(), profile.ID, nil)
	receipt, err := env.service.Apply(context.Background(), plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorWrite {
		t.Fatalf("expected write error, got %v", err)
	}
	if readHosts(t, env) != original {
		t.Fatal("original hosts changed on write failure")
	}
	// 回执：backup written / managed_block failed / verification skipped
	var statuses = map[string]app.WriteStatus{}
	for _, r := range receipt.Results {
		statuses[r.Entity] = r.Status
	}
	if statuses["backup"] != app.WriteWritten || statuses["managed_block"] != app.WriteFailed || statuses["verification"] != app.WriteSkipped {
		t.Fatalf("results = %+v", receipt.Results)
	}
	// 失败后可 Rollback（回滚的是失败前的备份 = 原内容）
	if _, err := env.service.Rollback(context.Background(), receipt.RollbackRef.ID); err != nil {
		t.Fatalf("rollback after failed apply: %v", err)
	}
	if readHosts(t, env) != original {
		t.Fatal("rollback after failed apply changed content")
	}
}

type failOnceMover struct{ called bool }

func (m *failOnceMover) Move(tempPath, target string) error {
	if m.called {
		return os.Rename(tempPath, target)
	}
	m.called = true
	return errors.New("move failed (injected)")
}

func TestApplyVerifyFailureAutoRollsBack(t *testing.T) {
	profile := cleanProfile()
	original := "127.0.0.1 keep.me\n"
	env := newTestEnv(t, original, profile)
	env.service.writer.SetMover(&corruptFirstMoveMover{})

	plan, _ := env.service.PreviewApply(context.Background(), profile.ID, nil)
	_, err := env.service.Apply(context.Background(), plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorReadback {
		t.Fatalf("expected readback error, got %v", err)
	}
	if readHosts(t, env) != original {
		t.Fatalf("auto rollback failed: %q", readHosts(t, env))
	}
}

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

func TestRollbackRestoresPreviousProfile(t *testing.T) {
	profileA := cleanProfile()
	profileB := conflictProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profileA, profileB)

	// 先应用 A
	planA, _ := env.service.PreviewApply(context.Background(), profileA.ID, nil)
	receiptA, err := env.service.Apply(context.Background(), planA.ID, nil)
	if err != nil {
		t.Fatalf("apply A: %v", err)
	}

	// 再应用 B（带冲突解决）
	planB, _ := env.service.PreviewApply(context.Background(), profileB.ID, nil)
	if _, err := env.service.Apply(context.Background(), planB.ID, map[string]string{"api.demo.test": "192.168.1.1"}); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	state, _ := env.service.AppliedState(context.Background())
	if state.ProfileID != profileB.ID || state.Drifted {
		t.Fatalf("state after B = %+v", state)
	}

	// 回滚到 A 的备份
	receipt, err := env.service.Rollback(context.Background(), receiptA.RollbackRef.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var verifyVerified bool
	for _, r := range receipt.Results {
		if r.Entity == "verification" {
			verifyVerified = r.ReadbackVerified && r.Status == app.WriteWritten
		}
	}
	if !verifyVerified {
		t.Fatalf("rollback receipt = %+v", receipt.Results)
	}

	// 回滚 A 的备份 = 恢复到 A 应用之前（无管理块）
	content := readHosts(t, env)
	if strings.Contains(content, "mHost start") || strings.Contains(content, "api.dev.local") {
		t.Fatalf("rollback content wrong: %q", content)
	}
	state, _ = env.service.AppliedState(context.Background())
	if state.ProfileID != "" || state.Drifted {
		t.Fatalf("state after rollback = %+v", state)
	}
}

func TestAppliedStateDriftDetection(t *testing.T) {
	profile := cleanProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	// 无块无状态 → 未应用
	state, err := env.service.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Drifted || state.ProfileID != "" {
		t.Fatalf("state = %+v", state)
	}

	// 外部写入块（无状态记录）→ drift
	external := "127.0.0.1 keep.me\n# ---- mHost start ----\n6.6.6.6 external.com\n# ---- mHost end ----\n"
	if err := os.WriteFile(env.hostsPath, []byte(external), 0o644); err != nil {
		t.Fatalf("write external: %v", err)
	}
	state, _ = env.service.AppliedState(context.Background())
	if !state.Drifted {
		t.Fatalf("external block should be drift: %+v", state)
	}

	// 应用后外部改块 → drift
	plan, _ := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tampered := strings.Replace(readHosts(t, env), "api.dev.local", "api.dev.local.evil", 1)
	if err := os.WriteFile(env.hostsPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	state, _ = env.service.AppliedState(context.Background())
	if !state.Drifted || state.ProfileID != profile.ID {
		t.Fatalf("tampered state = %+v", state)
	}

	// 块被整体删除 → drift
	if err := os.WriteFile(env.hostsPath, []byte("127.0.0.1 keep.me\n"), 0o644); err != nil {
		t.Fatalf("remove block: %v", err)
	}
	state, _ = env.service.AppliedState(context.Background())
	if !state.Drifted {
		t.Fatalf("removed block should be drift: %+v", state)
	}
}

func TestConcurrentAppliesSerialize(t *testing.T) {
	profile := cleanProfile()
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
			if err != nil {
				errs[idx] = err
				return
			}
			// 部分 worker 的 plan 会因修订号变化过期——两种结果都合法，
			// 但系统 hosts 必须始终保持一致（要么没写，要么写了完整块）。
			_, errs[idx] = env.service.Apply(context.Background(), plan.ID, nil)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, app.ErrStaleOperation) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// 恰好一个写入成功（修订号检查保证），其余过期
	content := readHosts(t, env)
	if strings.Count(content, "# ---- mHost start ----") != 1 {
		t.Fatalf("crossed writes: %q", content)
	}
	if !strings.Contains(content, "192.168.1.10 api.dev.local") {
		t.Fatalf("final block wrong: %q", content)
	}
}

func TestApplyRefusesSymlinkHosts(t *testing.T) {
	profile := cleanProfile()
	dir := t.TempDir()
	real := filepath.Join(dir, "real-hosts")
	if err := os.WriteFile(real, []byte("127.0.0.1 keep.me\n"), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}
	link := filepath.Join(dir, "hosts")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)
	env.service.writer = hostsfile.NewSystemWriter(link, env.backupDir, env.volume)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("expected symlink refusal")
	}
	got, _ := os.ReadFile(real)
	if string(got) != "127.0.0.1 keep.me\n" {
		t.Fatalf("symlink target modified: %q", got)
	}
}

func TestPreviewCurrentBlockReflectsExisting(t *testing.T) {
	profile := cleanProfile()
	existing := "127.0.0.1 keep.me\n# ---- mHost start ----\n9.9.9.9 legacy.com\n# ---- mHost end ----\n"
	env := newTestEnv(t, existing, profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if plan.CurrentBlock != "# ---- mHost start ----\n9.9.9.9 legacy.com\n# ---- mHost end ----\n" {
		t.Fatalf("current block = %q", plan.CurrentBlock)
	}
}

// failAlwaysMover 每次 Move 都返回固定错误（提权取消/失败场景）。
type failAlwaysMover struct{ err error }

func (m failAlwaysMover) Move(_, _ string) error { return m.err }

// TestApplyCanceledAuthorization 验证管理员授权被取消时的分类：
// 不是可重试的 ErrorWrite，而是 ErrorPermission + "未改动"语义，
// 回执里 managed_block 为 WriteSkipped（备份照旧生成，与源项目一致）。
func TestApplyCanceledAuthorization(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n", cleanProfile())

	writer := hostsfile.NewSystemWriter(env.hostsPath, env.backupDir, env.volume)
	writer.SetMover(failAlwaysMover{err: fmt.Errorf("%w: osascript exit status 1", elevate.ErrCanceled)})
	svc := New(env.root, env.hostsPath, env.backupDir, env.volume, WithWriter(writer))

	plan, err := svc.PreviewApply(context.Background(), cleanProfile().ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := svc.Apply(context.Background(), plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) {
		t.Fatalf("Apply() = %v, want UIError", err)
	}
	if uiErr.Kind != app.ErrorPermission {
		t.Fatalf("kind = %v, want ErrorPermission", uiErr.Kind)
	}
	if uiErr.Retryable {
		t.Fatal("canceled authorization must not be retryable")
	}
	var managed *app.ApplyResult
	for i := range receipt.Results {
		if receipt.Results[i].Entity == "managed_block" {
			managed = &receipt.Results[i]
		}
	}
	if managed == nil || managed.Status != app.WriteSkipped {
		t.Fatalf("managed_block result = %+v, want WriteSkipped", managed)
	}
	if got := readHosts(t, env); got != "127.0.0.1 keep.me\n" {
		t.Fatalf("hosts changed after cancel: %q", got)
	}
}
