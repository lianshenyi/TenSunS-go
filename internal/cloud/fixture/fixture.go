// Package fixture implements the desensitized, Consul-backed Provider shared
// by the cloud vendors supported during the fixture-only migration phases.
package fixture

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Provider reads a vendor-scoped fixture. Vendor is deliberately required so
// one account name cannot accidentally read another vendor's test dataset.
type Provider struct {
	Store  store.Store
	Vendor string
}

type value struct {
	Groups map[string]string      `json:"groups"`
	ECS    map[string]model.ECS   `json:"ecs"`
	RDS    map[string]model.RDS   `json:"rds"`
	Redis  map[string]model.Redis `json:"redis"`
	Error  string                 `json:"error"`
}

func (p Provider) load(ctx context.Context, account string) (value, error) {
	if p.Store == nil {
		return value{}, errors.New("cloud fixture store is nil")
	}
	if p.Vendor == "" {
		return value{}, errors.New("cloud fixture vendor is empty")
	}
	var fixture value
	found, err := p.Store.GetValue(ctx, "ConsulManager/fixtures/"+p.Vendor+"/"+account, &fixture)
	if err != nil {
		return value{}, fmt.Errorf("read %s fixture: %w", p.Vendor, err)
	}
	if !found {
		return value{}, fmt.Errorf("%s fixture for account %q is not configured", p.Vendor, account)
	}
	switch fixture.Error {
	case "":
	case "auth":
		return value{}, cloud.ErrAuthentication
	case "rate_limit":
		return value{}, cloud.ErrRateLimited
	case "timeout":
		return value{}, context.DeadlineExceeded
	case "temporary":
		return value{}, cloud.ErrTemporary
	default:
		if status, ok := httpStatus(fixture.Error); ok {
			return value{}, cloud.HTTPStatusError{StatusCode: status}
		}
		return value{}, errors.New(fixture.Error)
	}
	return fixture, nil
}

func httpStatus(text string) (int, bool) {
	statusText, ok := strings.CutPrefix(text, "http_")
	if !ok {
		return 0, false
	}
	status, err := strconv.Atoi(statusText)
	if err != nil || status < 500 || status > 599 {
		return 0, false
	}
	return status, true
}

func (p Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	v, err := p.load(ctx, account)
	return v.Groups, err
}

func (p Provider) ECS(ctx context.Context, account, _ string, _ bool) (map[string]model.ECS, error) {
	v, err := p.load(ctx, account)
	return v.ECS, err
}

func (p Provider) RDS(ctx context.Context, account, _ string) (map[string]model.RDS, error) {
	v, err := p.load(ctx, account)
	return v.RDS, err
}

func (p Provider) Redis(ctx context.Context, account, _ string) (map[string]model.Redis, error) {
	v, err := p.load(ctx, account)
	return v.Redis, err
}

var _ cloud.Provider = Provider{}
