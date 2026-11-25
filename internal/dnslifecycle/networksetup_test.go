package dnslifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeRunner struct {
	responses map[string]string
	commands  []recordedCommand
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	key := name + " " + strings.Join(args, " ")
	if response, ok := r.responses[key]; ok {
		return []byte(response), nil
	}
	return nil, errors.New("unexpected command: " + key)
}

func TestNetworkSetup_CaptureActiveServiceAndDNS(t *testing.T) {
	runner := &fakeRunner{responses: map[string]string{
		"route -n get default":               "interface: en0\n",
		"networksetup -listallhardwareports": "Hardware Port: Wi-Fi\nDevice: en0\n\nHardware Port: Ethernet\nDevice: en1\n",
		"networksetup -getdnsservers Wi-Fi":  "1.1.1.1\n8.8.8.8\n",
	}}
	dns := NewNetworkSetupWithRunner(runner)
	state, err := dns.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, originalDNS) {
		t.Fatalf("state = %+v", state)
	}
}

func TestNetworkSetup_CaptureDHCP(t *testing.T) {
	runner := &fakeRunner{responses: map[string]string{
		"route -n get default":               "interface: en0\n",
		"networksetup -listallhardwareports": "Hardware Port: Wi-Fi\nDevice: en0\n",
		"networksetup -getdnsservers Wi-Fi":  "There aren't any DNS Servers set\n",
	}}
	state, err := NewNetworkSetupWithRunner(runner).Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != KindDHCPEmpty || len(state.Servers) != 0 {
		t.Fatalf("state = %+v", state)
	}
}

func TestNetworkSetup_SetUsesDirectArgv(t *testing.T) {
	runner := &fakeRunner{responses: map[string]string{
		"networksetup -setdnsservers Wi-Fi 1.1.1.1 8.8.8.8": "",
	}}
	dns := NewNetworkSetupWithRunner(runner)
	if err := dns.Set(context.Background(), originalDNS); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{{name: "networksetup", args: []string{"-setdnsservers", "Wi-Fi", "1.1.1.1", "8.8.8.8"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %+v", runner.commands)
	}
}

func TestNetworkSetup_RejectsUnsafeInput(t *testing.T) {
	for _, state := range []DNSState{
		{Service: "Wi-Fi; touch /tmp/nope", Kind: KindManual, Servers: []string{"1.1.1.1"}},
		{Service: "Wi-Fi", Kind: KindManual, Servers: []string{"1.1.1.1;echo"}},
		{Service: "Wi-Fi", Kind: KindManual},
		{Service: "Wi-Fi", Kind: "unknown", Servers: []string{"1.1.1.1"}},
	} {
		runner := &fakeRunner{}
		if err := NewNetworkSetupWithRunner(runner).Set(context.Background(), state); err == nil {
			t.Errorf("Set(%+v) returned nil error", state)
		}
		if len(runner.commands) != 0 {
			t.Errorf("Set(%+v) executed commands: %+v", state, runner.commands)
		}
	}
}
