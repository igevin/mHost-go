package main

import (
	"context"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/igevin/mHost-go/internal/app/alphaboot"
	"github.com/igevin/mHost-go/internal/app/demo"
	"github.com/igevin/mHost-go/internal/desktop"
	"github.com/igevin/mHost-go/internal/storage"
)

// mHost Alpha 主入口：默认启动 Hosts-only Alpha（真实 HostsService，
// 数据目录自动 v1→v2 迁移）。
// 环境变量切换：
//   - MHOST_WORKBENCH=storage：Storage Workbench（迁移/验证工具台）
//   - MHOST_WORKBENCH=hosts：Hosts Rewrite Workbench（内存 Demo，不写 /etc/hosts）
//   - MHOST_DATA_DIR / MHOST_HOSTS_PATH：覆盖数据目录与系统 hosts 路径（测试/开发）
func main() {
	mygo.App.SetName("mHost")
	mygo.App.SetVersion("0.3.0-alpha")

	var model desktop.AppModel
	switch os.Getenv("MHOST_WORKBENCH") {
	case "storage":
		model = desktop.NewWorkbenchModel(storage.NewService(), &desktop.NativeDirectoryPicker{})
	case "hosts":
		hosts := desktop.NewHostsWorkbenchModel(demo.NewHostsService())
		hosts.DemoMode = true
		model = hosts
	default:
		boot, err := alphaboot.Prepare(context.Background(), alphaboot.Options{
			DataDir:   os.Getenv("MHOST_DATA_DIR"),
			HostsPath: os.Getenv("MHOST_HOSTS_PATH"),
		})
		if err != nil {
			log.Fatalf("mHost Alpha startup: %v", err)
		}
		model = desktop.NewAlphaModel(boot)
	}

	if err := desktop.NewApp(model).Run(); err != nil {
		log.Fatal(err)
	}
}
