package storage

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/igevin/mHost-go/internal/legacyprobe"
)

// 差分兼容测试：Phase 0 只读探针（已与源项目行为核对过）与本包正式
// 实现对同一批 fixture 的解析结果必须在领域层面一致——任何一边的
// 行为漂移都会被这里抓住。

var compatibilityFixtures = []string{
	"v1-hosts",
	"v1-full",
	"v2-full",
	"mixed-invalid",
	"v2-legacy-empty",
	"v2-legacy-filtered",
	"v2-legacy-placeholder",
	"v2-tagged-dhcp",
}

func TestProbeAndStorageAgree(t *testing.T) {
	for _, name := range compatibilityFixtures {
		t.Run(name, func(t *testing.T) {
			root := fixturePath(t, name)
			probeReport, err := legacyprobe.Scan(root)
			if err != nil {
				t.Fatalf("probe scan: %v", err)
			}
			scan, err := NewStore(NewOSVolume()).Scan(root)
			if err != nil {
				t.Fatalf("storage scan: %v", err)
			}

			if probeReport.Manifest != nil && scan.Manifest != nil {
				if probeReport.Manifest.Version != scan.Version {
					t.Fatalf("version: probe %d vs storage %d", probeReport.Manifest.Version, scan.Version)
				}
			}

			type profileKey struct {
				id, name, mode     string
				rules              int
				enabled, protected bool
			}
			// v1 文件没有 mode 字段：正式实现按所在目录推断模式，而
			// Phase 0 探针统一回退 hosts。这里以目录语义为准做比对。
			expectedMode := func(relPath, probeMode string) string {
				if strings.Contains(relPath, "profiles/dns") && probeMode == string(legacyprobe.ModeHosts) {
					return string(legacyprobe.ModeDNS)
				}
				return probeMode
			}
			probeProfiles := map[profileKey]int{}
			for _, profile := range probeReport.Profiles {
				mode := expectedMode(filepath.ToSlash(profile.Path), string(profile.Mode))
				key := profileKey{profile.ID, profile.Name, mode, profile.RuleCount, profile.Enabled, profile.Protected}
				probeProfiles[key]++
			}
			storageProfiles := map[profileKey]int{}
			for _, entry := range scan.Profiles {
				profile := entry.Profile
				key := profileKey{profile.ID, profile.Name, string(profile.Mode), len(profile.Rules), profile.Enabled, profile.Protected}
				storageProfiles[key]++
			}
			if len(probeProfiles) != len(storageProfiles) {
				t.Fatalf("profile sets differ:\nprobe   %v\nstorage %v", probeProfiles, storageProfiles)
			}
			for key, count := range probeProfiles {
				if storageProfiles[key] != count {
					t.Fatalf("profile mismatch for %+v: probe %d vs storage %d", key, count, storageProfiles[key])
				}
			}

			if probeReport.SnapshotCount != len(scan.Snapshots) {
				t.Fatalf("snapshots: probe %d vs storage %d", probeReport.SnapshotCount, len(scan.Snapshots))
			}

			if probeReport.AdBlock != nil && scan.AdBlock != nil {
				if probeReport.AdBlock.Enabled != scan.AdBlock.Enabled ||
					probeReport.AdBlock.SourceCount != len(scan.AdBlock.Sources) ||
					probeReport.AdBlock.WhitelistSize != len(scan.AdBlock.Whitelist) {
					t.Fatalf("adblock: probe %+v vs storage %+v", *probeReport.AdBlock, scan.AdBlock)
				}
			} else if (probeReport.AdBlock == nil) != (scan.AdBlock == nil) {
				t.Fatalf("adblock presence differs: probe %v vs storage %v", probeReport.AdBlock != nil, scan.AdBlock != nil)
			}

			// 损坏检测：双方必须命中同一组相对路径。
			probeIssuePaths := []string{}
			for _, issue := range probeReport.Issues {
				probeIssuePaths = append(probeIssuePaths, relativize(root, issue.Path))
			}
			storageIssuePaths := []string{}
			for _, issue := range scan.Issues {
				storageIssuePaths = append(storageIssuePaths, filepath.ToSlash(issue.Path))
			}
			sort.Strings(probeIssuePaths)
			sort.Strings(storageIssuePaths)
			if joinPaths(probeIssuePaths) != joinPaths(storageIssuePaths) {
				t.Fatalf("issue paths differ:\nprobe   %v\nstorage %v", probeIssuePaths, storageIssuePaths)
			}
		})
	}
}

func relativize(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func joinPaths(paths []string) string {
	out := ""
	for _, path := range paths {
		out += path + ";"
	}
	return out
}
