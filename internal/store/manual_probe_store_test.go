package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestListNodesFiltersByLifecycleStateAndBatchUpdateLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	nodes := []*Node{
		{
			URI:            "http://10.0.0.1:80",
			Name:           "active-node",
			Source:         NodeSourceManual,
			Enabled:        true,
			LifecycleState: NodeLifecycleActive,
		},
		{
			URI:            "http://10.0.0.2:80",
			Name:           "staged-node",
			Source:         NodeSourceSubscription,
			Enabled:        true,
			LifecycleState: NodeLifecycleStaged,
		},
		{
			URI:            "http://10.0.0.3:80",
			Name:           "disabled-node",
			Source:         NodeSourceManual,
			Enabled:        false,
			LifecycleState: NodeLifecycleDisabled,
		},
	}

	for _, node := range nodes {
		if err := st.CreateNode(ctx, node); err != nil {
			t.Fatalf("CreateNode(%s) error = %v", node.Name, err)
		}
	}

	activeNodes, err := st.ListNodes(ctx, NodeFilter{LifecycleState: NodeLifecycleActive})
	if err != nil {
		t.Fatalf("ListNodes(active) error = %v", err)
	}
	if len(activeNodes) != 1 || activeNodes[0].Name != "active-node" {
		t.Fatalf("ListNodes(active) = %#v, want only active-node", activeNodes)
	}

	stagedCount, err := st.CountNodes(ctx, NodeFilter{LifecycleState: NodeLifecycleStaged})
	if err != nil {
		t.Fatalf("CountNodes(staged) error = %v", err)
	}
	if stagedCount != 1 {
		t.Fatalf("CountNodes(staged) = %d, want 1", stagedCount)
	}

	if err := st.BatchUpdateNodeLifecycle(ctx, []int64{nodes[1].ID}, NodeLifecycleActive); err != nil {
		t.Fatalf("BatchUpdateNodeLifecycle() error = %v", err)
	}

	got, err := st.GetNode(ctx, nodes[1].ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNode() = nil, want non-nil")
	}
	if got.LifecycleState != NodeLifecycleActive {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, NodeLifecycleActive)
	}
	if !got.Enabled {
		t.Fatal("Enabled = false, want true for active lifecycle")
	}
}

func TestSaveAndLoadNodeManualProbeResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &Node{
		URI:            "http://20.0.0.1:80",
		Name:           "probe-node",
		Source:         NodeSourceManual,
		Enabled:        true,
		LifecycleState: NodeLifecycleStaged,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	checkedAt := time.Now().UTC().Truncate(time.Second)
	first := &NodeManualProbeResult{
		NodeID:    node.ID,
		Status:    ManualProbeStatusPass,
		LatencyMs: 320,
		Message:   "first pass",
		CheckedAt: checkedAt,
	}
	if err := st.SaveNodeManualProbeResult(ctx, first); err != nil {
		t.Fatalf("SaveNodeManualProbeResult(first) error = %v", err)
	}

	second := &NodeManualProbeResult{
		NodeID:    node.ID,
		Status:    ManualProbeStatusTimeout,
		LatencyMs: 0,
		TimedOut:  true,
		Message:   "timeout",
		CheckedAt: checkedAt.Add(time.Minute),
	}
	if err := st.SaveNodeManualProbeResult(ctx, second); err != nil {
		t.Fatalf("SaveNodeManualProbeResult(second) error = %v", err)
	}

	got, err := st.GetNodeManualProbeResult(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNodeManualProbeResult() = nil, want non-nil")
	}
	if got.Status != ManualProbeStatusTimeout {
		t.Fatalf("Status = %q, want %q", got.Status, ManualProbeStatusTimeout)
	}
	if !got.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
	if got.Message != "timeout" {
		t.Fatalf("Message = %q, want %q", got.Message, "timeout")
	}

	all, err := st.GetAllNodeManualProbeResults(ctx)
	if err != nil {
		t.Fatalf("GetAllNodeManualProbeResults() error = %v", err)
	}
	entry := all[node.ID]
	if entry == nil {
		t.Fatalf("GetAllNodeManualProbeResults()[%d] = nil, want non-nil", node.ID)
	}
	if entry.Status != ManualProbeStatusTimeout {
		t.Fatalf("all[%d].Status = %q, want %q", node.ID, entry.Status, ManualProbeStatusTimeout)
	}
}
