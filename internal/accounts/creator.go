// Package accounts creates cloud account tasks after resource groups are available.
package accounts

import (
	"context"
	"fmt"
	"slices"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// GroupSynchronizer is the account-creation dependency that must succeed
// before resource tasks are exposed to the scheduler.
type GroupSynchronizer interface {
	Group(ctx context.Context, vendor, account string) error
}

// Scheduler persists a task in the in-memory scheduler after it is written to
// Consul. The narrow interface keeps account creation testable.
type Scheduler interface {
	Upsert(ctx context.Context, spec scheduler.JobSpec) error
}

// Request is the legacy create form shape accepted by POST /api/jobs.
type Request struct {
	DialogStatus    string   `json:"dialogStatus"`
	Vendor          string   `json:"vendor"`
	Account         string   `json:"account"`
	AK              string   `json:"ak"`
	SK              string   `json:"sk"`
	ResourceTypes   []string `json:"restype"`
	Regions         []string `json:"region"`
	ProjectInterval int      `json:"proj_interval"`
	ECSInterval     int      `json:"ecs_interval"`
	RDSInterval     int      `json:"rds_interval"`
	RedisInterval   int      `json:"redis_interval"`
	ExternalIP      bool     `json:"isextip"`
}

// Creator coordinates secure credential persistence, group synchronization,
// task persistence, and scheduler registration.
type Creator struct {
	store  store.Store
	cipher *cryptox.Cipher
	groups GroupSynchronizer
	jobs   Scheduler
}

// NewCreator builds an account creation coordinator.
func NewCreator(st store.Store, cipher *cryptox.Cipher, groups GroupSynchronizer, jobs Scheduler) *Creator {
	return &Creator{store: st, cipher: cipher, groups: groups, jobs: jobs}
}

// Create stores encrypted credentials, synchronizes groups, then makes the
// requested task definitions available. A group failure never creates tasks.
func (c *Creator) Create(ctx context.Context, req Request) error {
	if err := validate(req); err != nil {
		return err
	}
	ak, err := c.cipher.Encrypt(req.AK)
	if err != nil {
		return fmt.Errorf("encrypt access key: %w", err)
	}
	sk, err := c.cipher.Encrypt(req.SK)
	if err != nil {
		return fmt.Errorf("encrypt secret key: %w", err)
	}
	if err := c.store.PutKV(ctx, credentialPath(req.Vendor, req.Account), map[string]string{"ak": ak, "sk": sk}); err != nil {
		return fmt.Errorf("store encrypted credentials: %w", err)
	}
	if err := c.groups.Group(ctx, req.Vendor, req.Account); err != nil {
		return fmt.Errorf("sync resource groups: %w", err)
	}
	for _, spec := range taskSpecs(req) {
		if err := c.store.PutKV(ctx, "ConsulManager/jobs/"+spec.ID, spec); err != nil {
			return fmt.Errorf("store task %s: %w", spec.ID, err)
		}
		if err := c.jobs.Upsert(ctx, spec); err != nil {
			return fmt.Errorf("schedule task %s: %w", spec.ID, err)
		}
	}
	return nil
}

func validate(req Request) error {
	if req.Vendor == "" || req.Account == "" || req.AK == "" || req.SK == "" {
		return fmt.Errorf("vendor, account, ak, and sk are required")
	}
	if req.ProjectInterval <= 0 {
		return fmt.Errorf("proj_interval must be positive")
	}
	for _, resourceType := range req.ResourceTypes {
		if !slices.Contains([]string{"group", "ecs", "rds", "redis"}, resourceType) {
			return fmt.Errorf("unsupported resource type %q", resourceType)
		}
	}
	for _, resourceType := range []string{"ecs", "rds", "redis"} {
		if !slices.Contains(req.ResourceTypes, resourceType) {
			continue
		}
		if len(req.Regions) == 0 {
			return fmt.Errorf("region is required for %s", resourceType)
		}
		if interval(req, resourceType) <= 0 {
			return fmt.Errorf("%s_interval must be positive", resourceType)
		}
	}
	return nil
}

func taskSpecs(req Request) []scheduler.JobSpec {
	specs := []scheduler.JobSpec{{
		ID: req.Vendor + "/" + req.Account + "/group", Func: "__main__:" + req.Vendor + ".group",
		Args: []any{req.Account}, Minutes: req.ProjectInterval, Trigger: "interval", ReplaceExisting: true,
	}}
	for _, resourceType := range []string{"ecs", "rds", "redis"} {
		if !slices.Contains(req.ResourceTypes, resourceType) {
			continue
		}
		for _, region := range req.Regions {
			args := []any{req.Account, region}
			if resourceType == "ecs" {
				args = append(args, req.ExternalIP)
			}
			specs = append(specs, scheduler.JobSpec{
				ID:   req.Vendor + "/" + req.Account + "/" + resourceType + "/" + region,
				Func: "__main__:" + req.Vendor + "." + resourceType, Args: args, Minutes: interval(req, resourceType),
				Trigger: "interval", ReplaceExisting: true,
			})
		}
	}
	return specs
}

func interval(req Request, resourceType string) int {
	switch resourceType {
	case "ecs":
		return req.ECSInterval
	case "rds":
		return req.RDSInterval
	case "redis":
		return req.RedisInterval
	default:
		return req.ProjectInterval
	}
}

func credentialPath(vendor, account string) string {
	return "ConsulManager/assets/" + vendor + "/aksk/" + account
}
