// Package sync contains the CloudProvider -> Consul persistence pipeline.
package sync

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
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

// ECS synchronizes a vendor-normalized ECS list to the legacy Consul Service
// shape. Provider failures are recorded before being returned to the scheduler.
func (c *Cloud) ECS(ctx context.Context, vendor, account, region string, externalIP bool) error {
	provider, err := c.registry.Get(vendor)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "ecs", region, err)
	}
	resources, err := provider.ECS(ctx, account, region, externalIP)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "ecs", region, fmt.Errorf("load ECS: %w", err))
	}
	on, off, err := c.writeECS(ctx, vendor, account, region, resources)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "ecs", region, err)
	}
	return c.recordResourceSuccess(ctx, vendor, account, "ecs", region, len(resources), on, off)
}

// RDS synchronizes vendor-normalized MySQL instances to Consul Services.
func (c *Cloud) RDS(ctx context.Context, vendor, account, region string) error {
	provider, err := c.registry.Get(vendor)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "rds", region, err)
	}
	resources, err := provider.RDS(ctx, account, region)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "rds", region, fmt.Errorf("load RDS: %w", err))
	}
	on, off, err := c.writeRDS(ctx, vendor, account, region, resources)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "rds", region, err)
	}
	return c.recordResourceSuccess(ctx, vendor, account, "rds", region, len(resources), on, off)
}

// Redis synchronizes vendor-normalized Redis instances to Consul Services.
func (c *Cloud) Redis(ctx context.Context, vendor, account, region string) error {
	provider, err := c.registry.Get(vendor)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "redis", region, err)
	}
	resources, err := provider.Redis(ctx, account, region)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "redis", region, fmt.Errorf("load Redis: %w", err))
	}
	on, off, err := c.writeRedis(ctx, vendor, account, region, resources)
	if err != nil {
		return c.recordResourceFailure(ctx, vendor, account, "redis", region, err)
	}
	return c.recordResourceSuccess(ctx, vendor, account, "redis", region, len(resources), on, off)
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

func (c *Cloud) writeECS(ctx context.Context, vendor, account, region string, resources map[string]model.ECS) (int, int, error) {
	if resources == nil {
		resources = map[string]model.ECS{}
	}
	if err := c.removeStale(ctx, vendor, account, region, "ecs", resources); err != nil {
		return 0, 0, err
	}
	on, off := 0, 0
	for id, resource := range resources {
		status := resourceStatus(resource.Status)
		if status == "on" {
			on++
		} else {
			off++
		}
		ip, port, err := c.customAddress(ctx, "ecs", id, resource.IP, defaultECSPort(resource.OSType))
		if err != nil {
			return 0, 0, err
		}
		meta := baseMeta(vendor, account, region, id, resource.Name, resource.Group, ip, port, status)
		meta["os"] = resource.OSType
		meta["cpu"] = resource.CPU
		meta["mem"] = resource.Mem
		meta["exp"] = resource.Exp
		for key, value := range resource.Tags {
			if isAlphaNumeric(key) {
				meta[key] = value
			}
		}
		if err := c.store.Register(ctx, serviceRegistration(id, vendor, account, "ecs", ip, port, []string{tagForStatus(status), resource.OSType, region}, meta)); err != nil {
			return 0, 0, fmt.Errorf("register ECS %s: %w", id, err)
		}
	}
	return on, off, nil
}

func (c *Cloud) writeRDS(ctx context.Context, vendor, account, region string, resources map[string]model.RDS) (int, int, error) {
	if resources == nil {
		resources = map[string]model.RDS{}
	}
	if err := c.removeStale(ctx, vendor, account, region, "rds", resources); err != nil {
		return 0, 0, err
	}
	on, off := 0, 0
	for id, resource := range resources {
		status := resourceStatus(resource.Status)
		if status == "on" {
			on++
		} else {
			off++
		}
		ip, port, err := c.customAddress(ctx, "rds", id, resource.IP, resource.Port)
		if err != nil {
			return 0, 0, err
		}
		meta := baseMeta(vendor, account, region, id, resource.Name, resource.Group, ip, port, status)
		meta["itype"], meta["ver"], meta["domain"] = resource.IType, resource.Ver, resource.Domain
		meta["disk"], meta["cpu"], meta["mem"], meta["exp"] = resource.Disk, resource.CPU, resource.Mem, resource.Exp
		meta["team"] = resource.Team
		if err := c.store.Register(ctx, serviceRegistration(id, vendor, account, "rds", ip, port, []string{tagForStatus(status), resource.IType, resource.Ver, region}, meta)); err != nil {
			return 0, 0, fmt.Errorf("register RDS %s: %w", id, err)
		}
	}
	return on, off, nil
}

func (c *Cloud) writeRedis(ctx context.Context, vendor, account, region string, resources map[string]model.Redis) (int, int, error) {
	if resources == nil {
		resources = map[string]model.Redis{}
	}
	if err := c.removeStale(ctx, vendor, account, region, "redis", resources); err != nil {
		return 0, 0, err
	}
	on, off := 0, 0
	for id, resource := range resources {
		status := resourceStatus(resource.Status)
		if status == "on" {
			on++
		} else {
			off++
		}
		ip, port, err := c.customAddress(ctx, "redis", id, resource.Domain, resource.Port)
		if err != nil {
			return 0, 0, err
		}
		meta := baseMeta(vendor, account, region, id, resource.Name, resource.Group, ip, port, status)
		meta["itype"], meta["ver"], meta["ip"], meta["mem"], meta["exp"] = resource.IType, resource.Ver, resource.IP, resource.Mem, resource.Exp
		if err := c.store.Register(ctx, serviceRegistration(id, vendor, account, "redis", ip, port, []string{tagForStatus(status), resource.IType, resource.Ver, region}, meta)); err != nil {
			return 0, 0, fmt.Errorf("register Redis %s: %w", id, err)
		}
	}
	return on, off, nil
}

func (c *Cloud) removeStale(ctx context.Context, vendor, account, region, resourceType string, current any) error {
	services, err := c.store.Services(ctx, fmt.Sprintf(`Service == %q and %q in Tags and Meta.account == %q`, serviceName(vendor, account, resourceType), region, account))
	if err != nil {
		return fmt.Errorf("list %s services: %w", resourceType, err)
	}
	for id := range services {
		if !containsResource(current, id) {
			if err := c.store.Deregister(ctx, id); err != nil {
				return fmt.Errorf("deregister stale %s %s: %w", resourceType, id, err)
			}
		}
	}
	return nil
}

func containsResource(resources any, id string) bool {
	switch values := resources.(type) {
	case map[string]model.ECS:
		_, ok := values[id]
		return ok
	case map[string]model.RDS:
		_, ok := values[id]
		return ok
	case map[string]model.Redis:
		_, ok := values[id]
		return ok
	default:
		return false
	}
}

func (c *Cloud) customAddress(ctx context.Context, resourceType, id, address string, port int) (string, int, error) {
	var custom map[string]any
	_, err := c.store.GetValue(ctx, "ConsulManager/assets/sync_"+resourceType+"_custom/"+id, &custom)
	if err != nil {
		return "", 0, fmt.Errorf("read custom %s %s: %w", resourceType, id, err)
	}
	if custom == nil {
		return address, port, nil
	}
	if value, ok := custom["ip"].(string); ok && value != "" {
		address = value
	}
	if value, ok := custom["port"].(float64); ok && value > 0 {
		port = int(value)
	}
	return address, port, nil
}

func (c *Cloud) recordResourceSuccess(ctx context.Context, vendor, account, resourceType, region string, count, on, off int) error {
	return c.store.PutKV(ctx, resourceRecordPath(vendor, account, resourceType, region), map[string]any{"status": 20000, "count": count, "on": on, "off": off, "update": c.now().Format("01.02/15:04"), "msg": resourceType + "同步成功"})
}

func (c *Cloud) recordResourceFailure(ctx context.Context, vendor, account, resourceType, region string, cause error) error {
	if err := c.store.PutKV(ctx, resourceRecordPath(vendor, account, resourceType, region), map[string]any{"status": 50000, "count": "无", "on": 0, "off": 0, "update": "失败", "msg": cause.Error()}); err != nil {
		return fmt.Errorf("record %s failure: %w", resourceType, err)
	}
	return cause
}

func serviceRegistration(id, vendor, account, resourceType, address string, port int, tags []string, meta map[string]string) store.ServiceRegistration {
	return store.ServiceRegistration{ID: id, Name: serviceName(vendor, account, resourceType), Address: address, Port: port, Tags: tags, Meta: meta, Check: &store.ServiceCheck{TCP: address + ":" + strconv.Itoa(port), Interval: "60s"}}
}
func serviceName(vendor, account, resourceType string) string {
	return vendor + "_" + account + "_" + resourceType
}
func resourceRecordPath(vendor, account, resourceType, region string) string {
	return "ConsulManager/record/jobs/" + vendor + "/" + account + "/" + resourceType + "/" + region
}
func tagForStatus(status string) string {
	if status == "on" {
		return "ON"
	}
	return "OFF"
}
func defaultECSPort(os string) int {
	if os == "windows" {
		return 9182
	}
	return 9100
}
func resourceStatus(value string) string {
	switch value {
	case "SHUTOFF", "Stopped", "STOPPED", "SHUTDOWN", "非运行中", "Unavailable", "Inactive", "Released":
		return "off"
	default:
		return "on"
	}
}
func isAlphaNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
func baseMeta(vendor, account, region, id, name, group, address string, port int, status string) map[string]string {
	return map[string]string{"iid": id, "name": name, "region": region, "group": group, "instance": address + ":" + strconv.Itoa(port), "account": account, "vendor": vendorDisplay(vendor), "stat": status}
}
func vendorDisplay(vendor string) string {
	switch vendor {
	case "alicloud":
		return "阿里云"
	case "tencent_cloud":
		return "腾讯云"
	case "huaweicloud":
		return "华为云"
	case "awscloud":
		return "AWS云"
	default:
		return "未找到"
	}
}

func groupPath(vendor, account string) string {
	return "ConsulManager/assets/" + vendor + "/group/" + account
}

func groupRecordPath(vendor, account string) string {
	return "ConsulManager/record/jobs/" + vendor + "/" + account + "/group"
}
