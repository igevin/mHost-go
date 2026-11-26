package dnslifecycle

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type NetworkSetup struct {
	runner         CommandRunner
	commandTimeout time.Duration
}

func NewNetworkSetup() *NetworkSetup {
	return &NetworkSetup{runner: newSystemRunner(), commandTimeout: 15 * time.Second}
}

func NewNetworkSetupWithRunner(runner CommandRunner) *NetworkSetup {
	return &NetworkSetup{runner: runner, commandTimeout: 15 * time.Second}
}

func (n *NetworkSetup) Capture(ctx context.Context) (DNSState, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	service, err := n.activeService(ctx)
	if err != nil {
		return DNSState{}, err
	}
	servers, err := n.readServers(ctx, service)
	if err != nil {
		return DNSState{}, err
	}
	if len(servers) == 0 {
		return DNSState{Service: service, Kind: KindDHCPEmpty}, nil
	}
	filtered := make([]string, 0, len(servers))
	for _, server := range servers {
		address, _ := netip.ParseAddr(server)
		if address.IsLoopback() || address.IsUnspecified() {
			continue
		}
		filtered = append(filtered, server)
	}
	if len(filtered) == 0 {
		return DNSState{}, fmt.Errorf("system DNS on %q contains only local resolvers; restore it before starting", service)
	}
	return DNSState{Service: service, Kind: KindManual, Servers: filtered}, nil
}

func (n *NetworkSetup) Set(ctx context.Context, state DNSState) error {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	if err := validateService(state.Service); err != nil {
		return err
	}
	arguments := []string{"-setdnsservers", state.Service}
	switch state.Kind {
	case KindDHCPEmpty:
		arguments = append(arguments, "Empty")
	case KindManual:
		if len(state.Servers) == 0 {
			return fmt.Errorf("manual DNS state requires at least one server")
		}
		for _, server := range state.Servers {
			if _, err := netip.ParseAddr(server); err != nil {
				return fmt.Errorf("invalid DNS server %q: %w", server, err)
			}
		}
		arguments = append(arguments, state.Servers...)
	default:
		return fmt.Errorf("unknown DNS recovery kind %q", state.Kind)
	}
	if n.runner == nil {
		return fmt.Errorf("networksetup is only supported on macOS")
	}
	script := administratorScript(shellCommand("networksetup", arguments...))
	if _, err := n.runner.Run(ctx, "/usr/bin/osascript", "-e", script); err != nil {
		return fmt.Errorf("privileged networksetup %s: %w", arguments[0], err)
	}
	return nil
}

func (n *NetworkSetup) Current(ctx context.Context, service string) (DNSState, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	if err := validateService(service); err != nil {
		return DNSState{}, err
	}
	servers, err := n.readServers(ctx, service)
	if err != nil {
		return DNSState{}, err
	}
	if len(servers) == 0 {
		return DNSState{Service: service, Kind: KindDHCPEmpty}, nil
	}
	return DNSState{Service: service, Kind: KindManual, Servers: servers}, nil
}

func (n *NetworkSetup) activeService(ctx context.Context) (string, error) {
	if n.runner == nil {
		return "", fmt.Errorf("networksetup is only supported on macOS")
	}
	route, err := n.runner.Run(ctx, "route", "-n", "get", "default")
	if err != nil {
		return "", fmt.Errorf("detect default route: %w", err)
	}
	device := ""
	for _, line := range strings.Split(string(route), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "interface:") {
			device = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "interface:"))
		}
	}
	if device == "" {
		return "", fmt.Errorf("default route has no interface")
	}
	ports, err := n.runner.Run(ctx, "networksetup", "-listallhardwareports")
	if err != nil {
		return "", fmt.Errorf("list hardware ports: %w", err)
	}
	service := hardwarePortForDevice(string(ports), device)
	if err := validateService(service); err != nil {
		return "", fmt.Errorf("active device %q: %w", device, err)
	}
	return service, nil
}

func (n *NetworkSetup) readServers(ctx context.Context, service string) ([]string, error) {
	if n.runner == nil {
		return nil, fmt.Errorf("networksetup is only supported on macOS")
	}
	output, err := n.runner.Run(ctx, "networksetup", "-getdnsservers", service)
	if err != nil {
		return nil, fmt.Errorf("get DNS servers for %q: %w", service, err)
	}
	text := strings.TrimSpace(string(output))
	if text == "" || strings.Contains(text, "There aren't any DNS Servers set") {
		return nil, nil
	}
	var servers []string
	for _, line := range strings.Split(text, "\n") {
		server := strings.TrimSpace(line)
		if server == "" {
			continue
		}
		if _, err := netip.ParseAddr(server); err != nil {
			return nil, fmt.Errorf("invalid DNS server %q from networksetup: %w", server, err)
		}
		servers = append(servers, server)
	}
	return servers, nil
}

func (n *NetworkSetup) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := n.commandTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func hardwarePortForDevice(output, device string) string {
	currentPort := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Hardware Port:") {
			currentPort = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
			continue
		}
		if strings.HasPrefix(line, "Device:") && strings.TrimSpace(strings.TrimPrefix(line, "Device:")) == device {
			return currentPort
		}
	}
	return ""
}

func validateService(service string) error {
	allowed := map[string]bool{
		"Wi-Fi":                    true,
		"Ethernet":                 true,
		"USB 10/100/1000 LAN":      true,
		"USB 10/100/1000 Ethernet": true,
		"Thunderbolt Bridge":       true,
		"Thunderbolt Ethernet":     true,
	}
	if !allowed[service] {
		return fmt.Errorf("network service %q is not in the Phase 0 allowlist", service)
	}
	return nil
}
