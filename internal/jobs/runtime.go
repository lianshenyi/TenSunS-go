// Package jobs maps legacy func strings to typed runtime calls.
package jobs

import (
	"context"
	"fmt"

	"github.com/lianshenyi/TenSunS-go/internal/jumpserver"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	syncservice "github.com/lianshenyi/TenSunS-go/internal/sync"
)

// Runtime is the scheduler.Executor used by the Go process. Its dependencies
// are injected so the cloud adapters and JMS client can be added without
// changing the scheduler or HTTP layer.
type Runtime struct {
	Cloud syncservice.Service
	JMS   jumpserver.Syncer
}

func (r *Runtime) Execute(ctx context.Context, spec scheduler.JobSpec) error {
	parsed, err := scheduler.ParseFunc(spec.Func)
	if err != nil {
		return err
	}
	if parsed.Kind == scheduler.KindJMS {
		if r.JMS == nil {
			return fmt.Errorf("JumpServer sync is not wired yet")
		}
		return r.JMS.Run(ctx, spec.JMSVendor(), spec.JMSAccount())
	}
	if r.Cloud == nil {
		return fmt.Errorf("cloud sync is not wired yet")
	}
	switch parsed.Kind {
	case scheduler.KindGroup:
		return r.Cloud.Group(ctx, parsed.Vendor, spec.Account())
	case scheduler.KindECS:
		return r.Cloud.ECS(ctx, parsed.Vendor, spec.Account(), spec.Region(), spec.ExternalIP())
	case scheduler.KindRDS:
		return r.Cloud.RDS(ctx, parsed.Vendor, spec.Account(), spec.Region())
	case scheduler.KindRedis:
		return r.Cloud.Redis(ctx, parsed.Vendor, spec.Account(), spec.Region())
	default:
		return fmt.Errorf("unsupported job kind %q", parsed.Kind)
	}
}
