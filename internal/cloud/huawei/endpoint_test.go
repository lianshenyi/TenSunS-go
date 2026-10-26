package huawei

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	cloudsync "github.com/lianshenyi/TenSunS-go/internal/sync"
)

func TestProviderUsesHTTPEndpointFixture(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/enterprise-projects":
			if r.URL.Query().Get("offset") == "1" {
				_, _ = w.Write([]byte(`{"enterprise_projects":[{"id":"ep-2","name":"staging"}],"total_count":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"enterprise_projects":[{"id":"ep-1","name":"production"}],"total_count":2}`))
		case "/v2/orders/suscriptions/resources/query":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/{project_id}/cloudservers/detail":
			if r.URL.Query().Get("marker") == "srv-1" {
				_, _ = w.Write([]byte(`{"servers":[{"id":"srv-2","name":"worker","status":"ACTIVE","enterprise_project_id":"ep-2","metadata":{"os_type":"Linux"},"addresses":{"net":[{"addr":"10.0.0.4"}]},"flavor":{"vcpus":"2","ram":"4096"}}],"count":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"servers":[{"id":"srv-1","name":"web","status":"ACTIVE","enterprise_project_id":"ep-1","metadata":{"os_type":"Linux"},"addresses":{"net":[{"addr":"10.0.0.1"}]},"flavor":{"vcpus":"2","ram":"4096"}}],"count":2}`))
		case "/v3/{project_id}/instances":
			if r.URL.Query().Get("offset") == "1" {
				_, _ = w.Write([]byte(`{"instances":[{"id":"rds-2","name":"mysql2","status":"ACTIVE","port":3306,"private_ips":["10.0.0.5"],"enterprise_project_id":"ep-2","type":"Single","datastore":{"type":"MySQL","version":"8.0"}}],"total_count":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"instances":[{"id":"rds-1","name":"mysql","status":"ACTIVE","port":3306,"private_ips":["10.0.0.2"],"enterprise_project_id":"ep-1","type":"Single","datastore":{"type":"MySQL","version":"8.0"}}],"total_count":2}`))
		case "/v2/{project_id}/instances":
			if r.URL.Query().Get("offset") == "1" {
				_, _ = w.Write([]byte(`{"instances":[{"instance_id":"redis-2","name":"redis2","status":"RUNNING","port":6379,"ip":"10.0.0.6","domain_name":"redis2.local","enterprise_project_id":"ep-2","spec_code":"dcs.single_node","engine_version":"6.2","max_memory":1024}],"instance_num":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"instances":[{"instance_id":"redis-1","name":"redis","status":"RUNNING","port":6379,"ip":"10.0.0.3","domain_name":"redis.local","enterprise_project_id":"ep-1","spec_code":"dcs.single_node","engine_version":"6.2","max_memory":1024}],"instance_num":2}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer fixture.Close()
	p := &Provider{Secrets: huaweiResolver(t), Endpoint: fixture.URL}
	if values, err := p.Group(context.Background(), "main"); err != nil || values["ep-1"] != "production" {
		t.Fatalf("Group = %#v, %v", values, err)
	}
	if values, err := p.ECS(context.Background(), "main", "cn-north-4", false); err != nil || len(values) != 2 || values["srv-1"].CPU != "2核" {
		t.Fatalf("ECS = %#v, %v", values, err)
	}
	if values, err := p.RDS(context.Background(), "main", "cn-north-4"); err != nil || len(values) != 2 || values["rds-1"].Group != "ep-1" {
		t.Fatalf("RDS = %#v, %v", values, err)
	}
	if values, err := p.Redis(context.Background(), "main", "cn-north-4"); err != nil || len(values) != 2 || values["redis-1"].Port != 6379 {
		t.Fatalf("Redis = %#v, %v", values, err)
	}
	assertProviderWritesConsul(t, p)
}

func assertProviderWritesConsul(t *testing.T, provider *Provider) {
	t.Helper()
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("huaweicloud", provider)
	svc := cloudsync.New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.Group(context.Background(), "huaweicloud", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ECS(context.Background(), "huaweicloud", "main", "cn-north-4", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RDS(context.Background(), "huaweicloud", "main", "cn-north-4"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Redis(context.Background(), "huaweicloud", "main", "cn-north-4"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ecs", "rds", "redis"} {
		if _, found := mock.GetKV("ConsulManager/record/jobs/huaweicloud/main/" + kind + "/cn-north-4"); !found {
			t.Fatalf("%s record missing", kind)
		}
	}
}

func TestProviderReturnsEndpointErrors(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fixture failure", http.StatusInternalServerError)
	}))
	defer fixture.Close()
	p := &Provider{Secrets: huaweiResolver(t), Endpoint: fixture.URL}
	for name, call := range map[string]func() error{
		"group": func() error { _, err := p.Group(context.Background(), "main"); return err },
		"ecs":   func() error { _, err := p.ECS(context.Background(), "main", "cn-north-4", false); return err },
		"rds":   func() error { _, err := p.RDS(context.Background(), "main", "cn-north-4"); return err },
		"redis": func() error { _, err := p.Redis(context.Background(), "main", "cn-north-4"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if call() == nil {
				t.Fatal("expected endpoint error")
			}
		})
	}
}

func huaweiResolver(t *testing.T) cloud.SecretResolver {
	t.Helper()
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ak, _ := cipher.Encrypt("ak")
	sk, _ := cipher.Encrypt("sk")
	mock := consulmock.New()
	if err := mock.SetKV("ConsulManager/assets/huaweicloud/aksk/main", map[string]string{"ak": ak, "sk": sk}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	return cloud.SecretResolver{Store: consul.New(server.URL+"/v1", "", server.Client()), Cipher: cipher}
}
