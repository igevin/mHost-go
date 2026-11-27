package hostssvc

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/storage"
)

// 回归（review 必须修复项）：带 IP 的禁用规则必须全程保持禁用——
// 渲染为注释行、不进入合并块、保存后不会被改写成启用。
func TestDisabledRuleWithIPStaysDisabledEndToEnd(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env",
		"127.0.0.1 live.test\n")
	// 落盘一个带 IP 的禁用规则（模拟迁移后的 v1/v2 profile）
	profile.Rules = append(profile.Rules, storage.HostRule{
		ID:      "55555555-5555-4555-8555-555555555555",
		IP:      ipPtr(t, "0.0.0.0"),
		Domains: []string{"telemetry.legacy.test"},
		Enabled: false,
		Source:  storage.RuleSource{Type: "Manual"},
	})
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 渲染为注释行
	if !strings.Contains(detail.Content, "# 0.0.0.0 telemetry.legacy.test") {
		t.Fatalf("disabled rule not rendered as comment: %q", detail.Content)
	}

	// 预览：禁用规则不进入合并块
	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if strings.Contains(plan.MergedBlock, "telemetry.legacy.test") {
		t.Fatalf("disabled rule entered merged block: %q", plan.MergedBlock)
	}

	// 应用后系统 hosts 不含禁用规则
	receipt, err := env.service.Apply(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	_ = receipt
	raw, _ := os.ReadFile(env.hostsPath)
	content := string(raw)
	if strings.Contains(content, "0.0.0.0 telemetry.legacy.test") && !strings.Contains(content, "# 0.0.0.0 telemetry.legacy.test") {
		t.Fatalf("disabled rule written to system hosts: %q", content)
	}

	// 保存往返：重新解析的内容落盘后再加载，禁用语义仍不丢（以注释形式）
	saved, err := env.service.SaveProfile(context.Background(), profile.ID, detail.Content, detail.Revision)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(saved.Content, "# 0.0.0.0 telemetry.legacy.test") {
		t.Fatalf("disabled rule lost after save: %q", saved.Content)
	}
	reloaded, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	plan2, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview 2: %v", err)
	}
	if strings.Contains(plan2.MergedBlock, "telemetry.legacy.test") {
		t.Fatalf("disabled rule resurrected after save: %q", plan2.MergedBlock)
	}
	_ = reloaded
}

func ipPtr(t *testing.T, s string) *netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad ip %q: %v", s, err)
	}
	return &a
}

// 回归（review 建议项 2）：禁用的 profile 不能预览/应用。
func TestDisabledProfileCannotBePreviewedOrApplied(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env", "127.0.0.1 a.com\n")
	profile.Enabled = false
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	_, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("preview should refuse disabled profile: %v", err)
	}
	if !strings.Contains(uiErr.Message, "disabled") {
		t.Fatalf("reason should say disabled: %+v", uiErr)
	}
}

// 回归（review 建议项 3）：损坏的 profile 文件在列表中被跳过，
// 但 issue 对用户可见。
func TestListProfilesRecordsIssueForCorruptFile(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "good", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)
	dir := filepath.Join(env.root, "profiles", "hosts")
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	profiles, err := env.service.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v", profiles)
	}
	issues := env.service.StorageIssues()
	if len(issues) != 1 || issues[0].Code != "profile_unreadable" {
		t.Fatalf("issues = %+v", issues)
	}
	if !strings.Contains(issues[0].Path, "corrupt.json") {
		t.Fatalf("issue path = %q", issues[0].Path)
	}
}

// 回归（review 建议项 4 + 备份失败分类）：备份失败映射为 ErrorBackup，
// 系统 hosts 未被触碰。
func TestApplyBackupFailureClassifiedAsBackupError(t *testing.T) {
	profile := cleanProfile()
	dir := t.TempDir()
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	original := "127.0.0.1 keep.me\n"
	if err := os.WriteFile(hostsPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	base := storage.NewOSVolume()
	vol := storage.NewFaultyVolume(base, storage.Faults{
		FailAtomicWrite: func(path string) bool {
			return filepath.Dir(path) == filepath.Join(dir, "backups")
		},
	})
	root := t.TempDir()
	env := &testEnv{root: root, hostsPath: hostsPath, backupDir: filepath.Join(dir, "backups"), volume: base}
	env.writeProfileFile(t, profile)
	env.service = New(root, hostsPath, env.backupDir, vol)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	receipt, err := env.service.Apply(context.Background(), plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorBackup {
		t.Fatalf("expected backup error, got %v", err)
	}
	var statuses = map[string]app.WriteStatus{}
	for _, r := range receipt.Results {
		statuses[r.Entity] = r.Status
	}
	if statuses["backup"] != app.WriteFailed || statuses["managed_block"] != app.WriteSkipped || statuses["verification"] != app.WriteSkipped {
		t.Fatalf("results = %+v", receipt.Results)
	}
	data, _ := os.ReadFile(hostsPath)
	if string(data) != original {
		t.Fatalf("hosts touched on backup failure: %q", data)
	}
}

// 回归（review 建议项 1）：应用「无启用规则」的 profile 后不得误报 drift。
func TestApplyEmptyProfileDoesNotReportDrift(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "empty",
		"# only comments\n# 127.0.0.1 disabled.test\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	state, err := env.service.AppliedState(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Drifted {
		t.Fatalf("fresh apply of empty profile reported drift: %+v", state)
	}
	if state.ProfileID != profile.ID {
		t.Fatalf("state profile = %q", state.ProfileID)
	}

	// 外部写入一个块（本服务记录的是空块哈希）→ drift
	raw, _ := os.ReadFile(env.hostsPath)
	if err := os.WriteFile(env.hostsPath, append(raw, []byte("# ---- mHost start ----\n6.6.6.6 x.com\n# ---- mHost end ----\n")...), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	state, _ = env.service.AppliedState(context.Background())
	if !state.Drifted {
		t.Fatal("external block should be drift")
	}
}

// 回归（review 建议项 2）：保存时按 (IP, domains) 沿用旧规则 ID 与 Source。
func TestSaveProfilePreservesRuleIdentityAndSource(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env",
		"127.0.0.1 keep.test\n")
	// 造一条 Remote 来源规则
	profile.Rules = append(profile.Rules, storage.HostRule{
		ID:      "66666666-6666-4666-8666-666666666666",
		IP:      ipPtr(t, "10.0.0.1"),
		Domains: []string{"remote-ads.test"},
		Enabled: true,
		Source:  storage.RuleSource{Type: "Remote", SourceID: "sub-1", SourceName: "订阅"},
	})
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	keepID := profile.Rules[0].ID

	// 编辑一行（改 keep.test 的 IP）后保存
	edited := strings.Replace(detail.Content, "127.0.0.1 keep.test", "127.0.0.2 keep.test", 1)
	saved, err := env.service.SaveProfile(context.Background(), profile.ID, edited, detail.Revision)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = saved

	stored, err := env.service.loadProfile(profile.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(stored.Rules) != 2 {
		t.Fatalf("rules = %+v", stored.Rules)
	}
	var keep, remote *storage.HostRule
	for i := range stored.Rules {
		switch stored.Rules[i].Domains[0] {
		case "keep.test":
			keep = &stored.Rules[i]
		case "remote-ads.test":
			remote = &stored.Rules[i]
		}
	}
	// 被编辑的规则（IP 变了）正确换新 ID
	if keep == nil || keep.ID == keepID {
		t.Fatalf("edited rule should get a new ID: %+v", keep)
	}
	// Remote 规则 Source 不被改写
	if remote == nil || remote.Source.Type != "Remote" || remote.Source.SourceID != "sub-1" {
		t.Fatalf("remote source rewritten: %+v", remote)
	}
	if remote.ID != "66666666-6666-4666-8666-666666666666" {
		t.Fatalf("remote id changed: %q", remote.ID)
	}
	// 改动的规则换新 ID
	if keep.IP.String() != "127.0.0.2" {
		t.Fatalf("edited ip not saved: %q", keep.IP)
	}
}
