package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/igevin/mHost-go/internal/app/demo"
	"github.com/igevin/mHost-go/internal/desktop"
	"github.com/igevin/mHost-go/internal/storage"
)

// mHost Phase 1 主入口：Storage Workbench + 真实存储服务。
// 设置 MHOST_WORKBENCH=hosts 可启动 Phase 2 的 Hosts 改写演示
// （内存 Demo 服务，不写 /etc/hosts）。
func main() {
	mygo.App.SetName("mHost")
	mygo.App.SetVersion("0.1.0")

	var model desktop.AppModel
	if os.Getenv("MHOST_WORKBENCH") == "hosts" {
		hosts := desktop.NewHostsWorkbenchModel(demo.NewHostsService())
		hosts.DemoMode = true
		model = hosts
	} else {
		workbench := desktop.NewWorkbenchModel(storage.NewService(), &desktop.NativeDirectoryPicker{})
		model = workbench
	}

	if err := desktop.NewApp(model).Run(); err != nil {
		log.Fatal(err)
	}
}
