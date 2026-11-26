package legacyprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixtureRoot(name string) string {
	return filepath.Join("..", "..", "fixtures", "storage", name)
}

func TestScan_V1DefaultsProfileMode(t *testing.T) {
	report, err := Scan(fixtureRoot("v1-hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("unexpected issues: %+v", report.Issues)
	}
	if report.Manifest == nil || report.Manifest.Version != 1 {
		t.Fatalf("manifest = %+v", report.Manifest)
	}
	if report.Manifest.DNSEnabled != nil || report.Manifest.OriginalDNS != nil {
		t.Fatalf("v1 optional fields = %+v", report.Manifest)
	}
	if len(report.Profiles) != 1 {
		t.Fatalf("profiles = %+v", report.Profiles)
	}
	profile := report.Profiles[0]
	if profile.Mode != ModeHosts || profile.Directory != ModeHosts || profile.RuleCount != 1 {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestScan_V2Full(t *testing.T) {
	report, err := Scan(fixtureRoot("v2-full"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("unexpected issues: %+v", report.Issues)
	}
	if report.Manifest == nil || report.Manifest.OriginalDNS == nil {
		t.Fatalf("manifest = %+v", report.Manifest)
	}
	if got := report.Manifest.OriginalDNS.Kind; got != OriginalDNSManual {
		t.Fatalf("original DNS kind = %q", got)
	}
	if got, want := report.Manifest.OriginalDNS.Servers, []string{"1.1.1.1", "8.8.8.8"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
	if len(report.Profiles) != 2 || report.Profiles[0].RuleCount != 1 || report.Profiles[1].RuleCount != 6 {
		t.Fatalf("profiles = %+v", report.Profiles)
	}
	if report.SnapshotCount != 1 || report.AdBlock == nil || !report.AdBlock.Enabled || report.AdBlock.SourceCount != 1 {
		t.Fatalf("document summary = %+v", report)
	}
	if report.Settings["quick_apply_on_toggle"] != true {
		t.Fatalf("settings = %+v", report.Settings)
	}
}

func TestScan_OriginalDNSCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantKind   OriginalDNSKind
		wantServer []string
	}{
		{name: "legacy empty", fixture: "v2-legacy-empty", wantKind: OriginalDNSDHCPEmpty},
		{name: "legacy Empty placeholder", fixture: "v2-legacy-placeholder", wantKind: OriginalDNSDHCPEmpty},
		{name: "legacy filters local addresses", fixture: "v2-legacy-filtered", wantKind: OriginalDNSManual, wantServer: []string{"1.1.1.1"}},
		{name: "tagged DHCP", fixture: "v2-tagged-dhcp", wantKind: OriginalDNSDHCPEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Scan(fixtureRoot(tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if report.Manifest == nil || report.Manifest.OriginalDNS == nil {
				t.Fatalf("manifest = %+v", report.Manifest)
			}
			got := report.Manifest.OriginalDNS
			if got.Kind != tt.wantKind || !reflect.DeepEqual(got.Servers, tt.wantServer) {
				t.Fatalf("original DNS = %+v, want kind %q servers %v", got, tt.wantKind, tt.wantServer)
			}
		})
	}
}

func TestScan_MixedInvalidReportsEveryFileAndContinues(t *testing.T) {
	report, err := Scan(fixtureRoot("mixed-invalid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Profiles) != 1 || report.Profiles[0].Name != "Still readable" || report.Profiles[0].Mode != ModeHosts {
		t.Fatalf("valid profiles = %+v", report.Profiles)
	}
	if len(report.Issues) != 3 {
		t.Fatalf("issues = %+v", report.Issues)
	}
	joined := ""
	for _, issue := range report.Issues {
		joined += issue.Path + ": " + issue.Message + "\n"
	}
	for _, want := range []string{"broken.json", "missing-fields.json", "wrong-type.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("issues do not contain %q:\n%s", want, joined)
		}
	}
}

func TestOriginalDNS_TaggedLoopbackOnlyFallsBackToDHCP(t *testing.T) {
	var state OriginalDNS
	if err := state.UnmarshalJSON([]byte(`{"kind":"manual","servers":["127.0.0.1","::1"]}`)); err != nil {
		t.Fatal(err)
	}
	if state.Kind != OriginalDNSDHCPEmpty || len(state.Servers) != 0 {
		t.Fatalf("original DNS = %+v", state)
	}
}

func TestScan_RejectsSnapshotIDOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := `{"version":1,"snapshots":[{"id":"../outside","name":"Outside","profile_count":0,"created_at":"2024-06-05T10:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, "snapshots", "index.json"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"id":"outside","name":"Outside","profiles":[],"created_at":"2024-06-05T10:00:00Z"}`
	if err := os.WriteFile(filepath.Join(root, "outside.json"), []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.SnapshotCount != 0 {
		t.Fatalf("snapshot count = %d", report.SnapshotCount)
	}
	if len(report.Issues) != 1 || !strings.Contains(report.Issues[0].Message, "invalid snapshot id") {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

func TestScan_DoesNotModifyInput(t *testing.T) {
	root := fixtureRoot("v2-full")
	before := hashTree(t, root)
	if _, err := Scan(root); err != nil {
		t.Fatal(err)
	}
	after := hashTree(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("input changed\nbefore: %v\nafter:  %v", before, after)
	}
}

func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		file, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
