//go:build darwin

package hostsfile

import (
	"errors"
	"os"
	"syscall"

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
	} else if !os.IsPermission(err) && !errors.Is(err, syscall.EXDEV) {
		// 权限/跨卷（如 TMPDIR 在其他卷，rename 返回 EXDEV）失败可经
		// 提权脚本（cat，天然跨卷）救回；其余失败提权同样无效，
		// 直接返回避免无谓的密码弹窗。
		return err
	}
	return elevate.Install(tempPath, target)
}
