package dnsproxy

import (
	"net"
	"testing"
	"time"

	"github.com/igevin/mHost-go/internal/dnsserver"
	"golang.org/x/net/dns/dnsmessage"
)

func TestProxy_ForwardsDNSQuery(t *testing.T) {
	backend, err := dnsserver.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	proxy, err := Start("127.0.0.1:0", backend.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	query := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 7, RecursionDesired: true},
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
	connection, err := net.Dial("udp", proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
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
	if response.ID != 7 || len(response.Answers) != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestProxy_CloseIsIdempotent(t *testing.T) {
	proxy, err := Start("127.0.0.1:0", "127.0.0.1:1053")
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStart_RejectsNonLoopbackAddresses(t *testing.T) {
	for _, test := range []struct{ listen, target string }{
		{listen: "0.0.0.0:0", target: "127.0.0.1:1053"},
		{listen: "127.0.0.1:0", target: "0.0.0.0:1053"},
	} {
		if _, err := Start(test.listen, test.target); err == nil {
			t.Errorf("Start(%q, %q) returned nil error", test.listen, test.target)
		}
	}
}
