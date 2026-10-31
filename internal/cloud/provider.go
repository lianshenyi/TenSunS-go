// Package cloud defines vendor adapters and their normalized resource output.
package cloud

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
)

// Provider is the contract from migration baseline section 10.2.
type Provider interface {
	Group(ctx context.Context, account string) (map[string]string, error)
	ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error)
	RDS(ctx context.Context, account, region string) (map[string]model.RDS, error)
	Redis(ctx context.Context, account, region string) (map[string]model.Redis, error)
}

// Registry maps vendor keys to adapters. Registration happens in main or
// vendor-specific init code; the skeleton intentionally registers none yet.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(vendor string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[vendor] = provider
}

func (r *Registry) Get(vendor string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[vendor]
	if !ok {
		return nil, fmt.Errorf("cloud provider %q is not registered", vendor)
	}
	return provider, nil
}

// Vendors returns a snapshot for startup logs.
func (r *Registry) Vendors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for vendor := range r.providers {
		out = append(out, vendor)
	}
	slices.Sort(out)
	return out
}
