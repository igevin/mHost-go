package hostssvc

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/hostsfile"
	"github.com/igevin/mHost-go/internal/storage"
)

// 本文件是 #16 契约之外的管理面操作（#25 Alpha 需要）：
// Profile 新建/复制/启停/删除。它们不改变 HostsService 的
// Preview/Apply/Rollback 语义；desktop 通过 ProfileAdmin 可选接口调用。

// ProfileAdmin 是 HostsWorkbench 对宿主服务的可选扩展要求：
// 提供 Profile 管理操作。demo 服务不实现它（管理按钮隐藏）。
type ProfileAdmin interface {
	// CreateProfile 新建 hosts profile（内容为 hosts 文本）。
	CreateProfile(ctx context.Context, name, content string) (app.HostsProfileDetail, error)
	// DuplicateProfile 复制现有 profile（含规则，名称加后缀）。
	DuplicateProfile(ctx context.Context, profileID string) (app.HostsProfileDetail, error)
	// SetProfileEnabled 切换启用标记（存储态，不代表已应用）。
	SetProfileEnabled(ctx context.Context, profileID string, enabled bool) (app.HostsProfileDetail, error)
	// DeleteProfile 删除 profile（受保护 profile 拒绝）。
	DeleteProfile(ctx context.Context, profileID string) error
	// ExportProfile 把 profile 内容导出为 <名称>.hosts 到 dir，返回路径。
	ExportProfile(ctx context.Context, profileID, dir string) (string, error)
	// ImportProfileFile 从 .hosts 文件创建 profile。
	ImportProfileFile(ctx context.Context, path string) (app.HostsProfileDetail, error)
	// SystemHostsContent 返回系统 hosts 当前内容（只读视图）。
	SystemHostsContent(ctx context.Context) (string, error)
}

// CreateProfile 新建 profile。名称去空白后不得为空；内容必须是
// 可解析的 hosts 文本（与 SaveProfile 同样的校验）。
func (s *Service) CreateProfile(ctx context.Context, name, content string) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	if strings.TrimSpace(name) == "" {
		return app.HostsProfileDetail{}, &app.UIError{Kind: app.ErrorValidation, Message: "Profile name is required"}
	}
	parsed := hostsfile.Parse(content)
	if len(parsed.Errors) > 0 {
		return app.HostsProfileDetail{}, parseErrorsError(parsed.Errors)
	}
	now := s.now().UTC()
	profile := storage.Profile{
		ID:        s.newID(),
		Name:      strings.TrimSpace(name),
		Enabled:   true,
		Tags:      []string{},
		Rules:     rulesToStorage(parsed.Rules),
		Mode:      storage.ModeHosts,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.writeProfile(profile); err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: s.toAppProfile(profile),
		Content:      profileContent(profile),
	}, nil
}

// DuplicateProfile 复制 profile；名称冲突时追加 " copy"/" copy 2"…
func (s *Service) DuplicateProfile(ctx context.Context, profileID string) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	source, err := s.loadProfile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	all, err := s.loadAllProfiles()
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	name := s.uniqueCopyName(source.Name, all)

	now := s.now().UTC()
	copy_ := source
	copy_.ID = s.newID()
	copy_.Name = name
	copy_.Protected = false
	copy_.CreatedAt = now
	copy_.UpdatedAt = now
	copy_.Rules = make([]storage.HostRule, len(source.Rules))
	for i, rule := range source.Rules {
		copy_.Rules[i] = rule
		copy_.Rules[i].ID = newUUID() // 规则 ID 唯一性
	}
	if err := s.writeProfile(copy_); err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: s.toAppProfile(copy_),
		Content:      profileContent(copy_),
	}, nil
}

func (s *Service) uniqueCopyName(base string, all []storage.Profile) string {
	existing := map[string]bool{}
	for _, profile := range all {
		existing[profile.Name] = true
	}
	candidate := base + " copy"
	for i := 2; existing[candidate]; i++ {
		candidate = fmt.Sprintf("%s copy %d", base, i)
	}
	return candidate
}

// SetProfileEnabled 切换启用标记。应用态以 /etc/hosts 管理块为准
// （AppliedState），这里的 Enabled 是存储态语义（与源项目一致：
// Enable 不等于已写入系统）。
func (s *Service) SetProfileEnabled(ctx context.Context, profileID string, enabled bool) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	profile, err := s.loadProfile(profileID)
	if err != nil {
		return app.HostsProfileDetail{}, err
	}
	profile.Enabled = enabled
	profile.UpdatedAt = s.now().UTC()
	if err := s.writeProfile(profile); err != nil {
		return app.HostsProfileDetail{}, err
	}
	return app.HostsProfileDetail{
		HostsProfile: s.toAppProfile(profile),
		Content:      profileContent(profile),
	}, nil
}

// DeleteProfile 删除 profile。受保护 profile 拒绝（ErrorValidation）；
// 当前正被应用的 profile 也拒绝——删除会让系统 hosts 里的管理块
// 失去归属而无提示（review 建议项），须先 Rollback 或应用其他 profile。
func (s *Service) DeleteProfile(ctx context.Context, profileID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	profile, err := s.loadProfile(profileID)
	if err != nil {
		return err
	}
	if profile.Protected {
		return &app.UIError{Kind: app.ErrorValidation, Message: fmt.Sprintf("Profile %q is protected and cannot be deleted", profile.Name)}
	}
	if state, serr := s.readAppliedState(); serr == nil && state.ProfileID == profileID {
		return &app.UIError{
			Kind:    app.ErrorValidation,
			Message: fmt.Sprintf("Profile %q is currently applied; roll back or apply another profile before deleting it", profile.Name),
		}
	}
	if err := s.volume.Remove(filepath.Join(s.profilesDir(), profile.ID+".json")); err != nil {
		return fmt.Errorf("delete profile %s: %w", profile.ID, err)
	}
	s.dropDraftPlans(profileID)
	return nil
}

// dropDraftPlans 删除某 profile 的未消费计划，避免删除后计划悬空。
func (s *Service) dropDraftPlans(profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, plan := range s.plans {
		if plan.profileID == profileID {
			delete(s.plans, id)
		}
	}
}

var _ ProfileAdmin = (*Service)(nil)
