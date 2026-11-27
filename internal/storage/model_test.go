package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	root := filepath.Join("..", "..", "fixtures", "storage")
	return filepath.Join(append([]string{root}, parts...)...)
}

func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, parts...))
	if err != nil {
		t.Fatalf("read fixture %v: %v", parts, err)
	}
	return data
}

func TestParseV2Manifest(t *testing.T) {
	var manifest Manifest
	if err := json.Unmarshal(readFixture(t, "v2-full", "manifest.json"), &manifest); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if manifest.Version != 2 || manifest.AppVersion != "0.4.2" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.DNSEnabled == nil || !*manifest.DNSEnabled {
		t.Fatalf("dns_enabled not parsed: %+v", manifest)
	}
	if manifest.OriginalDNS == nil || manifest.OriginalDNS.Kind != OriginalDNSManual {
		t.Fatalf("original_dns not parsed: %+v", manifest.OriginalDNS)
	}
	if len(manifest.OriginalDNS.Servers) != 2 {
		t.Fatalf("servers = %v", manifest.OriginalDNS.Servers)
	}
}

func TestParseV1ManifestMissingDNSFields(t *testing.T) {
	var manifest Manifest
	if err := json.Unmarshal(readFixture(t, "v1-hosts", "manifest.json"), &manifest); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("version = %d", manifest.Version)
	}
	if manifest.DNSEnabled != nil || manifest.OriginalDNS != nil {
		t.Fatalf("v1 manifest must not invent v2 fields: %+v", manifest)
	}
}

func TestOriginalDNSLegacyArrayForms(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		kind    OriginalDNSKind
		servers []string
	}{
		{"manual array", `["1.1.1.1", "8.8.8.8"]`, OriginalDNSManual, []string{"1.1.1.1", "8.8.8.8"}},
		{"empty sentinel", `["Empty"]`, OriginalDNSDHCPEmpty, nil},
		{"local resolvers filtered", `["127.0.0.1", "::1", "0.0.0.0"]`, OriginalDNSDHCPEmpty, nil},
		{"mixed filtered", `["127.0.0.1", "9.9.9.9"]`, OriginalDNSManual, []string{"9.9.9.9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var dns OriginalDNS
			if err := json.Unmarshal([]byte(tc.json), &dns); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if dns.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", dns.Kind, tc.kind)
			}
			if strings.Join(dns.Servers, ",") != strings.Join(tc.servers, ",") {
				t.Fatalf("servers = %v, want %v", dns.Servers, tc.servers)
			}
		})
	}
}

func TestOriginalDNSRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`"text"`, `123`, `{}`, `{"kind": "unknown"}`, `{"kind": "manual"}`} {
		var dns OriginalDNS
		if err := json.Unmarshal([]byte(raw), &dns); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestParseV2ProfileWithDefaults(t *testing.T) {
	var profile Profile
	if err := json.Unmarshal(readFixture(t, "v2-full", "profiles", "hosts", "22222222-2222-4222-8222-222222222222.json"), &profile); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if profile.Mode != ModeHosts {
		t.Fatalf("mode = %q", profile.Mode)
	}
	if profile.ID != "22222222-2222-4222-8222-222222222222" || len(profile.Rules) != 6 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestProfileModeDefaultsToHostsAndRejectsNull(t *testing.T) {
	valid := `{"id":"11111111-1111-4111-8111-111111111111","name":"x","enabled":true,"protected":false,"tags":[],"rules":[],"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`
	var profile Profile
	if err := json.Unmarshal([]byte(valid), &profile); err != nil {
		t.Fatalf("parse without mode: %v", err)
	}
	if profile.Mode != ModeHosts {
		t.Fatalf("default mode = %q", profile.Mode)
	}
	var withNull Profile
	err := json.Unmarshal([]byte(strings.Replace(valid, `"rules"`, `"mode":null,"rules"`, 1)), &withNull)
	if err == nil {
		t.Fatal("explicit null mode must be rejected")
	}
}

func TestProfileValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing name", `{"id":"11111111-1111-4111-8111-111111111111","enabled":true,"protected":false,"tags":[],"rules":[],"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`},
		{"bad uuid", `{"id":"nope","name":"x","enabled":true,"protected":false,"tags":[],"rules":[],"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`},
		{"missing created_at", `{"id":"11111111-1111-4111-8111-111111111111","name":"x","enabled":true,"protected":false,"tags":[],"rules":[],"updated_at":"2024-01-01T00:00:00Z"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var profile Profile
			err := json.Unmarshal([]byte(tc.json), &profile)
			var modelErr *ModelError
			if !errors.As(err, &modelErr) || modelErr.Kind != ModelErrValidation {
				t.Fatalf("err = %v, want validation ModelError", err)
			}
		})
	}
}

func TestCorruptedJSONIsSyntaxError(t *testing.T) {
	var profile Profile
	err := json.Unmarshal([]byte(`{"id": `), &profile)
	if ClassifyModelError(err) != ModelErrSyntax {
		t.Fatalf("err = %v, want syntax classification", err)
	}
}

func TestRemoteRuleRequiresSourceIdentity(t *testing.T) {
	base := `{"id":"11111111-1111-4111-8111-111111111111","name":"x","enabled":true,"protected":false,"tags":[],"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","rules":[%s]}`
	rule := `{"id":"32222222-2222-4222-8222-222222222221","ip":"127.0.0.1","domains":["a.test"],"enabled":true,"source":{"type":"Remote","source_id":"","source_name":"list"}}`
	var profile Profile
	if err := json.Unmarshal([]byte(fmtProfile(base, rule)), &profile); err == nil {
		t.Fatal("remote rule without source_id must fail")
	}
}

func fmtProfile(format, rule string) string {
	return strings.ReplaceAll(format, "%s", rule)
}

func TestProfileRoundTripIsStable(t *testing.T) {
	original := readFixture(t, "v2-full", "profiles", "hosts", "22222222-2222-4222-8222-222222222222.json")
	var profile Profile
	if err := json.Unmarshal(original, &profile); err != nil {
		t.Fatalf("parse: %v", err)
	}
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reparsed Profile
	if err := json.Unmarshal(encoded, &reparsed); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if reparsed.ID != profile.ID || len(reparsed.Rules) != len(profile.Rules) || reparsed.Rules[4].IP != nil {
		t.Fatalf("round trip diverged: %+v vs %+v", profile.Rules[4], reparsed.Rules[4])
	}
}

func TestSnapshotIndexAndSnapshotParse(t *testing.T) {
	var index SnapshotIndex
	if err := json.Unmarshal(readFixture(t, "v2-full", "snapshots", "index.json"), &index); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	if len(index.Snapshots) != 1 || index.Snapshots[0].ProfileCount != 1 {
		t.Fatalf("unexpected index: %+v", index)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(readFixture(t, "v2-full", "snapshots", "3fa85f64-5717-4562-b3fc-2c963f66afa6.json"), &snapshot); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	if len(snapshot.Profiles) != 1 {
		t.Fatalf("profiles = %d", len(snapshot.Profiles))
	}
}

func TestAdBlockStateParseAndDefaults(t *testing.T) {
	var state AdBlockState
	if err := json.Unmarshal(readFixture(t, "v2-full", "adblock.json"), &state); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !state.Enabled || len(state.Sources) != 1 || len(state.Whitelist) != 1 {
		t.Fatalf("unexpected state: %+v", state)
	}
	if state.Sources[0].Format != "hosts" {
		t.Fatalf("format default = %q", state.Sources[0].Format)
	}
}

func TestSettingsRoundTripWithDefaults(t *testing.T) {
	settings := DefaultSettings()
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reparsed Settings
	if err := json.Unmarshal(encoded, &reparsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reparsed.QuickApplyOnToggle != settings.QuickApplyOnToggle {
		t.Fatalf("round trip diverged: %+v", reparsed)
	}
	// 旧副本里可能完全没有 settings.json；缺字段按默认值补齐而不是报错。
	var partial Settings
	if err := json.Unmarshal([]byte(`{"quick_apply_on_toggle":true}`), &partial); err != nil {
		t.Fatalf("partial settings: %v", err)
	}
	if !partial.QuickApplyOnToggle || partial.PaletteHistory == nil {
		t.Fatalf("defaults not materialized: %+v", partial)
	}
}
