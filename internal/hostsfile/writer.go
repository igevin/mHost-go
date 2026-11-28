package hostsfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MaxBackups 是保留的 apply 备份上限（源项目 writer/backup.rs MAX_BACKUPS）。
const MaxBackups = 10

// 分类哨兵错误：服务层用 errors.Is 区分失败阶段，不依赖错误文案
// 子串匹配（review 建议项）。
var (
	// ErrBackup 写前备份失败（尚未触碰系统 hosts）。
	ErrBackup = errors.New("backup failed")
	// ErrReadback 写入后的读回验证失败（已按备份自动回滚）。
	ErrReadback = errors.New("readback verification failed")
)

// Volume 是写入器需要的文件系统操作子集；internal/storage 的
// OSVolume / FaultyVolume 均满足该接口，测试可直接注入故障。
type Volume interface {
	ReadFile(path string) ([]byte, error)
	MkdirAll(path string, perm fs.FileMode) error
	WriteAtomic(path string, data []byte, perm fs.FileMode) error
	Remove(path string) error
}

// Mover 把临时文件落到系统 hosts 路径。生产实现为同盘 rename；
// darwin 默认 PrivilegedMover（rename 失败后请求管理员授权，
// 对应源项目 elevated_move）。
type Mover interface {
	Move(tempPath, target string) error
}

// RenameMover 是同目录原子替换的 Mover 实现。
type RenameMover struct{}

func (RenameMover) Move(tempPath, target string) error {
	return os.Rename(tempPath, target)
}

// Flusher 在写入后刷新系统 DNS 缓存；失败不致命（源项目行为：记录日志）。
// 默认 NoopFlusher；macOS 实现在 #6 接入 privileged helper 后提供。
type Flusher interface {
	FlushDNS() error
}

type NoopFlusher struct{}

func (NoopFlusher) FlushDNS() error { return nil }

// BackupRecord 是一份 apply 备份的索引记录；备份文件本身存
// 写入前系统 hosts 的全量内容（0600）。
type BackupRecord struct {
	ID              string    `json:"id"`
	File            string    `json:"file"` // 相对于 BackupDir 的文件名
	ProfileID       string    `json:"profile_id"`
	ProfileName     string    `json:"profile_name"`
	PrevProfileID   string    `json:"prev_profile_id,omitempty"`
	PrevProfileName string    `json:"prev_profile_name,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	OperationID     string    `json:"operation_id"`
	ContentSHA256   string    `json:"content_sha256"`
}

// SystemWriter 安全写入系统 hosts 文件：符号链接拒绝、写前备份、
// 原子替换、写后验证、验证失败自动回滚、按 ID 回滚。
// 语义移植自源项目 mhost-apply 的 HostsWriter。
type SystemWriter struct {
	hostsPath  string
	backupDir  string
	volume     Volume
	mover      Mover
	flusher    Flusher
	now        func() time.Time
	newID      func() string
	maxBackups int
}

// WriterOption 定制 SystemWriter（测试注入故障与固定时钟）。
type WriterOption func(*SystemWriter)

func WithWriterClock(now func() time.Time) WriterOption {
	return func(w *SystemWriter) { w.now = now }
}

func WithWriterIDGenerator(fn func() string) WriterOption {
	return func(w *SystemWriter) { w.newID = fn }
}

func WithWriterMaxBackups(n int) WriterOption {
	return func(w *SystemWriter) { w.maxBackups = n }
}

// NewSystemWriter 创建写入器。hostsPath 通常是 /etc/hosts；
// backupDir 为备份目录（自动创建）。
func NewSystemWriter(hostsPath, backupDir string, volume Volume, opts ...WriterOption) *SystemWriter {
	w := &SystemWriter{
		hostsPath:  hostsPath,
		backupDir:  backupDir,
		volume:     volume,
		mover:      defaultMover(),
		flusher:    NoopFlusher{},
		now:        func() time.Time { return time.Now().UTC() },
		newID:      newWriterID,
		maxBackups: MaxBackups,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// SetMover / SetFlusher 供平台层替换实现（提权移动、真实 DNS flush）。
func (w *SystemWriter) SetMover(m Mover)     { w.mover = m }
func (w *SystemWriter) SetFlusher(f Flusher) { w.flusher = f }
func (w *SystemWriter) HostsPath() string    { return w.hostsPath }
func (w *SystemWriter) BackupDir() string    { return w.backupDir }

// Apply 把 resolved rules 写入管理块。流程与源项目一致：
// 确保常规文件 → 读当前内容 → 写前备份 → 构建新内容 → 原子替换 →
// flush DNS（不致命）→ 读回验证 → 验证失败自动从备份回滚。
// meta 登记到备份索引（profile 信息、上一个 applied profile）。
//
// 返回备份记录（backupRequired 时）与写入前的全量内容，
// 供上层记录 AppliedState 与回执中的旧管理块。
func (w *SystemWriter) Apply(rules []ResolvedRule, backupRequired bool, meta BackupRecord) (*BackupRecord, []byte, error) {
	if err := w.ensureRegularFile(); err != nil {
		return nil, nil, err
	}
	current, err := w.volume.ReadFile(w.hostsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read hosts file: %w", err)
	}

	var backup *BackupRecord
	if backupRequired {
		backup, err = w.createBackup(current, meta)
		if err != nil {
			return nil, current, fmt.Errorf("%w: %v", ErrBackup, err)
		}
	}

	newContent := BuildHostsContent(string(current), FormatManagedBlock(rulesFromResolved(rules)))
	if err := w.atomicWrite([]byte(newContent)); err != nil {
		return backup, current, err
	}

	// DNS 缓存刷新失败不阻断 apply（源项目仅记录日志）
	_ = w.flusher.FlushDNS()

	// 读回验证：重新读盘（源项目 verification::verify），
	// 失败时自动从备份回滚并再次读回比对。
	written, err := w.volume.ReadFile(w.hostsPath)
	if err != nil {
		return backup, current, fmt.Errorf("%w: read: %v", ErrReadback, err)
	}
	if err := Verify(string(written), rules); err != nil {
		verifyErr := fmt.Errorf("%w: %v", ErrReadback, err)
		if backup != nil {
			if rbErr := w.restoreBackupFile(backup); rbErr != nil {
				return backup, current, fmt.Errorf("%w; rollback also failed: %v", verifyErr, rbErr)
			}
			restored, rerr := w.volume.ReadFile(w.hostsPath)
			if rerr != nil {
				return backup, current, fmt.Errorf("%w; rollback readback failed: %v", verifyErr, rerr)
			}
			want, rerr := w.readBackupContent(backup)
			if rerr != nil {
				return backup, current, fmt.Errorf("%w; rollback backup unreadable: %v", verifyErr, rerr)
			}
			if string(restored) != string(want) {
				return backup, current, fmt.Errorf("%w; rollback content mismatch", verifyErr)
			}
		}
		return backup, current, verifyErr
	}

	return backup, current, nil
}

// RollbackTo 按备份 ID 恢复系统 hosts：定位记录 → 读备份内容 →
// 原子写回 → 读回比对（源项目 rollback 语义 + 按 ID 选择，
// 源项目只支持"最近一份"，这里按契约支持指定备份）。
func (w *SystemWriter) RollbackTo(backupID string) (*BackupRecord, error) {
	if err := w.ensureRegularFile(); err != nil {
		return nil, err
	}
	record, err := w.findBackup(backupID)
	if err != nil {
		return nil, err
	}
	content, err := w.readBackupContent(record)
	if err != nil {
		return record, err
	}
	if err := w.atomicWrite(content); err != nil {
		return record, err
	}
	written, err := w.volume.ReadFile(w.hostsPath)
	if err != nil {
		return record, fmt.Errorf("readback after rollback: %w", err)
	}
	if string(written) != string(content) {
		return record, errors.New("rollback content mismatch")
	}
	_ = w.flusher.FlushDNS()
	return record, nil
}

// ListBackups 返回备份记录，最新在前。
func (w *SystemWriter) ListBackups() ([]BackupRecord, error) {
	index, err := w.readIndex()
	if err != nil {
		return nil, err
	}
	sort.Slice(index, func(i, j int) bool { return index[i].CreatedAt.After(index[j].CreatedAt) })
	return index, nil
}

// CreateBackupForProfile 写前备份并登记 profile 信息（Apply 内部使用
// 裸 createBackup，service 层补齐元数据时用本方法）。
func (w *SystemWriter) CreateBackupForProfile(content []byte, meta BackupRecord) (*BackupRecord, error) {
	return w.createBackup(content, meta)
}

// CurrentContent 读取当前系统 hosts 内容。
func (w *SystemWriter) CurrentContent() ([]byte, error) {
	return w.volume.ReadFile(w.hostsPath)
}

// ensureRegularFile 拒绝符号链接与非常规文件（源项目 #19 安全修复）。
func (w *SystemWriter) ensureRegularFile() error {
	info, err := os.Lstat(w.hostsPath)
	if err != nil {
		return fmt.Errorf("stat hosts file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("'%s' is not a regular file (may be a symlink or special file). Refusing to write", w.hostsPath)
	}
	return nil
}

// atomicWrite 临时文件 + Mover 落位（源项目 tempfile + elevated_move）。
// 临时文件创建在目标同目录，rename 天然原子；目标目录不可写（如
// /etc）时回落到用户临时目录暂存，由平台 Mover（darwin 提权安装）
// 负责落位。失败时目标保持原样。
func (w *SystemWriter) atomicWrite(content []byte) error {
	dir := filepath.Dir(w.hostsPath)
	tmp, err := os.CreateTemp(dir, ".mhost-hosts-*")
	if err != nil {
		if !os.IsPermission(err) {
			return fmt.Errorf("create temp file: %w", err)
		}
		tmp, err = os.CreateTemp("", ".mhost-hosts-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
	}
	tmpPath := tmp.Name()
	// 清理兜底：Move 成功后文件已不存在，Remove 忽略 not-exist。
	defer func() { _ = os.Remove(tmpPath) }()

	// 保留原文件权限位（rename 替换 inode 后会丢失原 mode）
	if info, err := os.Stat(w.hostsPath); err == nil {
		_ = tmp.Chmod(info.Mode().Perm())
	} else {
		_ = tmp.Chmod(0o644)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := w.mover.Move(tmpPath, w.hostsPath); err != nil {
		return fmt.Errorf("move temp file into place: %w", err)
	}
	return nil
}

const backupIndexFile = "index.json"

// createBackup 备份全量内容（0600）并更新索引、裁剪旧备份。
func (w *SystemWriter) createBackup(content []byte, meta BackupRecord) (*BackupRecord, error) {
	if err := w.volume.MkdirAll(w.backupDir, 0o700); err != nil {
		return nil, err
	}
	id := w.newID()
	record := BackupRecord{
		ID:              id,
		File:            "hosts-" + id + ".bak",
		ProfileID:       meta.ProfileID,
		ProfileName:     meta.ProfileName,
		PrevProfileID:   meta.PrevProfileID,
		PrevProfileName: meta.PrevProfileName,
		CreatedAt:       w.now(),
		OperationID:     meta.OperationID,
		ContentSHA256:   sha256Hex(content),
	}
	if err := w.volume.WriteAtomic(filepath.Join(w.backupDir, record.File), content, 0o600); err != nil {
		return nil, err
	}
	if err := w.appendIndex(record); err != nil {
		return nil, err
	}
	if err := w.pruneBackups(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (w *SystemWriter) readBackupContent(record *BackupRecord) ([]byte, error) {
	return w.volume.ReadFile(filepath.Join(w.backupDir, record.File))
}

func (w *SystemWriter) restoreBackupFile(record *BackupRecord) error {
	content, err := w.readBackupContent(record)
	if err != nil {
		return err
	}
	return w.atomicWrite(content)
}

func (w *SystemWriter) findBackup(backupID string) (*BackupRecord, error) {
	index, err := w.readIndex()
	if err != nil {
		return nil, err
	}
	for i := range index {
		if index[i].ID == backupID {
			return &index[i], nil
		}
	}
	return nil, fmt.Errorf("no apply backup with ID %q", backupID)
}

func (w *SystemWriter) readIndex() ([]BackupRecord, error) {
	data, err := w.volume.ReadFile(filepath.Join(w.backupDir, backupIndexFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read backup index: %w", err)
	}
	var index []BackupRecord
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode backup index: %w", err)
	}
	return index, nil
}

func (w *SystemWriter) appendIndex(record BackupRecord) error {
	index, err := w.readIndex()
	if err != nil {
		return err
	}
	index = append(index, record)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return w.volume.WriteAtomic(filepath.Join(w.backupDir, backupIndexFile), data, 0o600)
}

// pruneBackups 按创建时间保留最近 maxBackups 份（源项目 MAX_BACKUPS=10，
// 按 mtime；这里用索引里的 CreatedAt，确定性更好）。
func (w *SystemWriter) pruneBackups() error {
	index, err := w.readIndex()
	if err != nil {
		return err
	}
	if len(index) <= w.maxBackups {
		return nil
	}
	sort.Slice(index, func(i, j int) bool { return index[i].CreatedAt.Before(index[j].CreatedAt) })
	drop := index[:len(index)-w.maxBackups]
	keep := index[len(index)-w.maxBackups:]
	for _, record := range drop {
		if err := w.volume.Remove(filepath.Join(w.backupDir, record.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune backup %s: %w", record.ID, err)
		}
	}
	data, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return err
	}
	return w.volume.WriteAtomic(filepath.Join(w.backupDir, backupIndexFile), data, 0o600)
}

func rulesFromResolved(resolved []ResolvedRule) []Rule {
	var rules []Rule
	for _, r := range resolved {
		ip := r.IP
		rules = append(rules, Rule{IP: &ip, Domains: []string{r.Domain}, Enabled: true})
	}
	return rules
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newWriterID() string {
	now := time.Now().UTC()
	return fmt.Sprintf("%d", now.UnixNano())
}
