package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleDebugCountsPeriodicProbeResults(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Stop()

	success := mgr.Register(NodeInfo{Tag: "tag-success", Name: "success-node"})
	success.SetProbe(func(ctx context.Context) (time.Duration, error) {
		return 25 * time.Millisecond, nil
	})

	failed := mgr.Register(NodeInfo{Tag: "tag-failed", Name: "failed-node"})
	failed.SetProbe(func(ctx context.Context) (time.Duration, error) {
		return 0, errors.New("dial timeout")
	})

	mgr.probeNodes(200*time.Millisecond, false)

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""

	req := httptest.NewRequest(http.MethodGet, "/api/debug", nil)
	rec := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Nodes        []struct {
			Tag           string          `json:"tag"`
			FailureCount  int             `json:"failure_count"`
			SuccessCount  int64           `json:"success_count"`
			LastLatencyMs int64           `json:"last_latency_ms"`
			LastError     string          `json:"last_error"`
			Timeline      []TimelineEvent `json:"timeline"`
		} `json:"nodes"`
		TotalCalls   int64       `json:"total_calls"`
		TotalSuccess int64       `json:"total_success"`
		SuccessRate  float64     `json:"success_rate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}

	if response.TotalCalls != 2 {
		t.Fatalf("total_calls = %d, want 2", response.TotalCalls)
	}
	if response.TotalSuccess != 1 {
		t.Fatalf("total_success = %d, want 1", response.TotalSuccess)
	}
	if response.SuccessRate != 50 {
		t.Fatalf("success_rate = %v, want 50", response.SuccessRate)
	}
	if len(response.Nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(response.Nodes))
	}

	var successNode, failedNode *struct {
		Tag           string          `json:"tag"`
		FailureCount  int             `json:"failure_count"`
		SuccessCount  int64           `json:"success_count"`
		LastLatencyMs int64           `json:"last_latency_ms"`
		LastError     string          `json:"last_error"`
		Timeline      []TimelineEvent `json:"timeline"`
	}
	for idx := range response.Nodes {
		node := &response.Nodes[idx]
		switch node.Tag {
		case "tag-success":
			successNode = node
		case "tag-failed":
			failedNode = node
		}
	}

	if successNode == nil {
		t.Fatal("success node missing from debug response")
	}
	if successNode.SuccessCount != 1 {
		t.Fatalf("success node success_count = %d, want 1", successNode.SuccessCount)
	}
	if successNode.LastLatencyMs != 25 {
		t.Fatalf("success node last_latency_ms = %d, want 25", successNode.LastLatencyMs)
	}
	if len(successNode.Timeline) != 1 || !successNode.Timeline[0].Success {
		t.Fatalf("success node timeline = %#v, want single success event", successNode.Timeline)
	}

	if failedNode == nil {
		t.Fatal("failed node missing from debug response")
	}
	if failedNode.FailureCount != 1 {
		t.Fatalf("failed node failure_count = %d, want 1", failedNode.FailureCount)
	}
	if failedNode.LastError != "dial timeout" {
		t.Fatalf("failed node last_error = %q, want %q", failedNode.LastError, "dial timeout")
	}
	if len(failedNode.Timeline) != 1 || failedNode.Timeline[0].Success {
		t.Fatalf("failed node timeline = %#v, want single failure event", failedNode.Timeline)
	}
}
