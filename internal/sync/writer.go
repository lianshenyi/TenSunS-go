package sync

import (
	"context"
	"fmt"
	"sort"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

func (s *Sync) writeECS(ctx context.Context, vendor, account, region string, resources map[string]model.ECS) (on, off int, err error) {
	name := vendor + "_" + account + "_ecs"
	ids := ids(resources)
	if err := s.stale(ctx, name, account, region, ids); err != nil {
		return 0, 0, err
	}
	for _, id := range sorted(ids) {
		resource := resources[id]
		stat, tag := "on", "ON"
		if isOff("ecs", resource.Status) {
			stat, tag, off = "off", "OFF", off+1
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "ecs", id)
		if err != nil {
			return 0, 0, fmt.Errorf("read ECS custom %s: %w", id, err)
		}
		if ip == "" {
			ip = resource.IP
		}
		if port == 0 {
			if resource.OSType == "windows" {
				port = 9182
			} else {
				port = 9100
			}
		}
		meta := map[string]string{"iid": id, "name": resource.Name, "region": regionName(vendor, region), "group": resource.Group, "instance": fmt.Sprintf("%s:%d", ip, port), "account": account, "vendor": vendorName(vendor), "os": resource.OSType, "cpu": resource.CPU, "mem": resource.Mem, "exp": resource.Exp, "stat": stat}
		for key, value := range resource.Tags {
			if asciiAlphaNum(key) {
				meta[key] = value
			}
		}
		if err := s.Store.Register(ctx, store.ServiceRegistration{ID: id, Name: name, Address: ip, Port: port, Tags: []string{tag, resource.OSType, region}, Meta: meta, Check: tcpCheck(ip, port)}); err != nil {
			return 0, 0, fmt.Errorf("register ECS %s: %w", id, err)
		}
	}
	return on, off, nil
}

func (s *Sync) writeRDS(ctx context.Context, vendor, account, region string, resources map[string]model.RDS) (on, off int, err error) {
	name := vendor + "_" + account + "_rds"
	ids := ids(resources)
	if err := s.stale(ctx, name, account, region, ids); err != nil {
		return 0, 0, err
	}
	for _, id := range sorted(ids) {
		resource := resources[id]
		stat, tag := "on", "ON"
		if isOff("rds", resource.Status) {
			stat, tag, off = "off", "OFF", off+1
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "rds", id)
		if err != nil {
			return 0, 0, fmt.Errorf("read RDS custom %s: %w", id, err)
		}
		if ip == "" {
			ip = resource.IP
		}
		if port == 0 {
			port = resource.Port
		}
		team := resource.Team
		if team == "" {
			team = "无"
		}
		meta := map[string]string{"iid": id, "name": resource.Name, "region": regionName(vendor, region), "group": resource.Group, "instance": fmt.Sprintf("%s:%d", ip, port), "account": account, "itype": resource.IType, "vendor": vendorName(vendor), "disk": resource.Disk, "cpu": resource.CPU, "mem": resource.Mem, "ver": resource.Ver, "domain": resource.Domain, "exp": resource.Exp, "stat": stat, "team": team}
		if err := s.Store.Register(ctx, store.ServiceRegistration{ID: id, Name: name, Address: ip, Port: port, Tags: []string{tag, resource.IType, resource.Ver, region}, Meta: meta, Check: tcpCheck(ip, port)}); err != nil {
			return 0, 0, fmt.Errorf("register RDS %s: %w", id, err)
		}
	}
	return on, off, nil
}

func (s *Sync) writeRedis(ctx context.Context, vendor, account, region string, resources map[string]model.Redis) (on, off int, err error) {
	name := vendor + "_" + account + "_redis"
	ids := ids(resources)
	if err := s.stale(ctx, name, account, region, ids); err != nil {
		return 0, 0, err
	}
	for _, id := range sorted(ids) {
		resource := resources[id]
		stat, tag := "on", "ON"
		if isOff("redis", resource.Status) {
			stat, tag, off = "off", "OFF", off+1
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "redis", id)
		if err != nil {
			return 0, 0, fmt.Errorf("read Redis custom %s: %w", id, err)
		}
		if ip == "" {
			ip = resource.Domain
		}
		if port == 0 {
			port = resource.Port
		}
		meta := map[string]string{"iid": id, "name": resource.Name, "region": regionName(vendor, region), "group": resource.Group, "instance": fmt.Sprintf("%s:%d", ip, port), "account": account, "itype": resource.IType, "vendor": vendorName(vendor), "mem": resource.Mem, "ver": resource.Ver, "ip": resource.IP, "exp": resource.Exp, "stat": stat}
		if err := s.Store.Register(ctx, store.ServiceRegistration{ID: id, Name: name, Address: ip, Port: port, Tags: []string{tag, resource.IType, resource.Ver, region}, Meta: meta, Check: tcpCheck(ip, port)}); err != nil {
			return 0, 0, fmt.Errorf("register Redis %s: %w", id, err)
		}
	}
	return on, off, nil
}

func tcpCheck(ip string, port int) *store.ServiceCheck {
	return &store.ServiceCheck{TCP: fmt.Sprintf("%s:%d", ip, port), Interval: "60s"}
}
func ids[T any](values map[string]T) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for id := range values {
		out[id] = struct{}{}
	}
	return out
}
func sorted(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func asciiAlphaNum(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
