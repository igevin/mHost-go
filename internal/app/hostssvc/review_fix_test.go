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
