//go:build !darwin

package dnslifecycle

import (
	"context"
	"fmt"
)

func (l *OSProxyLauncher) Start(context.Context) (ProxyProcess, error) {
	return nil, fmt.Errorf("privileged DNS proxy is only supported on macOS")
}

func (l *OSProxyLauncher) StopStale(context.Context) error {
	return fmt.Errorf("stale proxy cleanup is only supported on macOS")
}
