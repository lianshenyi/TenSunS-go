package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSchedulerLifecycle(t *testing.T) {
	fixed := time.Date(2026, 10, 23, 14, 0, 0, 0, time.Local)
	var calls atomic.Int32
	exec := ExecutorFunc(func(context.Context, JobSpec) error {
		calls.Add(1)
		return nil
	})
	s := New(exec, testLogger())
	defer s.Close()
	s.SetClock(func() time.Time { return fixed })

	spec := JobSpec{ID: "alicloud/main/ecs/cn-hangzhou", Func: "__main__:alicloud.ecs", Args: []any{"main", "cn-hangzhou", false}, Minutes: 5}
	if err := s.Upsert(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	next, ok := s.NextRun(spec.ID)
	if !ok || !next.Equal(fixed.Add(5*time.Minute)) {
		t.Fatalf("next = %v, %v", next, ok)
	}
	if err := s.UpdateInterval(spec.ID, 2); err != nil {
		t.Fatal(err)
	}
	next, _ = s.NextRun(spec.ID)
	if !next.Equal(fixed.Add(2 * time.Minute)) {
		t.Fatalf("updated next = %v", next)
	}
	if err := s.RunNow(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
	s.Remove(spec.ID)
	if _, ok := s.NextRun(spec.ID); ok {
		t.Fatal("removed job still has next run")
	}
	if err := s.RunNow(context.Background(), "missing"); err != ErrJobNotFound {
		t.Fatalf("RunNow missing = %v", err)
	}
}

func TestRunNowSerializesSameJob(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	exec := ExecutorFunc(func(context.Context, JobSpec) error {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return nil
	})
	s := New(exec, testLogger())
	defer s.Close()
	spec := JobSpec{ID: "job", Func: "__main__:alicloud.group", Args: []any{"main"}, Minutes: 10}
	if err := s.Upsert(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RunNow(context.Background(), "job"); err != nil {
				t.Errorf("RunNow: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent executions = %d", maxActive.Load())
	}
}

func TestUpsertReplacesRunner(t *testing.T) {
	fixed := time.Date(2026, 10, 23, 14, 0, 0, 0, time.Local)
	s := New(ExecutorFunc(func(context.Context, JobSpec) error { return nil }), testLogger())
	defer s.Close()
	s.SetClock(func() time.Time { return fixed })
	spec := JobSpec{ID: "job", Func: "__main__:alicloud.group", Args: []any{"main"}, Minutes: 60}
	_ = s.Upsert(context.Background(), spec)
	spec.Minutes = 1
	_ = s.Upsert(context.Background(), spec)
	if ids := s.IDs(); len(ids) != 1 {
		t.Fatalf("expected one job, got %v", ids)
	}
	next, _ := s.NextRun("job")
	if !next.Equal(fixed.Add(time.Minute)) {
		t.Fatalf("replacement next = %v", next)
	}
}

func TestRunNowRecoversPanic(t *testing.T) {
	s := New(ExecutorFunc(func(context.Context, JobSpec) error { panic("boom") }), testLogger())
	defer s.Close()
	spec := JobSpec{ID: "panic", Func: "__main__:alicloud.group", Args: []any{"main"}, Minutes: 10}
	if err := s.Upsert(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow(context.Background(), spec.ID); err == nil {
		t.Fatal("expected panic to be converted to an error")
	}
}

func TestConsecutiveFailuresAreIsolatedPerJob(t *testing.T) {
	var failedCalls atomic.Int32
	var healthyCalls atomic.Int32
	s := New(ExecutorFunc(func(_ context.Context, spec JobSpec) error {
		if spec.ID == "failing" {
			failedCalls.Add(1)
			return errors.New("provider unavailable")
		}
		healthyCalls.Add(1)
		return nil
	}), testLogger())
	defer s.Close()
	for _, spec := range []JobSpec{{ID: "failing", Func: "__main__:awscloud.ecs", Args: []any{"demo", "us-east-1", false}, Minutes: 10}, {ID: "healthy", Func: "__main__:awscloud.group", Args: []any{"demo"}, Minutes: 10}} {
		if err := s.Upsert(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := s.RunNow(context.Background(), "failing"); err == nil {
			t.Fatal("failing job unexpectedly succeeded")
		}
	}
	if err := s.RunNow(context.Background(), "healthy"); err != nil {
		t.Fatal(err)
	}
	if failedCalls.Load() != 2 || healthyCalls.Load() != 1 {
		t.Fatalf("failed=%d healthy=%d", failedCalls.Load(), healthyCalls.Load())
	}
	if _, ok := s.NextRun("healthy"); !ok {
		t.Fatal("healthy job was removed after another job failed")
	}
}
