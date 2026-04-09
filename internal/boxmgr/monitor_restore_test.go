package boxmgr

import (
	"context"
	"path/filepath"
	"testing"

	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
)

func TestRestoreMonitorStateFromStorePreloadsPersistedStats(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer dataStore.Close()

	node := &store.Node{
		URI:     "trojan://alpha",
		Name:    "alpha",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := dataStore.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	if err := dataStore.UpsertNodeStats(ctx, &store.NodeStats{
		NodeID:           node.ID,
		SuccessCount:     3,
		LastLatencyMs:    99,
		Available:        true,
		InitialCheckDone: true,
	}); err != nil {
		t.Fatalf("UpsertNodeStats() error = %v", err)
	}

	mgr, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Stop()

	if err := restoreMonitorStateFromStore(ctx, mgr, dataStore); err != nil {
		t.Fatalf("restoreMonitorStateFromStore() error = %v", err)
	}

	entry := mgr.Register(monitor.NodeInfo{Tag: "tag-alpha", Name: "alpha", URI: "trojan://alpha"})
	snap := entry.Snapshot()

	if !snap.InitialCheckDone {
		t.Fatalf("InitialCheckDone = false, want true")
	}
	if !snap.Available {
		t.Fatalf("Available = false, want true")
	}
	if snap.LastLatencyMs != 99 {
		t.Fatalf("LastLatencyMs = %d, want 99", snap.LastLatencyMs)
	}
	if snap.SuccessCount != 3 {
		t.Fatalf("SuccessCount = %d, want 3", snap.SuccessCount)
	}
}
