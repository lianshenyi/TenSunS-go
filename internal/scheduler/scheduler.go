// Package scheduler implements a dynamic interval scheduler with the subset
// of APScheduler semantics TenSunS relies on: replace_existing, one instance
// per job ID, mutable intervals, synchronous RunNow, and next-run reporting.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// ErrJobNotFound is returned when a job ID is absent from the in-memory
// scheduler. Callers should degrade nextime to "无" rather than fail.
var ErrJobNotFound = errors.New("job not found")

// Executor performs one typed job. Implementations must be safe to call from
// multiple goroutines but the scheduler guarantees per-ID serialization.
type Executor interface {
	Execute(ctx context.Context, spec JobSpec) error
}

// ExecutorFunc adapts a function to Executor.
type ExecutorFunc func(ctx context.Context, spec JobSpec) error

func (f ExecutorFunc) Execute(ctx context.Context, spec JobSpec) error { return f(ctx, spec) }

type jobState struct {
	spec    JobSpec
	ctx     context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
	nextRun time.Time
	runMu   sync.Mutex
}

// Scheduler owns all in-memory job runners.
type Scheduler struct {
	mu      sync.Mutex
	jobs    map[string]*jobState
	exec    Executor
	logger  *slog.Logger
	now     func() time.Time
	timeout time.Duration
	closed  bool
	wg      sync.WaitGroup
}

// New creates a scheduler. The default execution timeout is ten minutes,
// matching the migration baseline's recommendation for cloud/JMS jobs.
func New(exec Executor, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		jobs:    make(map[string]*jobState),
		exec:    exec,
		logger:  logger,
		now:     time.Now,
		timeout: 10 * time.Minute,
	}
}

// SetClock is intended for tests.
func (s *Scheduler) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

// SetExecutionTimeout changes the per-execution timeout. Zero disables it.
func (s *Scheduler) SetExecutionTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timeout = d
}

// Upsert replaces an existing runner and starts a new interval timer.
func (s *Scheduler) Upsert(_ context.Context, spec JobSpec) error {
	if spec.ID == "" {
		return errors.New("job id is empty")
	}
	if spec.Minutes <= 0 {
		return errors.New("job minutes must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("scheduler is closed")
	}
	if old := s.jobs[spec.ID]; old != nil {
		s.stopLocked(old)
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &jobState{spec: spec, ctx: ctx, cancel: cancel}
	s.jobs[spec.ID] = state
	s.scheduleLocked(state)
	return nil
}

// Remove stops a runner. It does not delete the Consul KV.
func (s *Scheduler) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.jobs[id]; state != nil {
		s.stopLocked(state)
		delete(s.jobs, id)
	}
}

// UpdateInterval keeps the job identity and changes only the interval.
func (s *Scheduler) UpdateInterval(id string, minutes int) error {
	if minutes <= 0 {
		return errors.New("job minutes must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("scheduler is closed")
	}
	state := s.jobs[id]
	if state == nil {
		return ErrJobNotFound
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	state.spec.Minutes = minutes
	s.scheduleLocked(state)
	return nil
}

// RunNow executes synchronously and waits for the current execution. A job
// that is already running is serialized behind its per-ID lock.
func (s *Scheduler) RunNow(ctx context.Context, id string) error {
	s.mu.Lock()
	state := s.jobs[id]
	if state == nil {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	timeout := s.timeout
	s.mu.Unlock()

	state.runMu.Lock()
	defer state.runMu.Unlock()
	if state.ctx.Err() != nil {
		return state.ctx.Err()
	}
	runCtx := state.ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(state.ctx, timeout)
	}
	defer cancel()
	return s.execute(runCtx, state.spec)
}

// NextRun reports the next scheduled fire time in local time.
func (s *Scheduler) NextRun(id string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.jobs[id]
	if state == nil || state.nextRun.IsZero() {
		return time.Time{}, false
	}
	return state.nextRun, true
}

// IDs returns a snapshot of scheduled job IDs.
func (s *Scheduler) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.jobs))
	for id := range s.jobs {
		out = append(out, id)
	}
	return out
}

// Close stops all timers, cancels in-flight contexts, and waits for running
// jobs to return.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, state := range s.jobs {
		s.stopLocked(state)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Scheduler) scheduleLocked(state *jobState) {
	delay := time.Duration(state.spec.Minutes) * time.Minute
	state.nextRun = s.now().Add(delay)
	s.logger.Info("job scheduled", "id", state.spec.ID, "minutes", state.spec.Minutes, "next", state.nextRun.Format("01.02/15:04"))
	state.timer = time.AfterFunc(delay, func() { s.fire(state) })
}

func (s *Scheduler) fire(state *jobState) {
	s.mu.Lock()
	current := !s.closed && s.jobs[state.spec.ID] == state
	if current {
		s.wg.Add(1)
	}
	timeout := s.timeout
	s.mu.Unlock()
	if !current {
		return
	}
	defer s.wg.Done()

	state.runMu.Lock()
	runCtx := state.ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(state.ctx, timeout)
	}
	err := s.execute(runCtx, state.spec)
	cancel()
	state.runMu.Unlock()
	if err != nil {
		s.logger.Error("job execution failed", "id", state.spec.ID, "error", err)
	} else {
		s.logger.Info("job execution succeeded", "id", state.spec.ID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.jobs[state.spec.ID] != state {
		return
	}
	now := s.now()
	// Schedule from the completion point. APScheduler's interval trigger
	// schedules from wall clock; this difference only matters for long jobs
	// and avoids piling up runs, which matches max_instances=1.
	nextDelay := time.Duration(state.spec.Minutes) * time.Minute
	state.nextRun = now.Add(nextDelay)
	state.timer = time.AfterFunc(nextDelay, func() { s.fire(state) })
}

func (s *Scheduler) execute(ctx context.Context, spec JobSpec) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job %s panicked: %v\n%s", spec.ID, recovered, debug.Stack())
		}
	}()
	return s.exec.Execute(ctx, spec)
}

func (s *Scheduler) stopLocked(state *jobState) {
	if state.timer != nil {
		state.timer.Stop()
	}
	if state.cancel != nil {
		state.cancel()
	}
}
