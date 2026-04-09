package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"easy_proxies/internal/config"
)

type BatchProbeJobStatus string

const (
	BatchProbeQueued    BatchProbeJobStatus = "queued"
	BatchProbeRunning   BatchProbeJobStatus = "running"
	BatchProbeCompleted BatchProbeJobStatus = "completed"
	BatchProbeFailed    BatchProbeJobStatus = "failed"
	BatchProbeCancelled BatchProbeJobStatus = "cancelled"
)

var ErrBatchProbeJobRunning = errors.New("a batch probe job is already running")

const defaultBatchProbeTimeout = 10 * time.Second

type BatchProbeJobResult struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type BatchProbeTarget struct {
	Tag         string
	Name        string
	StoreNodeID int64
	ConfigNode  config.NodeConfig
}

type BatchProbeJob struct {
	ID            string               `json:"id"`
	Status        BatchProbeJobStatus  `json:"status"`
	StartedAt     time.Time            `json:"started_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	CompletedAt   *time.Time           `json:"completed_at,omitempty"`
	Total         int                  `json:"total"`
	Completed     int                  `json:"completed"`
	Success       int                  `json:"success"`
	Failed        int                  `json:"failed"`
	ActiveWorkers int                  `json:"active_workers"`
	LastResult    *BatchProbeJobResult `json:"last_result,omitempty"`
	LastError     string               `json:"last_error,omitempty"`
}

type BatchProbeJobManager struct {
	mu                 sync.RWMutex
	job                *BatchProbeJob
	cancel             context.CancelFunc
	workers            int
	probeTimeout       time.Duration
	unavailableLatency time.Duration
}

func NewBatchProbeJobManager(workers int) *BatchProbeJobManager {
	if workers <= 0 {
		workers = 8
	}
	return &BatchProbeJobManager{
		workers:            workers,
		probeTimeout:       defaultBatchProbeTimeout,
		unavailableLatency: defaultBatchProbeTimeout,
	}
}

func (m *BatchProbeJobManager) Start(targets []BatchProbeTarget, probeFn func(ctx context.Context, target BatchProbeTarget) (int64, error)) (*BatchProbeJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job != nil && (m.job.Status == BatchProbeQueued || m.job.Status == BatchProbeRunning) {
		return nil, ErrBatchProbeJobRunning
	}

	now := time.Now()
	job := &BatchProbeJob{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Status:    BatchProbeQueued,
		StartedAt: now,
		UpdatedAt: now,
		Total:     len(targets),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.job = job
	m.cancel = cancel

	go m.run(ctx, job, targets, probeFn)

	return cloneBatchProbeJob(job), nil
}

func (m *BatchProbeJobManager) Status() *BatchProbeJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneBatchProbeJob(m.job)
}

func (m *BatchProbeJobManager) Cancel(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job == nil || m.job.ID != jobID {
		return fmt.Errorf("batch probe job %q not found", jobID)
	}
	if m.job.Status != BatchProbeQueued && m.job.Status != BatchProbeRunning {
		return fmt.Errorf("batch probe job %q is not running", jobID)
	}
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

func (m *BatchProbeJobManager) run(ctx context.Context, job *BatchProbeJob, targets []BatchProbeTarget, probeFn func(ctx context.Context, target BatchProbeTarget) (int64, error)) {
	m.update(job.ID, func(current *BatchProbeJob) {
		current.Status = BatchProbeRunning
		current.UpdatedAt = time.Now()
	})

	workCh := make(chan BatchProbeTarget)
	var wg sync.WaitGroup

	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range workCh {
				m.update(job.ID, func(current *BatchProbeJob) {
					current.ActiveWorkers++
					current.UpdatedAt = time.Now()
				})

				latency, err := m.runProbe(ctx, target, probeFn)
				result := BatchProbeJobResult{
					Tag:       target.Tag,
					Name:      target.Name,
					LatencyMs: latency,
				}
				if err != nil {
					result.Error = err.Error()
				}

				m.update(job.ID, func(current *BatchProbeJob) {
					current.ActiveWorkers--
					current.Completed++
					if err != nil {
						current.Failed++
						current.LastError = err.Error()
					} else {
						current.Success++
					}
					current.LastResult = &result
					current.UpdatedAt = time.Now()
				})
			}
		}()
	}

	go func() {
		for _, target := range targets {
			select {
			case <-ctx.Done():
				close(workCh)
				return
			case workCh <- target:
			}
		}
		close(workCh)
	}()

	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || m.job.ID != job.ID {
		return
	}
	now := time.Now()
	if ctx.Err() != nil {
		m.job.Status = BatchProbeCancelled
	} else {
		m.job.Status = BatchProbeCompleted
	}
	m.job.CompletedAt = &now
	m.job.ActiveWorkers = 0
	m.job.UpdatedAt = now
	m.cancel = nil
}

type batchProbeResult struct {
	latency int64
	err     error
}

func (m *BatchProbeJobManager) runProbe(ctx context.Context, target BatchProbeTarget, probeFn func(ctx context.Context, target BatchProbeTarget) (int64, error)) (int64, error) {
	timeout := m.effectiveProbeTimeout()
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan batchProbeResult, 1)
	go func() {
		latency, err := probeFn(probeCtx, target)
		resultCh <- batchProbeResult{latency: latency, err: err}
	}()

	select {
	case <-probeCtx.Done():
		return -1, batchProbeContextError(probeCtx.Err(), timeout)
	case result := <-resultCh:
		if err := probeCtx.Err(); err != nil {
			return -1, batchProbeContextError(err, timeout)
		}
		if result.err != nil {
			return -1, result.err
		}
		maxLatency := m.effectiveUnavailableLatency()
		if result.latency > maxLatency.Milliseconds() {
			return -1, fmt.Errorf("probe exceeded %dms", maxLatency.Milliseconds())
		}
		return result.latency, nil
	}
}

func (m *BatchProbeJobManager) effectiveProbeTimeout() time.Duration {
	if m.probeTimeout > 0 {
		return m.probeTimeout
	}
	return defaultBatchProbeTimeout
}

func (m *BatchProbeJobManager) effectiveUnavailableLatency() time.Duration {
	if m.unavailableLatency > 0 {
		return m.unavailableLatency
	}
	return m.effectiveProbeTimeout()
}

func batchProbeContextError(err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("probe timeout after %dms", timeout.Milliseconds())
	}
	return fmt.Errorf("probe cancelled: %w", err)
}

func (m *BatchProbeJobManager) update(jobID string, mutate func(job *BatchProbeJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || m.job.ID != jobID {
		return
	}
	mutate(m.job)
}

func cloneBatchProbeJob(job *BatchProbeJob) *BatchProbeJob {
	if job == nil {
		return nil
	}
	cloned := *job
	if job.LastResult != nil {
		resultCopy := *job.LastResult
		cloned.LastResult = &resultCopy
	}
	return &cloned
}
