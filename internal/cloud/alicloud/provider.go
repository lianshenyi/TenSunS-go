// Package alicloud implements the read-only Alibaba Cloud resource-group adapter.
package alicloud

import (
	"context"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/client"
	resource "github.com/alibabacloud-go/resourcemanager-20200331/v2/client"
	"github.com/alibabacloud-go/tea/tea"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

const credentialKeyPrefix = "ConsulManager/assets/alicloud/aksk/"

// Credentials holds an account's decrypted credentials only while making an
// Alibaba Cloud SDK request. It must never be logged or returned to callers.
type Credentials struct {
	AccessKeyID     string
	AccessKeySecret string
}

// CredentialLoader gets account credentials from their secure source.
type CredentialLoader interface {
	Load(ctx context.Context, account string) (Credentials, error)
}

// ResourceGroupClient is the read-only subset of Alibaba Cloud's official SDK
// used by this provider.
type ResourceGroupClient interface {
	ListResourceGroups(request *resource.ListResourceGroupsRequest) (*resource.ListResourceGroupsResponse, error)
}

type clientFactory func(Credentials) (ResourceGroupClient, error)

// Provider lists Alibaba Cloud resource groups. ECS, RDS, and Redis remain
// deliberately unavailable: this change is constrained to the group API.
type Provider struct {
	credentials CredentialLoader
	newClient   clientFactory
}

// New creates a Provider which loads existing encrypted Consul credentials.
func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{
		credentials: credentialStore{store: st, cipher: cipher},
		newClient:   newClient,
	}
}

// Group returns Id-to-DisplayName mappings from Alibaba Cloud's read-only
// ListResourceGroups API. The API request is limited to the first 100 rows to
// preserve the behavior of the legacy implementation.
func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	credentials, err := p.credentials.Load(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("load Alibaba Cloud credentials: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := p.newClient(credentials)
	if err != nil {
		return nil, fmt.Errorf("create Alibaba Cloud resource manager client: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := client.ListResourceGroups(&resource.ListResourceGroupsRequest{
		PageNumber: tea.Int32(1),
		PageSize:   tea.Int32(100),
	})
	if err != nil {
		return nil, redactedAPIError{cause: err}
	}

	groups := make(map[string]string)
	if response == nil || response.Body == nil || response.Body.ResourceGroups == nil {
		return groups, nil
	}
	for _, group := range response.Body.ResourceGroups.ResourceGroup {
		if group == nil || tea.StringValue(group.Id) == "" {
			continue
		}
		groups[tea.StringValue(group.Id)] = tea.StringValue(group.DisplayName)
	}
	return groups, nil
}

// redactedAPIError keeps a provider error usable with errors.Is/As without
// allowing a signed SDK request (and its credentials) into Consul records.
type redactedAPIError struct{ cause error }

func (e redactedAPIError) Error() string { return "Alibaba Cloud ListResourceGroups request failed" }

func (e redactedAPIError) Unwrap() error { return e.cause }

// ECS is outside the scope of the resource-group-only provider.
func (*Provider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return nil, fmt.Errorf("Alibaba Cloud ECS synchronization is not implemented")
}

// RDS is outside the scope of the resource-group-only provider.
func (*Provider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, fmt.Errorf("Alibaba Cloud RDS synchronization is not implemented")
}

// Redis is outside the scope of the resource-group-only provider.
func (*Provider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, fmt.Errorf("Alibaba Cloud Redis synchronization is not implemented")
}

type credentialStore struct {
	store  store.Store
	cipher *cryptox.Cipher
}

func (s credentialStore) Load(ctx context.Context, account string) (Credentials, error) {
	var encrypted map[string]string
	found, err := s.store.GetValue(ctx, credentialKeyPrefix+account, &encrypted)
	if err != nil {
		return Credentials{}, fmt.Errorf("read encrypted credentials: %w", err)
	}
	if !found || encrypted["ak"] == "" || encrypted["sk"] == "" {
		return Credentials{}, fmt.Errorf("encrypted credentials are missing")
	}
	ak, err := s.cipher.Decrypt(encrypted["ak"])
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt access key: %w", err)
	}
	sk, err := s.cipher.Decrypt(encrypted["sk"])
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt secret key: %w", err)
	}
	return Credentials{AccessKeyID: ak, AccessKeySecret: sk}, nil
}

func newClient(credentials Credentials) (ResourceGroupClient, error) {
	return resource.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(credentials.AccessKeyID),
		AccessKeySecret: tea.String(credentials.AccessKeySecret),
		Endpoint:        tea.String("resourcemanager.aliyuncs.com"),
	})
}
