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

	if !strings.Contains(text, `"total": 2`) {
		t.Fatalf("response missing total=2 job payload: %s", text)
	}
	if !strings.Contains(text, `"requested_tags": [`) || !strings.Contains(text, `"a"`) || !strings.Contains(text, `"c"`) {
		t.Fatalf("response missing selected tags: %s", text)
	}
	if strings.Contains(text, `"b"`) {
		t.Fatalf("response should not include unselected tag b: %s", text)
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

	close(block)
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
