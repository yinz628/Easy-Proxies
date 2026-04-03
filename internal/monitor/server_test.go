package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

type toggleCall struct {
	name    string
	enabled bool
}

type fakeSubscriptionRefresher struct {
	status             SubscriptionStatus
	refreshNowCalls    int
	refreshLegacyCalls int
	refreshTXTCalls    int
	refreshFeedCalls   []string
	refreshErr         error
}

func (f *fakeSubscriptionRefresher) RefreshNow() error {
	f.refreshNowCalls++
	return f.refreshErr
}

func (f *fakeSubscriptionRefresher) RefreshLegacy() error {
	f.refreshLegacyCalls++
	return f.refreshErr
}

func (f *fakeSubscriptionRefresher) RefreshTXT() error {
	f.refreshTXTCalls++
	return f.refreshErr
}

func (f *fakeSubscriptionRefresher) RefreshFeed(feedKey string) error {
	f.refreshFeedCalls = append(f.refreshFeedCalls, feedKey)
	return f.refreshErr
}

func (f *fakeSubscriptionRefresher) Status() SubscriptionStatus {
	return f.status
}

type fakeNodeManager struct {
	nodes                     []config.NodeConfig
	toggles                   []toggleCall
	deleted                   []string
	reloaded                  bool
	createNodeFn              func(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error)
	createConfigNodeRuntimeFn func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error)
}

type fakeConfigNodeRuntime struct {
	probeFn       func(ctx context.Context) (time.Duration, error)
	httpRequestFn func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error)
	closeFn       func() error
}

func (f *fakeNodeManager) ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error) {
	cloned := make([]config.NodeConfig, len(f.nodes))
	copy(cloned, f.nodes)
	return cloned, nil
}

func (f *fakeNodeManager) CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
	if f.createNodeFn != nil {
		return f.createNodeFn(ctx, node)
	}
	f.nodes = append(f.nodes, node)
	return node, nil
}

func (f *fakeNodeManager) UpdateNode(ctx context.Context, name string, node config.NodeConfig) (config.NodeConfig, error) {
	for i := range f.nodes {
		if f.nodes[i].Name == name {
			f.nodes[i] = node
			return node, nil
		}
	}
	return config.NodeConfig{}, ErrNodeNotFound
}

func (f *fakeNodeManager) DeleteNode(ctx context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeNodeManager) SetNodeEnabled(ctx context.Context, name string, enabled bool) error {
	f.toggles = append(f.toggles, toggleCall{name: name, enabled: enabled})
	return nil
}

func (f *fakeNodeManager) TriggerReload(ctx context.Context) error {
	f.reloaded = true
	return nil
}

func (f *fakeConfigNodeRuntime) Probe(ctx context.Context) (time.Duration, error) {
	if f.probeFn == nil {
		return 0, ErrNodeNotFound
	}
	return f.probeFn(ctx)
}

func (f *fakeConfigNodeRuntime) HTTPRequest(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
	if f.httpRequestFn == nil {
		return nil, ErrNodeNotFound
	}
	return f.httpRequestFn(ctx, method, rawURL, headers, maxBodyBytes)
}

func (f *fakeConfigNodeRuntime) Close() error {
	if f.closeFn == nil {
		return nil
	}
	return f.closeFn()
}

func (f *fakeNodeManager) CreateConfigNodeRuntime(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
	if f.createConfigNodeRuntimeFn == nil {
		return nil, ErrNodeNotFound
	}
	return f.createConfigNodeRuntimeFn(ctx, node)
}

func newManageListTestServer(t *testing.T) (*Server, *fakeNodeManager) {
	t.Helper()

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	alpha := mgr.Register(NodeInfo{Tag: "tag-alpha", Name: "alpha-hk", URI: "trojan://alpha", Port: 1001, Region: "hk", Country: "Hong Kong"})
	alpha.SetProbe(func(ctx context.Context) (time.Duration, error) { return 40 * time.Millisecond, nil })
	alpha.MarkInitialCheckDone(true)
	alpha.RecordSuccessWithLatency(40 * time.Millisecond)

	beta := mgr.Register(NodeInfo{Tag: "tag-beta", Name: "beta-us", URI: "trojan://beta", Port: 1002, Region: "us", Country: "United States"})
	beta.SetProbe(func(ctx context.Context) (time.Duration, error) { return 15 * time.Millisecond, nil })
	beta.MarkInitialCheckDone(true)
	beta.RecordSuccessWithLatency(15 * time.Millisecond)

	gamma := mgr.Register(NodeInfo{Tag: "tag-gamma", Name: "gamma-hk", URI: "trojan://gamma", Port: 1003, Region: "hk", Country: "Hong Kong"})
	gamma.SetProbe(func(ctx context.Context) (time.Duration, error) { return 80 * time.Millisecond, nil })
	gamma.MarkInitialCheckDone(true)
	gamma.MarkAvailable(false)

	delta := mgr.Register(NodeInfo{Tag: "tag-delta", Name: "delta-hk", URI: "trojan://delta", Port: 1004, Region: "hk", Country: "Hong Kong"})
	delta.SetProbe(func(ctx context.Context) (time.Duration, error) { return 25 * time.Millisecond, nil })
	delta.MarkInitialCheckDone(true)
	delta.RecordSuccessWithLatency(25 * time.Millisecond)

	nodeMgr := &fakeNodeManager{
		nodes: []config.NodeConfig{
			{Name: "alpha-hk", URI: "trojan://alpha", Port: 1001, Source: config.NodeSourceManual},
			{Name: "beta-us", URI: "trojan://beta", Port: 1002, Source: config.NodeSourceManual},
			{Name: "gamma-hk", URI: "trojan://gamma", Port: 1003, Source: config.NodeSourceSubscription},
			{Name: "delta-hk", URI: "trojan://delta", Port: 1004, Source: config.NodeSourceManual},
		},
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetNodeManager(nodeMgr)
	return server, nodeMgr
}

func newSubscriptionTestServer(t *testing.T, refresher *fakeSubscriptionRefresher) *Server {
	t.Helper()

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetSubscriptionRefresher(refresher)
	return server
}

func TestHandleManageNodesReturnsFilteredPage(t *testing.T) {
	server, _ := newManageListTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/manage?page=2&page_size=1&keyword=hk&status=normal&region=hk&source=manual&sort_key=latency&sort_dir=asc", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response ManageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Page != 2 || response.PageSize != 1 {
		t.Fatalf("page info = (%d,%d), want (2,1)", response.Page, response.PageSize)
	}
	if response.FilteredTotal != 2 {
		t.Fatalf("response.FilteredTotal = %d, want 2", response.FilteredTotal)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "alpha-hk" {
		t.Fatalf("response.Items = %#v, want second filtered row alpha-hk", response.Items)
	}
}

func TestHandleSubscriptionStatusReturnsStagedCount(t *testing.T) {
	server := newSubscriptionTestServer(t, &fakeSubscriptionRefresher{
		status: SubscriptionStatus{
			Enabled:          true,
			HasSubscriptions: true,
			NodeCount:        7,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/subscription/status", nil)
	rec := httptest.NewRecorder()

	server.handleSubscriptionStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}

	if got := response["staged_count"]; got != float64(7) {
		t.Fatalf("response[staged_count] = %#v, want 7", got)
	}
}

func TestHandleSubscriptionRefreshLegacyCallsLegacyRefresher(t *testing.T) {
	refresher := &fakeSubscriptionRefresher{
		status: SubscriptionStatus{NodeCount: 5},
	}
	server := newSubscriptionTestServer(t, refresher)

	req := httptest.NewRequest(http.MethodPost, "/api/subscription/refresh-legacy", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if refresher.refreshLegacyCalls != 1 {
		t.Fatalf("refreshLegacyCalls = %d, want 1", refresher.refreshLegacyCalls)
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if got := response["staged_count"]; got != float64(5) {
		t.Fatalf("response[staged_count] = %#v, want 5", got)
	}
}

func TestHandleSubscriptionRefreshTXTCallsTXTRefresher(t *testing.T) {
	refresher := &fakeSubscriptionRefresher{
		status: SubscriptionStatus{NodeCount: 3},
	}
	server := newSubscriptionTestServer(t, refresher)

	req := httptest.NewRequest(http.MethodPost, "/api/subscription/refresh-txt", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if refresher.refreshTXTCalls != 1 {
		t.Fatalf("refreshTXTCalls = %d, want 1", refresher.refreshTXTCalls)
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if got := response["staged_count"]; got != float64(3) {
		t.Fatalf("response[staged_count] = %#v, want 3", got)
	}
}

func TestHandleSubscriptionRefreshFeedReturnsStagedCount(t *testing.T) {
	refresher := &fakeSubscriptionRefresher{
		status: SubscriptionStatus{NodeCount: 2},
	}
	server := newSubscriptionTestServer(t, refresher)

	req := httptest.NewRequest(http.MethodPost, "/api/subscription/refresh-feed", strings.NewReader(`{"feed_key":"legacy:https://example.com/sub"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleSubscriptionRefreshFeed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(refresher.refreshFeedCalls) != 1 || refresher.refreshFeedCalls[0] != "legacy:https://example.com/sub" {
		t.Fatalf("refreshFeedCalls = %#v, want [legacy:https://example.com/sub]", refresher.refreshFeedCalls)
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if got := response["staged_count"]; got != float64(2) {
		t.Fatalf("response[staged_count] = %#v, want 2", got)
	}
}

func TestHandleManageNodesReturnsSummaryAndFacets(t *testing.T) {
	server, _ := newManageListTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/manage", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response ManageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Total != 4 || response.FilteredTotal != 4 {
		t.Fatalf("counts = (%d,%d), want (4,4)", response.Total, response.FilteredTotal)
	}
	if response.Summary["normal"] != 3 || response.Summary["unavailable"] != 1 {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if len(response.Facets.Regions) != 2 || response.Facets.Regions[0] != "hk" || response.Facets.Regions[1] != "us" {
		t.Fatalf("regions = %#v", response.Facets.Regions)
	}
	if len(response.Facets.Sources) != 2 || response.Facets.Sources[0] != "manual" || response.Facets.Sources[1] != "subscription" {
		t.Fatalf("sources = %#v", response.Facets.Sources)
	}
}

func TestHandleManageNodesIncludesManualProbeState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	node := &store.Node{
		URI:            "trojan://alpha",
		Name:           "alpha-hk",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := dataStore.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	checkedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if err := dataStore.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    node.ID,
		Status:    store.ManualProbeStatusTimeout,
		LatencyMs: 0,
		TimedOut:  true,
		Message:   "probe timeout after 10000ms",
		CheckedAt: checkedAt,
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "tag-alpha", Name: "alpha-hk", URI: "trojan://alpha", Port: 1001, Region: "hk", Country: "Hong Kong"})
	entry.MarkInitialCheckDone(true)
	entry.RecordSuccessWithLatency(40 * time.Millisecond)

	nodeMgr := &fakeNodeManager{
		nodes: []config.NodeConfig{
			{Name: "alpha-hk", URI: "trojan://alpha", Port: 1001, Source: config.NodeSourceManual},
		},
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetNodeManager(nodeMgr)
	server.SetStore(dataStore)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/manage", nil)
	rec := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response ManageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if len(response.Items) != 1 {
		t.Fatalf("len(response.Items) = %d, want 1", len(response.Items))
	}

	item := response.Items[0]
	if item.RuntimeStatus != "normal" {
		t.Fatalf("item.RuntimeStatus = %q, want normal", item.RuntimeStatus)
	}
	if item.ManualProbeStatus != store.ManualProbeStatusTimeout {
		t.Fatalf("item.ManualProbeStatus = %q, want %q", item.ManualProbeStatus, store.ManualProbeStatusTimeout)
	}
	if item.ManualProbeLatencyMS != 0 {
		t.Fatalf("item.ManualProbeLatencyMS = %d, want 0", item.ManualProbeLatencyMS)
	}
	if item.ManualProbeChecked == nil || *item.ManualProbeChecked != checkedAt.Unix() {
		t.Fatalf("item.ManualProbeChecked = %#v, want %d", item.ManualProbeChecked, checkedAt.Unix())
	}
	if item.ManualProbeMessage != "probe timeout after 10000ms" {
		t.Fatalf("item.ManualProbeMessage = %q, want timeout message", item.ManualProbeMessage)
	}
}

func TestHandleManageNodesRejectsInvalidPage(t *testing.T) {
	server, _ := newManageListTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/manage?page=0", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response["error"] == nil {
		t.Fatalf("response error = nil, want non-nil: %#v", response)
	}
}

func TestHandleIndexDisablesCacheForSPAEntry(t *testing.T) {
	server, _ := newManageListTestServer(t)

	for _, path := range []string{"/", "/monitor"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			server.srv.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
				t.Fatalf("Cache-Control = %q, want header containing no-store", got)
			}
			if got := rec.Header().Get("Pragma"); got != "no-cache" {
				t.Fatalf("Pragma = %q, want no-cache", got)
			}
		})
	}
}

func TestHandleProbeBatchStartResolvesFilterSelection(t *testing.T) {
	server, _ := newManageListTestServer(t)

	body := `{
		"selection": {
			"mode": "filter",
			"filter": {
				"page": 9,
				"page_size": 1,
				"status": "normal",
				"region": "hk",
				"source": "manual",
				"sort_key": "latency",
				"sort_dir": "desc"
			},
			"exclude_names": ["delta-hk"]
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	text := rec.Body.String()
	if !strings.Contains(text, `"total": 1`) {
		t.Fatalf("response missing resolved selection count: %s", text)
	}
	if strings.Contains(text, `"requested_tags"`) {
		t.Fatalf("response should not expose requested tags payload: %s", text)
	}
}

func TestHandleConfigNodesBatchToggleResolvesFilterSelection(t *testing.T) {
	server, nodeMgr := newManageListTestServer(t)

	body := `{
		"selection": {
			"mode": "filter",
			"filter": {
				"page": 99,
				"page_size": 1,
				"status": "normal",
				"region": "hk",
				"source": "manual"
			},
			"exclude_names": ["delta-hk"]
		},
		"enabled": false
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleConfigNodesBatchToggle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(nodeMgr.toggles) != 1 {
		t.Fatalf("len(nodeMgr.toggles) = %d, want 1", len(nodeMgr.toggles))
	}
	if nodeMgr.toggles[0].name != "alpha-hk" || nodeMgr.toggles[0].enabled {
		t.Fatalf("toggle call = %#v, want alpha-hk disabled", nodeMgr.toggles[0])
	}
}

func TestHandleExportPostReturnsSelectedURIs(t *testing.T) {
	server, _ := newManageListTestServer(t)

	body := `{
		"selection": {
			"mode": "filter",
			"filter": {
				"status": "normal",
				"region": "hk",
				"source": "manual"
			},
			"exclude_names": ["delta-hk"]
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "trojan://alpha" {
		t.Fatalf("export body = %q, want %q", rec.Body.String(), "trojan://alpha")
	}
}

func TestHandleProbeBatchStreamsSelectedNodesOnly(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	entryA := mgr.Register(NodeInfo{Tag: "a", Name: "node-a"})
	entryA.SetProbe(func(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil })
	entryB := mgr.Register(NodeInfo{Tag: "b", Name: "node-b"})
	entryB.SetProbe(func(ctx context.Context) (time.Duration, error) { return 20 * time.Millisecond, nil })
	entryC := mgr.Register(NodeInfo{Tag: "c", Name: "node-c"})
	entryC.SetProbe(func(ctx context.Context) (time.Duration, error) { return 30 * time.Millisecond, nil })

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	body := `{"tags":["a","c"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleProbeBatch(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	text := string(payload)

	if !strings.Contains(text, `"total": 2`) {
		t.Fatalf("response missing total=2 job payload: %s", text)
	}
	if strings.Contains(text, `"requested_tags"`) {
		t.Fatalf("response should not expose requested tags payload: %s", text)
	}
}

func TestHandleNodeProbePersistsManualProbeResultWithoutMutatingRuntimeState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	node := &store.Node{
		URI:            "trojan://alpha",
		Name:           "alpha",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := dataStore.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "tag-alpha", Name: "alpha", URI: "trojan://alpha"})
	entry.SetProbe(func(ctx context.Context) (time.Duration, error) {
		return 25 * time.Millisecond, nil
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(dataStore)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/tag-alpha/probe", nil)
	rec := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	result, err := dataStore.GetNodeManualProbeResult(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if result == nil {
		t.Fatal("GetNodeManualProbeResult() = nil, want non-nil")
	}
	if result.Status != store.ManualProbeStatusPass {
		t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusPass)
	}
	if result.LatencyMs != 25 {
		t.Fatalf("result.LatencyMs = %d, want 25", result.LatencyMs)
	}

	snap := entry.Snapshot()
	if snap.InitialCheckDone {
		t.Fatalf("snap.InitialCheckDone = true, want false")
	}
	if snap.LastLatencyMs != -1 {
		t.Fatalf("snap.LastLatencyMs = %d, want -1", snap.LastLatencyMs)
	}
}

func TestHandleProbeBatchRejectsEmptyTags(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch", strings.NewReader(`{"tags":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleProbeBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response["error"] == nil {
		t.Fatalf("response error = nil, want non-nil: %#v", response)
	}
}

func TestHandleProbeBatchStartReturnsJob(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entryA := mgr.Register(NodeInfo{Tag: "a", Name: "node-a"})
	entryA.SetProbe(func(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil })

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"tags":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status": "queued"`) && !strings.Contains(rec.Body.String(), `"status": "running"`) {
		t.Fatalf("response missing job status: %s", rec.Body.String())
	}
}

func TestHandleProbeBatchStartPersistsTimeoutManualProbeResult(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	node := &store.Node{
		URI:            "trojan://node-a",
		Name:           "node-a",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := dataStore.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entryA := mgr.Register(NodeInfo{Tag: "a", Name: "node-a", URI: "trojan://node-a"})
	entryA.SetProbe(func(ctx context.Context) (time.Duration, error) {
		return MaxAcceptedProbeLatency + time.Millisecond, nil
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(dataStore)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"tags":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := server.probeJobs.Status()
		if current != nil && current.Status == BatchProbeCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch probe job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	result, err := dataStore.GetNodeManualProbeResult(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if result == nil {
		t.Fatal("GetNodeManualProbeResult() = nil, want non-nil")
	}
	if result.Status != store.ManualProbeStatusTimeout {
		t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusTimeout)
	}
	if !result.TimedOut {
		t.Fatalf("result.TimedOut = false, want true")
	}

	snap := entryA.Snapshot()
	if snap.LastLatencyMs != -1 {
		t.Fatalf("snap.LastLatencyMs = %d, want -1", snap.LastLatencyMs)
	}
	if snap.InitialCheckDone {
		t.Fatalf("snap.InitialCheckDone = true, want false")
	}
}

func TestHandleProbeBatchStatusReturnsRunningJob(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	block := make(chan struct{})
	entryA := mgr.Register(NodeInfo{Tag: "a", Name: "node-a"})
	entryA.SetProbe(func(ctx context.Context) (time.Duration, error) {
		<-block
		return 10 * time.Millisecond, nil
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	startReq := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"tags":["a"]}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	server.handleProbeBatchStart(startRec, startReq)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/nodes/probe-batch/status", nil)
	statusRec := httptest.NewRecorder()
	server.handleProbeBatchStatus(statusRec, statusReq)

	if !strings.Contains(statusRec.Body.String(), `"total": 1`) {
		t.Fatalf("status response missing total: %s", statusRec.Body.String())
	}
	if strings.Contains(statusRec.Body.String(), `"requested_tags"`) {
		t.Fatalf("status response should not expose requested tags payload: %s", statusRec.Body.String())
	}

	close(block)
}

func TestHandleNodesOmitsTimelinePayload(t *testing.T) {
	server, _ := newManageListTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"timeline"`) {
		t.Fatalf("/api/nodes should omit timeline payload: %s", rec.Body.String())
	}
}

func TestHandleNodesReturnsOnlyActiveRuntimeNodes(t *testing.T) {
	server, nodeMgr := newManageListTestServer(t)
	nodeMgr.nodes = []config.NodeConfig{
		{
			Name:           "alpha-hk",
			URI:            "trojan://alpha",
			Port:           1001,
			Source:         config.NodeSourceManual,
			LifecycleState: store.NodeLifecycleActive,
		},
		{
			Name:   "beta-us",
			URI:    "trojan://beta",
			Port:   1002,
			Source: config.NodeSourceManual,
		},
		{
			Name:           "gamma-hk",
			URI:            "trojan://gamma",
			Port:           1003,
			Source:         config.NodeSourceSubscription,
			LifecycleState: store.NodeLifecycleStaged,
		},
		{
			Name:           "delta-hk",
			URI:            "trojan://delta",
			Port:           1004,
			Source:         config.NodeSourceTXTSubscription,
			Disabled:       true,
			LifecycleState: store.NodeLifecycleDisabled,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Nodes         []Snapshot     `json:"nodes"`
		TotalNodes    int            `json:"total_nodes"`
		RegionStats   map[string]int `json:"region_stats"`
		RegionHealthy map[string]int `json:"region_healthy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}

	if response.TotalNodes != 2 {
		t.Fatalf("response.TotalNodes = %d, want 2", response.TotalNodes)
	}
	if len(response.Nodes) != 2 {
		t.Fatalf("len(response.Nodes) = %d, want 2", len(response.Nodes))
	}

	gotNames := []string{response.Nodes[0].Name, response.Nodes[1].Name}
	if !(gotNames[0] == "beta-us" && gotNames[1] == "alpha-hk") {
		t.Fatalf("node names = %#v, want [beta-us alpha-hk]", gotNames)
	}
	if response.RegionStats["hk"] != 1 || response.RegionStats["us"] != 1 {
		t.Fatalf("region_stats = %#v, want hk=1 us=1", response.RegionStats)
	}
	if response.RegionHealthy["hk"] != 1 || response.RegionHealthy["us"] != 1 {
		t.Fatalf("region_healthy = %#v, want hk=1 us=1", response.RegionHealthy)
	}
}

func TestHandleDebugIncludesTimelinePayload(t *testing.T) {
	server, _ := newManageListTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/debug", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"timeline"`) {
		t.Fatalf("/api/debug should include timeline payload: %s", rec.Body.String())
	}
}

func TestHandleImportUsesProvidedNamePrefixForUnnamedNode(t *testing.T) {
	server, nodeMgr := newManageListTestServer(t)
	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{Name: "imported-1", URI: "trojan://existing"})

	var created []config.NodeConfig
	nodeMgr.createNodeFn = func(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
		created = append(created, node)
		nodeMgr.nodes = append(nodeMgr.nodes, node)
		return node, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"content":"socks5://1.1.1.1:1080","name_prefix":"hk"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(created) != 1 {
		t.Fatalf("len(created) = %d, want 1", len(created))
	}
	if created[0].Name != "hk-1" {
		t.Fatalf("created[0].Name = %q, want %q body=%s", created[0].Name, "hk-1", rec.Body.String())
	}

	var response struct {
		Renamed int `json:"renamed"`
		Items   []struct {
			FinalName string `json:"final_name"`
			Reason    string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if response.Renamed != 1 {
		t.Fatalf("response.Renamed = %d, want 1 body=%s", response.Renamed, rec.Body.String())
	}
	if len(response.Items) != 1 {
		t.Fatalf("len(response.Items) = %d, want 1 body=%s", len(response.Items), rec.Body.String())
	}
	if response.Items[0].FinalName != "hk-1" || response.Items[0].Reason != "missing_name" {
		t.Fatalf("response.Items[0] = %+v, want final_name=hk-1 reason=missing_name", response.Items[0])
	}
}

func TestHandleImportFallsBackWhenRequestedNameConflicts(t *testing.T) {
	server, nodeMgr := newManageListTestServer(t)
	nodeMgr.nodes = append(nodeMgr.nodes,
		config.NodeConfig{Name: "US-A", URI: "trojan://existing-name"},
		config.NodeConfig{Name: "hk-7", URI: "trojan://existing-prefix"},
	)

	var created []config.NodeConfig
	nodeMgr.createNodeFn = func(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
		created = append(created, node)
		nodeMgr.nodes = append(nodeMgr.nodes, node)
		return node, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"content":"socks5://1.1.1.1:1080#US-A","name_prefix":"hk"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(created) != 1 {
		t.Fatalf("len(created) = %d, want 1", len(created))
	}
	if created[0].Name != "hk-8" {
		t.Fatalf("created[0].Name = %q, want %q body=%s", created[0].Name, "hk-8", rec.Body.String())
	}

	var response struct {
		Renamed int `json:"renamed"`
		Items   []struct {
			RequestedName string `json:"requested_name"`
			FinalName     string `json:"final_name"`
			Reason        string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if response.Renamed != 1 {
		t.Fatalf("response.Renamed = %d, want 1 body=%s", response.Renamed, rec.Body.String())
	}
	if len(response.Items) != 1 {
		t.Fatalf("len(response.Items) = %d, want 1 body=%s", len(response.Items), rec.Body.String())
	}
	if response.Items[0].RequestedName != "US-A" || response.Items[0].FinalName != "hk-8" || response.Items[0].Reason != "name_conflict" {
		t.Fatalf("response.Items[0] = %+v, want requested_name=US-A final_name=hk-8 reason=name_conflict", response.Items[0])
	}
}

func TestHandleImportKeepsURIConflictAsLineError(t *testing.T) {
	server, nodeMgr := newManageListTestServer(t)
	nodeMgr.createNodeFn = func(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
		return config.NodeConfig{}, fmt.Errorf("save to store: create node: UNIQUE constraint failed: nodes.uri")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"content":"socks5://1.1.1.1:1080","name_prefix":"imported"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `第 1 行添加节点`) {
		t.Fatalf("body missing line-aware URI conflict detail: %s", rec.Body.String())
	}
}

func TestHandleImportStoresNodesAsStagedWhenStoreAvailable(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "import-staged.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)

	createCalls := 0
	nodeMgr.createNodeFn = func(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
		createCalls++
		nodeMgr.nodes = append(nodeMgr.nodes, node)
		return node, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"content":"socks5://1.1.1.1:1080#JP-1","name_prefix":"hk"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0 when store-backed staged import is available", createCalls)
	}

	imported, err := dataStore.GetNodeByName(ctx, "JP-1")
	if err != nil {
		t.Fatalf("GetNodeByName() error = %v", err)
	}
	if imported == nil {
		t.Fatal("GetNodeByName() = nil, want imported staged node")
	}
	if imported.Source != store.NodeSourceManual {
		t.Fatalf("imported.Source = %q, want %q", imported.Source, store.NodeSourceManual)
	}
	if imported.LifecycleState != store.NodeLifecycleStaged {
		t.Fatalf("imported.LifecycleState = %q, want %q", imported.LifecycleState, store.NodeLifecycleStaged)
	}
	if !imported.Enabled {
		t.Fatal("imported.Enabled = false, want true for staged compatibility")
	}
}

func TestHandleImportPreservesExistingStoreNodeOnDuplicateURI(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "import-duplicate.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	existing := &store.Node{
		URI:            "socks5://1.1.1.1:1080",
		Name:           "existing-node",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	}
	if err := dataStore.CreateNode(ctx, existing); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := dataStore.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    existing.ID,
		Status:    store.ManualProbeStatusPass,
		LatencyMs: 66,
		Message:   "keep history",
		CheckedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)
	nodeMgr.nodes = []config.NodeConfig{
		{
			Name:           existing.Name,
			URI:            existing.URI,
			Source:         config.NodeSourceManual,
			LifecycleState: store.NodeLifecycleActive,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"content":"socks5://1.1.1.1:1080","name_prefix":"hk"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	nodes, err := dataStore.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}

	got, err := dataStore.GetNodeByURI(ctx, existing.URI)
	if err != nil {
		t.Fatalf("GetNodeByURI() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNodeByURI() = nil, want non-nil")
	}
	if got.ID != existing.ID {
		t.Fatalf("node ID = %d, want %d", got.ID, existing.ID)
	}
	if got.Name != existing.Name {
		t.Fatalf("Name = %q, want %q", got.Name, existing.Name)
	}
	if got.Source != store.NodeSourceManual {
		t.Fatalf("Source = %q, want %q", got.Source, store.NodeSourceManual)
	}
	if got.LifecycleState != store.NodeLifecycleActive {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, store.NodeLifecycleActive)
	}

	manualProbe, err := dataStore.GetNodeManualProbeResult(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if manualProbe == nil || manualProbe.Message != "keep history" {
		t.Fatalf("manual probe = %#v, want preserved result", manualProbe)
	}

	var response struct {
		Imported int `json:"imported"`
		Items    []struct {
			Status    string `json:"status"`
			FinalName string `json:"final_name"`
			Reason    string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if response.Imported != 0 {
		t.Fatalf("response.Imported = %d, want 0", response.Imported)
	}
	if len(response.Items) != 1 {
		t.Fatalf("len(response.Items) = %d, want 1 body=%s", len(response.Items), rec.Body.String())
	}
	if response.Items[0].Status != "existing" {
		t.Fatalf("response.Items[0].Status = %q, want %q", response.Items[0].Status, "existing")
	}
	if response.Items[0].FinalName != existing.Name {
		t.Fatalf("response.Items[0].FinalName = %q, want %q", response.Items[0].FinalName, existing.Name)
	}
	if response.Items[0].Reason != "duplicate_uri" {
		t.Fatalf("response.Items[0].Reason = %q, want %q", response.Items[0].Reason, "duplicate_uri")
	}
}

func TestHandleProbeBatchStartRejectsWhenJobAlreadyRunning(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	block := make(chan struct{})
	entryA := mgr.Register(NodeInfo{Tag: "a", Name: "node-a"})
	entryA.SetProbe(func(ctx context.Context) (time.Duration, error) {
		<-block
		return 10 * time.Millisecond, nil
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	firstReq := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"tags":["a"]}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	server.handleProbeBatchStart(firstRec, firstReq)

	secondReq := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"tags":["a"]}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	server.handleProbeBatchStart(secondRec, secondReq)

	if secondRec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", secondRec.Code, http.StatusConflict, secondRec.Body.String())
	}

	close(block)
}

func TestHandleProbeBatchStartAcceptsStagedNodeByName(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-probe.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)

	stagedNode := &store.Node{
		URI:            "socks5://142.54.228.193:4145",
		Name:           "staged-probe-node",
		Source:         store.NodeSourceTXTSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{
		Name:           stagedNode.Name,
		URI:            stagedNode.URI,
		Source:         config.NodeSourceTXTSubscription,
		LifecycleState: store.NodeLifecycleStaged,
	})
	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		if node.Name != stagedNode.Name {
			t.Fatalf("CreateConfigNodeRuntime() received %q, want %q", node.Name, stagedNode.Name)
		}
		return &fakeConfigNodeRuntime{
			probeFn: func(ctx context.Context) (time.Duration, error) {
				return 55 * time.Millisecond, nil
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"names":["staged-probe-node"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := server.probeJobs.Status()
		if current != nil && current.Status == BatchProbeCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch probe job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	result, err := dataStore.GetNodeManualProbeResult(ctx, stagedNode.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if result == nil {
		t.Fatal("GetNodeManualProbeResult() = nil, want non-nil")
	}
	if result.Status != store.ManualProbeStatusPass {
		t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusPass)
	}
	if result.LatencyMs != 55 {
		t.Fatalf("result.LatencyMs = %d, want 55", result.LatencyMs)
	}
}

func TestRunManualProbeAndPersistPersistsCreateRuntimeFailureForStagedNode(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-probe-create-error.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)

	stagedNode := &store.Node{
		URI:            "socks5://198.51.100.10:1080",
		Name:           "staged-probe-create-error",
		Source:         store.NodeSourceTXTSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		return nil, context.Canceled
	}

	_, err = server.runManualProbeAndPersist(ctx, BatchProbeTarget{
		Name:        stagedNode.Name,
		StoreNodeID: stagedNode.ID,
		ConfigNode: config.NodeConfig{
			Name:           stagedNode.Name,
			URI:            stagedNode.URI,
			Source:         config.NodeSourceTXTSubscription,
			LifecycleState: store.NodeLifecycleStaged,
		},
	})
	if err == nil {
		t.Fatal("runManualProbeAndPersist() error = nil, want non-nil")
	}

	result, err := dataStore.GetNodeManualProbeResult(ctx, stagedNode.ID)
	if err != nil {
		t.Fatalf("GetNodeManualProbeResult() error = %v", err)
	}
	if result == nil {
		t.Fatal("GetNodeManualProbeResult() = nil, want persisted failure result")
	}
	if result.Status != store.ManualProbeStatusFail {
		t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusFail)
	}
	if result.Message == "" {
		t.Fatal("result.Message = empty, want failure message")
	}
}

func TestHandleProbeBatchStartPersistsTimeoutForStagedNodeWhenRuntimeNeedsClose(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-probe-timeout.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)
	server.probeJobs.probeTimeout = 30 * time.Millisecond
	server.probeJobs.unavailableLatency = 30 * time.Millisecond

	stagedNode := &store.Node{
		URI:            "socks5://198.51.100.20:1080",
		Name:           "staged-probe-timeout",
		Source:         store.NodeSourceTXTSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{
		Name:           stagedNode.Name,
		URI:            stagedNode.URI,
		Source:         config.NodeSourceTXTSubscription,
		LifecycleState: store.NodeLifecycleStaged,
	})

	closed := make(chan struct{})
	var closeOnce sync.Once
	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		return &fakeConfigNodeRuntime{
			probeFn: func(ctx context.Context) (time.Duration, error) {
				select {
				case <-closed:
					return 0, ctx.Err()
				case <-time.After(500 * time.Millisecond):
					return 0, ctx.Err()
				}
			},
			closeFn: func() error {
				closeOnce.Do(func() {
					close(closed)
				})
				return nil
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"names":["staged-probe-timeout"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := server.probeJobs.Status()
		if current != nil && current.Status == BatchProbeCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch probe job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	persistDeadline := time.Now().Add(200 * time.Millisecond)
	for {
		result, err := dataStore.GetNodeManualProbeResult(ctx, stagedNode.ID)
		if err != nil {
			t.Fatalf("GetNodeManualProbeResult() error = %v", err)
		}
		if result != nil {
			if result.Status != store.ManualProbeStatusTimeout {
				t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusTimeout)
			}
			return
		}
		if time.Now().After(persistDeadline) {
			t.Fatal("GetNodeManualProbeResult() = nil, want timeout result soon after job completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleProbeBatchStartPersistsTimeoutForStagedNodeWhenProbeIgnoresClose(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-probe-timeout-ignore-close.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)
	server.probeJobs.probeTimeout = 30 * time.Millisecond
	server.probeJobs.unavailableLatency = 30 * time.Millisecond

	stagedNode := &store.Node{
		URI:            "socks5://198.51.100.21:1080",
		Name:           "staged-probe-timeout-ignore-close",
		Source:         store.NodeSourceTXTSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{
		Name:           stagedNode.Name,
		URI:            stagedNode.URI,
		Source:         config.NodeSourceTXTSubscription,
		LifecycleState: store.NodeLifecycleStaged,
	})

	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		return &fakeConfigNodeRuntime{
			probeFn: func(ctx context.Context) (time.Duration, error) {
				time.Sleep(500 * time.Millisecond)
				return 0, ctx.Err()
			},
			closeFn: func() error {
				return nil
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"names":["staged-probe-timeout-ignore-close"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := server.probeJobs.Status()
		if current != nil && current.Status == BatchProbeCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch probe job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	persistDeadline := time.Now().Add(200 * time.Millisecond)
	for {
		result, err := dataStore.GetNodeManualProbeResult(ctx, stagedNode.ID)
		if err != nil {
			t.Fatalf("GetNodeManualProbeResult() error = %v", err)
		}
		if result != nil {
			if result.Status != store.ManualProbeStatusTimeout {
				t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusTimeout)
			}
			return
		}
		if time.Now().After(persistDeadline) {
			t.Fatal("GetNodeManualProbeResult() = nil, want timeout result without waiting for probe goroutine exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleProbeBatchStartPersistsTimeoutForStagedNodeWhenCreateRuntimeBlocks(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-probe-timeout-create-runtime-blocks.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)
	server.probeJobs.probeTimeout = 30 * time.Millisecond
	server.probeJobs.unavailableLatency = 30 * time.Millisecond

	stagedNode := &store.Node{
		URI:            "http://203.0.113.55:8080",
		Name:           "staged-probe-create-runtime-blocks",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{
		Name:           stagedNode.Name,
		URI:            stagedNode.URI,
		Source:         config.NodeSourceManual,
		LifecycleState: store.NodeLifecycleStaged,
	})

	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})
	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		<-release
		return nil, context.Canceled
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-batch/start", strings.NewReader(`{"names":["staged-probe-create-runtime-blocks"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleProbeBatchStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := server.probeJobs.Status()
		if current != nil && current.Status == BatchProbeCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch probe job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	persistDeadline := time.Now().Add(200 * time.Millisecond)
	for {
		result, err := dataStore.GetNodeManualProbeResult(ctx, stagedNode.ID)
		if err != nil {
			t.Fatalf("GetNodeManualProbeResult() error = %v", err)
		}
		if result != nil {
			if result.Status != store.ManualProbeStatusTimeout {
				t.Fatalf("result.Status = %q, want %q", result.Status, store.ManualProbeStatusTimeout)
			}
			return
		}
		if time.Now().After(persistDeadline) {
			t.Fatal("GetNodeManualProbeResult() = nil, want timeout result soon after job completion when runtime creation blocks")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleQualityCheckBatchAcceptsStagedNodeByName(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "staged-quality.db")
	dataStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer dataStore.Close()

	server, nodeMgr := newManageListTestServer(t)
	server.SetStore(dataStore)

	stagedNode := &store.Node{
		URI:            "socks5://68.71.249.153:48606",
		Name:           "staged-quality-node",
		Source:         store.NodeSourceTXTSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := dataStore.CreateNode(ctx, stagedNode); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := dataStore.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    stagedNode.ID,
		Status:    store.ManualProbeStatusPass,
		LatencyMs: 88,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}

	nodeMgr.nodes = append(nodeMgr.nodes, config.NodeConfig{
		Name:           stagedNode.Name,
		URI:            stagedNode.URI,
		Source:         config.NodeSourceTXTSubscription,
		LifecycleState: store.NodeLifecycleStaged,
	})
	nodeMgr.createConfigNodeRuntimeFn = func(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
		if node.Name != stagedNode.Name {
			t.Fatalf("CreateConfigNodeRuntime() received %q, want %q", node.Name, stagedNode.Name)
		}
		return &fakeConfigNodeRuntime{
			httpRequestFn: func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
				switch rawURL {
				case "https://api.openai.com/v1/models":
					return mockOpenAIUnauthorizedResult(180), nil
				case "https://api.anthropic.com/v1/messages":
					return mockAnthropicMethodNotAllowedResult(190), nil
				default:
					t.Fatalf("unexpected quality target %s", rawURL)
					return nil, nil
				}
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"names":["staged-quality-node"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleQualityCheckBatch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	completedJob := waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.Status == BatchQualityCompleted
	})
	if completedJob.LastResult == nil {
		t.Fatalf("LastResult = nil")
	}
	if completedJob.LastResult.Name != stagedNode.Name {
		t.Fatalf("LastResult.Name = %q, want %q", completedJob.LastResult.Name, stagedNode.Name)
	}
	if completedJob.LastResult.QualityStatus != quality.StatusDualAvailable {
		t.Fatalf("QualityStatus = %q, want %q", completedJob.LastResult.QualityStatus, quality.StatusDualAvailable)
	}
	if !completedJob.LastResult.ActivationReady {
		t.Fatalf("ActivationReady = false, want true")
	}

	check, err := dataStore.GetNodeQualityCheck(ctx, stagedNode.ID)
	if err != nil {
		t.Fatalf("GetNodeQualityCheck() error = %v", err)
	}
	if check == nil {
		t.Fatal("GetNodeQualityCheck() = nil, want non-nil")
	}
	if check.QualityStatus != quality.StatusDualAvailable {
		t.Fatalf("check.QualityStatus = %q, want %q", check.QualityStatus, quality.StatusDualAvailable)
	}
	if got, want := len(check.Items), 2; got != want {
		t.Fatalf("len(check.Items) = %d, want %d", got, want)
	}
}
