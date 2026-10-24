package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/tencentcloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/jumpserver"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store"
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

func TestSchedulerRuntimeJMSSyncWritesSuccessAndFailureRecords(t *testing.T) {
	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	t.Cleanup(consulServer.Close)
	st := consulstore.New(consulServer.URL+"/v1", "", nil)
	backend := &runtimeJMSFixture{}
	jmsServer := httptest.NewServer(backend)
	t.Cleanup(jmsServer.Close)
	cipher, err := cryptox.NewCipher("12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"ConsulManager/jms/jms_info":               map[string]any{"url": jmsServer.URL, "token": token, "ver": "V3"},
		"ConsulManager/jms/ecs_info":               map[string]any{"linux": []any{[]string{"ssh/22"}, "root"}},
		"ConsulManager/jms/alicloud/main/node_id":  map[string]any{"node_id": "parent"},
		"ConsulManager/assets/alicloud/group/main": map[string]string{"g1": "生产"},
		"ConsulManager/jms/custom_ecs_info":        map[string]any{},
	} {
		if err := st.PutKV(context.Background(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Register(context.Background(), store.ServiceRegistration{ID: "i-1", Name: "alicloud_main_ecs", Address: "10.0.0.1", Port: 9100, Meta: map[string]string{"name": "web-1", "group": "生产", "os": "linux", "region": "华东1(杭州)"}}); err != nil {
		t.Fatal(err)
	}
	client := &jumpserver.Client{Store: st, Cipher: cipher, HTTP: jmsServer.Client()}
	sched := scheduler.New(&Runtime{JMS: client}, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	t.Cleanup(sched.Close)
	spec := scheduler.JobSpec{ID: "alicloud/main/jms", Func: "__main__:sync_jms.run", Args: []any{"alicloud", "main"}, Minutes: 1}
	if err := sched.Upsert(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunNow(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	assertRuntimeJMSRecord(t, mock, 20000)
	backend.failChildren = http.StatusForbidden
	if err := sched.RunNow(context.Background(), spec.ID); err == nil {
		t.Fatal("forbidden sync unexpectedly succeeded")
	}
	if backend.childrenCalls != 5 { // success GET/POST/GET/tree, then one forbidden GET.
		t.Fatalf("children calls = %d", backend.childrenCalls)
	}
	assertRuntimeJMSRecord(t, mock, 50000)
}

func assertRuntimeJMSRecord(t *testing.T, mock *consulmock.Server, want int) {
	t.Helper()
	raw, ok := mock.GetKV("ConsulManager/record/jms/alicloud/main")
	if !ok {
		t.Fatal("JMS record missing")
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil || record["status"] != float64(want) {
		t.Fatalf("JMS record = %s err=%v", raw, err)
	}
}

type runtimeJMSFixture struct {
	childrenCalls int
	failChildren  int
}

func (h *runtimeJMSFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Token fixture-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.Contains(r.URL.Path, "/children/") {
		h.childrenCalls++
		if h.failChildren != 0 {
			w.WriteHeader(h.failChildren)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "ungrouped", "value": "未分组"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/assets/assets/") {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/nodes/children/tree/") {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/assets/hosts/" {
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNotFound)
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
