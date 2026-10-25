// Package sync contains the CloudProvider -> Consul persistence pipeline.
package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Service is the seam between typed jobs and the future Consul sync writer.
// Implementations will read AK/SK and group KV, call a cloud.Provider, then
// register/clean up Services and write execution records.
type Service interface {
	Group(ctx context.Context, vendor, account string) error
	ECS(ctx context.Context, vendor, account, region string, externalIP bool) error
	RDS(ctx context.Context, vendor, account, region string) error
	Redis(ctx context.Context, vendor, account, region string) error
}

// Cloud persists results returned by registered cloud providers. It is kept
// separate from the job runtime so provider adapters remain injectable.
type Cloud struct {
	registry *cloud.Registry
	store    store.Store
	now      func() time.Time
}

// NewCloud creates a cloud synchronization service backed by a provider
// registry and the existing Consul store abstraction.
func NewCloud(registry *cloud.Registry, st store.Store) *Cloud {
	return &Cloud{registry: registry, store: st, now: time.Now}
}

// Group obtains an account's resource groups and persists the normalized
// mapping and execution record expected by the existing read APIs.
func (c *Cloud) Group(ctx context.Context, vendor, account string) error {
	provider, err := c.registry.Get(vendor)
	if err != nil {
		return c.recordGroupFailure(ctx, vendor, account, err)
	}
	groups, err := provider.Group(ctx, account)
	if err != nil {
		return c.recordGroupFailure(ctx, vendor, account, fmt.Errorf("load resource groups: %w", err))
	}
	if groups == nil {
		groups = map[string]string{}
	}
	if err := c.store.PutKV(ctx, groupPath(vendor, account), groups); err != nil {
		return fmt.Errorf("store resource groups: %w", err)
	}
	if err := c.store.PutKV(ctx, groupRecordPath(vendor, account), map[string]any{
		"status": 20000,
		"count":  len(groups),
		"update": c.now().Format("01.02/15:04"),
		"msg":    "资源组同步成功",
	}); err != nil {
		return fmt.Errorf("store resource group record: %w", err)
	}
	return nil
}

// ECS is intentionally unavailable until the resource persistence pipeline is
// implemented; account creation only requires Group in this migration phase.
func (*Cloud) ECS(context.Context, string, string, string, bool) error {
	return fmt.Errorf("ECS synchronization is not implemented")
}

// RDS is intentionally unavailable until the resource persistence pipeline is
// implemented; account creation only requires Group in this migration phase.
func (*Cloud) RDS(context.Context, string, string, string) error {
	return fmt.Errorf("RDS synchronization is not implemented")
}

// Redis is intentionally unavailable until the resource persistence pipeline
// is implemented; account creation only requires Group in this migration phase.
func (*Cloud) Redis(context.Context, string, string, string) error {
	return fmt.Errorf("Redis synchronization is not implemented")
}

func (c *Cloud) recordGroupFailure(ctx context.Context, vendor, account string, cause error) error {
	if err := c.store.PutKV(ctx, groupRecordPath(vendor, account), map[string]any{
		"status": 50000,
		"count":  0,
		"update": c.now().Format("01.02/15:04"),
		"msg":    cause.Error(),
	}); err != nil {
		return fmt.Errorf("record resource group failure: %w", err)
	}
	return cause
}

func groupPath(vendor, account string) string {
	return "ConsulManager/assets/" + vendor + "/group/" + account
}

func groupRecordPath(vendor, account string) string {
	return "ConsulManager/record/jobs/" + vendor + "/" + account + "/group"
}
