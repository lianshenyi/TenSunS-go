package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Sync persists normalized provider output using the legacy Consul key and
// service contracts, allowing an immediate rollback to the Python backend.
type Sync struct {
	Store     store.Store
	Providers *cloud.Registry
	Now       func() time.Time
}

// New constructs the common Provider -> Consul persistence pipeline.
func New(st store.Store, providers *cloud.Registry) *Sync {
	return &Sync{Store: st, Providers: providers, Now: time.Now}
}

func (s *Sync) Group(ctx context.Context, vendor, account string) error {
	provider, err := s.provider(vendor)
	if err != nil {
		return s.failure(ctx, vendor, account, "group", "", err)
	}
	groups, err := provider.Group(ctx, account)
	if err == nil {
		err = s.Store.PutKV(ctx, assetPath(vendor, "group", account), groups)
	}
	if err != nil {
		return s.failure(ctx, vendor, account, "group", "", err)
	}
	count := len(groups)
	return s.putRecord(ctx, vendor, account, "group", "", record{Count: count, Update: s.now().Format("01.02/15:04"), Status: 20000, Msg: fmt.Sprintf("同步资源组成功！总数：%d", count)})
}

func (s *Sync) ECS(ctx context.Context, vendor, account, region string, externalIP bool) error {
	provider, err := s.provider(vendor)
	if err != nil {
		return s.failure(ctx, vendor, account, "ecs", region, err)
	}
	resources, err := provider.ECS(ctx, account, region, externalIP)
	if err == nil {
		var on, off int
		on, off, err = s.writeECS(ctx, vendor, account, region, resources)
		if err == nil {
			return s.putRecord(ctx, vendor, account, "ecs", region, record{Count: len(resources), Update: s.now().Format("01.02/15:04"), Status: 20000, On: on, Off: off, Msg: fmt.Sprintf("ECS同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off)})
		}
	}
	return s.failure(ctx, vendor, account, "ecs", region, err)
}

func (s *Sync) RDS(ctx context.Context, vendor, account, region string) error {
	provider, err := s.provider(vendor)
	if err != nil {
		return s.failure(ctx, vendor, account, "rds", region, err)
	}
	resources, err := provider.RDS(ctx, account, region)
	if err == nil {
		var on, off int
		on, off, err = s.writeRDS(ctx, vendor, account, region, resources)
		if err == nil {
			return s.putRecord(ctx, vendor, account, "rds", region, record{Count: len(resources), Update: s.now().Format("01.02/15:04"), Status: 20000, On: on, Off: off, Msg: fmt.Sprintf("rds同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off)})
		}
	}
	return s.failure(ctx, vendor, account, "rds", region, err)
}

func (s *Sync) Redis(ctx context.Context, vendor, account, region string) error {
	provider, err := s.provider(vendor)
	if err != nil {
		return s.failure(ctx, vendor, account, "redis", region, err)
	}
	resources, err := provider.Redis(ctx, account, region)
	if err == nil {
		var on, off int
		on, off, err = s.writeRedis(ctx, vendor, account, region, resources)
		if err == nil {
			return s.putRecord(ctx, vendor, account, "redis", region, record{Count: len(resources), Update: s.now().Format("01.02/15:04"), Status: 20000, On: on, Off: off, Msg: fmt.Sprintf("redis同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off)})
		}
	}
	return s.failure(ctx, vendor, account, "redis", region, err)
}

func (s *Sync) provider(vendor string) (cloud.Provider, error) {
	if s.Store == nil || s.Providers == nil {
		return nil, fmt.Errorf("cloud sync is not configured")
	}
	return s.Providers.Get(vendor)
}

func (s *Sync) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type record struct {
	Count  any    `json:"count"`
	Update string `json:"update"`
	Status int    `json:"status"`
	On     int    `json:"on,omitempty"`
	Off    int    `json:"off,omitempty"`
	Msg    string `json:"msg"`
}

func (s *Sync) putRecord(ctx context.Context, vendor, account, kind, region string, data record) error {
	return s.Store.PutKV(ctx, recordPath(vendor, account, kind, region), data)
}

func (s *Sync) failure(ctx context.Context, vendor, account, kind, region string, cause error) error {
	data := record{Count: "无", Update: "失败", Status: 50000, Msg: cause.Error()}
	var old record
	if found, err := s.Store.GetValue(ctx, recordPath(vendor, account, kind, region), &old); err == nil && found {
		data.Count, data.On, data.Off = old.Count, old.On, old.Off
	}
	if err := s.putRecord(ctx, vendor, account, kind, region, data); err != nil {
		return fmt.Errorf("%w; write failure record: %v", cause, err)
	}
	return cause
}

func assetPath(vendor, asset, account string) string {
	return "ConsulManager/assets/" + vendor + "/" + asset + "/" + account
}
func recordPath(vendor, account, kind, region string) string {
	prefix := "ConsulManager/record/jobs/" + vendor + "/" + account + "/" + kind
	if region != "" {
		return prefix + "/" + region
	}
	return prefix
}

func (s *Sync) stale(ctx context.Context, name, account, region string, current map[string]struct{}) error {
	filter := fmt.Sprintf(`Service == %q and %q in Tags and Meta.account == %q`, name, region, account)
	existing, err := s.Store.Services(ctx, filter)
	if err != nil {
		return fmt.Errorf("list existing %s services: %w", name, err)
	}
	for id := range existing {
		if _, found := current[id]; !found {
			if err := s.Store.Deregister(ctx, id); err != nil {
				return fmt.Errorf("deregister stale service %s: %w", id, err)
			}
		}
	}
	return nil
}

func isOff(kind, status string) bool {
	switch kind {
	case "ecs":
		return status == "SHUTOFF" || status == "Stopped" || status == "STOPPED"
	case "rds":
		return status == "SHUTDOWN" || status == "非运行中"
	default:
		return status == "SHUTDOWN" || status == "Unavailable" || status == "Inactive" || status == "Released" || status == "非运行中"
	}
}

func custom(ctx context.Context, st store.Store, kind, id string) (ip string, port int, err error) {
	var value struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	}
	_, err = st.GetValue(ctx, "ConsulManager/assets/sync_"+kind+"_custom/"+id, &value)
	return value.IP, value.Port, err
}

func regionName(vendor, region string) string { return config.RegionDisplay(vendor, region) }
func vendorName(vendor string) string         { return config.VendorDisplay(vendor) }

var _ Service = (*Sync)(nil)
var _ = model.ECS{}
