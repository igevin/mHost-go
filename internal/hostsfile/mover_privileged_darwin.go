//go:build darwin

package hostsfile

import (
	"os"

	"github.com/igevin/mHost-go/internal/elevate"
)

// PrivilegedMover 是 darwin 的默认 Mover：先尝试同盘 rename
// （root 或目标目录可写时直接成功），权限不足时经 osascript
// 请求管理员授权安装。提权脚本在 同一授权会话 内顺带刷新 DNS
// 缓存，避免安装+刷新两次弹窗。
type PrivilegedMover struct{}

func (PrivilegedMover) Move(tempPath, target string) error {
	if err := os.Rename(tempPath, target); err == nil {
		return nil
	}
	return elevate.Install(tempPath, target)
}
