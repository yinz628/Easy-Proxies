package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

type lifecycleTestNodeManager struct {
	nodes       []config.NodeConfig
	reloadCount int
}

func (m *lifecycleTestNodeManager) ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error) {
	cloned := make([]config.NodeConfig, len(m.nodes))
	copy(cloned, m.nodes)
	return cloned, nil
}

func (m *lifecycleTestNodeManager) CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
	m.nodes = append(m.nodes, node)
	return node, nil
}

func (m *lifecycleTestNodeManager) UpdateNode(ctx context.Context, name string, node config.NodeConfig) (config.NodeConfig, error) {
	for idx := range m.nodes {
		if m.nodes[idx].Name == name {
			m.nodes[idx] = node
			return node, nil
		}
	}
	return config.NodeConfig{}, ErrNodeNotFound
}

func (m *lifecycleTestNodeManager) DeleteNode(ctx context.Context, name string) error {
	return nil
}

func (m *lifecycleTestNodeManager) SetNodeEnabled(ctx context.Context, name string, enabled bool) error {
	return nil
}

func (m *lifecycleTestNodeManager) TriggerReload(ctx context.Context) error {
	m.reloadCount++
	return nil
}

func (m *lifecycleTestNodeManager) CreateConfigNodeRuntime(ctx context.Context, node config.NodeConfig) (ConfigNodeRuntime, error) {
	return nil, ErrNodeNotFound
}

type batchLifecycleResponse struct {
	Message string   `json:"message"`
	Action  string   `json:"action"`
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

func newLifecycleTestServer(t *testing.T, st store.Store, nodeMgr *lifecycleTestNodeManager) *Server {
	t.Helper()

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)
	server.SetNodeManager(nodeMgr)
	return server
}

func createLifecycleTestStore(t *testing.T) store.Store {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	return st
}

func createStoreNode(t *testing.T, st store.Store, node *store.Node) *store.Node {
	t.Helper()

	if err := st.CreateNode(context.Background(), node); err != nil {
		t.Fatalf("CreateNode(%s) error = %v", node.Name, err)
	}
	return node
}

func saveManualProbeResult(t *testing.T, st store.Store, nodeID int64, status string) {
	t.Helper()

	if err := st.SaveNodeManualProbeResult(context.Background(), &store.NodeManualProbeResult{
		NodeID:    nodeID,
		Status:    status,
		LatencyMs: 120,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult(%d) error = %v", nodeID, err)
	}
}

func saveOpenAIReadyQuality(t *testing.T, st store.Store, nodeID int64) {
	t.Helper()

	if err := st.SaveNodeQualityCheck(context.Background(), &store.NodeQualityCheck{
		NodeID:                 nodeID,
		QualityVersion:         quality.QualityVersionAIReachabilityV2,
		QualityStatus:          quality.StatusOpenAIOnly,
		QualityOpenAIStatus:    quality.StatusPass,
		QualityAnthropicStatus: quality.StatusFail,
		QualityGrade:           "B",
		QualitySummary:         "OpenAI 可用",
		QualityCheckedAt:       time.Now().UTC(),
		Items: []store.NodeQualityCheckItem{
			{Target: quality.TargetOpenAIReachability, Status: quality.StatusPass, HTTPStatus: http.StatusUnauthorized},
			{Target: quality.TargetAnthropicReachability, Status: quality.StatusFail, HTTPStatus: http.StatusForbidden},
		},
	}); err != nil {
		t.Fatalf("SaveNodeQualityCheck(%d) error = %v", nodeID, err)
	}
}

func decodeBatchLifecycleResponse(t *testing.T, body string) batchLifecycleResponse {
	t.Helper()

	var response batchLifecycleResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, body)
	}
	return response
}

func TestHandleBatchLifecycleActivatesReadyStagedNode(t *testing.T) {
	st := createLifecycleTestStore(t)
	defer st.Close()

	node := createStoreNode(t, st, &store.Node{
		Name:           "staged-ready",
		URI:            "socks5://10.0.0.1:1080",
		Source:         store.NodeSourceSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	})
	saveManualProbeResult(t, st, node.ID, store.ManualProbeStatusPass)
	saveOpenAIReadyQuality(t, st, node.ID)

	nodeMgr := &lifecycleTestNodeManager{
		nodes: []config.NodeConfig{
			{
				Name:                   node.Name,
				URI:                    node.URI,
				Source:                 config.NodeSourceSubscription,
				LifecycleState:         store.NodeLifecycleStaged,
				QualityVersion:         quality.QualityVersionAIReachabilityV2,
				QualityStatus:          quality.StatusOpenAIOnly,
				QualityOpenAIStatus:    quality.StatusPass,
				QualityAnthropicStatus: quality.StatusFail,
				QualityGrade:           "B",
				QualitySummary:         "OpenAI 可用",
			},
		},
	}
	server := newLifecycleTestServer(t, st, nodeMgr)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-lifecycle", strings.NewReader(`{"selection":{"mode":"names","names":["staged-ready"]},"action":"activate"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	response := decodeBatchLifecycleResponse(t, rec.Body.String())
	if response.Total != 1 || response.Success != 1 || response.Skipped != 0 {
		t.Fatalf("response = %+v, want total=1 success=1 skipped=0", response)
	}
	if len(response.Errors) != 0 {
		t.Fatalf("response.Errors = %#v, want empty", response.Errors)
	}
	if nodeMgr.reloadCount != 1 {
		t.Fatalf("reloadCount = %d, want 1", nodeMgr.reloadCount)
	}

	updated, err := st.GetNode(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if updated == nil || updated.LifecycleState != store.NodeLifecycleActive {
		t.Fatalf("updated lifecycle = %#v, want active", updated)
	}
}

func TestHandleBatchLifecycleSkipsBlockedStagedNode(t *testing.T) {
	st := createLifecycleTestStore(t)
	defer st.Close()

	node := createStoreNode(t, st, &store.Node{
		Name:           "staged-blocked",
		URI:            "socks5://10.0.0.2:1080",
		Source:         store.NodeSourceSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	})
	saveManualProbeResult(t, st, node.ID, store.ManualProbeStatusFail)
	saveOpenAIReadyQuality(t, st, node.ID)

	nodeMgr := &lifecycleTestNodeManager{
		nodes: []config.NodeConfig{
			{
				Name:                   node.Name,
				URI:                    node.URI,
				Source:                 config.NodeSourceSubscription,
				LifecycleState:         store.NodeLifecycleStaged,
				QualityVersion:         quality.QualityVersionAIReachabilityV2,
				QualityStatus:          quality.StatusOpenAIOnly,
				QualityOpenAIStatus:    quality.StatusPass,
				QualityAnthropicStatus: quality.StatusFail,
				QualityGrade:           "B",
				QualitySummary:         "OpenAI 可用",
			},
		},
	}
	server := newLifecycleTestServer(t, st, nodeMgr)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-lifecycle", strings.NewReader(`{"selection":{"mode":"names","names":["staged-blocked"]},"action":"activate"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	response := decodeBatchLifecycleResponse(t, rec.Body.String())
	if response.Total != 1 || response.Success != 0 || response.Skipped != 1 {
		t.Fatalf("response = %+v, want total=1 success=0 skipped=1", response)
	}
	if len(response.Errors) != 1 || !strings.Contains(response.Errors[0], "staged-blocked") {
		t.Fatalf("response.Errors = %#v, want blocked node error", response.Errors)
	}
	if nodeMgr.reloadCount != 0 {
		t.Fatalf("reloadCount = %d, want 0", nodeMgr.reloadCount)
	}

	updated, err := st.GetNode(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if updated == nil || updated.LifecycleState != store.NodeLifecycleStaged {
		t.Fatalf("updated lifecycle = %#v, want staged", updated)
	}
}

func TestHandleBatchLifecycleDeactivatesActiveNode(t *testing.T) {
	st := createLifecycleTestStore(t)
	defer st.Close()

	node := createStoreNode(t, st, &store.Node{
		Name:           "active-node",
		URI:            "socks5://10.0.0.3:1080",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	})

	nodeMgr := &lifecycleTestNodeManager{
		nodes: []config.NodeConfig{
			{
				Name:           node.Name,
				URI:            node.URI,
				Source:         config.NodeSourceManual,
				LifecycleState: store.NodeLifecycleActive,
			},
		},
	}
	server := newLifecycleTestServer(t, st, nodeMgr)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-lifecycle", strings.NewReader(`{"selection":{"mode":"names","names":["active-node"]},"action":"deactivate"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	response := decodeBatchLifecycleResponse(t, rec.Body.String())
	if response.Total != 1 || response.Success != 1 || response.Skipped != 0 {
		t.Fatalf("response = %+v, want total=1 success=1 skipped=0", response)
	}
	if nodeMgr.reloadCount != 1 {
		t.Fatalf("reloadCount = %d, want 1", nodeMgr.reloadCount)
	}

	updated, err := st.GetNode(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if updated == nil || updated.LifecycleState != store.NodeLifecycleStaged {
		t.Fatalf("updated lifecycle = %#v, want staged", updated)
	}
}

func TestHandleBatchLifecycleDisablesStoreNodesAndSkipsInlineNodes(t *testing.T) {
	st := createLifecycleTestStore(t)
	defer st.Close()

	alpha := createStoreNode(t, st, &store.Node{
		Name:           "active-store-a",
		URI:            "socks5://10.0.0.4:1080",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	})
	beta := createStoreNode(t, st, &store.Node{
		Name:           "active-store-b",
		URI:            "socks5://10.0.0.5:1080",
		Source:         store.NodeSourceSubscription,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleActive,
	})

	nodeMgr := &lifecycleTestNodeManager{
		nodes: []config.NodeConfig{
			{
				Name:           alpha.Name,
				URI:            alpha.URI,
				Source:         config.NodeSourceManual,
				LifecycleState: store.NodeLifecycleActive,
			},
			{
				Name:           beta.Name,
				URI:            beta.URI,
				Source:         config.NodeSourceSubscription,
				LifecycleState: store.NodeLifecycleActive,
			},
			{
				Name:           "inline-only",
				URI:            "socks5://10.0.0.99:1080",
				Source:         config.NodeSourceInline,
				LifecycleState: store.NodeLifecycleActive,
			},
		},
	}
	server := newLifecycleTestServer(t, st, nodeMgr)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-lifecycle", strings.NewReader(`{"selection":{"mode":"filter","filter":{"lifecycle_state":"active"}},"action":"disable"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	response := decodeBatchLifecycleResponse(t, rec.Body.String())
	if response.Total != 3 || response.Success != 2 || response.Skipped != 1 {
		t.Fatalf("response = %+v, want total=3 success=2 skipped=1", response)
	}
	if len(response.Errors) != 1 || !strings.Contains(response.Errors[0], "inline-only") {
		t.Fatalf("response.Errors = %#v, want inline node skip", response.Errors)
	}
	if nodeMgr.reloadCount != 1 {
		t.Fatalf("reloadCount = %d, want 1", nodeMgr.reloadCount)
	}

	for _, nodeID := range []int64{alpha.ID, beta.ID} {
		updated, err := st.GetNode(context.Background(), nodeID)
		if err != nil {
			t.Fatalf("GetNode(%d) error = %v", nodeID, err)
		}
		if updated == nil || updated.LifecycleState != store.NodeLifecycleDisabled {
			t.Fatalf("updated lifecycle for %d = %#v, want disabled", nodeID, updated)
		}
	}
}
