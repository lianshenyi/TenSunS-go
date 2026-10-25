package alicloud

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	resource "github.com/alibabacloud-go/resourcemanager-20200331/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/lianshenyi/TenSunS-go/internal/accounts"
	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	syncservice "github.com/lianshenyi/TenSunS-go/internal/sync"
)

type fakeCredentialLoader struct {
	credentials Credentials
	err         error
}

func (f fakeCredentialLoader) Load(context.Context, string) (Credentials, error) {
	return f.credentials, f.err
}

type fakeResourceGroupClient struct {
	response *resource.ListResourceGroupsResponse
	err      error
	request  *resource.ListResourceGroupsRequest
}

func (f *fakeResourceGroupClient) ListResourceGroups(request *resource.ListResourceGroupsRequest) (*resource.ListResourceGroupsResponse, error) {
	f.request = request
	return f.response, f.err
}

func TestProviderGroupListsFirstPage(t *testing.T) {
	client := &fakeResourceGroupClient{response: &resource.ListResourceGroupsResponse{Body: &resource.ListResourceGroupsResponseBody{
		ResourceGroups: &resource.ListResourceGroupsResponseBodyResourceGroups{ResourceGroup: []*resource.ListResourceGroupsResponseBodyResourceGroupsResourceGroup{
			{Id: tea.String("rg-platform"), DisplayName: tea.String("platform")},
			{Id: tea.String("rg-observability"), DisplayName: tea.String("observability")},
		}},
	}}}
	provider := &Provider{
		credentials: fakeCredentialLoader{credentials: Credentials{AccessKeyID: "test-id", AccessKeySecret: "test-secret"}},
		newClient:   func(Credentials) (ResourceGroupClient, error) { return client, nil },
	}

	groups, err := provider.Group(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if client.request == nil || tea.Int32Value(client.request.PageSize) != 100 || tea.Int32Value(client.request.PageNumber) != 1 {
		t.Fatalf("request = %#v, want PageNumber=1 PageSize=100", client.request)
	}
	if groups["rg-platform"] != "platform" || groups["rg-observability"] != "observability" {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestProviderGroupHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &Provider{
		credentials: fakeCredentialLoader{},
		newClient:   func(Credentials) (ResourceGroupClient, error) { t.Fatal("newClient called"); return nil, nil },
	}
	if _, err := provider.Group(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Group() error = %v, want context.Canceled", err)
	}
}

type fakeScheduler struct{ specs []scheduler.JobSpec }

func (f *fakeScheduler) Upsert(_ context.Context, spec scheduler.JobSpec) error {
	f.specs = append(f.specs, spec)
	return nil
}

func TestCredentialAndAPIFailuresDoNotCreateTasks(t *testing.T) {
	tests := []struct {
		name        string
		credentials CredentialLoader
		newClient   clientFactory
	}{
		{
			name:        "credential failure",
			credentials: fakeCredentialLoader{err: errors.New("credential unavailable")},
			newClient:   func(Credentials) (ResourceGroupClient, error) { t.Fatal("newClient called"); return nil, nil },
		},
		{
			name:        "API failure is redacted",
			credentials: fakeCredentialLoader{},
			newClient: func(Credentials) (ResourceGroupClient, error) {
				return &fakeResourceGroupClient{err: errors.New("request AccessKeyId=test-id Signature=test-signature rejected")}, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mock := consulmock.New()
			server := httptest.NewServer(mock.Handler())
			t.Cleanup(server.Close)
			st := consul.New(server.URL+"/v1", "", server.Client())
			cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
			if err != nil {
				t.Fatal(err)
			}
			registry := cloud.NewRegistry()
			registry.Register("alicloud", &Provider{credentials: test.credentials, newClient: test.newClient})
			jobs := &fakeScheduler{}
			creator := accounts.NewCreator(st, cipher, syncservice.NewCloud(registry, st), jobs)

			err = creator.Create(context.Background(), accounts.Request{Vendor: "alicloud", Account: "demo", AK: "test-id", SK: "test-secret", ProjectInterval: 60})
			if err == nil {
				t.Fatal("Create() error = nil")
			}
			if len(jobs.specs) != 0 {
				t.Fatalf("scheduled specs = %#v", jobs.specs)
			}
			if _, found := mock.GetKV("ConsulManager/jobs/alicloud/demo/group"); found {
				t.Fatal("group task persisted after resource group failure")
			}
			if test.name == "API failure is redacted" {
				var record map[string]any
				found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/alicloud/demo/group", &record)
				if err != nil || !found {
					t.Fatalf("failure record found=%v err=%v", found, err)
				}
				if record["msg"] != "资源组同步失败" {
					t.Fatalf("failure record message = %#v", record["msg"])
				}
			}
		})
	}
}

func TestProviderGroupCredentialAndAPIFailures(t *testing.T) {
	tests := []struct {
		name     string
		provider *Provider
	}{
		{
			name: "credential load",
			provider: &Provider{
				credentials: fakeCredentialLoader{err: errors.New("credential unavailable")},
				newClient:   func(Credentials) (ResourceGroupClient, error) { t.Fatal("newClient called"); return nil, nil },
			},
		},
		{
			name: "API response",
			provider: &Provider{
				credentials: fakeCredentialLoader{},
				newClient: func(Credentials) (ResourceGroupClient, error) {
					return &fakeResourceGroupClient{err: errors.New("API rejected request")}, nil
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.provider.Group(context.Background(), "demo"); err == nil {
				t.Fatal("Group() error = nil")
			}
		})
	}
}
