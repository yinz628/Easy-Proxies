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

	_, err := manager.Start([]string{"a"}, []Snapshot{{NodeInfo: NodeInfo{Tag: "a", Name: "node-a"}}}, func(ctx context.Context, snap Snapshot) (int64, error) {
		<-block
		return 10, nil
	})
	if err != nil {
		t.Fatalf("Start(first) error = %v", err)
	}

	_, err = manager.Start([]string{"b"}, []Snapshot{{NodeInfo: NodeInfo{Tag: "b", Name: "node-b"}}}, func(ctx context.Context, snap Snapshot) (int64, error) {
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
		[]string{"a", "b"},
		[]Snapshot{
			{NodeInfo: NodeInfo{Tag: "a", Name: "node-a"}},
			{NodeInfo: NodeInfo{Tag: "b", Name: "node-b"}},
		},
		func(ctx context.Context, snap Snapshot) (int64, error) {
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
