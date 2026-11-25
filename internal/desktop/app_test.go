package desktop

import (
	"context"
	"testing"
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
