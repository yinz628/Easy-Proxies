package boxmgr

import (
	"context"
	"path/filepath"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
)

func TestDeleteNodeDeletesStoreOnlyStagedNode(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	staged := &store.Node{
		URI:            "http://10.0.0.2:80",
		Name:           "staged-node",
		Source:         store.NodeSourceSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := st.CreateNode(ctx, staged); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr := New(&config.Config{
		Nodes: []config.NodeConfig{
			{Name: "active-node", URI: "http://10.0.0.1:80"},
		},
	}, monitor.Config{}, WithStore(st))

	if err := mgr.DeleteNode(ctx, staged.Name); err != nil {
		t.Fatalf("DeleteNode(%q) error = %v, want nil", staged.Name, err)
	}

	got, err := st.GetNodeByName(ctx, staged.Name)
	if err != nil {
		t.Fatalf("GetNodeByName(%q) error = %v", staged.Name, err)
	}
	if got != nil {
		t.Fatalf("GetNodeByName(%q) = %#v, want nil", staged.Name, got)
	}

	if len(mgr.cfg.Nodes) != 1 {
		t.Fatalf("len(mgr.cfg.Nodes) = %d, want 1", len(mgr.cfg.Nodes))
	}
	if gotName := mgr.cfg.Nodes[0].Name; gotName != "active-node" {
		t.Fatalf("mgr.cfg.Nodes[0].Name = %q, want %q", gotName, "active-node")
	}
}
