package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/config"
)

type toggleCall struct {
	name    string
	enabled bool
}

type fakeNodeManager struct {
	nodes    []config.NodeConfig
	toggles  []toggleCall
	deleted  []string
	reloaded bool
}

func (f *fakeNodeManager) ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error) {
	cloned := make([]config.NodeConfig, len(f.nodes))
	copy(cloned, f.nodes)
	return cloned, nil
}

func (f *fakeNodeManager) CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
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
