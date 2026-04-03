package app

import (
	"context"
	"path/filepath"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

func TestLoadNodesFromStoreUsesActiveLifecycleOnly(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	activeNode := &store.Node{
		URI:            "http://10.0.0.1:80",
		Name:           "active-node",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := st.CreateNode(ctx, activeNode); err != nil {
		t.Fatalf("CreateNode(active) error = %v", err)
	}

	stagedNode := &store.Node{
		URI:            "http://10.0.0.2:80",
		Name:           "staged-node",
		Source:         store.NodeSourceSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := st.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode(staged) error = %v", err)
	}

	cfg := &config.Config{}
	if err := loadNodesFromStore(ctx, cfg, st); err != nil {
		t.Fatalf("loadNodesFromStore() error = %v", err)
	}

	if len(cfg.Nodes) != 1 {
		t.Fatalf("len(cfg.Nodes) = %d, want 1", len(cfg.Nodes))
	}
	if got, want := cfg.Nodes[0].Name, "active-node"; got != want {
		t.Fatalf("cfg.Nodes[0].Name = %q, want %q", got, want)
	}
}
