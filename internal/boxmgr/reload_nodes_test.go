package boxmgr

import (
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

func TestMergeActiveStoreNodesForReloadSkipsNonActiveLifecycle(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{Name: "inline-node", URI: "trojan://inline"},
		},
	}

	added := mergeActiveStoreNodesForReload(cfg, []store.Node{
		{
			Name:           "active-node",
			URI:            "http://10.0.0.1:80",
			Source:         store.NodeSourceManual,
			Enabled:        true,
			LifecycleState: store.NodeLifecycleActive,
		},
		{
			Name:           "staged-node",
			URI:            "http://10.0.0.2:80",
			Source:         store.NodeSourceSubscription,
			Enabled:        true,
			LifecycleState: store.NodeLifecycleStaged,
		},
		{
			Name:           "disabled-node",
			URI:            "http://10.0.0.3:80",
			Source:         store.NodeSourceManual,
			Enabled:        false,
			LifecycleState: store.NodeLifecycleDisabled,
		},
		{
			Name:           "duplicate-inline",
			URI:            "trojan://inline",
			Source:         store.NodeSourceManual,
			Enabled:        true,
			LifecycleState: store.NodeLifecycleActive,
		},
	})

	if added != 1 {
		t.Fatalf("mergeActiveStoreNodesForReload() added %d nodes, want 1", added)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("len(cfg.Nodes) = %d, want 2", len(cfg.Nodes))
	}
	if got, want := cfg.Nodes[1].Name, "active-node"; got != want {
		t.Fatalf("cfg.Nodes[1].Name = %q, want %q", got, want)
	}
}
