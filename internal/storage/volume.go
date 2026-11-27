// Package storage 实现 mHost 兼容存储的正式磁盘层：领域模型、原子写入、
// 备份、v1→v2 迁移和 app.StorageService 契约。Phase 0 的 legacyprobe
// 只读探针保持独立，作为兼容性差分测试的对照实现。
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// DirPerm 是所有存储目录的权限：owner rwx，组和其他无权限。
	DirPerm fs.FileMode = 0o700
	// FilePerm 是所有存储文件的权限：owner rw，组和其他无权限。
	FilePerm fs.FileMode = 0o600
)

// Volume 抽象存储需要的文件系统操作，生产环境用 OSVolume，
// 测试用 FaultyVolume 注入故障。
type Volume interface {
	ReadFile(path string) ([]byte, error)
	MkdirAll(path string, perm fs.FileMode) error
	// WriteAtomic 以「临时文件 + fsync + rename」原子写入 path；
	// 失败时不得改变目标文件的原有内容，不得留下临时文件。
	WriteAtomic(path string, data []byte, perm fs.FileMode) error
	// Copy 原子地把 src 复制为 dst（dst 所在目录不存在时创建），
	// 并应用 perm。
	Copy(dst, src string, perm fs.FileMode) error
	Remove(path string) error
}

// OSVolume 基于真实文件系统的 Volume 实现。
type OSVolume struct{}

func NewOSVolume() *OSVolume { return &OSVolume{} }

func (OSVolume) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (OSVolume) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSVolume) WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeAtomic(path, data, perm, nil)
}

func (OSVolume) Copy(dst, src string, perm fs.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeAtomic(dst, data, perm, nil)
}

func (OSVolume) Remove(path string) error {
	return os.Remove(path)
}

// writeAtomic 在同一目录写临时文件后 rename 覆盖目标：
// 同目录 rename 在 POSIX/Windows 上都是原子替换，失败时目标保持原样。
func writeAtomic(path string, data []byte, perm fs.FileMode, fail func(string) bool) error {
	if fail != nil && fail(path) {
		return fmt.Errorf("injected atomic write failure for %s", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	tmp, err := tempPath(dir)
	if err != nil {
		return err
	}
	// 无论成败都把临时文件清掉，避免污染目录、误导用户。
	defer os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename over target: %w", err)
	}
	// fsync 目录，保证 rename 落盘；失败仅降级为警告语义，这里直接忽略
	// 平台上不支持目录 fsync 的情况。
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func tempPath(dir string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return filepath.Join(dir, ".tmp-"+hex.EncodeToString(random[:])), nil
}

// Faults 描述 FaultyVolume 注入故障的条件；任一回调返回 true 即触发。
type Faults struct {
	// FailAtomicWrite 在临时文件创建前注入失败，模拟磁盘/权限错误。
	FailAtomicWrite func(path string) bool
	// FailCopy 在备份复制前注入失败。
	FailCopy func(dst, src string) bool
}

// FaultyVolume 包装另一个 Volume，按 Faults 注入故障，用于测试
// 「写入失败不留下半文件、不破坏原数据」。
type FaultyVolume struct {
	base   Volume
	faults Faults
}

func NewFaultyVolume(base Volume, faults Faults) *FaultyVolume {
	return &FaultyVolume{base: base, faults: faults}
}

func (v *FaultyVolume) ReadFile(path string) ([]byte, error) {
	return v.base.ReadFile(path)
}

func (v *FaultyVolume) MkdirAll(path string, perm fs.FileMode) error {
	return v.base.MkdirAll(path, perm)
}

func (v *FaultyVolume) WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeAtomic(path, data, perm, v.faults.FailAtomicWrite)
}

func (v *FaultyVolume) Copy(dst, src string, perm fs.FileMode) error {
	if v.faults.FailCopy != nil && v.faults.FailCopy(dst, src) {
		return fmt.Errorf("injected copy failure for %s", dst)
	}
	return v.base.Copy(dst, src, perm)
}

func (v *FaultyVolume) Remove(path string) error {
	return v.base.Remove(path)
}

var _ Volume = (*OSVolume)(nil)
var _ Volume = (*FaultyVolume)(nil)

// IsNotExist 判断错误是否表示文件不存在。
func IsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
