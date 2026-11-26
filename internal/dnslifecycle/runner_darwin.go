//go:build darwin

package dnslifecycle

import (
	"context"
	"fmt"
	"os/exec"
)

type execRunner struct{}

func newSystemRunner() CommandRunner {
	return execRunner{}
}

func (execRunner) Run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, output)
	}
	return output, nil
}
