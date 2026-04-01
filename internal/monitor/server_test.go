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
)

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

	if !strings.Contains(text, `"type":"start","total":2`) {
		t.Fatalf("response missing start event for 2 nodes: %s", text)
	}
	if !strings.Contains(text, `"tag":"a"`) || !strings.Contains(text, `"tag":"c"`) {
		t.Fatalf("response missing selected tags: %s", text)
	}
	if strings.Contains(text, `"tag":"b"`) {
		t.Fatalf("response should not include unselected tag b: %s", text)
	}
	if !strings.Contains(text, `"type":"complete","total":2`) {
		t.Fatalf("response missing complete event: %s", text)
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
