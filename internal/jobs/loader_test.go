package jobs

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestLoadFromConsulSkipsUnknownFunc(t *testing.T) {
	mock := consulmock.New()
	if err := mock.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	if err := mock.SetKV("ConsulManager/jobs/legacy/main/exp/cn-hangzhou", map[string]any{
		"id": "legacy/main/exp/cn-hangzhou", "func": "__main__:legacy.exp",
		"args": []any{"main", "cn-hangzhou"}, "minutes": 10,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mock.Handler())
	defer ts.Close()
	st := consul.New(ts.URL+"/v1", "", ts.Client())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(scheduler.ExecutorFunc(func(context.Context, scheduler.JobSpec) error { return nil }), logger)
	defer sched.Close()
	sched.SetClock(func() time.Time { return time.Date(2026, 10, 23, 14, 0, 0, 0, time.Local) })
	loaded, skipped, err := LoadFromConsul(context.Background(), st, sched, logger)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != 5 || skipped != 1 {
		t.Fatalf("loaded=%d skipped=%d", loaded, skipped)
	}
	if _, ok := sched.NextRun("alicloud/main/jms"); !ok {
		t.Fatal("JMS task was not loaded")
	}
	if _, ok := sched.NextRun("legacy/main/exp/cn-hangzhou"); ok {
		t.Fatal("unknown task should not be scheduled")
	}
}
