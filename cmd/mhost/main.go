package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/igevin/mHost-go/internal/app/demo"
	"github.com/igevin/mHost-go/internal/desktop"
)

func main() {
	mygo.App.SetName("mHost Phase 2")
	mygo.App.SetVersion("0.0.0")
	service := demo.NewHostsService()
	model := desktop.NewHostsWorkbenchModel(service)
	model.DemoMode = true
	if err := desktop.NewApp(model).Run(); err != nil {
		log.Fatal(err)
	}
}
