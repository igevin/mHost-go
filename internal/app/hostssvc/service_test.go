package hostssvc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

// testEnv 组装：临时工作区（含 hosts profile 存储）+ 临时系统 hosts +
// 临时备份目录 + 真实服务。
type testEnv struct {
	root      string
	hostsPath string
	backupDir string
	service   *Service
	volume    *storage.OSVolume
}

func newTestEnv(t *testing.T, hostsContent string, profiles ...storage.Profile) *testEnv {
	t.Helper()
	root := t.TempDir()
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte(hostsContent), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	env := &testEnv{
		root:      root,
		hostsPath: hostsPath,
		backupDir: filepath.Join(t.TempDir(), "backups"),
		volume:    storage.NewOSVolume(),
	}
	for _, profile := range profiles {
		env.writeProfileFile(t, profile)
	}
	env.service = New(root, hostsPath, env.backupDir, env.volume)
	return env
}

func (e *testEnv) writeProfileFile(t *testing.T, profile storage.Profile) {
	t.Helper()
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	dir := filepath.Join(e.root, "profiles", "hosts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, profile.ID+".json"), data, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

func testProfile(id, name, content string) storage.Profile {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	parsed := hostsfile.Parse(content)
	return storage.Profile{
		ID:        id,
		Name:      name,
		Enabled:   true,
		Protected: false,
		Tags:      []string{},
		Rules:     rulesToStorage(parsed.Rules),
		Mode:      storage.ModeHosts,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestListLoadSaveProfiles(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "开发环境",
		"# 开发环境\n127.0.0.1 localhost # loopback\n192.168.1.10 api.dev.local db.dev.local\n# 10.0.0.9 old.dev.local\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	profiles, err := env.service.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(profiles))
	}
	if profiles[0].Name != "开发环境" || profiles[0].RuleCount != 2 {
		t.Fatalf("profile = %+v", profiles[0])
	}

	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 文本往返：注释行保留、禁用行渲染为注释、多域名展开
	want := "# 开发环境\n127.0.0.1 localhost # loopback\n192.168.1.10 api.dev.local\n192.168.1.10 db.dev.local\n# 10.0.0.9 old.dev.local\n"
	if detail.Content != want {
		t.Fatalf("content = %q, want %q", detail.Content, want)
	}

	// 保存合法内容 → 修订号变化
	saved, err := env.service.SaveProfile(context.Background(), profile.ID, detail.Content, detail.Revision)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Revision == detail.Revision {
		t.Fatal("revision should bump after save")
	}

	// 过期修订 → ErrStaleOperation
	if _, err := env.service.SaveProfile(context.Background(), profile.ID, detail.Content, detail.Revision); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("expected stale operation, got %v", err)
	}

	// 未知 profile
	if _, err := env.service.LoadProfile(context.Background(), "99999999-9999-4999-8999-999999999999"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestSaveProfileRejectsParseErrors(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "broken", "127.0.0.1 ok.test\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	_, err = env.service.SaveProfile(context.Background(), profile.ID, "not-an-ip broken.test\n127.0.0.1 ok.test\n", detail.Revision)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !strings.Contains(uiErr.Detail, "not-an-ip") {
		t.Fatalf("error detail should name the token: %+v", uiErr)
	}

	// 存储未被破坏
	reloaded, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Content != detail.Content {
		t.Fatalf("profile content changed: %q", reloaded.Content)
	}
}

func TestSaveProfileRoundTripStability(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "rt", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)

	detail, err := env.service.LoadProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	saved, err := env.service.SaveProfile(context.Background(), profile.ID, detail.Content, detail.Revision)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Content != detail.Content {
		t.Fatalf("round trip not stable:\n%q\n%q", detail.Content, saved.Content)
	}
}

func TestListSkipsCorruptProfileFiles(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "good", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)
	dir := filepath.Join(env.root, "profiles", "hosts")
	if err := os.WriteFile(filepath.Join(dir, "garbage.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	profiles, err := env.service.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d, want 1 (corrupt skipped)", len(profiles))
	}
}
