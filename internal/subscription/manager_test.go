package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
	"easy_proxies/internal/txtsub"
)

func TestCollectFeedRequestsIncludesOnlyEnabledTXTFeedsForAutoRefresh(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-enabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: true,
				},
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/socks.txt",
					DefaultProtocol:   "socks5",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feeds := manager.collectFeedRequests(false)
	if len(feeds) != 2 {
		t.Fatalf("len(feeds) = %d, want 2", len(feeds))
	}

	if feeds[0].Kind != feedKindLegacy {
		t.Fatalf("feeds[0].Kind = %q, want %q", feeds[0].Kind, feedKindLegacy)
	}

	if feeds[1].Name != "txt-enabled" {
		t.Fatalf("feeds[1].Name = %q, want %q", feeds[1].Name, "txt-enabled")
	}
}

func TestCollectFeedRequestsIncludesDisabledTXTFeedsForManualRefresh(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feeds := manager.collectFeedRequests(true)
	if len(feeds) != 1 {
		t.Fatalf("len(feeds) = %d, want 1", len(feeds))
	}

	if feeds[0].Kind != feedKindTXT {
		t.Fatalf("feeds[0].Kind = %q, want %q", feeds[0].Kind, feedKindTXT)
	}

	if feeds[0].NormalizedURL != "https://raw.githubusercontent.com/user/repo/main/http.txt" {
		t.Fatalf("feeds[0].NormalizedURL = %q, want GitHub raw URL", feeds[0].NormalizedURL)
	}
}

func TestFindTXTFeedRequestByFeedKeyReturnsOnlyMatchingTXTFeed(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feed, err := manager.findTXTFeedRequestByFeedKey("txt:https://raw.githubusercontent.com/user/repo/main/http.txt")
	if err != nil {
		t.Fatalf("findTXTFeedRequestByFeedKey() error = %v", err)
	}

	if feed.Kind != feedKindTXT {
		t.Fatalf("feed.Kind = %q, want %q", feed.Kind, feedKindTXT)
	}

	if feed.Name != "txt-disabled" {
		t.Fatalf("feed.Name = %q, want %q", feed.Name, "txt-disabled")
	}
}

func TestFindTXTFeedRequestByFeedKeyRejectsLegacyFeed(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
		},
	}

	_, err := manager.findTXTFeedRequestByFeedKey("legacy:https://example.com/sub")
	if err == nil {
		t.Fatal("findTXTFeedRequestByFeedKey() error = nil, want non-nil")
	}
}

func TestBuildStoreNodesForFeedMarksImportedNodesStaged(t *testing.T) {
	nodes := buildStoreNodesForFeed(feedRequest{
		Kind:    feedKindLegacy,
		FeedKey: "legacy:https://example.com/sub",
	}, []config.NodeConfig{
		{
			Name:   "legacy-node",
			URI:    "http://1.1.1.1:80",
			Source: config.NodeSourceSubscription,
		},
	})

	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if got, want := nodes[0].LifecycleState, store.NodeLifecycleStaged; got != want {
		t.Fatalf("nodes[0].LifecycleState = %q, want %q", got, want)
	}
	if !nodes[0].Enabled {
		t.Fatal("nodes[0].Enabled = false, want true for staged compatibility")
	}
}

func TestRefreshFeedSupportsLegacyFeedAndOnlyCountsStagedCandidates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.CreateNode(ctx, &store.Node{
		URI:            "http://9.9.9.9:80",
		Name:           "active-manual",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}); err != nil {
		t.Fatalf("CreateNode(active manual) error = %v", err)
	}

	subscriptionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://1.1.1.1:80#legacy-node\n"))
	}))
	defer subscriptionServer.Close()

	manager := New(&config.Config{
		Subscriptions: []string{subscriptionServer.URL},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Timeout: 2 * time.Second,
		},
	}, nil, WithStore(st))
	defer manager.Stop()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("RefreshFeed() panicked, want staged import without runtime reload: %v", recovered)
		}
	}()

	feedKey := txtsub.BuildFeedKey(txtsub.FeedKindLegacy, subscriptionServer.URL)
	if err := manager.RefreshFeed(feedKey); err != nil {
		t.Fatalf("RefreshFeed(%q) error = %v", feedKey, err)
	}

	nodes, err := st.ListNodes(ctx, store.NodeFilter{Source: store.NodeSourceSubscription})
	if err != nil {
		t.Fatalf("ListNodes(subscription) error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if got, want := nodes[0].LifecycleState, store.NodeLifecycleStaged; got != want {
		t.Fatalf("nodes[0].LifecycleState = %q, want %q", got, want)
	}

	if got, want := manager.Status().NodeCount, 1; got != want {
		t.Fatalf("manager.Status().NodeCount = %d, want %d staged candidate only", got, want)
	}
}

func TestRefreshFeedLegacyPreservesExistingSubscriptionNodeDataForSameURI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	subscriptionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://1.1.1.1:80\n"))
	}))
	defer subscriptionServer.Close()

	ctx := context.Background()
	feedKey := txtsub.BuildFeedKey(txtsub.FeedKindLegacy, subscriptionServer.URL)
	existing := &store.Node{
		URI:            "http://1.1.1.1:80",
		Name:           "legacy-node-old",
		Source:         store.NodeSourceSubscription,
		FeedKey:        feedKey,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := st.CreateNode(ctx, existing); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := st.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    existing.ID,
		Status:    store.ManualProbeStatusPass,
		LatencyMs: 120,
		Message:   "preserve me",
		CheckedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}
	if err := st.SaveNodeQualityCheck(ctx, &store.NodeQualityCheck{
		NodeID:                 existing.ID,
		QualityVersion:         quality.QualityVersionAIReachabilityV2,
		QualityStatus:          quality.StatusOpenAIOnly,
		QualityOpenAIStatus:    quality.StatusPass,
		QualityAnthropicStatus: quality.StatusFail,
		QualityScore:           func() *int { value := 70; return &value }(),
		QualityGrade:           "B",
		QualitySummary:         "preserve quality",
		QualityCheckedAt:       time.Now().UTC().Truncate(time.Second),
		Items: []store.NodeQualityCheckItem{
			{Target: quality.TargetOpenAIReachability, Status: quality.StatusPass, HTTPStatus: 401, LatencyMs: 120},
		},
	}); err != nil {
		t.Fatalf("SaveNodeQualityCheck() error = %v", err)
	}

	manager := New(&config.Config{
		Subscriptions: []string{subscriptionServer.URL},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Timeout: 2 * time.Second,
		},
	}, nil, WithStore(st))
	defer manager.Stop()

	if err := manager.RefreshFeed(feedKey); err != nil {
		t.Fatalf("RefreshFeed(%q) error = %v", feedKey, err)
	}

	got, err := st.GetNodeByURI(ctx, existing.URI)
	if err != nil {
		t.Fatalf("GetNodeByURI() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNodeByURI() = nil, want non-nil")
	}
	if got.ID != existing.ID {
		t.Fatalf("node ID = %d, want %d", got.ID, existing.ID)
	}
	if got.LifecycleState != store.NodeLifecycleActive {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, store.NodeLifecycleActive)
	}
	if got.Source != store.NodeSourceSubscription {
		t.Fatalf("Source = %q, want %q", got.Source, store.NodeSourceSubscription)
	}
	manualProbe, err := st.GetNodeManualProbeResult(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if manualProbe == nil || manualProbe.Message != "preserve me" {
		t.Fatalf("manual probe = %#v, want preserved result", manualProbe)
	}

	check, err := st.GetNodeQualityCheck(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetNodeQualityCheck() error = %v", err)
	}
	if check == nil || check.QualitySummary != "preserve quality" {
		t.Fatalf("quality check = %#v, want preserved result", check)
	}
}

func TestRefreshFeedLegacyDoesNotResetExistingManualNodeOnDuplicateURI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	subscriptionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://2.2.2.2:80\n"))
	}))
	defer subscriptionServer.Close()

	ctx := context.Background()
	existing := &store.Node{
		URI:            "http://2.2.2.2:80",
		Name:           "manual-node",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := st.CreateNode(ctx, existing); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := st.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    existing.ID,
		Status:    store.ManualProbeStatusPass,
		LatencyMs: 88,
		Message:   "manual history",
		CheckedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}

	manager := New(&config.Config{
		Subscriptions: []string{subscriptionServer.URL},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Timeout: 2 * time.Second,
		},
	}, nil, WithStore(st))
	defer manager.Stop()

	feedKey := txtsub.BuildFeedKey(txtsub.FeedKindLegacy, subscriptionServer.URL)
	if err := manager.RefreshFeed(feedKey); err != nil {
		t.Fatalf("RefreshFeed(%q) error = %v", feedKey, err)
	}

	got, err := st.GetNodeByURI(ctx, existing.URI)
	if err != nil {
		t.Fatalf("GetNodeByURI() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNodeByURI() = nil, want non-nil")
	}
	if got.ID != existing.ID {
		t.Fatalf("node ID = %d, want %d", got.ID, existing.ID)
	}
	if got.Source != store.NodeSourceManual {
		t.Fatalf("Source = %q, want %q", got.Source, store.NodeSourceManual)
	}
	if got.LifecycleState != store.NodeLifecycleActive {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, store.NodeLifecycleActive)
	}
	if got.Name != "manual-node" {
		t.Fatalf("Name = %q, want %q", got.Name, "manual-node")
	}

	manualProbe, err := st.GetNodeManualProbeResult(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if manualProbe == nil || manualProbe.Message != "manual history" {
		t.Fatalf("manual probe = %#v, want preserved result", manualProbe)
	}

	if got, want := manager.Status().NodeCount, 0; got != want {
		t.Fatalf("manager.Status().NodeCount = %d, want %d because duplicate manual node should not become staged", got, want)
	}
}
