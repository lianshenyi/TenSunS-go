// Package sync contains the CloudProvider -> Consul persistence pipeline.
package sync

import "context"

// Service is the seam between typed jobs and the future Consul sync writer.
// Implementations will read AK/SK and group KV, call a cloud.Provider, then
// register/clean up Services and write execution records.
type Service interface {
	Group(ctx context.Context, vendor, account string) error
	ECS(ctx context.Context, vendor, account, region string, externalIP bool) error
	RDS(ctx context.Context, vendor, account, region string) error
	Redis(ctx context.Context, vendor, account, region string) error
}
