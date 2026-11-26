package dnslifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestStopWithEscalation(t *testing.T) {
	tests := []struct {
		name        string
		stopOn      string
		wantSignals []string
		wantErr     bool
	}{
		{name: "SIGTERM stops proxy", stopOn: "TERM", wantSignals: []string{"TERM"}},
		{name: "SIGKILL stops unresponsive proxy", stopOn: "KILL", wantSignals: []string{"TERM", "KILL"}},
		{name: "unresponsive proxy returns error", wantSignals: []string{"TERM", "KILL"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			running := true
			var signals []string
			err := stopWithEscalation(
				context.Background(),
				42,
				func() bool { return running },
				func() error { return nil },
				func(signal string) error {
					signals = append(signals, signal)
					if signal == tt.stopOn {
						running = false
					}
					return nil
				},
				5*time.Millisecond,
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(signals, tt.wantSignals) {
				t.Fatalf("signals = %v, want %v", signals, tt.wantSignals)
			}
		})
	}
}

func TestStopWithEscalation_GracefulShutdown(t *testing.T) {
	err := stopWithEscalation(
		context.Background(),
		42,
		func() bool { return false },
		func() error { return errors.New("must not request shutdown after exit") },
		nil,
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
}
