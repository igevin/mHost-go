package dnsserver

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestServer_RespondsToPhase0Query(t *testing.T) {
	server, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	query := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name:  dnsmessage.MustNewName("phase0.test."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		}},
	}
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("udp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(packet); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 65535)
	n, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	var response dnsmessage.Message
	if err := response.Unpack(buffer[:n]); err != nil {
		t.Fatal(err)
	}
	if response.Header.ID != 42 || !response.Header.Response || response.Header.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("response header = %+v", response.Header)
	}
	if len(response.Answers) != 1 {
		t.Fatalf("answers = %+v", response.Answers)
	}
	if got := response.Answers[0].Body.(*dnsmessage.AResource).A; got != [4]byte{192, 0, 2, 53} {
		t.Fatalf("answer = %v", got)
	}
}

func TestServer_CloseIsIdempotent(t *testing.T) {
	server, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStart_RejectsNonLoopbackListener(t *testing.T) {
	if _, err := Start("0.0.0.0:0"); err == nil {
		t.Fatal("expected non-loopback listener to be rejected")
	}
}
