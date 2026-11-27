package hostssvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
)

func TestCreateProfile(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n")

	detail, err := env.service.CreateProfile(context.Background(), "新环境", "127.0.0.1 a.com\n")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if detail.ID == "" || detail.Name != "新环境" || detail.RuleCount != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	profiles, err := env.service.ListProfiles(context.Background())
	if err != nil || len(profiles) != 1 {
		t.Fatalf("list = %v err=%v", profiles, err)
	}

	// 空名称拒绝
	if _, err := env.service.CreateProfile(context.Background(), "  ", "127.0.0.1 a.com\n"); err == nil {
		t.Fatal("empty name should fail")
	}
	// 错误内容拒绝（与 SaveProfile 同校验）
	if _, err := env.service.CreateProfile(context.Background(), "bad", "not-an-ip x.com\n"); err == nil {
		t.Fatal("invalid content should fail")
	}
}

func TestDuplicateProfile(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "开发环境", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	copy1, err := env.service.DuplicateProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if copy1.ID == profile.ID || copy1.Name != "开发环境 copy" {
		t.Fatalf("copy1 = %+v", copy1)
	}
	if copy1.Content != "127.0.0.1 a.com\n" {
		t.Fatalf("copy content = %q", copy1.Content)
	}

	copy2, err := env.service.DuplicateProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("duplicate 2: %v", err)
	}
	if copy2.Name != "开发环境 copy 2" {
		t.Fatalf("copy2 name = %q", copy2.Name)
	}

	profiles, _ := env.service.ListProfiles(context.Background())
	if len(profiles) != 3 {
		t.Fatalf("profiles = %d, want 3", len(profiles))
	}

	// 复制受保护 profile：副本不再受保护
	protected := testProfile("33333333-3333-4333-8333-333333333333", "内置", "127.0.0.1 mhost.internal\n")
	protected.Protected = true
	env.writeProfileFile(t, protected)
	copy3, err := env.service.DuplicateProfile(context.Background(), protected.ID)
	if err != nil {
		t.Fatalf("duplicate protected: %v", err)
	}
	if copy3.Protected {
		t.Fatal("copy of protected profile should not be protected")
	}
}

func TestSetProfileEnabled(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	detail, err := env.service.SetProfileEnabled(context.Background(), profile.ID, false)
	if err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if detail.Revision == 0 {
		t.Fatal("revision should be set")
	}

	// 存储态写盘生效：再次切换修订号继续递增
	back, err := env.service.SetProfileEnabled(context.Background(), profile.ID, true)
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if back.Revision <= detail.Revision {
		t.Fatalf("revision should increase: %d -> %d", detail.Revision, back.Revision)
	}
}

func TestDeleteProfile(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	// 删除前建一个计划，删除后计划应失效
	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if err := env.service.DeleteProfile(context.Background(), profile.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	profiles, _ := env.service.ListProfiles(context.Background())
	if len(profiles) != 0 {
		t.Fatalf("profiles = %d, want 0", len(profiles))
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("plan should be stale after delete, got %v", err)
	}

	// 删除不存在 → not found
	if err := env.service.DeleteProfile(context.Background(), profile.ID); err == nil {
		t.Fatal("expected not found")
	}
}

func TestDeleteProtectedProfileRefused(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "内置", "127.0.0.1 mhost.internal\n")
	profile.Protected = true
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	err := env.service.DeleteProfile(context.Background(), profile.ID)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("expected validation error, got %v", err)
	}
	profiles, _ := env.service.ListProfiles(context.Background())
	if len(profiles) != 1 {
		t.Fatal("protected profile was deleted")
	}
}

func TestDeleteAppliedProfileRefused(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "env", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	plan, err := env.service.PreviewApply(context.Background(), profile.ID, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.service.Apply(context.Background(), plan.ID, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	err = env.service.DeleteProfile(context.Background(), profile.ID)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !strings.Contains(uiErr.Message, "applied") {
		t.Fatalf("reason should mention applied: %+v", uiErr)
	}
	profiles, _ := env.service.ListProfiles(context.Background())
	if len(profiles) != 1 {
		t.Fatal("applied profile was deleted")
	}

	// 回滚后允许删除
	backups, _ := env.service.ListApplyBackups(context.Background())
	if len(backups) == 0 {
		t.Fatal("no backups")
	}
	if _, err := env.service.Rollback(context.Background(), backups[0].ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := env.service.DeleteProfile(context.Background(), profile.ID); err != nil {
		t.Fatalf("delete after rollback: %v", err)
	}
}
