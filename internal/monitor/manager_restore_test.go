package monitor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerRegisterRestoresPreloadedNodeState(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Stop()

	lastFailure := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	lastSuccess := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	mgr.PreloadNodeStates([]RestoreEntry{
		{
			URI:  "trojan://alpha",
			Name: "alpha",
			State: RestoredNodeState{
				FailureCount:     2,
				SuccessCount:     5,
				LastError:        "timeout",
				LastFailure:      lastFailure,
				LastSuccess:      lastSuccess,
				LastLatencyMs:    321,
				Available:        true,
				InitialCheckDone: true,
				TotalUpload:      1234,
				TotalDownload:    5678,
			},
		},
	})

	entry := mgr.Register(NodeInfo{Tag: "tag-alpha", Name: "alpha", URI: "trojan://alpha"})
	snap := entry.Snapshot()

	if snap.FailureCount != 2 {
		t.Fatalf("FailureCount = %d, want 2", snap.FailureCount)
	}
	if snap.SuccessCount != 5 {
		t.Fatalf("SuccessCount = %d, want 5", snap.SuccessCount)
	}
	if snap.LastError != "timeout" {
		t.Fatalf("LastError = %q, want timeout", snap.LastError)
	}
	if !snap.LastFailure.Equal(lastFailure) {
		t.Fatalf("LastFailure = %v, want %v", snap.LastFailure, lastFailure)
	}
	if !snap.LastSuccess.Equal(lastSuccess) {
		t.Fatalf("LastSuccess = %v, want %v", snap.LastSuccess, lastSuccess)
	}
	if snap.LastLatencyMs != 321 {
		t.Fatalf("LastLatencyMs = %d, want 321", snap.LastLatencyMs)
	}
	if !snap.Available {
		t.Fatalf("Available = false, want true")
	}
	if !snap.InitialCheckDone {
		t.Fatalf("InitialCheckDone = false, want true")
	}
	if snap.TotalUpload != 1234 {
		t.Fatalf("TotalUpload = %d, want 1234", snap.TotalUpload)
	}
	if snap.TotalDownload != 5678 {
		t.Fatalf("TotalDownload = %d, want 5678", snap.TotalDownload)
	}
}

func TestStartPeriodicHealthCheckSkipsImmediateProbeForRestoredNodes(t *testing.T) {
	mgr, err := NewManager(Config{ProbeTarget: "http://example.com:80"})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Stop()

	mgr.PreloadNodeStates([]RestoreEntry{
		{
			URI: "trojan://alpha",
			State: RestoredNodeState{
				LastLatencyMs:    88,
				Available:        true,
				InitialCheckDone: true,
			},
		},
	})

	entry := mgr.Register(NodeInfo{Tag: "tag-alpha", Name: "alpha", URI: "trojan://alpha"})

	var probeCalls atomic.Int32
	entry.SetProbe(func(ctx context.Context) (time.Duration, error) {
		probeCalls.Add(1)
		return 10 * time.Millisecond, nil
	})

	mgr.StartPeriodicHealthCheck(time.Hour, 50*time.Millisecond)
	time.Sleep(150 * time.Millisecond)

	if got := probeCalls.Load(); got != 0 {
		t.Fatalf("probeCalls = %d, want 0", got)
	}
}
