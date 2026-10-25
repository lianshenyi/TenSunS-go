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
	err    error
}

func (p fakeProvider) Group(context.Context, string) (map[string]string, error) {
	return p.groups, p.err
}
func (fakeProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return nil, errors.New("not used")
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
}
