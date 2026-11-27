package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testVolume(t *testing.T) *OSVolume {
	t.Helper()
	return NewOSVolume()
}

func TestWriteAtomicCreatesOwnerOnlyFile(t *testing.T) {
	volume := testVolume(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "data.json")

	if err := volume.WriteAtomic(target, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != `{"a":1}` {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 0600", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(dir, "nested"))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm = %o, want 0700", info.Mode().Perm())
	}
}

func TestWriteAtomicLeavesNoTempFileOnFailure(t *testing.T) {
	volume := NewFaultyVolume(NewOSVolume(), Faults{
		FailAtomicWrite: func(string) bool { return true },
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "data.json")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := volume.WriteAtomic(target, []byte("changed"), 0o600)
	if err == nil {
		t.Fatal("expected injected write failure")
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read back: %v", readErr)
	}
	if string(data) != "original" {
		t.Fatalf("target mutated on failure: %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}

func TestWriteAtomicReplaceKeepsOldContentWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "data.json")
	if err := NewOSVolume().WriteAtomic(target, []byte("v1"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	count := 0
	volume := NewFaultyVolume(NewOSVolume(), Faults{
		FailAtomicWrite: func(string) bool {
			count++
			return count == 1
		},
	})
	if err := volume.WriteAtomic(target, []byte("v2"), 0o600); err == nil {
		t.Fatal("expected injected write failure")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1" {
		t.Fatalf("target mutated on failure: %q", data)
	}
	if err := volume.WriteAtomic(target, []byte("v2"), 0o600); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestCopyPreservesContentAndAppliesPerm(t *testing.T) {
	volume := testVolume(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	dst := filepath.Join(dir, "backups", "dst.json")
	payload := bytes.Repeat([]byte("x"), 4096)
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := volume.Copy(dst, src, 0o600); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("copied content differs")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 0600", info.Mode().Perm())
	}
}

func TestCopyFailsWhenSourceMissing(t *testing.T) {
	volume := testVolume(t)
	dir := t.TempDir()
	err := volume.Copy(filepath.Join(dir, "dst"), filepath.Join(dir, "missing"), 0o600)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}
