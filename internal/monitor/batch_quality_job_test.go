package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

func TestBatchQualityJobManagerTracksProgressAndCompletion(t *testing.T) {
	manager := NewBatchQualityJobManager(2)
	release := make(chan struct{})

	var running int32
	var maxRunning int32
	var startedCount int32

	job, err := manager.Start(
		[]BatchQualityTarget{
			{Tag: "node-a", Name: "node-a"},
			{Tag: "node-b", Name: "node-b"},
			{Tag: "node-c", Name: "node-c"},
		},
		func(ctx context.Context, target BatchQualityTarget) (*store.NodeQualityCheck, error) {
			current := atomic.AddInt32(&running, 1)
			updateMaxInt32(&maxRunning, current)
			atomic.AddInt32(&startedCount, 1)
			<-release
			atomic.AddInt32(&running, -1)

			score := 100
			return &store.NodeQualityCheck{
				QualityVersion:         quality.QualityVersionAIReachabilityV2,
				QualityStatus:          quality.StatusDualAvailable,
				QualityOpenAIStatus:    quality.StatusPass,
				QualityAnthropicStatus: quality.StatusPass,
				QualityScore:           &score,
				QualityGrade:           "A",
				QualitySummary:         "all good",
				QualityCheckedAt:       time.Now(),
				Items: []store.NodeQualityCheckItem{
					{Target: quality.TargetOpenAIReachability, Status: quality.StatusPass, HTTPStatus: http.StatusUnauthorized},
					{Target: quality.TargetAnthropicReachability, Status: quality.StatusPass, HTTPStatus: http.StatusMethodNotAllowed},
				},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	waitForBatchQualityJob(t, manager, 2*time.Second, func(current *BatchQualityJob) bool {
		return current != nil && current.ID == job.ID && current.ActiveWorkers == 2
	})

	time.Sleep(50 * time.Millisecond)
	if got, want := atomic.LoadInt32(&startedCount), int32(2); got != want {
		t.Fatalf("started workers = %d, want %d before release", got, want)
	}

	close(release)

	current := waitForBatchQualityJob(t, manager, 2*time.Second, func(current *BatchQualityJob) bool {
		return current != nil && current.ID == job.ID && current.Status == BatchQualityCompleted
	})

	if got, want := current.Completed, 3; got != want {
		t.Fatalf("Completed = %d, want %d", got, want)
	}
	if got, want := current.Success, 3; got != want {
		t.Fatalf("Success = %d, want %d", got, want)
	}
	if got, want := current.Failed, 0; got != want {
		t.Fatalf("Failed = %d, want %d", got, want)
	}
	if got, want := atomic.LoadInt32(&maxRunning), int32(2); got != want {
		t.Fatalf("max concurrent workers = %d, want %d", got, want)
	}
	if current.LastResult == nil {
		t.Fatalf("LastResult = nil")
	}
	if current.LastResult.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("LastResult.QualityVersion = %q", current.LastResult.QualityVersion)
	}
	if current.LastResult.QualityStatus != quality.StatusDualAvailable {
		t.Fatalf("LastResult.QualityStatus = %q", current.LastResult.QualityStatus)
	}
	if current.LastResult.QualityOpenAIStatus != quality.StatusPass {
		t.Fatalf("LastResult.QualityOpenAIStatus = %q", current.LastResult.QualityOpenAIStatus)
	}
	if current.LastResult.QualityAnthropicStatus != quality.StatusPass {
		t.Fatalf("LastResult.QualityAnthropicStatus = %q", current.LastResult.QualityAnthropicStatus)
	}
	if current.LastResult.QualityScore == nil || *current.LastResult.QualityScore != 100 {
		t.Fatalf("LastResult.QualityScore = %#v, want 100", current.LastResult.QualityScore)
	}
	if current.LastResult.QualityGrade != "A" {
		t.Fatalf("LastResult.QualityGrade = %q", current.LastResult.QualityGrade)
	}
	if current.LastResult.QualitySummary != "all good" {
		t.Fatalf("LastResult.QualitySummary = %q", current.LastResult.QualitySummary)
	}
	if current.LastResult.QualityCheckedAt.IsZero() {
		t.Fatalf("LastResult.QualityCheckedAt should be set")
	}
	if got, want := len(current.LastResult.Items), 2; got != want {
		t.Fatalf("len(LastResult.Items) = %d, want %d", got, want)
	}
}

func TestHandleQualityCheckBatchStatus(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "batch-quality-status.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &store.Node{
		URI:     "http://6.6.6.6:80",
		Name:    "node-status",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-status", Name: "node-status", URI: node.URI})

	firstTargetStarted := make(chan struct{})
	release := make(chan struct{})
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		switch rawURL {
		case "https://api.openai.com/v1/models":
			select {
			case <-firstTargetStarted:
			default:
				close(firstTargetStarted)
			}
			<-release
			return mockOpenAIUnauthorizedResult(120), nil
		case "https://api.anthropic.com/v1/messages":
			return mockAnthropicMethodNotAllowedResult(130), nil
		default:
			t.Fatalf("unexpected quality target %s", rawURL)
			return nil, nil
		}
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	startReq := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":["node-status"]}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	server.handleQualityCheckBatch(startRec, startReq)

	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want %d body=%s", startRec.Code, http.StatusOK, startRec.Body.String())
	}

	var startPayload struct {
		Job *BatchQualityJob `json:"job"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal(start) error = %v body=%s", err, startRec.Body.String())
	}
	if startPayload.Job == nil {
		t.Fatalf("start job should not be nil, body=%s", startRec.Body.String())
	}

	select {
	case <-firstTargetStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("quality check did not start")
	}

	runningJob := waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.ID == startPayload.Job.ID && job.Status == BatchQualityRunning
	})
	if got, want := runningJob.Total, 1; got != want {
		t.Fatalf("running Total = %d, want %d", got, want)
	}

	close(release)

	completedJob := waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.ID == startPayload.Job.ID && job.Status == BatchQualityCompleted
	})
	if got, want := completedJob.Completed, 1; got != want {
		t.Fatalf("Completed = %d, want %d", got, want)
	}
	if got, want := completedJob.Success, 1; got != want {
		t.Fatalf("Success = %d, want %d", got, want)
	}
	if completedJob.LastResult == nil {
		t.Fatalf("LastResult = nil")
	}
	if completedJob.LastResult.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("QualityVersion = %q", completedJob.LastResult.QualityVersion)
	}
	if completedJob.LastResult.QualityStatus != quality.StatusDualAvailable {
		t.Fatalf("QualityStatus = %q", completedJob.LastResult.QualityStatus)
	}
	if completedJob.LastResult.QualityOpenAIStatus != quality.StatusPass {
		t.Fatalf("QualityOpenAIStatus = %q", completedJob.LastResult.QualityOpenAIStatus)
	}
	if completedJob.LastResult.QualityAnthropicStatus != quality.StatusPass {
		t.Fatalf("QualityAnthropicStatus = %q", completedJob.LastResult.QualityAnthropicStatus)
	}
	if completedJob.LastResult.QualityScore == nil || *completedJob.LastResult.QualityScore != 100 {
		t.Fatalf("QualityScore = %#v, want 100", completedJob.LastResult.QualityScore)
	}
	if got, want := completedJob.LastResult.QualityGrade, "A"; got != want {
		t.Fatalf("QualityGrade = %q, want %q", got, want)
	}
	if got, want := completedJob.LastResult.QualitySummary, "OpenAI 可用，Anthropic 可用"; got != want {
		t.Fatalf("QualitySummary = %q, want %q", got, want)
	}
	if completedJob.LastResult.QualityCheckedAt.IsZero() {
		t.Fatalf("QualityCheckedAt should not be zero")
	}
	if got, want := len(completedJob.LastResult.Items), 2; got != want {
		t.Fatalf("len(Items) = %d, want %d", got, want)
	}

	statusRec := httptest.NewRecorder()
	server.handleQualityCheckBatchStatus(statusRec, httptest.NewRequest(http.MethodGet, "/api/nodes/quality-check-batch/status", nil))
	if strings.Contains(statusRec.Body.String(), "exit_ip") {
		t.Fatalf("status response should not include exit fields: %s", statusRec.Body.String())
	}
}

func TestHandleQualityCheckBatchCancelCancelsRunningJob(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "batch-quality-cancel.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &store.Node{
		URI:     "http://7.7.7.7:80",
		Name:    "node-cancel",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	entry := mgr.Register(NodeInfo{Tag: "node-cancel", Name: "node-cancel", URI: node.URI})
	entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	startReq := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":["node-cancel"]}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	server.handleQualityCheckBatch(startRec, startReq)

	var startPayload struct {
		Job *BatchQualityJob `json:"job"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal(start) error = %v body=%s", err, startRec.Body.String())
	}
	if startPayload.Job == nil {
		t.Fatalf("job should not be nil, body=%s", startRec.Body.String())
	}

	waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.ID == startPayload.Job.ID && (job.Status == BatchQualityQueued || job.Status == BatchQualityRunning)
	})

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch/cancel", strings.NewReader(`{"job_id":"`+startPayload.Job.ID+`"}`))
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelRec := httptest.NewRecorder()
	server.handleQualityCheckBatchCancel(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d body=%s", cancelRec.Code, http.StatusOK, cancelRec.Body.String())
	}

	cancelled := waitForQualityStatus(t, server, 2*time.Second, func(job *BatchQualityJob) bool {
		return job != nil && job.ID == startPayload.Job.ID && job.Status == BatchQualityCancelled
	})
	if cancelled.ActiveWorkers != 0 {
		t.Fatalf("ActiveWorkers = %d, want 0", cancelled.ActiveWorkers)
	}
}

func TestHandleQualityCheckBatchStreamEmitsEachCompletedResult(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "batch-quality-stream.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	nodes := []*store.Node{
		{URI: "http://8.8.8.8:80", Name: "node-stream-a", Source: store.NodeSourceManual, Enabled: true},
		{URI: "http://9.9.9.9:80", Name: "node-stream-b", Source: store.NodeSourceManual, Enabled: true},
	}
	for _, node := range nodes {
		if err := st.CreateNode(ctx, node); err != nil {
			t.Fatalf("CreateNode(%s) error = %v", node.Name, err)
		}
	}

	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	release := make(chan struct{})
	var startedCount int32
	for _, node := range nodes {
		entry := mgr.Register(NodeInfo{Tag: node.Name, Name: node.Name, URI: node.URI})
		entry.SetHTTPRequest(func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
			switch rawURL {
			case "https://api.openai.com/v1/models":
				atomic.AddInt32(&startedCount, 1)
				<-release
				return mockOpenAIUnauthorizedResult(120), nil
			case "https://api.anthropic.com/v1/messages":
				return mockAnthropicMethodNotAllowedResult(130), nil
			default:
				t.Fatalf("unexpected quality target %s", rawURL)
				return nil, nil
			}
		})
	}

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, nil)
	server.cfg.Password = ""
	server.SetStore(st)

	startReq := httptest.NewRequest(http.MethodPost, "/api/nodes/quality-check-batch", strings.NewReader(`{"tags":["node-stream-a","node-stream-b"]}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	server.handleQualityCheckBatch(startRec, startReq)

	var startPayload struct {
		Job *BatchQualityJob `json:"job"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal(start) error = %v body=%s", err, startRec.Body.String())
	}
	if startPayload.Job == nil {
		t.Fatalf("job should not be nil, body=%s", startRec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&startedCount) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("quality checks did not start before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	streamReq := httptest.NewRequest(http.MethodGet, "/api/nodes/quality-check-batch/stream?job_id="+startPayload.Job.ID, nil)
	streamRec := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		server.handleQualityCheckBatchStream(streamRec, streamReq)
		close(streamDone)
	}()

	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("stream did not complete")
	}

	body := streamRec.Body.String()
	if got, want := strings.Count(body, `"type":"progress"`), 2; got != want {
		t.Fatalf("progress event count = %d, want %d body=%s", got, want, body)
	}
	if !strings.Contains(body, `"type":"complete"`) {
		t.Fatalf("stream missing complete event: %s", body)
	}
	if strings.Contains(body, "exit_ip") {
		t.Fatalf("stream should not include exit metadata: %s", body)
	}
}

func waitForBatchQualityJob(t *testing.T, manager *BatchQualityJobManager, timeout time.Duration, predicate func(job *BatchQualityJob) bool) *BatchQualityJob {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		current := manager.Status()
		if predicate(current) {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch quality job did not reach expected state: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForQualityStatus(t *testing.T, server *Server, timeout time.Duration, predicate func(job *BatchQualityJob) bool) *BatchQualityJob {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		rec := httptest.NewRecorder()
		server.handleQualityCheckBatchStatus(rec, httptest.NewRequest(http.MethodGet, "/api/nodes/quality-check-batch/status", nil))

		var payload struct {
			Job *BatchQualityJob `json:"job"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(status) error = %v body=%s", err, rec.Body.String())
		}
		if predicate(payload.Job) {
			return payload.Job
		}
		if time.Now().After(deadline) {
			t.Fatalf("status job did not reach expected state: %+v body=%s", payload.Job, rec.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func updateMaxInt32(dst *int32, current int32) {
	for {
		existing := atomic.LoadInt32(dst)
		if current <= existing {
			return
		}
		if atomic.CompareAndSwapInt32(dst, existing, current) {
			return
		}
	}
}
