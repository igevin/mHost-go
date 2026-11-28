//go:build !darwin

package hostsfile

// defaultMover 在非 darwin 平台上为同盘原子 rename（开发/CI 环境
// 里目标目录可写，无需提权）。
func defaultMover() Mover { return RenameMover{} }
