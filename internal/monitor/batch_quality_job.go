package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

type BatchQualityJobStatus string

const (
	BatchQualityQueued    BatchQualityJobStatus = "queued"
	BatchQualityRunning   BatchQualityJobStatus = "running"
	BatchQualityCompleted BatchQualityJobStatus = "completed"
	BatchQualityFailed    BatchQualityJobStatus = "failed"
	BatchQualityCancelled BatchQualityJobStatus = "cancelled"
)

var ErrBatchQualityJobRunning = errors.New("a batch quality job is already running")

const defaultBatchQualityCheckTimeout = 45 * time.Second

type BatchQualityTarget struct {
	Tag         string
	Name        string
	StoreNodeID int64
	ConfigNode  config.NodeConfig
}

type BatchQualityJobResult struct {
	Tag                    string                       `json:"tag"`
	Name                   string                       `json:"name"`
	URI                    string                       `json:"uri"`
	Error                  string                       `json:"error,omitempty"`
	QualityVersion         string                       `json:"quality_version,omitempty"`
	QualityStatus          string                       `json:"quality_status,omitempty"`
	QualityOpenAIStatus    string                       `json:"quality_openai_status,omitempty"`
	QualityAnthropicStatus string                       `json:"quality_anthropic_status,omitempty"`
	ActivationReady        bool                         `json:"activation_ready"`
	ActivationBlockReason  string                       `json:"activation_block_reason,omitempty"`
	QualityScore           *int                         `json:"quality_score,omitempty"`
	QualityGrade           string                       `json:"quality_grade,omitempty"`
	QualitySummary         string                       `json:"quality_summary,omitempty"`
	QualityCheckedAt       time.Time                    `json:"quality_checked_at,omitempty"`
	Items                  []store.NodeQualityCheckItem `json:"items,omitempty"`
}

type BatchQualityJob struct {
	ID            string                 `json:"id"`
	Status        BatchQualityJobStatus  `json:"status"`
	StartedAt     time.Time              `json:"started_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	CompletedAt   *time.Time             `json:"completed_at,omitempty"`
	Total         int                    `json:"total"`
	Completed     int                    `json:"completed"`
	Success       int                    `json:"success"`
	Failed        int                    `json:"failed"`
	ActiveWorkers int                    `json:"active_workers"`
	LastResult    *BatchQualityJobResult `json:"last_result,omitempty"`
	LastError     string                 `json:"last_error,omitempty"`
}

type BatchQualityJobEvent struct {
	JobID     string
	Completed int
	Success   int
	Failed    int
	Total     int
	Result    *BatchQualityJobResult
}

type BatchQualityJobManager struct {
	mu               sync.RWMutex
	job              *BatchQualityJob
	cancel           context.CancelFunc
	workers          int
	checkTimeout     time.Duration
	subscribers      map[uint64]chan BatchQualityJobEvent
	nextSubscriberID uint64
}

func NewBatchQualityJobManager(workers int) *BatchQualityJobManager {
	if workers <= 0 {
		workers = 4
	}
	return &BatchQualityJobManager{
		workers:      workers,
		checkTimeout: defaultBatchQualityCheckTimeout,
		subscribers:  make(map[uint64]chan BatchQualityJobEvent),
	}
}

func (m *BatchQualityJobManager) Start(targets []BatchQualityTarget, checkFn func(ctx context.Context, target BatchQualityTarget) (*store.NodeQualityCheck, error)) (*BatchQualityJob, error) {
	if checkFn == nil {
		return nil, errors.New("batch quality check function is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job != nil && (m.job.Status == BatchQualityQueued || m.job.Status == BatchQualityRunning) {
		return nil, ErrBatchQualityJobRunning
	}

	now := time.Now()
	job := &BatchQualityJob{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Status:    BatchQualityQueued,
		StartedAt: now,
		UpdatedAt: now,
		Total:     len(targets),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.closeSubscribersLocked()
	m.job = job
	m.cancel = cancel

	go m.run(ctx, job, targets, checkFn)

	return cloneBatchQualityJob(job), nil
}

func (m *BatchQualityJobManager) Status() *BatchQualityJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneBatchQualityJob(m.job)
}

func (m *BatchQualityJobManager) Cancel(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job == nil || m.job.ID != jobID {
		return fmt.Errorf("batch quality job %q not found", jobID)
	}
	if m.job.Status != BatchQualityQueued && m.job.Status != BatchQualityRunning {
		return fmt.Errorf("batch quality job %q is not running", jobID)
	}
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

func (m *BatchQualityJobManager) Subscribe(jobID string) (*BatchQualityJob, <-chan BatchQualityJobEvent, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job == nil || m.job.ID != jobID {
		return nil, nil, nil, fmt.Errorf("batch quality job %q not found", jobID)
	}

	id := m.nextSubscriberID
	m.nextSubscriberID++
	ch := make(chan BatchQualityJobEvent, m.workers*4)
	m.subscribers[id] = ch

	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if subscriber, ok := m.subscribers[id]; ok {
			delete(m.subscribers, id)
			close(subscriber)
		}
	}

	return cloneBatchQualityJob(m.job), ch, cancel, nil
}

func (m *BatchQualityJobManager) run(ctx context.Context, job *BatchQualityJob, targets []BatchQualityTarget, checkFn func(ctx context.Context, target BatchQualityTarget) (*store.NodeQualityCheck, error)) {
	m.update(job.ID, func(current *BatchQualityJob) {
		current.Status = BatchQualityRunning
		current.UpdatedAt = time.Now()
	})

	if len(targets) == 0 {
		m.finish(job.ID, BatchQualityCompleted)
		return
	}

	workCh := make(chan BatchQualityTarget)
	var wg sync.WaitGroup

	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range workCh {
				m.update(job.ID, func(current *BatchQualityJob) {
					current.ActiveWorkers++
					current.UpdatedAt = time.Now()
				})

				result, err := m.runCheck(ctx, target, checkFn)
				event := m.recordResult(job.ID, result, err)
				if event != nil {
					m.broadcast(*event)
				}
			}
		}()
	}

	go func() {
		defer close(workCh)
		for _, target := range targets {
			select {
			case <-ctx.Done():
				return
			case workCh <- target:
			}
		}
	}()

	wg.Wait()
	if ctx.Err() != nil {
		m.finish(job.ID, BatchQualityCancelled)
		return
	}
	m.finish(job.ID, BatchQualityCompleted)
}

type batchQualityOutcome struct {
	check *store.NodeQualityCheck
	err   error
}

func (m *BatchQualityJobManager) runCheck(ctx context.Context, target BatchQualityTarget, checkFn func(ctx context.Context, target BatchQualityTarget) (*store.NodeQualityCheck, error)) (*BatchQualityJobResult, error) {
	timeout := m.effectiveCheckTimeout()
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan batchQualityOutcome, 1)
	go func() {
		check, err := checkFn(checkCtx, target)
		resultCh <- batchQualityOutcome{check: check, err: err}
	}()

	select {
	case <-checkCtx.Done():
		err := batchQualityContextError(checkCtx.Err(), timeout)
		return batchQualityJobResultFromStore(target, nil, err), err
	case outcome := <-resultCh:
		if err := checkCtx.Err(); err != nil {
			wrapped := batchQualityContextError(err, timeout)
			return batchQualityJobResultFromStore(target, nil, wrapped), wrapped
		}
		if outcome.err != nil {
			return batchQualityJobResultFromStore(target, outcome.check, outcome.err), outcome.err
		}
		return batchQualityJobResultFromStore(target, outcome.check, nil), nil
	}
}

func (m *BatchQualityJobManager) effectiveCheckTimeout() time.Duration {
	if m.checkTimeout > 0 {
		return m.checkTimeout
	}
	return defaultBatchQualityCheckTimeout
}

func batchQualityContextError(err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("quality check timeout after %dms", timeout.Milliseconds())
	}
	return fmt.Errorf("quality check cancelled: %w", err)
}

func batchQualityJobResultFromStore(target BatchQualityTarget, check *store.NodeQualityCheck, err error) *BatchQualityJobResult {
	result := &BatchQualityJobResult{
		Tag:  target.Tag,
		Name: target.Name,
		URI:  target.ConfigNode.URI,
	}
	if err != nil {
		result.Error = err.Error()
	}
	if check == nil {
		return result
	}

	result.QualityVersion = check.QualityVersion
	result.QualityStatus = check.QualityStatus
	result.QualityOpenAIStatus = check.QualityOpenAIStatus
	result.QualityAnthropicStatus = check.QualityAnthropicStatus
	result.ActivationReady = check.ActivationReady
	result.ActivationBlockReason = check.ActivationBlockReason
	if check.QualityScore != nil {
		score := *check.QualityScore
		result.QualityScore = &score
	}
	result.QualityGrade = check.QualityGrade
	result.QualitySummary = check.QualitySummary
	result.QualityCheckedAt = check.QualityCheckedAt
	if len(check.Items) > 0 {
		result.Items = make([]store.NodeQualityCheckItem, len(check.Items))
		copy(result.Items, check.Items)
	}
	return result
}

func (m *BatchQualityJobManager) update(jobID string, mutate func(job *BatchQualityJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || m.job.ID != jobID {
		return
	}
	mutate(m.job)
}

func (m *BatchQualityJobManager) finish(jobID string, status BatchQualityJobStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || m.job.ID != jobID {
		return
	}
	now := time.Now()
	m.job.Status = status
	m.job.CompletedAt = &now
	m.job.ActiveWorkers = 0
	m.job.UpdatedAt = now
	m.cancel = nil
}

func (m *BatchQualityJobManager) recordResult(jobID string, result *BatchQualityJobResult, err error) *BatchQualityJobEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || m.job.ID != jobID {
		return nil
	}

	m.job.ActiveWorkers--
	m.job.Completed++
	m.job.LastResult = cloneBatchQualityJobResult(result)
	m.job.UpdatedAt = time.Now()
	if err != nil {
		m.job.Failed++
		m.job.LastError = err.Error()
	} else {
		m.job.Success++
	}

	return &BatchQualityJobEvent{
		JobID:     m.job.ID,
		Completed: m.job.Completed,
		Success:   m.job.Success,
		Failed:    m.job.Failed,
		Total:     m.job.Total,
		Result:    cloneBatchQualityJobResult(result),
	}
}

func (m *BatchQualityJobManager) broadcast(event BatchQualityJobEvent) {
	m.mu.RLock()
	channels := make([]chan BatchQualityJobEvent, 0, len(m.subscribers))
	for _, ch := range m.subscribers {
		channels = append(channels, ch)
	}
	m.mu.RUnlock()

	for _, ch := range channels {
		ch <- cloneBatchQualityJobEvent(event)
	}
}

func (m *BatchQualityJobManager) closeSubscribersLocked() {
	for id, ch := range m.subscribers {
		delete(m.subscribers, id)
		close(ch)
	}
}

func cloneBatchQualityJob(job *BatchQualityJob) *BatchQualityJob {
	if job == nil {
		return nil
	}

	cloned := *job
	if job.LastResult != nil {
		cloned.LastResult = cloneBatchQualityJobResult(job.LastResult)
	}
	return &cloned
}

func cloneBatchQualityJobResult(result *BatchQualityJobResult) *BatchQualityJobResult {
	if result == nil {
		return nil
	}

	cloned := *result
	if result.QualityScore != nil {
		score := *result.QualityScore
		cloned.QualityScore = &score
	}
	if len(result.Items) > 0 {
		cloned.Items = make([]store.NodeQualityCheckItem, len(result.Items))
		copy(cloned.Items, result.Items)
	}
	return &cloned
}

func cloneBatchQualityJobEvent(event BatchQualityJobEvent) BatchQualityJobEvent {
	cloned := event
	cloned.Result = cloneBatchQualityJobResult(event.Result)
	return cloned
}
