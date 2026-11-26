package dnslifecycle

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	proxyReadyFile    = "proxy.ready"
	proxyPIDFile      = "proxy.pid"
	proxyShutdownFile = "proxy.shutdown"
	proxyLogFile      = "proxy.log"
)

type OSProxyLauncher struct {
	proxyPath  string
	runtimeDir string
}

func NewOSProxyLauncher(proxyPath, runtimeDir string) *OSProxyLauncher {
	return &OSProxyLauncher{proxyPath: proxyPath, runtimeDir: runtimeDir}
}

func (l *OSProxyLauncher) path(name string) string {
	return filepath.Join(l.runtimeDir, name)
}

func proxyCommand(proxyPath, runtimeDir string) string {
	arguments := []string{
		proxyPath,
		"-listen", "127.0.0.1:53",
		"-target", "127.0.0.1:1053",
		"-ready-file", filepath.Join(runtimeDir, proxyReadyFile),
		"-shutdown-file", filepath.Join(runtimeDir, proxyShutdownFile),
	}
	for index := range arguments {
		arguments[index] = shellQuote(arguments[index])
	}
	return "nohup " + strings.Join(arguments, " ") + " >" + shellQuote(filepath.Join(runtimeDir, proxyLogFile)) + " 2>&1 & echo $!"
}

func administratorScript(command string) string {
	return "do shell script " + strconv.Quote(command) + " with administrator privileges"
}

func shellCommand(name string, arguments ...string) string {
	parts := append([]string{name}, arguments...)
	for index := range parts {
		parts[index] = shellQuote(parts[index])
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateProxyExecutable(path string) error {
	if path == "" {
		return fmt.Errorf("DNS proxy path is empty")
	}
	return nil
}
