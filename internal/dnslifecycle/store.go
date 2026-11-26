package dnslifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type RecoveryKind string

const (
	KindManual    RecoveryKind = "manual"
	KindDHCPEmpty RecoveryKind = "dhcp_empty"
)

type DNSState struct {
	Service string       `json:"service"`
	Kind    RecoveryKind `json:"kind"`
	Servers []string     `json:"servers,omitempty"`
}

type Store interface {
	Save(DNSState) error
	Load() (DNSState, error)
	Pending() (bool, error)
	Clear() error
}

type FileStore struct {
	directory string
}

func NewFileStore(directory string) *FileStore {
	return &FileStore{directory: directory}
}

func (s *FileStore) Save(state DNSState) error {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return fmt.Errorf("create recovery directory: %w", err)
	}
	if err := os.Chmod(s.directory, 0o700); err != nil {
		return fmt.Errorf("secure recovery directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode original DNS: %w", err)
	}
	data = append(data, '\n')
	if err := writePrivateFile(s.originalPath(), data); err != nil {
		return fmt.Errorf("save original DNS: %w", err)
	}
	if err := writePrivateFile(s.markerPath(), []byte("DNS recovery required\n")); err != nil {
		return fmt.Errorf("save recovery marker: %w", err)
	}
	return nil
}

func (s *FileStore) Load() (DNSState, error) {
	pending, err := s.Pending()
	if err != nil {
		return DNSState{}, err
	}
	if !pending {
		return DNSState{}, fmt.Errorf("no DNS recovery is pending")
	}
	data, err := os.ReadFile(s.originalPath())
	if err != nil {
		return DNSState{}, fmt.Errorf("read original DNS: %w", err)
	}
	var state DNSState
	if err := json.Unmarshal(data, &state); err != nil {
		return DNSState{}, fmt.Errorf("decode original DNS: %w", err)
	}
	if state.Service == "" || (state.Kind != KindManual && state.Kind != KindDHCPEmpty) {
		return DNSState{}, fmt.Errorf("invalid original DNS state")
	}
	return state, nil
}

func (s *FileStore) Pending() (bool, error) {
	_, err := os.Stat(s.markerPath())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat recovery marker: %w", err)
	}
	return true, nil
}

func (s *FileStore) Clear() error {
	if err := removeIfExists(s.markerPath()); err != nil {
		return fmt.Errorf("remove recovery marker: %w", err)
	}
	if err := removeIfExists(s.originalPath()); err != nil {
		return fmt.Errorf("remove original DNS: %w", err)
	}
	return nil
}

func (s *FileStore) originalPath() string {
	return filepath.Join(s.directory, "original-dns.json")
}

func (s *FileStore) markerPath() string {
	return filepath.Join(s.directory, "recovery_pending")
}

func writePrivateFile(path string, data []byte) (returnErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".phase0-recovery-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
