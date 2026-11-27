package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
)

// 存储布局常量：所有路径都相对 Workspace.Root。
const (
	FileManifest      = "manifest.json"
	DirProfiles       = "profiles"
	DirSnapshots      = "snapshots"
	FileSnapshotIndex = "snapshots/index.json"
	FileAdBlock       = "adblock.json"
	FileSettings      = "settings.json"
	DirBackups        = "backups"
	FileBackupIndex   = "backups/index.json"
)

// Issue code 常量，供扫描与写入路径共用，服务层原样映射到 UI。
const (
	CodeInvalidJSON    = "invalid_json"
	CodeMissingFields  = "missing_fields"
	CodeWrongType      = "wrong_type"
	CodeNotDirectory   = "not_directory"
	CodeReadFailed     = "read_failed"
	CodeBackupFailed   = "backup_failed"
	CodeWriteFailed    = "write_failed"
	CodeReadbackFailed = "readback_mismatch"
)

// ScanIssue 是扫描期产生的逐文件问题。
type ScanIssue = app.StorageIssue

// ProfileEntry 是一个解析成功的 Profile 及其相对路径。
type ProfileEntry struct {
	RelPath string
	Profile Profile
}

// SnapshotEntry 是一个解析成功的快照及其元数据。
type SnapshotEntry struct {
	RelPath string
	Meta    SnapshotMeta
}

// ScanResult 是一次目录扫描的完整结果：语义明确的数据与
// 逐文件问题分开，调用方决定哪些错误是致命的、哪些只是局部损坏。
type ScanResult struct {
	Version   int
	Manifest  *Manifest
	Profiles  []ProfileEntry
	Snapshots []SnapshotEntry
	AdBlock   *AdBlockState
	Settings  *Settings
	Issues    []ScanIssue
}

// ProfileCount 返回两种模式目录下的 Profile 总数。
func (r *ScanResult) ProfileCount() int { return len(r.Profiles) }

// HasIssues 返回是否存在 error 级问题。
func (r *ScanResult) HasErrorIssues() bool {
	for _, issue := range r.Issues {
		if issue.Severity == app.SeverityError {
			return true
		}
	}
	return false
}

// Store 在指定根目录上执行只读扫描、树复制与备份登记；
// 所有写路径都通过 Volume 走原子写入。
type Store struct {
	vol Volume
}

func NewStore(vol Volume) *Store {
	return &Store{vol: vol}
}

// Scan 读取根目录并解析全部存储组件。manifest 缺失/损坏/版本
// 不受支持是致命错误（无法判定存储版本）；其余组件的问题以
// ScanIssue 形式局部上报，不阻断有效数据的读取。
func (s *Store) Scan(root string) (ScanResult, error) {
	result := ScanResult{Profiles: []ProfileEntry{}, Snapshots: []SnapshotEntry{}, Issues: []ScanIssue{}}
	info, err := os.Stat(root)
	if err != nil {
		if IsNotExist(err) {
			return result, fmt.Errorf("scan root: %w", err)
		}
		return result, fmt.Errorf("scan root %q: %w", root, err)
	}
	if !info.IsDir() {
		return result, fmt.Errorf("scan root %q is not a directory", root)
	}

	manifest, err := s.readManifest(root)
	if err != nil {
		return result, err
	}
	result.Manifest = manifest
	result.Version = manifest.Version

	s.scanProfiles(root, ModeHosts, &result)
	s.scanProfiles(root, ModeDNS, &result)
	s.scanSnapshots(root, &result)
	s.scanAdBlock(root, &result)
	s.scanSettings(root, &result)

	sort.Slice(result.Issues, func(i, j int) bool {
		if result.Issues[i].Path == result.Issues[j].Path {
			return result.Issues[i].Message < result.Issues[j].Message
		}
		return result.Issues[i].Path < result.Issues[j].Path
	})
	return result, nil
}

func (s *Store) readManifest(root string) (*Manifest, error) {
	rel := FileManifest
	data, err := s.vol.ReadFile(filepath.Join(root, rel))
	if err != nil {
		if IsNotExist(err) {
			return nil, validationIssueError(rel, "manifest.json is missing; not an mHost storage directory")
		}
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	var manifest Manifest
	if err := unmarshalModel(data, &manifest); err != nil {
		return nil, modelIssueError(rel, err)
	}
	if manifest.Version != 1 && manifest.Version != 2 {
		return nil, validationIssueError(rel, fmt.Sprintf("unsupported storage version %d", manifest.Version))
	}
	return &manifest, nil
}

// parseProfile 解析单个 profile 文件；v1 文件没有 mode 字段时按所在
// 目录推断模式（profiles/dns/ 下即 dns），避免迁移 round-trip 把
// DNS profile 误写成 hosts。
func parseProfile(data []byte, directoryMode ProfileMode) (Profile, error) {
	var profile Profile
	if err := unmarshalModel(data, &profile); err != nil {
		return profile, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err == nil {
		if _, ok := fields["mode"]; !ok {
			profile.Mode = directoryMode
		}
	}
	return profile, nil
}

// directoryModeForProfile 从相对路径推断目录对应的模式。
func directoryModeForProfile(rel string) ProfileMode {
	if strings.HasPrefix(rel, filepath.Join(DirProfiles, string(ModeDNS))) {
		return ModeDNS
	}
	return ModeHosts
}

func (s *Store) scanProfiles(root string, mode ProfileMode, result *ScanResult) {
	dir := filepath.Join(DirProfiles, string(mode))
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		if !IsNotExist(err) {
			result.Issues = append(result.Issues, scanIssue(CodeReadFailed, filepath.Join(dir), err))
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		rel := filepath.Join(dir, entry.Name())
		data, err := s.vol.ReadFile(filepath.Join(root, rel))
		if err != nil {
			result.Issues = append(result.Issues, scanIssue(CodeReadFailed, rel, err))
			continue
		}
		profile, err := parseProfile(data, mode)
		if err != nil {
			result.Issues = append(result.Issues, modelScanIssue(rel, err))
			continue
		}
		result.Profiles = append(result.Profiles, ProfileEntry{RelPath: rel, Profile: profile})
	}
}

func (s *Store) scanSnapshots(root string, result *ScanResult) {
	data, err := s.vol.ReadFile(filepath.Join(root, FileSnapshotIndex))
	if err != nil && !IsNotExist(err) {
		result.Issues = append(result.Issues, scanIssue(CodeReadFailed, FileSnapshotIndex, err))
	}
	// index 缺失或损坏恰恰是孤儿产生的典型场景（登记/写出失败），
	// 此时以空索引继续跑孤儿扫描，让残留文件可见（review 建议项）。
	index := SnapshotIndex{}
	if err == nil {
		if uerr := unmarshalModel(data, &index); uerr != nil {
			result.Issues = append(result.Issues, modelScanIssue(FileSnapshotIndex, uerr))
		}
	}
	for _, meta := range index.Snapshots {
		rel := filepath.Join(DirSnapshots, meta.ID+".json")
		data, err := s.vol.ReadFile(filepath.Join(root, rel))
		if err != nil {
			issue := scanIssue(CodeReadFailed, rel, err)
			if IsNotExist(err) {
				issue = ScanIssue{Code: "missing_snapshot", Severity: app.SeverityError, Path: rel, Message: fmt.Sprintf("snapshot %s is referenced by the index but its file is missing", meta.ID), Recoverable: false}
			}
			result.Issues = append(result.Issues, issue)
			continue
		}
		var snapshot Snapshot
		if err := unmarshalModel(data, &snapshot); err != nil {
			result.Issues = append(result.Issues, modelScanIssue(rel, err))
			continue
		}
		result.Snapshots = append(result.Snapshots, SnapshotEntry{RelPath: rel, Meta: meta})
	}
	s.scanOrphanSnapshots(root, &index, result)
}

// scanOrphanSnapshots 报告「存在但未被 index 引用」的快照文件
// （#23）：登记失败的写出残留不会被静默丢弃，用户在 Issues 页可见，
// 可自行删除文件后由扫描确认消失。
func (s *Store) scanOrphanSnapshots(root string, index *SnapshotIndex, result *ScanResult) {
	referenced := map[string]bool{}
	for _, meta := range index.Snapshots {
		referenced[meta.ID+".json"] = true
	}
	names, err := s.vol.ReadDir(filepath.Join(root, DirSnapshots))
	if err != nil {
		result.Issues = append(result.Issues, scanIssue(CodeReadFailed, DirSnapshots, err))
		return
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") || name == "index.json" || referenced[name] {
			continue
		}
		rel := filepath.Join(DirSnapshots, name)
		result.Issues = append(result.Issues, ScanIssue{
			Code: "orphan_snapshot", Severity: app.SeverityWarning, Path: rel,
			Message:     fmt.Sprintf("snapshot file %s is not referenced by the index (a registration failed); delete the file to clean up", name),
			Recoverable: true,
		})
	}
}

func (s *Store) scanAdBlock(root string, result *ScanResult) {
	data, err := s.vol.ReadFile(filepath.Join(root, FileAdBlock))
	if err != nil {
		if !IsNotExist(err) {
			result.Issues = append(result.Issues, scanIssue(CodeReadFailed, FileAdBlock, err))
		}
		return
	}
	var state AdBlockState
	if err := unmarshalModel(data, &state); err != nil {
		result.Issues = append(result.Issues, modelScanIssue(FileAdBlock, err))
		return
	}
	result.AdBlock = &state
}

func (s *Store) scanSettings(root string, result *ScanResult) {
	data, err := s.vol.ReadFile(filepath.Join(root, FileSettings))
	if err != nil {
		if !IsNotExist(err) {
			result.Issues = append(result.Issues, scanIssue(CodeReadFailed, FileSettings, err))
		}
		return
	}
	var settings Settings
	if err := unmarshalModel(data, &settings); err != nil {
		result.Issues = append(result.Issues, modelScanIssue(FileSettings, err))
		return
	}
	result.Settings = &settings
}

// CopyTree 把 src 目录树完整复制到 dst（dst 必须不存在或为空），
// 目录 0700、文件 0600，保持相对结构。返回前校验 src 仍是目录。
func (s *Store) CopyTree(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat copy source: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("copy source %q is not a directory", src)
	}
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		return fmt.Errorf("destination %q is not empty", dst)
	} else if err != nil && !IsNotExist(err) {
		return fmt.Errorf("read destination %q: %w", dst, err)
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return s.vol.MkdirAll(target, DirPerm)
		}
		if !entry.Type().IsRegular() {
			// 符号链接等非普通文件不复制，避免把逃逸链接带进工作副本。
			return nil
		}
		return s.vol.Copy(target, path, FilePerm)
	})
}

// backupRecord 是备份登记项；backupFile 是其在 backups/ 下的实际文件名。
type backupRecord struct {
	ID          string    `json:"id"`
	Entity      string    `json:"entity"`
	Path        string    `json:"path"`
	CreatedAt   time.Time `json:"created_at"`
	OperationID string    `json:"operation_id"`
}

type backupIndex struct {
	Backups []backupRecord `json:"backups"`
}

// RecordBackup 把 root 下 rel 指向的现有文件备份进 backups/ 并登记。
// 文件不存在或复制失败时返回错误，调用方必须中止该目标的覆盖写入。
func (s *Store) RecordBackup(root, rel, entity, operationID string, now time.Time, id string) (app.BackupRef, error) {
	source := filepath.Join(root, rel)
	if _, err := os.Stat(source); err != nil {
		return app.BackupRef{}, fmt.Errorf("backup source %s: %w", rel, err)
	}
	if err := s.vol.Copy(filepath.Join(root, DirBackups, id+".json"), source, FilePerm); err != nil {
		return app.BackupRef{}, fmt.Errorf("copy backup for %s: %w", rel, err)
	}
	record := backupRecord{ID: id, Entity: entity, Path: rel, CreatedAt: now, OperationID: operationID}
	index, err := s.loadBackupIndex(root)
	if err != nil {
		return app.BackupRef{}, err
	}
	index.Backups = append(index.Backups, record)
	if err := s.saveBackupIndex(root, index); err != nil {
		return app.BackupRef{}, err
	}
	return backupRef(record), nil
}

// Backups 返回登记的全部备份，新在后。
func (s *Store) Backups(root string) ([]app.BackupRef, error) {
	index, err := s.loadBackupIndex(root)
	if err != nil {
		return nil, err
	}
	refs := make([]app.BackupRef, 0, len(index.Backups))
	for _, record := range index.Backups {
		refs = append(refs, backupRef(record))
	}
	return refs, nil
}

// BackupContent 返回备份登记的原始文件内容。backupID 必须是本服务
// 生成的 UUID，拒绝任何路径分隔符与 .. 越界。
func (s *Store) BackupContent(root, backupID string) ([]byte, error) {
	if !validUUID(backupID) {
		return nil, fmt.Errorf("invalid backup id %q", backupID)
	}
	return s.vol.ReadFile(filepath.Join(root, DirBackups, backupID+".json"))
}

// validBackupRecord 校验登记记录：id 必须是 UUID，path 必须是
// 工作副本内的相对路径（拒绝绝对路径与 .. 逃逸）。备份索引来自
// 磁盘，可能是用户从任意来源复制的目录，加载时统一丢弃非法记录。
func validBackupRecord(record backupRecord) bool {
	if !validUUID(record.ID) {
		return false
	}
	if record.Path == "" || filepath.IsAbs(record.Path) {
		return false
	}
	cleaned := filepath.Clean(record.Path)
	return cleaned != ".." && !strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func (s *Store) loadBackupIndex(root string) (backupIndex, error) {
	index := backupIndex{Backups: []backupRecord{}}
	data, err := s.vol.ReadFile(filepath.Join(root, FileBackupIndex))
	if err != nil {
		if IsNotExist(err) {
			return index, nil
		}
		return index, fmt.Errorf("read backup index: %w", err)
	}
	if err := unmarshalModel(data, &index); err != nil {
		return index, fmt.Errorf("decode backup index: %w", err)
	}
	if index.Backups == nil {
		index.Backups = []backupRecord{}
	}
	// 丢弃非法记录：path 越界或 id 非 UUID 的条目不得进入恢复路径。
	kept := index.Backups[:0]
	for _, record := range index.Backups {
		if validBackupRecord(record) {
			kept = append(kept, record)
		}
	}
	index.Backups = kept
	return index, nil
}

func (s *Store) saveBackupIndex(root string, index backupIndex) error {
	data, err := marshalModel(index)
	if err != nil {
		return fmt.Errorf("encode backup index: %w", err)
	}
	if err := s.vol.WriteAtomic(filepath.Join(root, FileBackupIndex), data, FilePerm); err != nil {
		return fmt.Errorf("write backup index: %w", err)
	}
	return nil
}

func backupRef(record backupRecord) app.BackupRef {
	return app.BackupRef{
		ID:          record.ID,
		WorkspaceID: "",
		Entity:      record.Entity,
		Path:        record.Path,
		CreatedAt:   record.CreatedAt,
		OperationID: record.OperationID,
	}
}

// ValidateDestination 校验工作副本目标路径：不得为空、与源相同、
// 互相嵌套，也不得经由符号链接逃出。返回清洗后的绝对路径对。
func ValidateDestination(sourceRoot, destinationRoot string) (string, error) {
	if strings.TrimSpace(destinationRoot) == "" {
		return "", errors.New("destination is empty")
	}
	sourceAbs, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	destAbs, err := filepath.Abs(destinationRoot)
	if err != nil {
		return "", fmt.Errorf("resolve destination path: %w", err)
	}
	if sourceAbs == destAbs {
		return "", errors.New("destination must differ from the source")
	}
	sourceResolved := resolvePath(sourceAbs)
	destResolved := resolvePath(destAbs)
	if pathWithin(destResolved, sourceResolved) || pathWithin(sourceResolved, destResolved) {
		return "", errors.New("destination must not contain or be contained by the source")
	}
	return destAbs, nil
}

// pathWithin 返回 child 是否等于或在 parent 内部。
func pathWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolvePath 尽量解析符号链接；解析失败时退回原路径，
// 由后续 Stat/复制步骤报错。
func resolvePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func scanIssue(code, rel string, err error) ScanIssue {
	return ScanIssue{Code: code, Severity: app.SeverityError, Path: rel, Message: err.Error(), Detail: err.Error(), Recoverable: true}
}

// modelScanIssue 按错误类别生成问题：语法错误归为 invalid_json，
// 校验错误按缺字段/错类型细分。
func modelScanIssue(rel string, err error) ScanIssue {
	code := CodeInvalidJSON
	message := err.Error()
	switch ClassifyModelError(err) {
	case ModelErrValidation:
		if strings.Contains(message, "missing") || strings.Contains(message, "required") {
			code = CodeMissingFields
		} else {
			code = CodeWrongType
		}
	}
	return ScanIssue{Code: code, Severity: app.SeverityError, Path: rel, Message: message, Detail: err.Error(), Recoverable: true}
}

func validationIssueError(rel, message string) error {
	return &app.UIError{Kind: app.ErrorValidation, Path: rel, Message: message, Retryable: false}
}

func modelIssueError(rel string, err error) error {
	kind := app.ErrorValidation
	if ClassifyModelError(err) == ModelErrSyntax {
		kind = app.ErrorInvalidJSON
	}
	return &app.UIError{Kind: kind, Path: rel, Message: err.Error(), Detail: err.Error(), Cause: err}
}
