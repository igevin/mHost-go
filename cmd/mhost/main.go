package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/igevin/mHost-go/internal/app/demo"
	"github.com/igevin/mHost-go/internal/desktop"
)

func main() {
	mygo.App.SetName("mHost Phase 1")
	mygo.App.SetVersion("0.0.0")
	service := demo.NewStorageService()
	picker := &desktop.NativeDirectoryPicker{}
	model := desktop.NewWorkbenchModel(service, picker)
	model.DemoMode = true
	if err := desktop.NewApp(model).Run(); err != nil {
		log.Fatal(err)
	}
}
