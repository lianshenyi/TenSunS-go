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

type recordingGroups struct {
	vendors  []string
	accounts []string
}

func (g *recordingGroups) Group(_ context.Context, vendor, account string) error {
	g.vendors = append(g.vendors, vendor)
	g.accounts = append(g.accounts, account)
	return nil
}

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

func TestCreatorCreatesPVEAccount(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, _ := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	jobs := &fakeScheduler{}
	groups := &recordingGroups{}
	creator := NewCreator(st, cipher, groups, jobs)
	tlsVerify := false
	req := Request{
		Vendor: "pve", Account: "demo",
		APIBaseURL: "https://pve.example:8006/api2/json", TokenID: "reader@pve!tensuns", TokenSecret: "plain-secret",
		TLSVerify: &tlsVerify,
		ResourceTypes:   []string{"group", "ecs"},
		Regions:         []string{"lab-cluster"},
		ProjectInterval: 60, ECSInterval: 10,
	}
	if err := creator.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Credentials land where sync.PVE reads them, with an encrypted secret.
	var creds map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/assets/pve/api/demo", &creds); err != nil || !found {
		t.Fatalf("pve credentials found=%v err=%v", found, err)
	}
	if creds["api_base_url"] != "https://pve.example:8006/api2/json" || creds["token_id"] != "reader@pve!tensuns" || creds["cluster"] != "lab-cluster" {
		t.Fatalf("pve credentials = %#v", creds)
	}
	if plain, err := cipher.Decrypt(creds["token_secret"].(string)); err != nil || plain != "plain-secret" {
		t.Fatalf("token secret plaintext=%q err=%v", plain, err)
	}
	if v, ok := creds["tls_verify"].(bool); !ok || v {
		t.Fatalf("tls_verify = %#v", creds["tls_verify"])
	}
	// The legacy AK/SK path must not be written for PVE.
	if _, found := mock.GetKV("ConsulManager/assets/pve/aksk/demo"); found {
		t.Fatal("unexpected aksk KV for PVE account")
	}
	// Group sync is requested for vendor pve so the router can send it to sync.PVE.
	if len(groups.vendors) != 1 || groups.vendors[0] != "pve" || groups.accounts[0] != "demo" {
		t.Fatalf("group sync calls = %v %v", groups.vendors, groups.accounts)
	}
	if len(jobs.specs) != 2 || jobs.specs[0].ID != "pve/demo/group" || jobs.specs[1].ID != "pve/demo/ecs/lab-cluster" {
		t.Fatalf("scheduled specs = %#v", jobs.specs)
	}
}

func TestCreatorPVEAccountDefaultsTLSVerifyWhenAbsent(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, _ := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	creator := NewCreator(st, cipher, fakeGroups{}, &fakeScheduler{})
	req := Request{
		Vendor: "pve", Account: "demo",
		APIBaseURL: "https://pve.example:8006/api2/json", TokenID: "reader@pve!tensuns", TokenSecret: "plain-secret",
		ResourceTypes: []string{"group"}, ProjectInterval: 60,
	}
	if err := creator.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var creds map[string]any
	if found, err := st.GetValue(context.Background(), "ConsulManager/assets/pve/api/demo", &creds); err != nil || !found {
		t.Fatalf("pve credentials found=%v err=%v", found, err)
	}
	// An absent tls_verify keeps the KV free of the field so sync.PVE applies
	// its legacy default of certificate verification enabled.
	if _, ok := creds["tls_verify"]; ok {
		t.Fatalf("tls_verify must be omitted when absent, got %#v", creds)
	}
}

func TestCreatorRejectsPVEWithoutTokenCredentials(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, _ := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	jobs := &fakeScheduler{}
	groups := &recordingGroups{}
	creator := NewCreator(st, cipher, groups, jobs)
	req := Request{Vendor: "pve", Account: "demo", ResourceTypes: []string{"group"}, ProjectInterval: 60}
	if err := creator.Create(context.Background(), req); err == nil {
		t.Fatal("Create() error = nil")
	}
	if _, found := mock.GetKV("ConsulManager/assets/pve/api/demo"); found {
		t.Fatal("credentials written for invalid PVE request")
	}
	if len(groups.vendors) != 0 {
		t.Fatalf("group sync ran for invalid request: %v", groups.vendors)
	}
	if len(jobs.specs) != 0 {
		t.Fatalf("scheduled specs = %#v", jobs.specs)
	}
}

func TestCreatorRejectsCloudVendorWithoutAKSK(t *testing.T) {
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	st := consul.New(server.URL+"/v1", "", server.Client())
	cipher, _ := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	creator := NewCreator(st, cipher, fakeGroups{}, &fakeScheduler{})
	req := Request{Vendor: "alicloud", Account: "demo", ResourceTypes: []string{"group"}, ProjectInterval: 60}
	if err := creator.Create(context.Background(), req); err == nil {
		t.Fatal("Create() error = nil")
	}
	if _, found := mock.GetKV("ConsulManager/assets/alicloud/aksk/demo"); found {
		t.Fatal("credentials written for invalid cloud request")
	}
}
