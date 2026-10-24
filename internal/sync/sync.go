// Package sync contains the CloudProvider -> Consul persistence pipeline.
package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/config"
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

// ConsulService persists normalized provider data using the existing Python
// compatible Consul key and service shapes.
type ConsulService struct {
	Store     store.Store
	Providers *cloud.Registry
	Now       func() time.Time
}

func (s *ConsulService) Group(ctx context.Context, vendor, account string) error {
	p, err := s.provider(vendor)
	if err != nil {
		return s.fail(ctx, vendor, account, "group", "", err)
	}
	var groups map[string]string
	err = retry(ctx, func() error { var callErr error; groups, callErr = p.Group(ctx, account); return callErr })
	if err == nil {
		err = s.Store.PutKV(ctx, groupPath(vendor, account), groups)
	}
	if err != nil {
		return s.fail(ctx, vendor, account, "group", "", err)
	}
	return s.record(ctx, vendor, account, "group", "", len(groups), 0, 0, "同步资源组成功！总数："+strconv.Itoa(len(groups)), 20000)
}

func (s *ConsulService) ECS(ctx context.Context, vendor, account, region string, externalIP bool) error {
	p, err := s.provider(vendor)
	if err != nil {
		return s.fail(ctx, vendor, account, "ecs", region, err)
	}
	var resources map[string]model.ECS
	err = retry(ctx, func() error {
		var callErr error
		resources, callErr = p.ECS(ctx, account, region, externalIP)
		return callErr
	})
	if err == nil {
		var on, off int
		on, off, err = s.writeECS(ctx, vendor, account, region, resources)
		if err == nil {
			return s.record(ctx, vendor, account, "ecs", region, len(resources), on, off, fmt.Sprintf("ECS同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off), 20000)
		}
	}
	return s.fail(ctx, vendor, account, "ecs", region, err)
}

func (s *ConsulService) RDS(ctx context.Context, vendor, account, region string) error {
	p, err := s.provider(vendor)
	if err != nil {
		return s.fail(ctx, vendor, account, "rds", region, err)
	}
	var resources map[string]model.RDS
	err = retry(ctx, func() error { var callErr error; resources, callErr = p.RDS(ctx, account, region); return callErr })
	if err == nil {
		var on, off int
		on, off, err = s.writeRDS(ctx, vendor, account, region, resources)
		if err == nil {
			return s.record(ctx, vendor, account, "rds", region, len(resources), on, off, fmt.Sprintf("rds同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off), 20000)
		}
	}
	return s.fail(ctx, vendor, account, "rds", region, err)
}

func (s *ConsulService) Redis(ctx context.Context, vendor, account, region string) error {
	p, err := s.provider(vendor)
	if err != nil {
		return s.fail(ctx, vendor, account, "redis", region, err)
	}
	var resources map[string]model.Redis
	err = retry(ctx, func() error { var callErr error; resources, callErr = p.Redis(ctx, account, region); return callErr })
	if err == nil {
		var on, off int
		on, off, err = s.writeRedis(ctx, vendor, account, region, resources)
		if err == nil {
			return s.record(ctx, vendor, account, "redis", region, len(resources), on, off, fmt.Sprintf("redis同步成功！总数：%d，开机：%d，关机：%d", len(resources), on, off), 20000)
		}
	}
	return s.fail(ctx, vendor, account, "redis", region, err)
}

func (s *ConsulService) provider(vendor string) (cloud.Provider, error) {
	if s.Store == nil || s.Providers == nil {
		return nil, errors.New("cloud sync dependencies are not configured")
	}
	return s.Providers.Get(vendor)
}
func (s *ConsulService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func groupPath(vendor, account string) string {
	return "ConsulManager/assets/" + vendor + "/group/" + account
}
func recordPath(vendor, account, kind, region string) string {
	p := "ConsulManager/record/jobs/" + vendor + "/" + account + "/" + kind
	if region != "" {
		p += "/" + region
	}
	return p
}
func (s *ConsulService) record(ctx context.Context, vendor, account, kind, region string, count, on, off int, msg string, status int) error {
	v := map[string]any{"count": count, "update": s.now().Format("01.02/15:04"), "status": status, "msg": msg}
	if kind != "group" {
		v["on"], v["off"] = on, off
	}
	return s.Store.PutKV(ctx, recordPath(vendor, account, kind, region), v)
}
func (s *ConsulService) fail(ctx context.Context, vendor, account, kind, region string, cause error) error {
	if cause == nil {
		cause = errors.New("cloud sync failed")
	}
	path := recordPath(vendor, account, kind, region)
	var old map[string]any
	found, err := s.Store.GetValue(ctx, path, &old)
	if err != nil {
		return fmt.Errorf("%w (read failure record: %v)", cause, err)
	}
	if !found || old == nil {
		old = map[string]any{"count": "无"}
	}
	old["update"], old["status"], old["msg"] = "失败"+s.now().Format("01.02/15:04"), 50000, cause.Error()
	if err := s.Store.PutKV(ctx, path, old); err != nil {
		return fmt.Errorf("%w (write failure record: %v)", cause, err)
	}
	return cause
}

func retry(ctx context.Context, call func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = call(); err == nil || !retryable(err) {
			return err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(10*(attempt+1)) * time.Millisecond):
			}
		}
	}
	return err
}
func retryable(err error) bool {
	return errors.Is(err, cloud.ErrRateLimited) || errors.Is(err, cloud.ErrTemporary) || errors.Is(err, context.DeadlineExceeded)
}

func (s *ConsulService) existing(ctx context.Context, vendor, account, kind, region string) (map[string]store.Service, error) {
	return s.Store.Services(ctx, fmt.Sprintf(`Service == %q and %q in Tags and Meta.account == %q`, vendor+"_"+account+"_"+kind, region, account))
}
func (s *ConsulService) reconcile(ctx context.Context, vendor, account, kind, region string, ids map[string]struct{}, regs []store.ServiceRegistration) error {
	old, err := s.existing(ctx, vendor, account, kind, region)
	if err != nil {
		return err
	}
	for id := range old {
		if _, ok := ids[id]; !ok {
			if err := s.Store.Deregister(ctx, id); err != nil {
				return fmt.Errorf("deregister stale %s: %w", id, err)
			}
		}
	}
	for _, reg := range regs {
		if err := s.Store.Register(ctx, reg); err != nil {
			return fmt.Errorf("register %s: %w", reg.ID, err)
		}
	}
	return nil
}
func custom(ctx context.Context, st store.Store, kind, id string) (string, int, error) {
	var v struct {
		IP   string `json:"ip"`
		Port *int   `json:"port"`
	}
	found, err := st.GetValue(ctx, "ConsulManager/assets/sync_"+kind+"_custom/"+id, &v)
	if err != nil || !found {
		return "", 0, err
	}
	if v.Port == nil {
		return v.IP, 0, nil
	}
	return v.IP, *v.Port, nil
}
func state(status string, offValues ...string) (string, string) {
	for _, value := range offValues {
		if status == value {
			return "OFF", "off"
		}
	}
	return "ON", "on"
}
func baseMeta(vendor, account, id, name, region, group, instance string) map[string]string {
	return map[string]string{"iid": id, "name": name, "region": config.RegionDisplay(vendor, region), "group": group, "instance": instance, "account": account, "vendor": config.VendorDisplay(vendor)}
}

func (s *ConsulService) writeECS(ctx context.Context, vendor, account, region string, data map[string]model.ECS) (int, int, error) {
	ids, regs := map[string]struct{}{}, make([]store.ServiceRegistration, 0, len(data))
	on, off := 0, 0
	for id, v := range data {
		tag, stat := state(v.Status, "SHUTOFF", "Stopped", "STOPPED")
		if stat == "off" {
			off++
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "ecs", id)
		if err != nil {
			return 0, 0, err
		}
		if ip == "" {
			ip = v.IP
		}
		if port == 0 {
			if v.OSType == "windows" {
				port = 9182
			} else {
				port = 9100
			}
		}
		meta := baseMeta(vendor, account, id, v.Name, region, v.Group, ip+":"+strconv.Itoa(port))
		meta["os"], meta["cpu"], meta["mem"], meta["exp"], meta["stat"] = strings.ToLower(v.OSType), v.CPU, v.Mem, v.Exp, stat
		for k, val := range v.Tags {
			if alphanumeric(k) {
				meta[k] = val
			}
		}
		ids[id] = struct{}{}
		regs = append(regs, store.ServiceRegistration{ID: id, Name: vendor + "_" + account + "_ecs", Address: ip, Port: port, Tags: []string{tag, strings.ToLower(v.OSType), region}, Meta: meta, Check: &store.ServiceCheck{TCP: ip + ":" + strconv.Itoa(port), Interval: "60s"}})
	}
	return on, off, s.reconcile(ctx, vendor, account, "ecs", region, ids, regs)
}
func (s *ConsulService) writeRDS(ctx context.Context, vendor, account, region string, data map[string]model.RDS) (int, int, error) {
	ids, regs := map[string]struct{}{}, make([]store.ServiceRegistration, 0, len(data))
	on, off := 0, 0
	for id, v := range data {
		tag, stat := state(v.Status, "SHUTDOWN", "非运行中")
		if stat == "off" {
			off++
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "rds", id)
		if err != nil {
			return 0, 0, err
		}
		if ip == "" {
			ip = v.IP
		}
		if port == 0 {
			port = v.Port
		}
		meta := baseMeta(vendor, account, id, v.Name, region, v.Group, ip+":"+strconv.Itoa(port))
		meta["itype"], meta["disk"], meta["cpu"], meta["mem"], meta["ver"], meta["domain"], meta["exp"], meta["stat"], meta["team"] = v.IType, v.Disk, v.CPU, v.Mem, v.Ver, v.Domain, v.Exp, stat, v.Team
		if meta["team"] == "" {
			meta["team"] = "无"
		}
		ids[id] = struct{}{}
		regs = append(regs, store.ServiceRegistration{ID: id, Name: vendor + "_" + account + "_rds", Address: ip, Port: port, Tags: []string{tag, v.IType, v.Ver, region}, Meta: meta, Check: &store.ServiceCheck{TCP: ip + ":" + strconv.Itoa(port), Interval: "60s"}})
	}
	return on, off, s.reconcile(ctx, vendor, account, "rds", region, ids, regs)
}
func (s *ConsulService) writeRedis(ctx context.Context, vendor, account, region string, data map[string]model.Redis) (int, int, error) {
	ids, regs := map[string]struct{}{}, make([]store.ServiceRegistration, 0, len(data))
	on, off := 0, 0
	for id, v := range data {
		tag, stat := state(v.Status, "SHUTDOWN", "Unavailable", "Inactive", "Released", "非运行中")
		if stat == "off" {
			off++
		} else {
			on++
		}
		ip, port, err := custom(ctx, s.Store, "redis", id)
		if err != nil {
			return 0, 0, err
		}
		if ip == "" {
			ip = v.Domain
		}
		if port == 0 {
			port = v.Port
		}
		meta := baseMeta(vendor, account, id, v.Name, region, v.Group, ip+":"+strconv.Itoa(port))
		meta["itype"], meta["mem"], meta["ver"], meta["ip"], meta["exp"], meta["stat"] = v.IType, v.Mem, v.Ver, v.IP, v.Exp, stat
		ids[id] = struct{}{}
		regs = append(regs, store.ServiceRegistration{ID: id, Name: vendor + "_" + account + "_redis", Address: ip, Port: port, Tags: []string{tag, v.IType, v.Ver, region}, Meta: meta, Check: &store.ServiceCheck{TCP: ip + ":" + strconv.Itoa(port), Interval: "60s"}})
	}
	return on, off, s.reconcile(ctx, vendor, account, "redis", region, ids, regs)
}
func alphanumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

var _ Service = (*ConsulService)(nil)
