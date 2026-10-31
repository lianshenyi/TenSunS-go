package sync

import "context"

// VendorRouter sends explicitly mapped vendors to their specialized service
// and keeps all other vendors on the default cloud synchronization service.
type VendorRouter struct {
	defaultService Service
	services       map[string]Service
}

// NewVendorRouter creates a vendor-aware Service without changing the legacy
// scheduler contract.
func NewVendorRouter(defaultService Service, services map[string]Service) *VendorRouter {
	return &VendorRouter{defaultService: defaultService, services: services}
}

func (r *VendorRouter) service(vendor string) Service {
	if service := r.services[vendor]; service != nil {
		return service
	}
	return r.defaultService
}

func (r *VendorRouter) Group(ctx context.Context, vendor, account string) error {
	return r.service(vendor).Group(ctx, vendor, account)
}

func (r *VendorRouter) ECS(ctx context.Context, vendor, account, region string, externalIP bool) error {
	return r.service(vendor).ECS(ctx, vendor, account, region, externalIP)
}

func (r *VendorRouter) RDS(ctx context.Context, vendor, account, region string) error {
	return r.service(vendor).RDS(ctx, vendor, account, region)
}

func (r *VendorRouter) Redis(ctx context.Context, vendor, account, region string) error {
	return r.service(vendor).Redis(ctx, vendor, account, region)
}

var _ Service = (*VendorRouter)(nil)
