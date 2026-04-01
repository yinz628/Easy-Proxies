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

	"easy_proxies/internal/store"
)

func TestHandleQualityCheckReturnsSummary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.CreateNode(ctx, &store.Node{
		URI:     "http://1.1.1.1:80",
		Name:    "node-a",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-a", Name: "node-a", URI: "http://1.1.1.1:80"})
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		switch rawURL {
		case "http://ip-api.com/json/?lang=en":
			return &HTTPCheckResult{
				StatusCode: http.StatusOK,
				LatencyMs:  120,
				Body:       []byte(`{"status":"success","query":"1.1.1.1","country":"US","countryCode":"US","regionName":"California"}`),
				Headers:    map[string]string{},
			}, nil
		case "https://api.openai.com/v1/models":
			return &HTTPCheckResult{StatusCode: http.StatusUnauthorized, LatencyMs: 180, Headers: map[string]string{}, Body: []byte(`{}`)}, nil
		case "https://api.anthropic.com/v1/messages":
			return &HTTPCheckResult{StatusCode: http.StatusMethodNotAllowed, LatencyMs: 190, Headers: map[string]string{}, Body: []byte(`{}`)}, nil
		case "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta":
			return &HTTPCheckResult{StatusCode: http.StatusOK, LatencyMs: 200, Headers: map[string]string{}, Body: []byte(`{}`)}, nil
		default:
			return nil, nil
		}
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-a/quality-check", nil)
	rec := httptest.NewRecorder()
	server.handleNodeAction(rec, req)

	var response struct {
		Message string                 `json:"message"`
		Result  store.NodeQualityCheck `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if response.Message == "" {
		t.Fatalf("message = empty, response=%s", rec.Body.String())
	}
	if response.Result.QualityGrade == "" {
		t.Fatalf("quality grade = empty, response=%s", rec.Body.String())
	}
}

func TestHandleQualityCheckRejectsUnknownTag(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/missing/quality-check", nil)
	rec := httptest.NewRecorder()
	server.handleNodeAction(rec, req)

	if !strings.Contains(rec.Body.String(), "数据存储未初始化") && !strings.Contains(rec.Body.String(), "node missing not found") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestHandleQualityCheckGetReturnsPersistedResult(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-get.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &store.Node{
		URI:     "http://3.3.3.3:80",
		Name:    "node-c",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := st.SaveNodeQualityCheck(ctx, &store.NodeQualityCheck{
		NodeID:           node.ID,
		QualityStatus:    "healthy",
		QualityScore:     intPtr(88),
		QualityGrade:     "B",
		QualitySummary:   "persisted",
		QualityCheckedAt: time.Now(),
		Items: []store.NodeQualityCheckItem{
			{Target: "base_connectivity", Status: "pass", HTTPStatus: 200, LatencyMs: 99, Message: "ok"},
		},
	}); err != nil {
		t.Fatalf("SaveNodeQualityCheck() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	mgr.Register(NodeInfo{Tag: "node-c", Name: "node-c", URI: node.URI})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-c/quality-check", nil)
	rec := httptest.NewRecorder()
	server.handleNodeAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "persisted") {
		t.Fatalf("response missing persisted summary: %s", rec.Body.String())
	}
}

func TestHandleQualityCheckBatchRejectsEmptyTags(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleQualityCheckBatch(rec, req)

	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want bad request or service unavailable", rec.Code)
	}
}

func TestQualityCheckPersistsTimestamp(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-ts.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &store.Node{URI: "http://2.2.2.2:80", Name: "node-b", Source: store.NodeSourceManual, Enabled: true}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	check := &store.NodeQualityCheck{
		NodeID:           node.ID,
		QualityStatus:    "healthy",
		QualityScore:     intPtr(90),
		QualityGrade:     "A",
		QualitySummary:   "ok",
		QualityCheckedAt: time.Now(),
	}
	if err := st.SaveNodeQualityCheck(ctx, check); err != nil {
		t.Fatalf("SaveNodeQualityCheck() error = %v", err)
	}
	got, err := st.GetNodeQualityCheck(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeQualityCheck() error = %v", err)
	}
	if got == nil || got.QualityCheckedAt.IsZero() {
		t.Fatalf("QualityCheckedAt not persisted: %+v", got)
	}
}

func intPtr(v int) *int {
	return &v
}
