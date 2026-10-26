package sync

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

type fakeProvider struct{ ecs map[string]model.ECS }

func (p fakeProvider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{"1": "project"}, nil
}
func (p fakeProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return p.ecs, nil
}
func (fakeProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return map[string]model.RDS{}, nil
}
func (fakeProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return map[string]model.Redis{}, nil
}

func TestECSWritesServicesCleansStaleAndRecords(t *testing.T) {
	mock := consulmock.New()
	mock.SetService("old", "tencent_cloud_main_ecs", "10.0.0.9", 9100, []string{"ON", "linux", "ap-guangzhou"}, map[string]string{"account": "main"})
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("tencent_cloud", fakeProvider{ecs: map[string]model.ECS{"ins-1": {Name: "web", Group: "project", OSType: "linux", Status: "RUNNING", IP: "10.0.0.1", CPU: "2核", Mem: "4GB", Exp: "按量", Tags: map[string]string{"env": "prod", "bad-key": "ignored"}}}})
	svc := New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	svc.Now = func() time.Time { return time.Date(2026, 10, 26, 8, 9, 0, 0, time.Local) }
	if err := svc.ECS(context.Background(), "tencent_cloud", "main", "ap-guangzhou", false); err != nil {
		t.Fatal(err)
	}
	if _, found := mock.GetKV("ConsulManager/record/jobs/tencent_cloud/main/ecs/ap-guangzhou"); !found {
		t.Fatal("execution record was not written")
	}
	if _, found := mock.GetKV("ConsulManager/assets/sync_ecs_custom/ins-1"); found {
		t.Fatal("unexpected custom override")
	}
	if _, found := mock.GetKV("old"); found {
		t.Fatal("service is not KV")
	}
	var record map[string]any
	raw, _ := mock.GetKV("ConsulManager/record/jobs/tencent_cloud/main/ecs/ap-guangzhou")
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["status"] != float64(20000) {
		t.Fatalf("record=%v", record)
	}
}
