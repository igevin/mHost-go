//go:build darwin

package elevate

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"time"
)

// Install 请求 macOS 管理员授权，把 src 原子安装到 target
// （保留 target 原权限位），落位成功后尽力刷新 DNS 缓存。
// mode 取 target 现有权限位；stat 失败回退 0644。
func Install(src, target string) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	// 人类输密码不设短超时（脚本内 with timeout of 0 seconds），
	// 但 exec 整体保留 5 分钟宽松上限：用户长时间不理会授权框时
	// 返回错误，Apply 不会永久持有 applyMu 阻塞后续操作。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return InstallWith(func(name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		return cmd.CombinedOutput()
	}, src, target, mode)
}
