package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/tencentcloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	consulstore "github.com/lianshenyi/TenSunS-go/internal/store/consul"
	syncservice "github.com/lianshenyi/TenSunS-go/internal/sync"
)

// TestSchedulerRuntimeFixtureEndToEnd proves legacy job KVs make it through
// the scheduler and Runtime before the provider writes each compatible record.
func TestSchedulerRuntimeFixtureEndToEnd(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consulstore.New(server.URL+"/v1", "", nil)
	registry := cloud.NewRegistry()
	registry.Register("tencent_cloud", tencentcloud.NewFixtureProvider(st))
	runtime := &Runtime{Cloud: &syncservice.ConsulService{Store: st, Providers: registry}}
	sched := scheduler.New(runtime, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	t.Cleanup(sched.Close)

	if err := mock.SetKV("ConsulManager/fixtures/tencent_cloud/main", integrationFixture()); err != nil {
		t.Fatal(err)
	}
	jobs := []scheduler.JobSpec{
		{ID: "tencent_cloud/main/group", Func: "__main__:tencent_cloud.group", Args: []any{"main"}, Minutes: 1},
		{ID: "tencent_cloud/main/ecs/ap-shanghai", Func: "__main__:tencent_cloud.ecs", Args: []any{"main", "ap-shanghai", false}, Minutes: 1},
		{ID: "tencent_cloud/main/rds/ap-shanghai", Func: "__main__:tencent_cloud.rds", Args: []any{"main", "ap-shanghai"}, Minutes: 1},
		{ID: "tencent_cloud/main/redis/ap-shanghai", Func: "__main__:tencent_cloud.redis", Args: []any{"main", "ap-shanghai"}, Minutes: 1},
	}
	for _, job := range jobs {
		raw, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		if err := mock.SetKV("ConsulManager/jobs/"+job.ID, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	loaded, skipped, err := LoadFromConsul(context.Background(), st, sched, nil)
	if err != nil || loaded != 4 || skipped != 0 {
		t.Fatalf("loaded=%d skipped=%d err=%v", loaded, skipped, err)
	}
	for _, job := range jobs {
		if err := sched.RunNow(context.Background(), job.ID); err != nil {
			t.Fatalf("run %s: %v", job.ID, err)
		}
	}
	for _, kind := range []string{"group", "ecs", "rds", "redis"} {
		path := "ConsulManager/record/jobs/tencent_cloud/main/" + kind
		if kind != "group" {
			path += "/ap-shanghai"
		}
		raw, ok := mock.GetKV(path)
		if !ok {
			t.Fatalf("missing %s record", kind)
		}
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil || record["status"] != float64(20000) || record["count"] != float64(1) {
			t.Fatalf("%s record=%s err=%v", kind, raw, err)
		}
	}
}

func integrationFixture() map[string]any {
	return map[string]any{
		"groups": map[string]string{"rg-1": "生产"},
		"ecs":    map[string]any{"i-1": map[string]any{"name": "web", "group": "生产", "ostype": "linux", "status": "Running", "ip": "10.0.0.1"}},
		"rds":    map[string]any{"rm-1": map[string]any{"name": "mysql", "domain": "db.local", "ip": "db.local", "port": 3306, "group": "生产", "status": "Running", "itype": "主实例", "ver": "8.0"}},
		"redis":  map[string]any{"r-1": map[string]any{"name": "cache", "domain": "cache.local", "ip": "10.0.0.3", "port": 6379, "group": "生产", "status": "Running", "itype": "主从", "ver": "6.0"}},
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
