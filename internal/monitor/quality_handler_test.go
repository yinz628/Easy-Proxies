package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/quality"
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
	requestedURLs := make([]string, 0, 2)
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		requestedURLs = append(requestedURLs, rawURL)
		switch rawURL {
		case "https://api.openai.com/v1/models":
			return mockOpenAIUnauthorizedResult(180), nil
		case "https://api.anthropic.com/v1/messages":
			return mockAnthropicMethodNotAllowedResult(190), nil
		default:
			return nil, fmt.Errorf("unexpected quality target %s", rawURL)
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
	if got, want := len(response.Result.Items), 2; got != want {
		t.Fatalf("quality item count = %d, want %d response=%s", got, want, rec.Body.String())
	}
	if containsString(requestedURLs, "http://ip-api.com/json/?lang=en") {
		t.Fatalf("unexpected ip-api request, requested=%v", requestedURLs)
	}
	if strings.Contains(rec.Body.String(), "gemini") {
		t.Fatalf("response unexpectedly contains gemini target: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "base_connectivity") {
		t.Fatalf("response unexpectedly contains base_connectivity: %s", rec.Body.String())
	}
	assertQualityAggregate(
		t,
		response.Result.QualityStatus,
		response.Result.QualityGrade,
		response.Result.QualitySummary,
		quality.StatusDualAvailable,
		"A",
		"OpenAI \u53ef\u7528\uff0cAnthropic \u53ef\u7528",
	)
	assertItemSet(t, response.Result.Items, map[string]qualityItemExpectation{
		quality.TargetOpenAIReachability: {
			Status:     quality.StatusPass,
			HTTPStatus: http.StatusUnauthorized,
		},
		quality.TargetAnthropicReachability: {
			Status:     quality.StatusPass,
			HTTPStatus: http.StatusMethodNotAllowed,
		},
	})
	if response.Result.ExitIP != "" || response.Result.ExitCountry != "" || response.Result.ExitCountryCode != "" || response.Result.ExitRegion != "" {
		t.Fatalf("exit metadata should be empty, result=%+v", response.Result)
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

	if !strings.Contains(rec.Body.String(), "数据存储未初始化") && !strings.Contains(rec.Body.String(), "鏁版嵁瀛樺偍鏈垵濮嬪寲") && !strings.Contains(rec.Body.String(), "node missing not found") {
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
			{Target: quality.TargetOpenAIReachability, Status: quality.StatusPass, HTTPStatus: http.StatusUnauthorized, LatencyMs: 99, Message: "ok"},
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
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-empty-tags.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleQualityCheckBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rec.Body.String())
	}
	if payload.Error == "" {
		t.Fatalf("error should not be empty, body=%s", rec.Body.String())
	}
}

func TestHandleQualityCheckBatchIncludesPersistedDetailPayload(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-batch.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.CreateNode(ctx, &store.Node{
		URI:     "http://4.4.4.4:80",
		Name:    "node-d",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-d", Name: "node-d", URI: "http://4.4.4.4:80"})
	requestedURLs := make([]string, 0, 2)
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		requestedURLs = append(requestedURLs, rawURL)
		switch rawURL {
		case "https://api.openai.com/v1/models":
			return mockOpenAIUnauthorizedResult(180), nil
		case "https://api.anthropic.com/v1/messages":
			return mockAnthropicMethodNotAllowedResult(190), nil
		default:
			return nil, fmt.Errorf("unexpected quality target %s", rawURL)
		}
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":["node-d"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleQualityCheckBatch(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"job"`) {
		t.Fatalf("batch response missing job payload: %s", body)
	}
	if containsString(requestedURLs, "http://ip-api.com/json/?lang=en") {
		t.Fatalf("unexpected ip-api request, requested=%v", requestedURLs)
	}
	if strings.Contains(body, "gemini") {
		t.Fatalf("batch response unexpectedly contains gemini target: %s", body)
	}
	if strings.Contains(body, "base_connectivity") {
		t.Fatalf("batch response unexpectedly contains base_connectivity: %s", body)
	}
	if strings.Contains(body, "exit_ip") {
		t.Fatalf("batch response unexpectedly contains exit metadata: %s", body)
	}

	var startPayload struct {
		Job *BatchQualityJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, body)
	}
	if startPayload.Job == nil {
		t.Fatalf("job should not be nil, body=%s", body)
	}

	completed := waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.ID == startPayload.Job.ID && job.Status == BatchQualityCompleted
	})
	if completed.LastResult == nil {
		t.Fatalf("LastResult = nil")
	}

	assertQualityAggregate(
		t,
		completed.LastResult.QualityStatus,
		completed.LastResult.QualityGrade,
		completed.LastResult.QualitySummary,
		quality.StatusDualAvailable,
		"A",
		"OpenAI \u53ef\u7528\uff0cAnthropic \u53ef\u7528",
	)
	assertItemSet(t, completed.LastResult.Items, map[string]qualityItemExpectation{
		quality.TargetOpenAIReachability: {
			Status:     quality.StatusPass,
			HTTPStatus: http.StatusUnauthorized,
		},
		quality.TargetAnthropicReachability: {
			Status:     quality.StatusPass,
			HTTPStatus: http.StatusMethodNotAllowed,
		},
	})
	if completed.LastResult.QualityCheckedAt.IsZero() {
		t.Fatalf("QualityCheckedAt should not be zero")
	}
}

func TestQualityCheckerRejectsAllowedStatusWithoutOfficialSignature(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-signature.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.CreateNode(ctx, &store.Node{
		URI:     "http://5.5.5.5:80",
		Name:    "node-e",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-e", Name: "node-e", URI: "http://5.5.5.5:80"})
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		switch rawURL {
		case "https://api.openai.com/v1/models":
			return &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				LatencyMs:  120,
				Headers: map[string]string{
					"Content-Type": "application/json; charset=utf-8",
				},
				Body: []byte(`{"error":{"message":"You didn't provide an API key.","type":"invalid_request_error","code":null}}`),
			}, nil
		case "https://api.anthropic.com/v1/messages":
			return &HTTPCheckResult{
				StatusCode: http.StatusMethodNotAllowed,
				LatencyMs:  130,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: []byte(`{"message":"method not allowed"}`),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected quality target %s", rawURL)
		}
	})

	checker := NewQualityChecker(mgr, st, nil)
	result, err := checker.CheckNode(ctx, "node-e")
	if err != nil {
		t.Fatalf("CheckNode() error = %v", err)
	}

	assertQualityAggregate(
		t,
		result.QualityStatus,
		result.QualityGrade,
		result.QualitySummary,
		quality.StatusUnavailable,
		"F",
		"OpenAI \u4e0d\u53ef\u7528\uff0cAnthropic \u4e0d\u53ef\u7528",
	)
	assertItemSet(t, result.Items, map[string]qualityItemExpectation{
		quality.TargetOpenAIReachability: {
			Status:     quality.StatusFail,
			HTTPStatus: http.StatusUnauthorized,
		},
		quality.TargetAnthropicReachability: {
			Status:     quality.StatusFail,
			HTTPStatus: http.StatusMethodNotAllowed,
		},
	})
}

func TestMatchesOfficialAPISignatureAcceptsPrettyJSONWithProviderSignals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		resp   *HTTPCheckResult
	}{
		{
			name:   "openai pretty json with openai header",
			target: quality.TargetOpenAIReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"Content-Type":   "application/json; charset=utf-8",
					"OpenAI-Version": "2020-10-01",
				},
				Body: []byte("{\n  \"error\": {\n    \"message\": \"You didn't provide an API key.\",\n    \"type\": \"invalid_request_error\",\n    \"code\": null\n  }\n}"),
			},
		},
		{
			name:   "anthropic pretty json with request-id header",
			target: quality.TargetAnthropicReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"Content-Type": "application/json",
					"Request-Id":   "req-header",
				},
				Body: []byte("{\n  \"error\": {\n    \"message\": \"authentication failed\",\n    \"type\": \"authentication_error\"\n  },\n  \"type\": \"error\"\n}"),
			},
		},
		{
			name:   "anthropic pretty json with request_id field",
			target: quality.TargetAnthropicReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: []byte("{\n  \"error\": {\n    \"message\": \"authentication failed\",\n    \"type\": \"authentication_error\"\n  },\n  \"type\": \"error\",\n  \"request_id\": \"req-body\"\n}"),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !matchesOfficialAPISignature(tt.target, tt.resp) {
				t.Fatalf("matchesOfficialAPISignature(%q) = false, want true", tt.target)
			}
		})
	}
}

func TestMatchesOfficialAPISignatureAcceptsOfficialSignalsWithoutEnvelopeHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		resp   *HTTPCheckResult
	}{
		{
			name:   "openai header only empty body",
			target: quality.TargetOpenAIReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"OpenAI-Processing-Ms": "12",
				},
				Body: []byte(""),
			},
		},
		{
			name:   "anthropic request-id header only empty body",
			target: quality.TargetAnthropicReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"Request-Id": "req-header-only",
				},
				Body: []byte(""),
			},
		},
		{
			name:   "anthropic request_id body without headers",
			target: quality.TargetAnthropicReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Body:       []byte(`{"type":"error","error":{"type":"authentication_error","message":"authentication failed"},"request_id":"req-body-only"}`),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !matchesOfficialAPISignature(tt.target, tt.resp) {
				t.Fatalf("matchesOfficialAPISignature(%q) = false, want true", tt.target)
			}
		})
	}
}

func TestMatchesOfficialAPISignatureRejectsGenericJSONWithoutProviderSignals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		resp   *HTTPCheckResult
	}{
		{
			name:   "openai invalid_request_error without openai headers",
			target: quality.TargetOpenAIReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusUnauthorized,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: []byte(`{"error":{"message":"You didn't provide an API key.","type":"invalid_request_error","code":null}}`),
			},
		},
		{
			name:   "anthropic generic method not allowed only",
			target: quality.TargetAnthropicReachability,
			resp: &HTTPCheckResult{
				StatusCode: http.StatusMethodNotAllowed,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: []byte(`{"type":"error","error":{"type":"bad_request","message":"method not allowed"}}`),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if matchesOfficialAPISignature(tt.target, tt.resp) {
				t.Fatalf("matchesOfficialAPISignature(%q) = true, want false", tt.target)
			}
		})
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

func TestHandleQualityCheckIncludesActivationReadiness(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "quality-activation.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &store.Node{
		URI:            "http://10.10.10.10:80",
		Name:           "node-activation",
		Source:         store.NodeSourceManual,
		Enabled:        true,
		LifecycleState: store.NodeLifecycleStaged,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := st.SaveNodeManualProbeResult(ctx, &store.NodeManualProbeResult{
		NodeID:    node.ID,
		Status:    store.ManualProbeStatusPass,
		LatencyMs: 120,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveNodeManualProbeResult() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-activation", Name: "node-activation", URI: node.URI})
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		switch rawURL {
		case "https://api.openai.com/v1/models":
			return mockOpenAIUnauthorizedResult(180), nil
		case "https://api.anthropic.com/v1/messages":
			return mockAnthropicMethodNotAllowedResult(190), nil
		default:
			return nil, fmt.Errorf("unexpected quality target %s", rawURL)
		}
	})

	checker := NewQualityChecker(mgr, st, nil)
	result, err := checker.CheckNode(ctx, "node-activation")
	if err != nil {
		t.Fatalf("CheckNode() error = %v", err)
	}

	if !result.ActivationReady {
		t.Fatalf("ActivationReady = false, want true")
	}
	if result.ActivationBlockReason != "" {
		t.Fatalf("ActivationBlockReason = %q, want empty", result.ActivationBlockReason)
	}
}

func intPtr(v int) *int {
	return &v
}

type qualityProgressPayload struct {
	Type            string                       `json:"type"`
	QualityStatus   string                       `json:"quality_status"`
	QualityGrade    string                       `json:"quality_grade"`
	QualitySummary  string                       `json:"quality_summary"`
	ExitIP          string                       `json:"exit_ip"`
	ExitCountry     string                       `json:"exit_country"`
	ExitCountryCode string                       `json:"exit_country_code"`
	ExitRegion      string                       `json:"exit_region"`
	Items           []store.NodeQualityCheckItem `json:"items"`
}

func extractProgressEvent(t *testing.T, body string) qualityProgressPayload {
	t.Helper()

	for _, event := range strings.Split(body, "\n\n") {
		event = strings.TrimSpace(event)
		if !strings.HasPrefix(event, "data: ") {
			continue
		}

		var payload qualityProgressPayload
		if err := json.Unmarshal([]byte(strings.TrimPrefix(event, "data: ")), &payload); err != nil {
			t.Fatalf("json.Unmarshal(progress event) error = %v body=%s", err, body)
		}
		if payload.Type == "progress" {
			return payload
		}
	}

	t.Fatalf("progress event not found in body=%s", body)
	return qualityProgressPayload{}
}

type qualityItemExpectation struct {
	Status     string
	HTTPStatus int
}

func assertQualityAggregate(t *testing.T, gotStatus, gotGrade, gotSummary, wantStatus, wantGrade, wantSummary string) {
	t.Helper()

	if gotStatus != wantStatus {
		t.Fatalf("QualityStatus = %q, want %q", gotStatus, wantStatus)
	}
	if gotGrade != wantGrade {
		t.Fatalf("QualityGrade = %q, want %q", gotGrade, wantGrade)
	}
	if gotSummary != wantSummary {
		t.Fatalf("QualitySummary = %q, want %q", gotSummary, wantSummary)
	}
}

func assertItemSet(t *testing.T, items []store.NodeQualityCheckItem, want map[string]qualityItemExpectation) {
	t.Helper()

	if got := len(items); got != len(want) {
		t.Fatalf("item count = %d, want %d", got, len(want))
	}

	actual := make(map[string]store.NodeQualityCheckItem, len(items))
	for _, item := range items {
		actual[item.Target] = item
	}

	for target, expected := range want {
		item, ok := actual[target]
		if !ok {
			t.Fatalf("missing item for target %q, items=%+v", target, items)
		}
		if item.Status != expected.Status {
			t.Fatalf("%s status = %q, want %q", target, item.Status, expected.Status)
		}
		if item.HTTPStatus != expected.HTTPStatus {
			t.Fatalf("%s http status = %d, want %d", target, item.HTTPStatus, expected.HTTPStatus)
		}
	}
}

func mockOpenAIUnauthorizedResult(latencyMs int64) *HTTPCheckResult {
	return &HTTPCheckResult{
		StatusCode: http.StatusUnauthorized,
		LatencyMs:  latencyMs,
		Headers: map[string]string{
			"Content-Type":         "application/json; charset=utf-8",
			"OpenAI-Processing-Ms": "12",
		},
		Body: []byte(`{"error":{"message":"You didn't provide an API key.","type":"invalid_request_error","param":null,"code":null}}`),
	}
}

func mockAnthropicMethodNotAllowedResult(latencyMs int64) *HTTPCheckResult {
	return &HTTPCheckResult{
		StatusCode: http.StatusMethodNotAllowed,
		LatencyMs:  latencyMs,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Request-Id":   "req-anthropic",
		},
		Body: []byte(`{"type":"error","error":{"type":"method_not_allowed","message":"Method not allowed"},"request_id":"req-anthropic"}`),
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
