package dnslifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	originalDNS = DNSState{Service: "Wi-Fi", Kind: KindManual, Servers: []string{"1.1.1.1", "8.8.8.8"}}
	loopbackDNS = DNSState{Service: "Wi-Fi", Kind: KindManual, Servers: []string{"127.0.0.1"}}
)

type fakeDNS struct {
	mu         sync.Mutex
	current    DNSState
	capture    DNSState
	setErr     error
	currentErr error
	setCalls   []DNSState
	setStarted chan struct{}
	setBlock   chan struct{}
}

func (f *fakeDNS) Capture(context.Context) (DNSState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneState(f.capture), nil
}

func (f *fakeDNS) Set(_ context.Context, state DNSState) error {
	if f.setBlock != nil && (len(state.Servers) != 1 || state.Servers[0] != "127.0.0.1") {
		f.setStarted <- struct{}{}
		<-f.setBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, cloneState(state))
	if f.setErr != nil && len(state.Servers) == 1 && state.Servers[0] == "127.0.0.1" {
		return f.setErr
	}
	f.current = cloneState(state)
	return nil
}

func (f *fakeDNS) Current(_ context.Context, service string) (DNSState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.currentErr != nil {
		return DNSState{}, f.currentErr
	}
	state := cloneState(f.current)
	state.Service = service
	return state, nil
}

func (f *fakeDNS) snapshot() DNSState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneState(f.current)
}

func cloneState(state DNSState) DNSState {
	state.Servers = append([]string(nil), state.Servers...)
	return state
}

type fakeProcess struct {
	wait    chan error
	stopErr error
	stopped bool
	mu      sync.Mutex
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{wait: make(chan error, 1)}
}

func (p *fakeProcess) Stop(context.Context) error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.wait <- nil
	return p.stopErr
}

func (p *fakeProcess) Wait() error {
	return <-p.wait
}

func (p *fakeProcess) exit(err error) {
	p.wait <- err
}

type fakeLauncher struct {
	process      *fakeProcess
	startErr     error
	stopStaleErr error
	staleCalls   int
}

func (l *fakeLauncher) Start(context.Context) (ProxyProcess, error) {
	if l.startErr != nil {
		return nil, l.startErr
	}
	l.process = newFakeProcess()
	return l.process, nil
}

func (l *fakeLauncher) StopStale(context.Context) error {
	l.staleCalls++
	return l.stopStaleErr
}

func newTestManager(t *testing.T, dns *fakeDNS, launcher *fakeLauncher) (*Manager, *FileStore) {
	t.Helper()
	store := NewFileStore(t.TempDir())
	return NewManager(store, dns, launcher), store
}

func TestManager_StartAndStopRestoreOriginalDNS(t *testing.T) {
	dns := &fakeDNS{capture: originalDNS, current: originalDNS}
	launcher := &fakeLauncher{}
	manager, store := newTestManager(t, dns, launcher)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !manager.Running() {
		t.Fatal("manager is not running")
	}
	if got := dns.snapshot(); !reflect.DeepEqual(got, loopbackDNS) {
		t.Fatalf("current DNS = %+v", got)
	}
	pending, err := store.Pending()
	if err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dns.snapshot(); !reflect.DeepEqual(got, originalDNS) {
		t.Fatalf("restored DNS = %+v", got)
	}
	pending, err = store.Pending()
	if err != nil || pending {
		t.Fatalf("pending after stop = %v, err = %v", pending, err)
	}
	calls := len(dns.setCalls)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(dns.setCalls) != calls {
		t.Fatal("idempotent stop changed DNS again")
	}
}

func TestManager_StartFailureRestoresAndStopsProxy(t *testing.T) {
	tests := []struct {
		name        string
		startErr    error
		setErr      error
		wantStopped bool
	}{
		{name: "proxy start fails", startErr: errors.New("authorization failed")},
		{name: "system DNS change fails", setErr: errors.New("networksetup failed"), wantStopped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dns := &fakeDNS{capture: originalDNS, current: originalDNS, setErr: tt.setErr}
			launcher := &fakeLauncher{startErr: tt.startErr}
			manager, store := newTestManager(t, dns, launcher)
			if err := manager.Start(context.Background()); err == nil {
				t.Fatal("expected start error")
			}
			if got := dns.snapshot(); !reflect.DeepEqual(got, originalDNS) {
				t.Fatalf("DNS after failure = %+v", got)
			}
			pending, err := store.Pending()
			if err != nil || pending {
				t.Fatalf("pending = %v, err = %v", pending, err)
			}
			if launcher.process != nil {
				launcher.process.mu.Lock()
				stopped := launcher.process.stopped
				launcher.process.mu.Unlock()
				if stopped != tt.wantStopped {
					t.Fatalf("proxy stopped = %v, want %v", stopped, tt.wantStopped)
				}
			}
		})
	}
}

func TestManager_StopFailureRestoresButKeepsMarker(t *testing.T) {
	dns := &fakeDNS{capture: originalDNS, current: originalDNS}
	launcher := &fakeLauncher{}
	manager, store := newTestManager(t, dns, launcher)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	launcher.process.stopErr = errors.New("shutdown timed out")
	if err := manager.Stop(context.Background()); err == nil {
		t.Fatal("expected proxy stop error")
	}
	if got := dns.snapshot(); !reflect.DeepEqual(got, originalDNS) {
		t.Fatalf("DNS after failed proxy stop = %+v", got)
	}
	pending, err := store.Pending()
	if err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
}

func TestManager_StopKeepsMarkerWhenVerificationFails(t *testing.T) {
	dns := &fakeDNS{capture: originalDNS, current: originalDNS}
	launcher := &fakeLauncher{}
	manager, store := newTestManager(t, dns, launcher)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	dns.currentErr = errors.New("cannot read DNS")
	if err := manager.Stop(context.Background()); err == nil {
		t.Fatal("expected verification error")
	}
	pending, err := store.Pending()
	if err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
}

func TestManager_UnexpectedProxyExitTriggersRecovery(t *testing.T) {
	dns := &fakeDNS{capture: originalDNS, current: originalDNS}
	launcher := &fakeLauncher{}
	manager, store := newTestManager(t, dns, launcher)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	launcher.process.exit(errors.New("proxy crashed"))
	waitFor(t, func() bool {
		pending, err := store.Pending()
		return err == nil && !pending && reflect.DeepEqual(dns.snapshot(), originalDNS)
	})
	if manager.Running() {
		t.Fatal("manager still running after proxy exit")
	}
	select {
	case err := <-manager.Errors():
		if err == nil || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("proxy event = %v", err)
		}
	default:
		t.Fatal("proxy exit did not publish a lifecycle event")
	}
}

func TestManager_RunningDoesNotBlockDuringUnexpectedRecovery(t *testing.T) {
	dns := &fakeDNS{
		capture:    originalDNS,
		current:    originalDNS,
		setStarted: make(chan struct{}, 1),
		setBlock:   make(chan struct{}),
	}
	launcher := &fakeLauncher{}
	manager, _ := newTestManager(t, dns, launcher)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var unblock sync.Once
	unblockRecovery := func() { unblock.Do(func() { close(dns.setBlock) }) }
	defer unblockRecovery()

	launcher.process.exit(errors.New("proxy crashed"))
	select {
	case <-dns.setStarted:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	running := make(chan bool, 1)
	go func() { running <- manager.Running() }()
	select {
	case <-running:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Running blocked behind DNS recovery")
	}
	unblockRecovery()
}

func TestManager_RecoverAfterApplicationKill(t *testing.T) {
	dns := &fakeDNS{current: loopbackDNS}
	launcher := &fakeLauncher{}
	manager, store := newTestManager(t, dns, launcher)
	if err := store.Save(originalDNS); err != nil {
		t.Fatal(err)
	}
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if launcher.staleCalls != 1 {
		t.Fatalf("stale cleanup calls = %d", launcher.staleCalls)
	}
	if got := dns.snapshot(); !reflect.DeepEqual(got, originalDNS) {
		t.Fatalf("restored DNS = %+v", got)
	}
	pending, err := store.Pending()
	if err != nil || pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
}

func TestManager_RecoveryFailureRetainsMarker(t *testing.T) {
	dns := &fakeDNS{current: loopbackDNS}
	launcher := &fakeLauncher{stopStaleErr: errors.New("proxy still running")}
	manager, store := newTestManager(t, dns, launcher)
	if err := store.Save(originalDNS); err != nil {
		t.Fatal(err)
	}
	if err := manager.Recover(context.Background()); err == nil {
		t.Fatal("expected recovery error")
	}
	pending, err := store.Pending()
	if err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if got := dns.snapshot(); !reflect.DeepEqual(got, loopbackDNS) {
		t.Fatalf("DNS changed despite stale cleanup failure: %+v", got)
	}
}

func TestManager_StartRejectsUnrecordedLoopback(t *testing.T) {
	dns := &fakeDNS{capture: originalDNS, current: originalDNS}
	dns.capture.Servers = []string{"127.0.0.1"}
	manager, _ := newTestManager(t, dns, &fakeLauncher{})
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("expected unrecorded loopback capture to fail")
	}
}

func TestFileStore_PersistsPrivateRecoveryState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	store := NewFileStore(dir)
	pending, err := store.Pending()
	if err != nil || pending {
		t.Fatalf("initial pending = %v, err = %v", pending, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Pending created runtime directory: %v", err)
	}
	if err := store.Save(originalDNS); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || !reflect.DeepEqual(loaded, originalDNS) {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
	for _, name := range []string{"original-dns.json", "recovery_pending"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o", name, got)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("runtime dir mode = %o", got)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true")
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}
