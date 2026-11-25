package legacyprobe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ProfileSummary struct {
	Path      string      `json:"path"`
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Directory ProfileMode `json:"directory"`
	Mode      ProfileMode `json:"mode"`
	RuleCount int         `json:"rule_count"`
	Enabled   bool        `json:"enabled"`
	Protected bool        `json:"protected"`
}

type AdBlockSummary struct {
	Enabled       bool `json:"enabled"`
	SourceCount   int  `json:"source_count"`
	WhitelistSize int  `json:"whitelist_size"`
}

type Report struct {
	Manifest      *Manifest        `json:"manifest,omitempty"`
	Profiles      []ProfileSummary `json:"profiles"`
	Settings      map[string]any   `json:"settings,omitempty"`
	SnapshotCount int              `json:"snapshot_count"`
	AdBlock       *AdBlockSummary  `json:"adblock,omitempty"`
	Issues        []Issue          `json:"issues"`
}

func Scan(root string) (Report, error) {
	report := Report{Profiles: []ProfileSummary{}, Issues: []Issue{}}
	info, err := os.Stat(root)
	if err != nil {
		return report, fmt.Errorf("stat probe root: %w", err)
	}
	if !info.IsDir() {
		return report, fmt.Errorf("probe root %q is not a directory", root)
	}

	var manifest Manifest
	if readJSON(filepath.Join(root, "manifest.json"), &manifest, true, &report) {
		report.Manifest = &manifest
	}
	for _, directory := range []ProfileMode{ModeDNS, ModeHosts} {
		scanProfiles(root, directory, &report)
	}
	scanSettings(root, &report)
	scanSnapshots(root, &report)
	scanAdBlock(root, &report)
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Path == report.Issues[j].Path {
			return report.Issues[i].Message < report.Issues[j].Message
		}
		return report.Issues[i].Path < report.Issues[j].Path
	})
	return report, nil
}

func scanProfiles(root string, directory ProfileMode, report *Report) {
	dir := filepath.Join(root, "profiles", string(directory))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		addIssue(report, dir, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var profile Profile
		if !readJSON(path, &profile, false, report) {
			continue
		}
		report.Profiles = append(report.Profiles, ProfileSummary{
			Path:      path,
			ID:        profile.ID,
			Name:      profile.Name,
			Directory: directory,
			Mode:      profile.Mode,
			RuleCount: len(profile.Rules),
			Enabled:   profile.Enabled,
			Protected: profile.Protected,
		})
	}
}

func scanSettings(root string, report *Report) {
	var settings map[string]any
	if readJSON(filepath.Join(root, "settings.json"), &settings, true, report) {
		report.Settings = settings
	}
}

func scanSnapshots(root string, report *Report) {
	dir := filepath.Join(root, "snapshots")
	var index snapshotIndex
	if !readJSON(filepath.Join(dir, "index.json"), &index, true, report) {
		return
	}
	for _, meta := range index.Snapshots {
		var snapshot Snapshot
		if readJSON(filepath.Join(dir, meta.ID+".json"), &snapshot, false, report) {
			report.SnapshotCount++
		}
	}
}

func scanAdBlock(root string, report *Report) {
	var state adBlockState
	if readJSON(filepath.Join(root, "adblock.json"), &state, true, report) {
		report.AdBlock = &AdBlockSummary{
			Enabled:       state.Enabled,
			SourceCount:   len(state.Sources),
			WhitelistSize: len(state.Whitelist),
		}
	}
}

func readJSON(path string, target any, optional bool, report *Report) bool {
	data, err := os.ReadFile(path)
	if optional && errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		addIssue(report, path, err)
		return false
	}
	if err := json.Unmarshal(data, target); err != nil {
		addIssue(report, path, err)
		return false
	}
	return true
}

func addIssue(report *Report, path string, err error) {
	report.Issues = append(report.Issues, Issue{Path: path, Message: err.Error()})
}
