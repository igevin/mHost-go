package hostssvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemHostsContent(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n")
	content, err := env.service.SystemHostsContent(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if content != "127.0.0.1 keep.me\n" {
		t.Fatalf("content = %q", content)
	}
}

func TestExportProfile(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "开发/env", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)
	dir := t.TempDir()

	path, err := env.service.ExportProfile(context.Background(), profile.ID, dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.HasSuffix(path, ".hosts") || !strings.Contains(filepath.Base(path), "_env") {
		t.Fatalf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if string(data) != "127.0.0.1 a.com\n" {
		t.Fatalf("export content = %q", data)
	}

	// 空目录拒绝
	if _, err := env.service.ExportProfile(context.Background(), profile.ID, "  "); err == nil {
		t.Fatal("empty dir should fail")
	}
	// 未知 profile
	if _, err := env.service.ExportProfile(context.Background(), "99999999-9999-4999-8999-999999999999", dir); err == nil {
		t.Fatal("unknown profile should fail")
	}
}

func TestImportProfileFile(t *testing.T) {
	env := newTestEnv(t, "127.0.0.1 keep.me\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "office.hosts")
	if err := os.WriteFile(path, []byte("10.0.0.1 office.local\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	detail, err := env.service.ImportProfileFile(context.Background(), path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if detail.Name != "office" || detail.RuleCount != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	// 非 .hosts 拒绝
	if _, err := env.service.ImportProfileFile(context.Background(), filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("non-.hosts should fail")
	}
	// 错误内容拒绝且不建 profile
	bad := filepath.Join(dir, "bad.hosts")
	if err := os.WriteFile(bad, []byte("not-an-ip x.com\n"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := env.service.ImportProfileFile(context.Background(), bad); err == nil {
		t.Fatal("invalid content should fail")
	}
	profiles, _ := env.service.ListProfiles(context.Background())
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestExportProfileKeepsUnicodeName(t *testing.T) {
	profile := testProfile("11111111-1111-4111-8111-111111111111", "默认", "127.0.0.1 a.com\n")
	env := newTestEnv(t, "127.0.0.1 keep.me\n", profile)
	dir := t.TempDir()

	path, err := env.service.ExportProfile(context.Background(), profile.ID, dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if filepath.Base(path) != "默认.hosts" {
		t.Fatalf("basename = %q, want 默认.hosts", filepath.Base(path))
	}
}
