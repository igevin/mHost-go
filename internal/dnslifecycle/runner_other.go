//go:build !darwin

package dnslifecycle

func newSystemRunner() CommandRunner {
	return nil
}
