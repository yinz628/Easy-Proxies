package pool

import (
	"context"
	"errors"
	"net"
	"testing"

	"easy_proxies/internal/monitor"

	"github.com/sagernet/sing-box/adapter"
	boxlog "github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

type fakeOutbound struct {
	dialCalls int
}

var _ adapter.Outbound = (*fakeOutbound)(nil)

func (f *fakeOutbound) Type() string {
	return "fake"
}

func (f *fakeOutbound) Tag() string {
	return "fake"
}

func (f *fakeOutbound) Network() []string {
	return []string{"tcp"}
}

func (f *fakeOutbound) Dependencies() []string {
	return nil
}

func (f *fakeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	f.dialCalls++
	return nil, errors.New("unexpected dial")
}

func (f *fakeOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unexpected listen packet")
}

func TestProbeAllMembersOnStartupSkipsRestoredNodes(t *testing.T) {
	mgr, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.com:80"})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Stop()

	mgr.PreloadNodeStates([]monitor.RestoreEntry{
		{
			URI: "trojan://alpha",
			State: monitor.RestoredNodeState{
				LastLatencyMs:    66,
				Available:        true,
				InitialCheckDone: true,
			},
		},
	})

	entry := mgr.Register(monitor.NodeInfo{Tag: "tag-alpha", Name: "alpha", URI: "trojan://alpha"})
	outbound := &fakeOutbound{}
	p := &poolOutbound{
		ctx:     context.Background(),
		logger:  boxlog.StdLogger(),
		monitor: mgr,
		members: []*memberState{
			{
				tag:      "tag-alpha",
				outbound: outbound,
				entry:    entry,
			},
		},
	}

	p.probeAllMembersOnStartup()

	if outbound.dialCalls != 0 {
		t.Fatalf("dialCalls = %d, want 0", outbound.dialCalls)
	}

	snap := entry.Snapshot()
	if !snap.InitialCheckDone {
		t.Fatalf("InitialCheckDone = false, want true")
	}
	if !snap.Available {
		t.Fatalf("Available = false, want true")
	}
}
