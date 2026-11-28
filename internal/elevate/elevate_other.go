//go:build !darwin

package elevate

import (
	"errors"
	"runtime"
)

// Install 在非 darwin 平台不可用：提权安装没有对应实现，
// 调用方应在此之前用 rename 直写（root/测试场景）。
func Install(_, _ string) error {
	return errors.New("privileged install is not supported on " + runtime.GOOS)
}
