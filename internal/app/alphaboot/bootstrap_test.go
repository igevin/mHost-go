package alphaboot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/igevin/mHost-go/internal/storage"
)

// copyFixture 把 fixtures/storage/<name> 复制到临时目录（v1 迁移写盘需要）。
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "fixtures", "storage", name)
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	copyTree(t, src, dst, entries)
	return dst
}

func copyTree(t *testing.T, src, dst string, entries []os.DirEntry) {
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(to, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			sub, err := os.ReadDir(from)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			copyTree(t, from, to, sub)
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
}

func tempHostsPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 keep.me\n"), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	return path
}

func TestPrepareMigratesV1Data(t *testing.T) {
	dataDir := copyFixture(t, "v1-full")
	hostsPath := tempHostsPath(t)

	result, err := Prepare(context.Background(), Options{DataDir: dataDir, HostsPath: hostsPath})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if result.DataDir != dataDir || result.HostsPath != hostsPath {
		t.Fatalf("result = %+v", result)
	}
	if result.Migration == nil {
		t.Fatal("expected migration receipt for v1 data")
	}
	if result.Service == nil {
		t.Fatal("service missing")
	}

	// v1 fixture 的 hosts profile 迁移后可见
	profiles, err := result.Service.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("expected migrated hosts profiles")
	}

	// 数据目录已升级 v2
	manifest, err := os.ReadFile(filepath.Join(dataDir, storage.FileManifest))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !contains(string(manifest), "\"version\": 2") {
		t.Fatalf("manifest not upgraded: %s", manifest)
	}
}

func TestPrepareCreatesDefaultProfileWhenEmpty(t *testing.T) {
	dataDir := t.TempDir()
	hostsPath := tempHostsPath(t)

	result, err := Prepare(context.Background(), Options{DataDir: dataDir, HostsPath: hostsPath})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if result.Migration != nil {
		t.Fatal("empty dir should not migrate")
	}
	profiles, err := result.Service.ListProfiles(context.Background())
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles = %v err=%v", profiles, err)
	}
	if profiles[0].Name != "默认" || profiles[0].RuleCount != 1 {
		t.Fatalf("default profile = %+v", profiles[0])
	}

	// 再次启动不重复创建
	result2, err := Prepare(context.Background(), Options{DataDir: dataDir, HostsPath: hostsPath})
	if err != nil {
		t.Fatalf("prepare 2: %v", err)
	}
	profiles2, _ := result2.Service.ListProfiles(context.Background())
	if len(profiles2) != 1 {
		t.Fatalf("default profile duplicated: %d", len(profiles2))
	}
}

func TestPrepareKeepsExistingV2Profiles(t *testing.T) {
	dataDir := copyFixture(t, "v2-full")
	hostsPath := tempHostsPath(t)

	result, err := Prepare(context.Background(), Options{DataDir: dataDir, HostsPath: hostsPath})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if result.Migration != nil {
		t.Fatal("v2 data should not migrate")
	}
	profiles, err := result.Service.ListProfiles(context.Background())
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles = %v err=%v", profiles, err)
	}
	for _, profile := range profiles {
		if profile.Name == "默认" {
			t.Fatal("default profile should not be added when profiles exist")
		}
	}
}

func contains(text, sub string) bool {
	return len(sub) == 0 || (len(text) >= len(sub) && index(text, sub) >= 0)
}

func index(text, sub string) int {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
