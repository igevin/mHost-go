package dnslifecycle

import (
	"context"
	"fmt"
	"time"
)

func stopWithEscalation(
	ctx context.Context,
	pid int,
	exists func() bool,
	requestStop func() error,
	sendSignal func(string) error,
	gracePeriod time.Duration,
) error {
	if !exists() {
		return nil
	}
	if err := requestStop(); err != nil {
		return fmt.Errorf("request graceful proxy shutdown: %w", err)
	}
	for _, signal := range []string{"", "TERM", "KILL"} {
		if signal != "" {
			if !exists() {
				return nil
			}
			if err := sendSignal(signal); err != nil {
				return fmt.Errorf("send SIG%s to proxy pid %d: %w", signal, pid, err)
			}
		}
		waitCtx, cancel := context.WithTimeout(ctx, gracePeriod)
		err := waitForProcessExit(waitCtx, exists)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("stop proxy pid %d: %w", pid, ctx.Err())
		}
	}
	return fmt.Errorf("proxy pid %d did not stop after SIGKILL", pid)
}

func waitForProcessExit(ctx context.Context, exists func() bool) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !exists() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
