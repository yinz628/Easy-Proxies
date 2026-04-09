package boxmgr

import (
	"context"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

func TestStartEntersIdleWhenNoNodesAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Config{
		Mode: "pool",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     2323,
			Protocol: "http",
		},
		Pool: config.PoolConfig{
			Mode:             "sequential",
			FailureThreshold: 3,
		},
	}

	mgr := New(cfg, monitor.Config{Enabled: false})
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v, want idle startup success", err)
	}
	defer mgr.Close()

	if !mgr.idle {
		t.Fatalf("mgr.idle = false, want true")
	}
	if mgr.currentBox != nil {
		t.Fatalf("mgr.currentBox = %v, want nil", mgr.currentBox)
	}
}
