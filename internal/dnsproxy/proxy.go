package dnsproxy

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

type Proxy struct {
	listener  *net.UDPConn
	target    *net.UDPAddr
	closeOnce sync.Once
	closeErr  error
	workers   sync.WaitGroup
}

func Start(listen, target string) (*Proxy, error) {
	listenAddress, err := loopbackAddress(listen)
	if err != nil {
		return nil, fmt.Errorf("listen address: %w", err)
	}
	targetAddress, err := loopbackAddress(target)
	if err != nil {
		return nil, fmt.Errorf("target address: %w", err)
	}
	listener, err := net.ListenUDP("udp", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for DNS proxy: %w", err)
	}
	proxy := &Proxy{listener: listener, target: targetAddress}
	proxy.workers.Add(1)
	go proxy.serve()
	return proxy, nil
}

func (p *Proxy) Addr() string {
	return p.listener.LocalAddr().String()
}

func (p *Proxy) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.listener.Close()
		p.workers.Wait()
	})
	return p.closeErr
}

func (p *Proxy) serve() {
	defer p.workers.Done()
	buffer := make([]byte, 65535)
	for {
		n, client, err := p.listener.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		packet := append([]byte(nil), buffer[:n]...)
		p.workers.Add(1)
		go p.forward(packet, client)
	}
}

func (p *Proxy) forward(packet []byte, client *net.UDPAddr) {
	defer p.workers.Done()
	connection, err := net.DialUDP("udp", nil, p.target)
	if err != nil {
		return
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return
	}
	if _, err := connection.Write(packet); err != nil {
		return
	}
	buffer := make([]byte, 65535)
	n, err := connection.Read(buffer)
	if err != nil {
		return
	}
	_, _ = p.listener.WriteToUDP(buffer[:n], client)
}

func loopbackAddress(value string) (*net.UDPAddr, error) {
	address, err := net.ResolveUDPAddr("udp", value)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP address %q: %w", value, err)
	}
	if address.IP == nil || !address.IP.IsLoopback() {
		return nil, fmt.Errorf("address %q is not an explicit loopback address", value)
	}
	return address, nil
}
