package accounts

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

type fakeGroups struct{ err error }

func (f fakeGroups) Group(context.Context, string, string) error { return f.err }

type fakeScheduler struct{ specs []scheduler.JobSpec }

func (f *fakeScheduler) Upsert(_ context.Context, spec scheduler.JobSpec) error {
	f.specs = append(f.specs, spec)
	return nil
}

func TestCreatorCreatesTasksOnlyAfterGroupSync(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	jobs := &fakeScheduler{}
	creator := NewCreator(st, cipher, fakeGroups{}, jobs)
	req := Request{Vendor: "fake", Account: "demo", AK: "access", SK: "secret", ResourceTypes: []string{"group", "ecs", "rds"}, Regions: []string{"test-1"}, ProjectInterval: 60, ECSInterval: 10, RDSInterval: 20}
	if err := creator.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(jobs.specs) != 3 || jobs.specs[1].ID != "fake/demo/ecs/test-1" {
		t.Fatalf("scheduled specs = %#v", jobs.specs)
	}
	var credentials map[string]string
	if found, err := st.GetValue(context.Background(), "ConsulManager/assets/fake/aksk/demo", &credentials); err != nil || !found {
		t.Fatalf("credentials found=%v err=%v", found, err)
	}
	if plaintext, err := cipher.Decrypt(credentials["ak"]); err != nil || plaintext != "access" {
		t.Fatalf("credential plaintext=%q err=%v", plaintext, err)
	}
}

func TestCreatorDoesNotCreateTasksAfterGroupFailure(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, _ := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	jobs := &fakeScheduler{}
	creator := NewCreator(st, cipher, fakeGroups{err: errors.New("groups failed")}, jobs)
	req := Request{Vendor: "fake", Account: "demo", AK: "access", SK: "secret", ProjectInterval: 60}
	if err := creator.Create(context.Background(), req); err == nil {
		t.Fatal("Create() error = nil")
	}
	if len(jobs.specs) != 0 {
		t.Fatalf("scheduled specs = %#v", jobs.specs)
	}
	if _, found := mock.GetKV("ConsulManager/jobs/fake/demo/group"); found {
		t.Fatal("group task persisted after group failure")
	}
}
