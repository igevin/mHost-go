package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/igevin/mHost-go/internal/desktop"
	"github.com/igevin/mHost-go/internal/dnslifecycle"
	"github.com/igevin/mHost-go/internal/dnsserver"
)

const initialHosts = `# mHost Phase 0 synthetic Hosts
127.0.0.1 localhost.test
127.0.0.1 api.test web.test # shared local services
10.0.0.8 api.test
::1 ipv6.test

not-an-ip broken.test
`

func main() {
	mygo.App.SetName("mHost Phase 0")
	mygo.App.SetVersion("0.0.0")
	userData, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		log.Fatal(err)
	}
	runtimeDir := filepath.Join(userData, "phase0-runtime")
	manager := dnslifecycle.NewManager(
		dnslifecycle.NewFileStore(runtimeDir),
		dnslifecycle.NewNetworkSetup(),
		dnslifecycle.NewOSProxyLauncher(filepath.Join(resources, "bin", "mhost-dns-proxy"), runtimeDir),
	)
	model := desktop.NewModel(initialHosts, manager)
	server, err := dnsserver.Start("127.0.0.1:1053")
	if err != nil {
		model.DNSServiceAvailable = false
		model.DNSStatus = "DNS service unavailable: " + err.Error()
	}
	if err := manager.Recover(context.Background()); err != nil {
		model.DNSStatus = "Recovery pending: " + err.Error()
	}
	app := desktop.NewApp(model, manager, server)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
