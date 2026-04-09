package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBatchProbeJobManagerRejectsConcurrentStart(t *testing.T) {
	manager := NewBatchProbeJobManager(1)
	block := make(chan struct{})

	_, err := manager.Start([]BatchProbeTarget{{Tag: "a", Name: "node-a"}}, func(ctx context.Context, target BatchProbeTarget) (int64, error) {
		<-block
		return 10, nil
	})
	if err != nil {
		t.Fatalf("Start(first) error = %v", err)
	}

	_, err = manager.Start([]BatchProbeTarget{{Tag: "b", Name: "node-b"}}, func(ctx context.Context, target BatchProbeTarget) (int64, error) {
		return 0, nil
	})
	if !errors.Is(err, ErrBatchProbeJobRunning) {
		t.Fatalf("Start(second) error = %v, want ErrBatchProbeJobRunning", err)
	}

	close(block)
}

func TestBatchProbeJobManagerTracksProgressAndCompletion(t *testing.T) {
	manager := NewBatchProbeJobManager(2)
	job, err := manager.Start(
		[]BatchProbeTarget{
			{Tag: "a", Name: "node-a"},
			{Tag: "b", Name: "node-b"},
		},
		func(ctx context.Context, target BatchProbeTarget) (int64, error) {
			time.Sleep(20 * time.Millisecond)
			return 15, nil
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := manager.Status()
		if current != nil && current.ID == job.ID && current.Status == BatchProbeCompleted {
			if current.Completed != 2 {
				t.Fatalf("Completed = %d, want 2", current.Completed)
			}
			if current.Success != 2 {
				t.Fatalf("Success = %d, want 2", current.Success)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBatchProbeJobManagerTreatsHighLatencyAsFailure(t *testing.T) {
	manager := NewBatchProbeJobManager(1)
	job, err := manager.Start(
		[]BatchProbeTarget{{Tag: "slow", Name: "slow-node"}},
		func(ctx context.Context, target BatchProbeTarget) (int64, error) {
			return 10001, nil
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current := manager.Status()
		if current != nil && current.ID == job.ID && current.Status == BatchProbeCompleted {
			if current.Completed != 1 {
				t.Fatalf("Completed = %d, want 1", current.Completed)
			}
			if current.Success != 0 {
				t.Fatalf("Success = %d, want 0", current.Success)
			}
			if current.Failed != 1 {
				t.Fatalf("Failed = %d, want 1", current.Failed)
			}
			if current.LastResult == nil || current.LastResult.Error == "" {
				t.Fatalf("LastResult = %+v, want timeout error", current.LastResult)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBatchProbeJobManagerCancelCompletesHungProbe(t *testing.T) {
	manager := NewBatchProbeJobManager(1)
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})

	job, err := manager.Start(
		[]BatchProbeTarget{{Tag: "hung", Name: "hung-node"}},
		func(ctx context.Context, target BatchProbeTarget) (int64, error) {
			<-release
			return 0, nil
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	startDeadline := time.Now().Add(300 * time.Millisecond)
	for {
		current := manager.Status()
		if current != nil && current.ID == job.ID && current.ActiveWorkers == 1 {
			break
		}
		if time.Now().After(startDeadline) {
			t.Fatalf("worker did not start before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := manager.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		current := manager.Status()
		if current != nil && current.ID == job.ID && current.Status == BatchProbeCancelled {
			if current.ActiveWorkers != 0 {
				t.Fatalf("ActiveWorkers = %d, want 0", current.ActiveWorkers)
			}
			if current.Completed != 1 {
				t.Fatalf("Completed = %d, want 1", current.Completed)
			}
			if current.Failed != 1 {
				t.Fatalf("Failed = %d, want 1", current.Failed)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not cancel before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBatchProbeJobManagerTimesOutHungProbe(t *testing.T) {
	manager := NewBatchProbeJobManager(1)
	manager.probeTimeout = 20 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})

	job, err := manager.Start(
		[]BatchProbeTarget{{Tag: "timeout", Name: "timeout-node"}},
		func(ctx context.Context, target BatchProbeTarget) (int64, error) {
			<-release
			return 0, nil
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		current := manager.Status()
		if current != nil && current.ID == job.ID && current.Status == BatchProbeCompleted {
			if current.ActiveWorkers != 0 {
				t.Fatalf("ActiveWorkers = %d, want 0", current.ActiveWorkers)
			}
			if current.Completed != 1 {
				t.Fatalf("Completed = %d, want 1", current.Completed)
			}
			if current.Failed != 1 {
				t.Fatalf("Failed = %d, want 1", current.Failed)
			}
			if current.LastResult == nil || current.LastResult.Error == "" {
				t.Fatalf("LastResult = %+v, want timeout error", current.LastResult)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not time out before deadline: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
