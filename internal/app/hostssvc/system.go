package hostssvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	app "github.com/igevin/mHost-go/internal/app"
)

// SystemHostsContent 返回系统 hosts 当前全量内容（只读视图用）。
// 读取失败映射为 UIError。
func (s *Service) SystemHostsContent(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content, err := s.writer.CurrentContent()
	if err != nil {
		return "", &app.UIError{Kind: app.ErrorPermission, Message: "Cannot read system hosts file", Detail: err.Error(), Cause: err}
	}
	return string(content), nil
}

// ExportProfile 把 profile 内容导出为 hosts 文本文件到 dir，
// 文件名 <profile 名>.hosts（非法文件名字符替换为 '_'）。
// 返回写出路径。已存在时覆盖（导出语义为最新快照）。
func (s *Service) ExportProfile(ctx context.Context, profileID, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	detail, err := s.LoadProfile(ctx, profileID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dir) == "" {
		return "", &app.UIError{Kind: app.ErrorInvalidPath, Message: "Choose an export directory"}
	}
	name := sanitizeFileName(detail.Name)
	if name == "" {
		name = "profile"
	}
	path := filepath.Join(dir, name+".hosts")
	if err := os.WriteFile(path, []byte(detail.Content), 0o644); err != nil {
		return "", &app.UIError{Kind: app.ErrorWrite, Message: "Export failed", Path: path, Detail: err.Error(), Cause: err}
	}
	return path, nil
}

// sanitizeFileName 保留 Unicode 字母与数字（中文名不再整体变 _，
// review 建议项），仅替换路径分隔符、控制字符与文件系统保留字符。
func sanitizeFileName(name string) string {
	var out strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			out.WriteRune(r)
		case r == '-', r == '_', r == '.', r == ' ':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	return strings.Trim(out.String(), ". ")
}

// ImportProfileFile 从 path 读取 hosts 文本并创建 profile。
// 名称取文件名去扩展名；内容校验与 CreateProfile 一致。
func (s *Service) ImportProfileFile(ctx context.Context, path string) (app.HostsProfileDetail, error) {
	if err := ctx.Err(); err != nil {
		return app.HostsProfileDetail{}, err
	}
	if !strings.HasSuffix(strings.ToLower(path), ".hosts") {
		return app.HostsProfileDetail{}, &app.UIError{Kind: app.ErrorValidation, Message: "Choose a .hosts file"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return app.HostsProfileDetail{}, &app.UIError{Kind: app.ErrorInvalidPath, Message: "Cannot read the file", Path: path, Detail: err.Error(), Cause: err}
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.TrimSpace(name) == "" {
		name = "Imported"
	}
	detail, err := s.CreateProfile(ctx, name, string(data))
	if err != nil {
		return app.HostsProfileDetail{}, fmt.Errorf("import %s: %w", path, err)
	}
	return detail, nil
}
