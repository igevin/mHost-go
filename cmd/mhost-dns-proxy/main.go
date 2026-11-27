package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/igevin/mHost-go/internal/dnsproxy"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:53", "loopback DNS proxy listen address")
	target := flag.String("target", "127.0.0.1:1053", "loopback DNS service address")
	readyFile := flag.String("ready-file", "", "write the proxy pid after listening")
	shutdownFile := flag.String("shutdown-file", "", "stop when this file exists")
	check := flag.Bool("check", false, "check the packaged executable without binding a port")
	flag.Parse()
	if *check {
		fmt.Println("mhost-dns-proxy phase0 self-check ok")
		return
	}

	proxy, err := dnsproxy.Start(*listen, *target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start proxy: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := proxy.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close proxy: %v\n", err)
		}
	}()
	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write readiness: %v\n", err)
			os.Exit(1)
		}
		defer func() {
			if err := os.Remove(*readyFile); err != nil {
				fmt.Fprintf(os.Stderr, "remove readiness file: %v\n", err)
			}
		}()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-signals:
			return
		case <-ticker.C:
			if *shutdownFile == "" {
				continue
			}
			if _, err := os.Stat(*shutdownFile); err == nil {
				return
			}
		}
	}
}
