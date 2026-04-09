package boxmgr

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

func TestKickoffPendingHealthCheckTriggersProbeAndUnblocksAvailabilityGate(t *testing.T) {
	monitorMgr, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.com:80"})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer monitorMgr.Stop()

	mgr := New(
		&config.Config{
			SubscriptionRefresh: config.SubscriptionRefreshConfig{
				MinAvailableNodes: 1,
			},
		},
		monitor.Config{ProbeTarget: "http://example.com:80"},
	)
	mgr.monitorMgr = monitorMgr
	mgr.minAvailableNodes = 1

	pendingEntry := monitorMgr.Register(monitor.NodeInfo{
		Tag:  "pending-node",
		Name: "pending-node",
		URI:  "http://127.0.0.1:8080",
	})
	var pendingProbeCalls atomic.Int32
	pendingEntry.SetProbe(func(ctx context.Context) (time.Duration, error) {
		pendingProbeCalls.Add(1)
		return 5 * time.Millisecond, nil
	})

	restoredEntry := monitorMgr.Register(monitor.NodeInfo{
		Tag:  "restored-node",
		Name: "restored-node",
		URI:  "http://127.0.0.1:8081",
	})
	restoredEntry.MarkInitialCheckDone(true)
	var restoredProbeCalls atomic.Int32
	restoredEntry.SetProbe(func(ctx context.Context) (time.Duration, error) {
		restoredProbeCalls.Add(1)
		return 5 * time.Millisecond, nil
	})

	mgr.kickoffPendingHealthCheck(100 * time.Millisecond)

	if err := mgr.waitForHealthCheck(2 * time.Second); err != nil {
		t.Fatalf("waitForHealthCheck() error = %v, want nil", err)
	}

	if got := pendingProbeCalls.Load(); got != 1 {
		t.Fatalf("pending probe calls = %d, want 1", got)
	}
	if got := restoredProbeCalls.Load(); got != 0 {
		t.Fatalf("restored probe calls = %d, want 0", got)
	}

	snap := pendingEntry.Snapshot()
	if !snap.InitialCheckDone {
		t.Fatalf("pending snapshot InitialCheckDone = false, want true")
	}
	if !snap.Available {
		t.Fatalf("pending snapshot Available = false, want true")
	}
}
