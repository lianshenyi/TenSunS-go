package awscloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		w.Header().Set("Content-Type", "text/xml")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.Form.Get("Action") {
		case "DescribeInstances":
			if r.Form.Get("NextToken") == "more" {
				_, _ = w.Write([]byte(`<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-2</instanceId><privateIpAddress>10.0.0.2</privateIpAddress><instanceState><name>stopped</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`))
				return
			}
			_, _ = w.Write([]byte(`<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-1</instanceId><privateIpAddress>10.0.0.1</privateIpAddress><instanceState><name>running</name></instanceState><tagSet><item><key>Name</key><value>web</value></item></tagSet></item></instancesSet></item></reservationSet><nextToken>more</nextToken></DescribeInstancesResponse>`))
		case "DescribeDBInstances":
			if r.Form.Get("Marker") == "more" {
				_, _ = w.Write([]byte(`<DescribeDBInstancesResponse><DescribeDBInstancesResult><DBInstances><DBInstance><DBInstanceIdentifier>db-2</DBInstanceIdentifier><DBInstanceStatus>available</DBInstanceStatus><Engine>mysql</Engine><DBClusterIdentifier>cluster-2</DBClusterIdentifier><Endpoint><Address>db-2.local</Address><Port>3306</Port></Endpoint></DBInstance></DBInstances></DescribeDBInstancesResult></DescribeDBInstancesResponse>`))
				return
			}
			_, _ = w.Write([]byte(`<DescribeDBInstancesResponse><DescribeDBInstancesResult><DBInstances><DBInstance><DBInstanceIdentifier>db-1</DBInstanceIdentifier><DBInstanceStatus>available</DBInstanceStatus><Engine>mysql</Engine><DBClusterIdentifier>cluster-1</DBClusterIdentifier><Endpoint><Address>db-1.local</Address><Port>3306</Port></Endpoint></DBInstance></DBInstances><Marker>more</Marker></DescribeDBInstancesResult></DescribeDBInstancesResponse>`))
		case "DescribeCacheClusters":
			if r.Form.Get("Marker") == "more" {
				_, _ = w.Write([]byte(`<DescribeCacheClustersResponse><DescribeCacheClustersResult><CacheClusters><CacheCluster><CacheClusterId>cache-2</CacheClusterId><Engine>redis</Engine><CacheClusterStatus>available</CacheClusterStatus><ReplicationGroupId>rep-2</ReplicationGroupId><CacheNodes><CacheNode><Endpoint><Address>cache-2.local</Address><Port>6379</Port></Endpoint></CacheNode></CacheNodes></CacheCluster></CacheClusters></DescribeCacheClustersResult></DescribeCacheClustersResponse>`))
				return
			}
			_, _ = w.Write([]byte(`<DescribeCacheClustersResponse><DescribeCacheClustersResult><CacheClusters><CacheCluster><CacheClusterId>cache-1</CacheClusterId><Engine>redis</Engine><CacheClusterStatus>available</CacheClusterStatus><ReplicationGroupId>rep-1</ReplicationGroupId><CacheNodes><CacheNode><Endpoint><Address>cache-1.local</Address><Port>6379</Port></Endpoint></CacheNode></CacheNodes></CacheCluster></CacheClusters><Marker>more</Marker></DescribeCacheClustersResult></DescribeCacheClustersResponse>`))
		default:
			t.Fatalf("unexpected AWS action %q", r.Form.Get("Action"))
		}
	}))
	defer fixture.Close()
	p := &Provider{Secrets: awsResolver(t), Endpoint: fixture.URL}
	if groups, err := p.Group(context.Background(), "main"); err != nil || groups["1"] != "1" {
		t.Fatalf("Group = %#v, %v", groups, err)
	}
	if values, err := p.ECS(context.Background(), "main", "us-east-1", false); err != nil || len(values) != 2 || values["i-1"].Name != "web" {
		t.Fatalf("ECS = %#v, %v", values, err)
	}
	if values, err := p.RDS(context.Background(), "main", "us-east-1"); err != nil || len(values) != 2 || values["db-2"].Port != 3306 {
		t.Fatalf("RDS = %#v, %v", values, err)
	}
	if values, err := p.Redis(context.Background(), "main", "us-east-1"); err != nil || len(values) != 2 {
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
	registry.Register("awscloud", provider)
	svc := cloudsync.New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.Group(context.Background(), "awscloud", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ECS(context.Background(), "awscloud", "main", "us-east-1", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RDS(context.Background(), "awscloud", "main", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Redis(context.Background(), "awscloud", "main", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ecs", "rds", "redis"} {
		if _, found := mock.GetKV("ConsulManager/record/jobs/awscloud/main/" + kind + "/us-east-1"); !found {
			t.Fatalf("%s record missing", kind)
		}
	}
	client := consul.New(server.URL+"/v1", "", server.Client())
	services, err := client.Services(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertService(t, services, "i-1", "awscloud_main_ecs", "10.0.0.1", 9100, "无")
	assertService(t, services, "db-1", "awscloud_main_rds", "db-1.local", 3306, "cluster-1")
	assertService(t, services, "cache-1", "awscloud_main_redis", "cache-1.local", 6379, "rep-1")
}

func assertService(t *testing.T, services map[string]store.Service, id, name, address string, port int, group string) {
	t.Helper()
	service, found := services[id]
	if !found || service.Name != name || service.Address != address || service.Port != port || service.Meta["group"] != group {
		t.Fatalf("service %s = %#v", id, service)
	}
}

func TestProviderReturnsEndpointErrors(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fixture failure", http.StatusInternalServerError)
	}))
	defer fixture.Close()
	p := &Provider{Secrets: awsResolver(t), Endpoint: fixture.URL}
	for name, call := range map[string]func() error{
		"ecs":   func() error { _, err := p.ECS(context.Background(), "main", "us-east-1", false); return err },
		"rds":   func() error { _, err := p.RDS(context.Background(), "main", "us-east-1"); return err },
		"redis": func() error { _, err := p.Redis(context.Background(), "main", "us-east-1"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if call() == nil {
				t.Fatal("expected endpoint error")
			}
		})
	}
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	registry := cloud.NewRegistry()
	registry.Register("awscloud", p)
	svc := cloudsync.New(consul.New(server.URL+"/v1", "", server.Client()), registry)
	if err := svc.ECS(context.Background(), "awscloud", "main", "us-east-1", false); err == nil {
		t.Fatal("expected endpoint error")
	}
	assertFailureRecord(t, mock, "ConsulManager/record/jobs/awscloud/main/ecs/us-east-1")
}

func assertFailureRecord(t *testing.T, mock *consulmock.Server, path string) {
	t.Helper()
	var record map[string]any
	raw, found := mock.GetKV(path)
	if !found || json.Unmarshal(raw, &record) != nil || record["status"] != float64(50000) {
		t.Fatalf("failure record = %s", raw)
	}
}

func awsResolver(t *testing.T) cloud.SecretResolver {
	t.Helper()
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ak, _ := cipher.Encrypt("ak")
	sk, _ := cipher.Encrypt("sk")
	mock := consulmock.New()
	if err := mock.SetKV("ConsulManager/assets/awscloud/aksk/main", map[string]string{"ak": ak, "sk": sk}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	return cloud.SecretResolver{Store: consul.New(server.URL+"/v1", "", server.Client()), Cipher: cipher}
}
