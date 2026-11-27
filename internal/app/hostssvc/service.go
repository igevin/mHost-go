// Package hostssvc 是 app.HostsService 的真实实现（issue #4）：
// Profile 读写落在 v2 工作区存储（JSON），Apply/Rollback 通过
// hostsfile.SystemWriter 改写系统 hosts 管理块。交互语义与
// internal/app/demo 的内存实现保持一致（#16 契约不得削弱），
// 解析/合并/写入语义与源项目 mHost v0.4.2 一致（hostsfile 包 + 差分测试）。
package hostssvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

// Service 实现 app.HostsService。Apply Lock（applyMu）串行化系统
// hosts 写入；plan 的创建/过期校验由 mu 保护。
type Service struct {
	root      string
	volume    hostsfile.Volume
	writer    *hostsfile.SystemWriter
	now       func() time.Time
	newID     func() string
	operation *operationIDGen

	mu    sync.Mutex
	plans map[string]*applyPlan
	seq   uint64

	applyMu sync.Mutex // Apply Lock：串行化 Apply/Rollback 的系统写入
}

// Option 定制 Service（测试固定时钟/ID/写入器）。
type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func WithIDGenerator(fn func() string) Option {
	return func(s *Service) { s.newID = fn }
}

// WithWriter 覆盖默认 SystemWriter（测试注入故障写入器）。
func WithWriter(w *hostsfile.SystemWriter) Option {
	return func(s *Service) { s.writer = w }
}

// New 创建服务。root 是 v2 工作区根（profiles/hosts/*.json 所在处）；
// hostsPath 是系统 hosts 文件（生产为 /etc/hosts）；backupDir 存
// apply 备份与 applied-state。
func New(root, hostsPath, backupDir string, volume hostsfile.Volume, opts ...Option) *Service {
	s := &Service{
		root:   root,
		volume: volume,
		writer: hostsfile.NewSystemWriter(hostsPath, backupDir, volume),
		now:    func() time.Time { return time.Now().UTC() },
		newID:  newUUID,
		plans:  map[string]*applyPlan{},
	}
	s.operation = &operationIDGen{next: s.nextSeq}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) nextSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}

type operationIDGen struct {
	next func() uint64
}

func (g *operationIDGen) new() string {
	return fmt.Sprintf("op-%d", g.next())
}

// profilesDir 是 hosts profile 的 v2 存储目录。
func (s *Service) profilesDir() string {
	return filepath.Join(s.root, storage.DirProfiles, string(storage.ModeHosts))
}

// ListProfiles 返回工作区内全部 hosts profile（按名称排序）。
func (s *Service) ListProfiles(ctx context.Context) ([]app.HostsProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profiles, err := s.loadAllProfiles()
	if err != nil {
		return nil, err
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	out := make([]app.HostsProfile, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, s.toAppProfile(profile))
	}
	return out, nil
}

// LoadProfile 返回 profile 内容与修订号。内容是把存储规则按
// 源项目 formatter 语义渲染的 hosts 文本（禁用规则渲染为注释行）。
func (s *Service) LoadProfile(ctx context.Context, profileID string) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	profile, err := s.loadProfile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: s.toAppProfile(profile),
		Content:      profileContent(profile),
	}, nil
}

// SaveProfile 把编辑后的 hosts 文本解析为结构化规则落盘。
// 与 demo 语义不同的强化点：含解析错误的文本在此拒绝保存
// （返回 ErrorValidation 并逐行列出原因），保证存储内永远是
// 合法规则；PreviewApply 的 Blockers 作为防御性兜底保留。
func (s *Service) SaveProfile(ctx context.Context, profileID string, content string, baseRevision uint64) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	profile, err := s.loadProfile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	if profileRevision(profile) != baseRevision {
		return app.HostsProfileDetail{}, app.ErrStaleOperation
	}

	parsed := hostsfile.Parse(content)
	if len(parsed.Errors) > 0 {
		return app.HostsProfileDetail{}, parseErrorsError(parsed.Errors)
	}

	profile.Rules = rulesToStorage(parsed.Rules)
	profile.UpdatedAt = s.now()
	if err := s.writeProfile(profile); err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: s.toAppProfile(profile),
		Content:      profileContent(profile),
	}, nil
}

// --- profile 存储 ---

func (s *Service) loadAllProfiles() ([]storage.Profile, error) {
	entries, err := filepath.Glob(filepath.Join(s.profilesDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	var profiles []storage.Profile
	for _, path := range entries {
		profile, err := s.readProfileFile(path)
		if err != nil {
			// 单个损坏文件不应拖垮列表：跳过并继续（问题在存储层可见）
			continue
		}
		if profile.Mode == storage.ModeHosts {
			profiles = append(profiles, profile)
		}
	}
	return profiles, nil
}

func (s *Service) loadProfile(profileID string) (storage.Profile, error) {
	if !validUUID(profileID) {
		return storage.Profile{}, notFoundError(profileID)
	}
	path := filepath.Join(s.profilesDir(), profileID+".json")
	profile, err := s.readProfileFile(path)
	if err != nil {
		return storage.Profile{}, notFoundError(profileID)
	}
	if profile.Mode != storage.ModeHosts {
		return storage.Profile{}, notFoundError(profileID)
	}
	return profile, nil
}

func (s *Service) readProfileFile(path string) (storage.Profile, error) {
	data, err := s.volume.ReadFile(path)
	if err != nil {
		return storage.Profile{}, err
	}
	var profile storage.Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return storage.Profile{}, err
	}
	return profile, nil
}

func (s *Service) writeProfile(profile storage.Profile) error {
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return s.volume.WriteAtomic(filepath.Join(s.profilesDir(), profile.ID+".json"), data, storage.FilePerm)
}

func (s *Service) toAppProfile(profile storage.Profile) app.HostsProfile {
	return app.HostsProfile{
		ID:        profile.ID,
		Name:      profile.Name,
		Enabled:   profile.Enabled,
		Protected: profile.Protected,
		RuleCount: countEnabledRules(profile),
		Revision:  profileRevision(profile),
	}
}

// profileRevision 用 UpdatedAt 纳秒时间戳作为乐观锁修订号。
func profileRevision(profile storage.Profile) uint64 {
	return uint64(profile.UpdatedAt.UnixNano())
}

func countEnabledRules(profile storage.Profile) int {
	count := 0
	for _, rule := range profile.Rules {
		if rule.Enabled && rule.IP != nil && len(rule.Domains) > 0 {
			count++
		}
	}
	return count
}

// profileContent 把存储规则渲染为 hosts 文本：
// 启用规则正常渲染，禁用规则渲染为注释行（文本模型里"禁用=注释"，
// 与解析侧 `# ip domain` → 注释占位规则 互为往返）。
func profileContent(profile storage.Profile) string {
	var rules []hostsfile.Rule
	for _, rule := range profile.Rules {
		if rule.IP == nil {
			comment := "#"
			if rule.Comment != nil && *rule.Comment != "" {
				text := *rule.Comment
				if !strings.HasPrefix(text, "#") {
					text = "# " + text
				}
				comment = text
			}
			rules = append(rules, hostsfile.Rule{Enabled: false, Comment: &comment, CommentOnly: true})
			continue
		}
		ip := *rule.IP
		rules = append(rules, hostsfile.Rule{
			IP:      &ip,
			Domains: append([]string(nil), rule.Domains...),
			Enabled: rule.Enabled,
			Comment: rule.Comment,
		})
	}
	return hostsfile.FormatRules(rules)
}

// rulesToStorage 把解析出的文本规则转为 v2 存储模型。
// 注释占位规则保留原文（禁用规则因此不丢）；新规则来源记为 Manual。
func rulesToStorage(parsed []hostsfile.Rule) []storage.HostRule {
	rules := make([]storage.HostRule, 0, len(parsed))
	for _, rule := range parsed {
		if rule.CommentOnly {
			comment := ""
			if rule.Comment != nil {
				comment = *rule.Comment
			}
			rules = append(rules, storage.HostRule{
				ID:      newUUID(),
				Domains: []string{},
				Enabled: false,
				Comment: &comment,
				Source:  storage.RuleSource{Type: "Manual"},
			})
			continue
		}
		rules = append(rules, storage.HostRule{
			ID:      newUUID(),
			IP:      rule.IP,
			Domains: append([]string(nil), rule.Domains...),
			Enabled: rule.Enabled,
			Comment: rule.Comment,
			Source:  storage.RuleSource{Type: "Manual"},
		})
	}
	return rules
}

func parseErrorsError(errs []hostsfile.ParseError) error {
	lines := make([]string, 0, len(errs))
	for _, err := range errs {
		lines = append(lines, err.Error())
	}
	return &app.UIError{
		Kind:    app.ErrorValidation,
		Message: fmt.Sprintf("Content has %d parse error(s); fix them before saving", len(errs)),
		Detail:  strings.Join(lines, "\n"),
	}
}

func notFoundError(profileID string) error {
	return &app.UIError{
		Kind:    app.ErrorNotFound,
		Message: fmt.Sprintf("No hosts profile with ID %q", profileID),
	}
}

// validUUID 做格式校验（8-4-4-4-12 十六进制），与存储层一致。
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// newUUID 生成随机 UUID v4（与 storage.newUUID 同法）。
func newUUID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	var buf [36]byte
	hex.Encode(buf[0:8], id[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], id[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], id[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], id[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], id[10:16])
	return string(buf[:])
}
