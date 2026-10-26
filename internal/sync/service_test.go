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

type fakeProvider struct {
	ecs   map[string]model.ECS
	rds   map[string]model.RDS
	redis map[string]model.Redis
}

func (p fakeProvider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{"1": "project"}, nil
}
func (p fakeProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return p.ecs, nil
}
func (p fakeProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return p.rds, nil
}
func (p fakeProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return p.redis, nil
}

func TestECSWritesServicesCleansStaleAndRecords(t *testing.T) {
	mock := consulmock.New()
	if err := mock.SetKV("ConsulManager/assets/tencent_cloud/group/main", map[string]string{"1": "project"}); err != nil {
		t.Fatal(err)
	}
	mock.SetService("old", "tencent_cloud_main_ecs", "10.0.0.9", 9100, []string{"ON", "linux", "ap-guangzhou"}, map[string]string{"account": "main"})
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("tencent_cloud", fakeProvider{ecs: map[string]model.ECS{"ins-1": {Name: "web", Group: "1", OSType: "linux", Status: "RUNNING", IP: "10.0.0.1", CPU: "2核", Mem: "4GB", Exp: "按量", Tags: map[string]string{"env": "prod", "bad-key": "ignored"}}}})
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

func TestECSFailsAndRecordsWhenGroupMappingIsMissing(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("huaweicloud", fakeProvider{ecs: map[string]model.ECS{"srv-1": {Group: "project-1"}}})
	svc := New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.ECS(context.Background(), "huaweicloud", "main", "cn-north-4", false); err == nil {
		t.Fatal("ECS unexpectedly succeeded without group mapping")
	}
	var record map[string]any
	raw, found := mock.GetKV("ConsulManager/record/jobs/huaweicloud/main/ecs/cn-north-4")
	if !found || json.Unmarshal(raw, &record) != nil || record["status"] != float64(50000) {
		t.Fatalf("failure record = %s", raw)
	}
}

func TestAllProviderResourceKindsWriteConsul(t *testing.T) {
	for _, vendor := range []string{"awscloud", "tencent_cloud", "huaweicloud"} {
		t.Run(vendor, func(t *testing.T) {
			mock := consulmock.New()
			if vendor != "awscloud" {
				if err := mock.SetKV("ConsulManager/assets/"+vendor+"/group/main", map[string]string{"1": "production"}); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(mock.Handler())
			defer server.Close()
			registry := cloud.NewRegistry()
			registry.Register(vendor, fakeProvider{
				ecs:   map[string]model.ECS{"ecs-1": {Name: "web", Group: groupFor(vendor), OSType: "linux", Status: "RUNNING", IP: "10.0.0.1", CPU: "1核", Mem: "1GB", Exp: "按量"}},
				rds:   map[string]model.RDS{"rds-1": {Name: "db", Group: groupFor(vendor), Status: "运行中", IP: "10.0.0.2", Port: 3306, IType: "mysql", Ver: "8", Exp: "按量"}},
				redis: map[string]model.Redis{"redis-1": {Name: "cache", Group: groupFor(vendor), Status: "运行中", Domain: "10.0.0.3", IP: "10.0.0.3", Port: 6379, IType: "redis", Ver: "7", Exp: "按量"}},
			})
			svc := New(consul.New(server.URL+"/v1", "", server.Client()), registry)
			if err := svc.Group(context.Background(), vendor, "main"); err != nil {
				t.Fatal(err)
			}
			// Non-AWS fixtures deliberately exercise the persisted group lookup.
			if vendor != "awscloud" {
				if err := mock.SetKV("ConsulManager/assets/"+vendor+"/group/main", map[string]string{"1": "production"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.ECS(context.Background(), vendor, "main", "test-1", false); err != nil {
				t.Fatal(err)
			}
			if err := svc.RDS(context.Background(), vendor, "main", "test-1"); err != nil {
				t.Fatal(err)
			}
			if err := svc.Redis(context.Background(), vendor, "main", "test-1"); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"ecs", "rds", "redis"} {
				if _, found := mock.GetKV("ConsulManager/record/jobs/" + vendor + "/main/" + kind + "/test-1"); !found {
					t.Fatalf("%s record missing", kind)
				}
			}
		})
	}
}

func groupFor(vendor string) string {
	if vendor == "awscloud" {
		return "无"
	}
	return "1"
}
