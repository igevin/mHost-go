package dnslifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
)

type SystemDNS interface {
	Capture(context.Context) (DNSState, error)
	Set(context.Context, DNSState) error
	Current(context.Context, string) (DNSState, error)
}

type ProxyProcess interface {
	Stop(context.Context) error
	Wait() error
}

type ProxyLauncher interface {
	Start(context.Context) (ProxyProcess, error)
	StopStale(context.Context) error
}

type Manager struct {
	store    Store
	dns      SystemDNS
	launcher ProxyLauncher

	operation sync.Mutex
	mu        sync.Mutex
	process   ProxyProcess
	errors    chan error
}

func NewManager(store Store, dns SystemDNS, launcher ProxyLauncher) *Manager {
	return &Manager{store: store, dns: dns, launcher: launcher, errors: make(chan error, 1)}
}

func (m *Manager) Errors() <-chan error {
	return m.errors
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.process != nil
}

func (m *Manager) Start(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	if m.Running() {
		return fmt.Errorf("DNS proxy is already running")
	}
	if err := m.recoverLocked(ctx); err != nil {
		return fmt.Errorf("recover previous DNS session: %w", err)
	}
	original, err := m.dns.Capture(ctx)
	if err != nil {
		return fmt.Errorf("capture original DNS: %w", err)
	}
	for _, server := range original.Servers {
		if address, parseErr := netip.ParseAddr(server); parseErr == nil && (address.IsLoopback() || address.IsUnspecified()) {
			return fmt.Errorf("system DNS already points to %s without a recovery record", address)
		}
	}
	if err := m.store.Save(original); err != nil {
		return fmt.Errorf("persist DNS recovery state: %w", err)
	}
	process, err := m.launcher.Start(ctx)
	if err != nil {
		staleErr := m.launcher.StopStale(ctx)
		recoveryErr := m.restorePendingWithPolicyLocked(ctx, staleErr == nil)
		return errors.Join(fmt.Errorf("start DNS proxy: %w", err), staleErr, recoveryErr)
	}
	m.mu.Lock()
	m.process = process
	m.mu.Unlock()
	loopback := DNSState{Service: original.Service, Kind: KindManual, Servers: []string{"127.0.0.1"}}
	if err := m.dns.Set(ctx, loopback); err != nil {
		stopErr := process.Stop(context.Background())
		m.mu.Lock()
		m.process = nil
		m.mu.Unlock()
		recoveryErr := m.restorePendingWithPolicyLocked(context.Background(), stopErr == nil)
		return errors.Join(fmt.Errorf("point system DNS at proxy: %w", err), stopErr, recoveryErr)
	}
	go m.monitor(process)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	m.mu.Lock()
	process := m.process
	m.process = nil
	m.mu.Unlock()
	var stopErr error
	if process != nil {
		stopErr = process.Stop(ctx)
		if stopErr != nil {
			stopErr = fmt.Errorf("stop DNS proxy: %w", stopErr)
		}
	}
	return errors.Join(stopErr, m.restorePendingWithPolicyLocked(ctx, stopErr == nil))
}

func (m *Manager) Recover(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	return m.recoverLocked(ctx)
}

func (m *Manager) recoverLocked(ctx context.Context) error {
	pending, err := m.store.Pending()
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	if err := m.launcher.StopStale(ctx); err != nil {
		return fmt.Errorf("stop stale DNS proxy: %w", err)
	}
	return m.restorePendingLocked(ctx)
}

func (m *Manager) monitor(process ProxyProcess) {
	waitErr := process.Wait()
	m.operation.Lock()
	defer m.operation.Unlock()
	m.mu.Lock()
	if m.process != process {
		m.mu.Unlock()
		return
	}
	m.process = nil
	m.mu.Unlock()
	recoveryErr := m.restorePendingLocked(context.Background())
	if waitErr == nil {
		waitErr = errors.New("DNS proxy exited unexpectedly")
	} else {
		waitErr = fmt.Errorf("DNS proxy exited: %w", waitErr)
	}
	m.report(errors.Join(waitErr, recoveryErr))
}

func (m *Manager) restorePendingLocked(ctx context.Context) error {
	return m.restorePendingWithPolicyLocked(ctx, true)
}

func (m *Manager) restorePendingWithPolicyLocked(ctx context.Context, clear bool) error {
	pending, err := m.store.Pending()
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	original, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load recovery state: %w", err)
	}
	if err := m.dns.Set(ctx, original); err != nil {
		return fmt.Errorf("restore original DNS: %w", err)
	}
	current, err := m.dns.Current(ctx, original.Service)
	if err != nil {
		return fmt.Errorf("verify restored DNS: %w", err)
	}
	if !statesEqual(current, original) {
		return fmt.Errorf("verify restored DNS: got %+v, want %+v", current, original)
	}
	if clear {
		if err := m.store.Clear(); err != nil {
			return fmt.Errorf("clear verified recovery state: %w", err)
		}
	}
	return nil
}

func (m *Manager) report(err error) {
	if err == nil {
		return
	}
	select {
	case m.errors <- err:
	default:
	}
}

func statesEqual(left, right DNSState) bool {
	return left.Service == right.Service && left.Kind == right.Kind && slices.Equal(left.Servers, right.Servers)
}
