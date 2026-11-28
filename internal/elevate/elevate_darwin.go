//go:build darwin

package elevate

import "os/exec"

// Install 请求 macOS 管理员授权，把 src 内容安装到 target
// （截断写，保留 inode/权限），并刷新 DNS 缓存。
func Install(src, target string) error {
	return InstallWith(func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		return cmd.CombinedOutput()
	}, src, target)
}
