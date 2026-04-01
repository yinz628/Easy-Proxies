package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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

type BatchProbeJobResult struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type BatchProbeJob struct {
	ID            string              `json:"id"`
	Status        BatchProbeJobStatus `json:"status"`
	StartedAt     time.Time           `json:"started_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	CompletedAt   *time.Time          `json:"completed_at,omitempty"`
	Total         int                 `json:"total"`
	Completed     int                 `json:"completed"`
	Success       int                 `json:"success"`
	Failed        int                 `json:"failed"`
	ActiveWorkers int                 `json:"active_workers"`
	RequestedTags []string            `json:"requested_tags"`
	LastResult    *BatchProbeJobResult `json:"last_result,omitempty"`
	LastError     string              `json:"last_error,omitempty"`
}

type BatchProbeJobManager struct {
	mu      sync.RWMutex
	job     *BatchProbeJob
	cancel  context.CancelFunc
	workers int
}

func NewBatchProbeJobManager(workers int) *BatchProbeJobManager {
	if workers <= 0 {
		workers = 8
	}
	return &BatchProbeJobManager{workers: workers}
}

func (m *BatchProbeJobManager) Start(tags []string, snapshots []Snapshot, probeFn func(ctx context.Context, snap Snapshot) (int64, error)) (*BatchProbeJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.job != nil && (m.job.Status == BatchProbeQueued || m.job.Status == BatchProbeRunning) {
		return nil, ErrBatchProbeJobRunning
	}

	now := time.Now()
	job := &BatchProbeJob{
		ID:            fmt.Sprintf("%d", now.UnixNano()),
		Status:        BatchProbeQueued,
		StartedAt:     now,
		UpdatedAt:     now,
		Total:         len(snapshots),
		RequestedTags: append([]string(nil), tags...),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.job = job
	m.cancel = cancel

	go m.run(ctx, job, snapshots, probeFn)

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

func (m *BatchProbeJobManager) run(ctx context.Context, job *BatchProbeJob, snapshots []Snapshot, probeFn func(ctx context.Context, snap Snapshot) (int64, error)) {
	m.update(job.ID, func(current *BatchProbeJob) {
		current.Status = BatchProbeRunning
		current.UpdatedAt = time.Now()
	})

	workCh := make(chan Snapshot)
	var wg sync.WaitGroup

	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for snap := range workCh {
				m.update(job.ID, func(current *BatchProbeJob) {
					current.ActiveWorkers++
					current.UpdatedAt = time.Now()
				})

				latency, err := probeFn(ctx, snap)
				result := BatchProbeJobResult{
					Tag:       snap.Tag,
					Name:      snap.Name,
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
		for _, snap := range snapshots {
			select {
			case <-ctx.Done():
				close(workCh)
				return
			case workCh <- snap:
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
	if job.RequestedTags != nil {
		cloned.RequestedTags = append([]string(nil), job.RequestedTags...)
	}
	if job.LastResult != nil {
		resultCopy := *job.LastResult
		cloned.LastResult = &resultCopy
	}
	return &cloned
}
