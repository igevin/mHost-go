//go:build darwin

package hostsfile

// defaultMover 在 darwin 上优先提权路径：rename 先试，失败再弹
// 管理员授权框；测试与 root 场景行为与 RenameMover 一致。
func defaultMover() Mover { return PrivilegedMover{} }
