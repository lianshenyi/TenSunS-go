package sync

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

type fakeProvider struct {
	groups map[string]string
	ecs    map[string]model.ECS
	err    error
}

func (p fakeProvider) Group(context.Context, string) (map[string]string, error) {
	return p.groups, p.err
}
func (p fakeProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return p.ecs, p.err
}
func (fakeProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, errors.New("not used")
}
func (fakeProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, errors.New("not used")
}

func TestCloudGroupPersistsGroupsAndRecord(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	registry := cloud.NewRegistry()
	registry.Register("fake", fakeProvider{groups: map[string]string{"g-1": "platform"}})

	if err := NewCloud(registry, st).Group(context.Background(), "fake", "demo"); err != nil {
		t.Fatal(err)
	}
	var groups map[string]string
	if found, err := st.GetValue(context.Background(), "ConsulManager/assets/fake/group/demo", &groups); err != nil || !found || groups["g-1"] != "platform" {
		t.Fatalf("groups found=%v err=%v value=%v", found, err, groups)
	}
	var record map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/fake/demo/group", &record); err != nil || !found || record["status"] != float64(20000) || record["count"] != float64(1) {
		t.Fatalf("record found=%v err=%v value=%v", found, err, record)
	}
}

func TestCloudGroupFailureWritesFailureRecord(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	registry := cloud.NewRegistry()
	registry.Register("fake", fakeProvider{err: errors.New("provider unavailable")})

	if err := NewCloud(registry, st).Group(context.Background(), "fake", "demo"); err == nil {
		t.Fatal("Group() error = nil")
	}
	if _, found := mock.GetKV("ConsulManager/assets/fake/group/demo"); found {
		t.Fatal("groups persisted after provider failure")
	}
	var record map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/fake/demo/group", &record); err != nil || !found || record["msg"] != "资源组同步失败" {
		t.Fatalf("record found=%v err=%v value=%v", found, err, record)
	}
}

func TestCloudECSSyncReplacesStaleServiceAndWritesRecord(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	if err := st.PutKV(context.Background(), "ConsulManager/assets/fake/group/demo", map[string]string{"g-1": "platform"}); err != nil {
		t.Fatal(err)
	}
	mock.SetService("gone", "fake_demo_ecs", "10.0.0.9", 9100, []string{"ON", "linux", "r1"}, map[string]string{"account": "demo"})
	registry := cloud.NewRegistry()
	registry.Register("fake", fakeProvider{ecs: map[string]model.ECS{
		"i-1": {Name: "web", Group: "g-1", OSType: "linux", Status: "Running", Region: "r1", IP: "10.0.0.1", CPU: "2核", Mem: "4GB", Exp: "按量", Tags: map[string]string{"team": "ops", "bad-key": "no"}},
	}})

	if err := NewCloud(registry, st).ECS(context.Background(), "fake", "demo", "r1", false); err != nil {
		t.Fatal(err)
	}
	services, err := st.Services(context.Background(), `Service == "fake_demo_ecs"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := services["gone"]; ok {
		t.Fatal("stale service was not removed")
	}
	service, ok := services["i-1"]
	if !ok || service.Meta["instance"] != "10.0.0.1:9100" || service.Meta["group"] != "platform" || service.Meta["team"] != "ops" || service.Meta["bad-key"] != "" {
		t.Fatalf("service=%+v", service)
	}
	var record map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/fake/demo/ecs/r1", &record); err != nil || !found || record["status"] != float64(20000) || record["on"] != float64(1) {
		t.Fatalf("record found=%v err=%v value=%v", found, err, record)
	}
}

func TestCloudECSFailureWritesFailureRecord(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	registry := cloud.NewRegistry()
	registry.Register("fake", fakeProvider{err: errors.New("request credentials=secret failed")})

	if err := NewCloud(registry, st).ECS(context.Background(), "fake", "demo", "r1", false); err == nil {
		t.Fatal("ECS() error = nil")
	}
	var record map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/fake/demo/ecs/r1", &record); err != nil || !found || record["status"] != float64(50000) {
		t.Fatalf("record found=%v err=%v value=%v", found, err, record)
	}
	if record["msg"] != "ecs同步失败" {
		t.Fatalf("failure record leaked provider error: %v", record)
	}
}
