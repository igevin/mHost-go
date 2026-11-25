//go:build darwin

package dnslifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (l *OSProxyLauncher) Start(ctx context.Context) (ProxyProcess, error) {
	if err := validateProxyExecutable(l.proxyPath); err != nil {
		return nil, err
	}
	info, err := os.Stat(l.proxyPath)
	if err != nil {
		return nil, fmt.Errorf("stat DNS proxy: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("DNS proxy %q is not an executable file", l.proxyPath)
	}
	for _, name := range []string{proxyReadyFile, proxyShutdownFile} {
		if err := removeIfExists(l.path(name)); err != nil {
			return nil, fmt.Errorf("clear stale proxy %s: %w", name, err)
		}
	}
	script := "do shell script " + strconv.Quote(proxyCommand(l.proxyPath, l.runtimeDir)) + " with administrator privileges"
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("authorize DNS proxy: %w: %s", err, output)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		return nil, fmt.Errorf("parse DNS proxy pid %q", output)
	}
	if err := writePrivateFile(l.path(proxyPIDFile), []byte(strconv.Itoa(pid)+"\n")); err != nil {
		return nil, fmt.Errorf("save DNS proxy pid: %w", err)
	}
	process := &OSProxyProcess{pid: pid, launcher: l}
	if err := process.waitReady(ctx); err != nil {
		return nil, err
	}
	return process, nil
}

func (l *OSProxyLauncher) StopStale(ctx context.Context) error {
	data, err := os.ReadFile(l.path(proxyPIDFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read stale proxy pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid stale proxy pid %q", data)
	}
	if !processExists(pid) {
		l.cleanup()
		return nil
	}
	output, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect stale proxy: %w: %s", err, output)
	}
	if filepath.Base(strings.TrimSpace(string(output))) != "mhost-dns-proxy" {
		return fmt.Errorf("pid %d belongs to %q, not mhost-dns-proxy", pid, strings.TrimSpace(string(output)))
	}
	process := &OSProxyProcess{pid: pid, launcher: l}
	return process.Stop(ctx)
}

type OSProxyProcess struct {
	pid      int
	launcher *OSProxyLauncher
}

func (p *OSProxyProcess) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(p.launcher.path(proxyReadyFile))
		if err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(p.pid) {
			return nil
		}
		if !processExists(p.pid) {
			return fmt.Errorf("DNS proxy exited before readiness; inspect %s", p.launcher.path(proxyLogFile))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for DNS proxy readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *OSProxyProcess) Wait() error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if !processExists(p.pid) {
			return nil
		}
	}
	return nil
}

func (p *OSProxyProcess) Stop(ctx context.Context) error {
	if !processExists(p.pid) {
		p.launcher.cleanup()
		return nil
	}
	if err := writePrivateFile(p.launcher.path(proxyShutdownFile), []byte("stop\n")); err != nil {
		return fmt.Errorf("write proxy shutdown signal: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !processExists(p.pid) {
			p.launcher.cleanup()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("proxy pid %d did not stop: %w", p.pid, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (l *OSProxyLauncher) cleanup() {
	for _, name := range []string{proxyReadyFile, proxyPIDFile, proxyShutdownFile} {
		_ = os.Remove(l.path(name))
	}
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
