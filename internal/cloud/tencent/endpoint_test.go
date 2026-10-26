package tencent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	cloudsync "github.com/lianshenyi/TenSunS-go/internal/sync"
)

func TestProviderUsesHTTPEndpointFixture(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-TC-Action") {
		case "DescribeProjects":
			_, _ = w.Write([]byte(`{"Response":{"Projects":[{"ProjectId":1,"Name":"production"}],"RequestId":"x"}}`))
		case "DescribeInstances":
			if r.Header.Get("X-TC-Version") == "2018-04-12" {
				_, _ = w.Write([]byte(`{"Response":{"InstanceSet":[{"InstanceId":"redis-1","InstanceName":"cache","ProjectId":1,"WanIp":"10.0.0.3","Port":6379,"Status":2,"Type":5,"Engine":"Redis_7.0","Size":1024,"DeadlineTime":"0000-00-00 00:00:00"}],"RequestId":"x"}}`))
				return
			}
			var request struct {
				Offset int64
				Limit  int64
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Limit != 100 {
				t.Fatalf("CVM Limit = %d, want 100", request.Limit)
			}
			if request.Offset == 0 {
				instances := make([]map[string]any, 100)
				for i := range instances {
					instances[i] = map[string]any{"InstanceId": "ins-page-" + strconv.Itoa(i), "InstanceName": "web", "Placement": map[string]any{"ProjectId": 1}, "PrivateIpAddresses": []string{"10.0.0.1"}, "CPU": 2, "Memory": 4, "InstanceState": "RUNNING", "OsName": "CentOS"}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Response": map[string]any{"InstanceSet": instances, "RequestId": "x"}})
				return
			}
			if request.Offset != 100 {
				t.Fatalf("CVM Offset = %d, want 100", request.Offset)
			}
			_, _ = w.Write([]byte(`{"Response":{"InstanceSet":[{"InstanceId":"ins-101","InstanceName":"worker","Placement":{"ProjectId":1},"PrivateIpAddresses":["10.0.0.11"],"CPU":4,"Memory":8,"InstanceState":"RUNNING","OsName":"CentOS"}],"RequestId":"x"}}`))
		case "DescribeDBInstances":
			_, _ = w.Write([]byte(`{"Response":{"Items":[{"InstanceId":"cdb-1","InstanceName":"mysql","ProjectId":1,"Vip":"10.0.0.2","Vport":3306,"Status":1,"InstanceType":1,"DeadlineTime":"0000-00-00 00:00:00"}],"RequestId":"x"}}`))
		default:
			t.Fatalf("unexpected action %q", r.Header.Get("X-TC-Action"))
		}
	}))
	defer fixture.Close()
	p := &Provider{Secrets: testResolver(t), Endpoint: fixture.URL}
	if groups, err := p.Group(context.Background(), "main"); err != nil || groups["1"] != "production" {
		t.Fatalf("Group = %#v, %v", groups, err)
	}
	if ecs, err := p.ECS(context.Background(), "main", "ap-guangzhou", false); err != nil || len(ecs) != 101 || ecs["ins-101"].IP != "10.0.0.11" || ecs["ins-101"].Group != "1" {
		t.Fatalf("ECS = %#v, %v", ecs, err)
	}
	if rds, err := p.RDS(context.Background(), "main", "ap-guangzhou"); err != nil || rds["cdb-1"].Status != "运行中" || rds["cdb-1"].IType != "主实例" || rds["cdb-1"].Exp != "按量" {
		t.Fatalf("RDS = %#v, %v", rds, err)
	}
	if redis, err := p.Redis(context.Background(), "main", "ap-guangzhou"); err != nil || redis["redis-1"].Status != "运行中" || redis["redis-1"].IType != "单机" || redis["redis-1"].Exp != "按量" || redis["redis-1"].Domain != "10.0.0.3" {
		t.Fatalf("Redis = %#v, %v", redis, err)
	}
	assertProviderWritesConsul(t, p)
}

func assertProviderWritesConsul(t *testing.T, provider *Provider) {
	t.Helper()
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("tencent_cloud", provider)
	svc := cloudsync.New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.Group(context.Background(), "tencent_cloud", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ECS(context.Background(), "tencent_cloud", "main", "ap-guangzhou", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RDS(context.Background(), "tencent_cloud", "main", "ap-guangzhou"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Redis(context.Background(), "tencent_cloud", "main", "ap-guangzhou"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ecs", "rds", "redis"} {
		if _, found := mock.GetKV("ConsulManager/record/jobs/tencent_cloud/main/" + kind + "/ap-guangzhou"); !found {
			t.Fatalf("%s record missing", kind)
		}
	}
	client := consul.New(server.URL+"/v1", "", server.Client())
	services, err := client.Services(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertService(t, services, "ins-page-0", "tencent_cloud_main_ecs", "10.0.0.1", 9100, "production")
	assertService(t, services, "cdb-1", "tencent_cloud_main_rds", "10.0.0.2", 3306, "production")
	assertService(t, services, "redis-1", "tencent_cloud_main_redis", "10.0.0.3", 6379, "production")

	missingGroups := consulmock.New()
	missingServer := httptest.NewServer(missingGroups.Handler())
	defer missingServer.Close()
	missingRegistry := cloud.NewRegistry()
	missingRegistry.Register("tencent_cloud", provider)
	missingSync := cloudsync.New(consul.New(missingServer.URL+"/v1", "", missingServer.Client()), missingRegistry)
	if err := missingSync.ECS(context.Background(), "tencent_cloud", "main", "ap-guangzhou", false); err != nil {
		t.Fatalf("Tencent ECS without group mapping: %v", err)
	}
	missingServices, err := consul.New(missingServer.URL+"/v1", "", missingServer.Client()).Services(context.Background(), `ID == "ins-page-0"`)
	if err != nil {
		t.Fatal(err)
	}
	assertService(t, missingServices, "ins-page-0", "tencent_cloud_main_ecs", "10.0.0.1", 9100, "无")
}

func assertService(t *testing.T, services map[string]store.Service, id, name, address string, port int, group string) {
	t.Helper()
	service, found := services[id]
	if !found || service.Name != name || service.Address != address || service.Port != port || service.Meta["group"] != group {
		t.Fatalf("service %s = %#v", id, service)
	}
}

func TestProviderEndpointErrorWritesFailureRecord(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fixture failure", http.StatusInternalServerError)
	}))
	defer fixture.Close()
	p := &Provider{Secrets: testResolver(t), Endpoint: fixture.URL}
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("tencent_cloud", p)
	svc := cloudsync.New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.ECS(context.Background(), "tencent_cloud", "main", "ap-guangzhou", false); err == nil {
		t.Fatal("expected endpoint error")
	}
	var record map[string]any
	raw, found := mock.GetKV("ConsulManager/record/jobs/tencent_cloud/main/ecs/ap-guangzhou")
	if !found || json.Unmarshal(raw, &record) != nil || record["status"] != float64(50000) {
		t.Fatalf("failure record = %s", raw)
	}
}

func testResolver(t *testing.T) cloud.SecretResolver {
	t.Helper()
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ak, _ := cipher.Encrypt("ak")
	sk, _ := cipher.Encrypt("sk")
	mock := consulmock.New()
	if err := mock.SetKV("ConsulManager/assets/tencent_cloud/aksk/main", map[string]string{"ak": ak, "sk": sk}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	return cloud.SecretResolver{Store: consul.New(server.URL+"/v1", "", server.Client()), Cipher: cipher}
}
