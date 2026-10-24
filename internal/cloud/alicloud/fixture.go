// Package alicloud contains the phase-one, desensitized fixture provider.
package alicloud

import (
	"context"
	"errors"
	"fmt"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// FixtureProvider reads explicitly provisioned, desensitized data. It makes
// scheduled integration safe until the real Alibaba SDK adapter is approved.
type FixtureProvider struct{ Store store.Store }

type fixture struct {
	Groups map[string]string      `json:"groups"`
	ECS    map[string]model.ECS   `json:"ecs"`
	RDS    map[string]model.RDS   `json:"rds"`
	Redis  map[string]model.Redis `json:"redis"`
	Error  string                 `json:"error"`
}

func (p FixtureProvider) load(ctx context.Context, account string) (fixture, error) {
	if p.Store == nil {
		return fixture{}, errors.New("alicloud fixture store is nil")
	}
	var value fixture
	found, err := p.Store.GetValue(ctx, "ConsulManager/fixtures/alicloud/"+account, &value)
	if err != nil {
		return fixture{}, fmt.Errorf("read alicloud fixture: %w", err)
	}
	if !found {
		return fixture{}, fmt.Errorf("alicloud fixture for account %q is not configured", account)
	}
	switch value.Error {
	case "":
	case "auth":
		return fixture{}, cloud.ErrAuthentication
	case "rate_limit":
		return fixture{}, cloud.ErrRateLimited
	case "timeout":
		return fixture{}, context.DeadlineExceeded
	default:
		return fixture{}, errors.New(value.Error)
	}
	return value, nil
}

func (p FixtureProvider) Group(ctx context.Context, account string) (map[string]string, error) {
	v, e := p.load(ctx, account)
	return v.Groups, e
}
func (p FixtureProvider) ECS(ctx context.Context, account, _ string, _ bool) (map[string]model.ECS, error) {
	v, e := p.load(ctx, account)
	return v.ECS, e
}
func (p FixtureProvider) RDS(ctx context.Context, account, _ string) (map[string]model.RDS, error) {
	v, e := p.load(ctx, account)
	return v.RDS, e
}
func (p FixtureProvider) Redis(ctx context.Context, account, _ string) (map[string]model.Redis, error) {
	v, e := p.load(ctx, account)
	return v.Redis, e
}

var _ cloud.Provider = FixtureProvider{}
