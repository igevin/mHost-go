package desktop

import (
	"context"
	"testing"

	"github.com/igevin/mHost-go/internal/dnsserver"
)

type fakeDNSManager struct {
	running    bool
	startCalls int
	stopCalls  int
}

func (f *fakeDNSManager) Start(context.Context) error {
	f.startCalls++
	f.running = true
	return nil
}

func (f *fakeDNSManager) Stop(context.Context) error {
	f.stopCalls++
	f.running = false
	return nil
}

func (f *fakeDNSManager) Running() bool {
	return f.running
}

func (f *fakeDNSManager) Errors() <-chan error {
	return nil
}

type fakeServer struct {
	closeCalls int
}

func (f *fakeServer) Close() error {
	f.closeCalls++
	return nil
}

func TestApp_CleanupIsIdempotent(t *testing.T) {
	manager := &fakeDNSManager{running: true}
	server := &fakeServer{}
	app := &App{manager: manager, server: server}
	app.Cleanup()
	app.Cleanup()
	if manager.stopCalls != 1 || server.closeCalls != 1 {
		t.Fatalf("cleanup calls = stop %d, close %d", manager.stopCalls, server.closeCalls)
	}
}

func TestApp_CleanupAllowsNilDNSServer(t *testing.T) {
	var server *dnsserver.Server
	app := NewApp(NewModel("", nil), nil, server)
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestApp_CloseHidesOnlyWhenNotQuitting(t *testing.T) {
	app := &App{}
	if !app.shouldHideOnClose() {
		t.Fatal("ordinary close should hide the window")
	}
	app.quitting = true
	if app.shouldHideOnClose() {
		t.Fatal("quit should allow the window to close")
	}
}
