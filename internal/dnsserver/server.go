package dnsserver

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

type Server struct {
	connection *net.UDPConn
	closeOnce  sync.Once
	closeErr   error
	workers    sync.WaitGroup
}

func Start(listen string) (*Server, error) {
	address, err := loopbackAddress(listen)
	if err != nil {
		return nil, err
	}
	connection, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for DNS: %w", err)
	}
	server := &Server{connection: connection}
	server.workers.Add(1)
	go server.serve()
	return server, nil
}

func (s *Server) Addr() string {
	return s.connection.LocalAddr().String()
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.connection.Close()
		s.workers.Wait()
	})
	return s.closeErr
}

func (s *Server) serve() {
	defer s.workers.Done()
	buffer := make([]byte, 65535)
	for {
		n, client, err := s.connection.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		response, err := makeResponse(buffer[:n])
		if err != nil {
			continue
		}
		_, _ = s.connection.WriteToUDP(response, client)
	}
}

func makeResponse(query []byte) ([]byte, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(query)
	if err != nil {
		return nil, fmt.Errorf("parse DNS header: %w", err)
	}
	question, err := parser.Question()
	if err != nil {
		return nil, fmt.Errorf("parse DNS question: %w", err)
	}
	message := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 header.ID,
			Response:           true,
			Authoritative:      true,
			RecursionDesired:   header.RecursionDesired,
			RecursionAvailable: true,
			RCode:              dnsmessage.RCodeNameError,
		},
		Questions: []dnsmessage.Question{question},
	}
	if question.Name.String() == "phase0.test." && question.Type == dnsmessage.TypeA && question.Class == dnsmessage.ClassINET {
		message.Header.RCode = dnsmessage.RCodeSuccess
		message.Answers = []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{
				Name:  question.Name,
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   30,
			},
			Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 53}},
		}}
	}
	packet, err := message.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack DNS response: %w", err)
	}
	return packet, nil
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
