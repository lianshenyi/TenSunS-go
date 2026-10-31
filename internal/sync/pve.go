package sync

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/pve"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// PVE implements the existing cloud-job Service contract for vendor=pve.
// Other vendors deliberately remain unsupported until their adapters exist.
type PVE struct {
	store  store.Store
	cipher *cryptox.Cipher
	http   *http.Client
	newPVE func(pve.Credentials, pve.IPSelection, *http.Client) (*pve.Client, error)
}

// NewPVE constructs the PVE job service. The HTTP client may be nil.
func NewPVE(st store.Store, cipher *cryptox.Cipher, client *http.Client) *PVE {
	return &PVE{store: st, cipher: cipher, http: client, newPVE: pve.NewClient}
}

type pveAccount struct {
	APIBaseURL  string          `json:"api_base_url"`
	TokenID     string          `json:"token_id"`
	TokenSecret string          `json:"token_secret"`
	TLSVerify   *bool           `json:"tls_verify"`
	Cluster     string          `json:"cluster"`
	IPSelection pve.IPSelection `json:"ip_selection"`
}

func (s *PVE) Group(ctx context.Context, vendor, account string) error {
	if vendor != "pve" {
		return unsupportedVendor(vendor)
	}
	cfg, err := s.account(ctx, account)
	if err != nil {
		return err
	}
	cluster := cfg.Cluster
	if cluster == "" {
		cluster = "pve"
	}
	return s.store.PutKV(ctx, "ConsulManager/assets/pve/group/"+account, map[string]string{cluster: cluster})
}

func (s *PVE) ECS(ctx context.Context, vendor, account, region string, _ bool) error {
	if vendor != "pve" {
		return unsupportedVendor(vendor)
	}
	cfg, err := s.account(ctx, account)
	if err != nil {
		return err
	}
	if cfg.Cluster != "" && region != cfg.Cluster {
		return fmt.Errorf("PVE account %q is configured for cluster %q, not region %q", account, cfg.Cluster, region)
	}
	secret, err := s.cipher.Decrypt(cfg.TokenSecret)
	if err != nil {
		return fmt.Errorf("decrypt PVE token secret: %w", err)
	}
	client, err := s.newPVE(pve.Credentials{APIBaseURL: cfg.APIBaseURL, TokenID: cfg.TokenID, TokenSecret: secret, TLSVerify: cfg.TLSVerify}, cfg.IPSelection, s.http)
	if err != nil {
		return err
	}
	vms, err := client.ListQEMUVMs(ctx)
	if err != nil {
		return fmt.Errorf("list PVE QEMU VMs: %w", err)
	}
	if err := s.reconcileECS(ctx, account, region, vms); err != nil {
		return err
	}
	return nil
}

func (s *PVE) RDS(context.Context, string, string, string) error {
	return fmt.Errorf("PVE does not provide RDS resources")
}
func (s *PVE) Redis(context.Context, string, string, string) error {
	return fmt.Errorf("PVE does not provide Redis resources")
}

func (s *PVE) account(ctx context.Context, account string) (pveAccount, error) {
	if s.store == nil || s.cipher == nil {
		return pveAccount{}, fmt.Errorf("PVE sync dependencies are not configured")
	}
	var cfg pveAccount
	found, err := s.store.GetValue(ctx, "ConsulManager/assets/pve/api/"+account, &cfg)
	if err != nil {
		return pveAccount{}, err
	}
	if !found {
		return pveAccount{}, fmt.Errorf("PVE account %q is not configured", account)
	}
	if cfg.APIBaseURL == "" || cfg.TokenID == "" || cfg.TokenSecret == "" {
		return pveAccount{}, fmt.Errorf("PVE account %q has incomplete credentials", account)
	}
	return cfg, nil
}

func (s *PVE) reconcileECS(ctx context.Context, account, region string, vms []pve.VM) error {
	serviceName := "pve_" + account + "_ecs"
	services, err := s.store.Services(ctx, fmt.Sprintf("Service == %q and %q in Tags and Meta.account == %q", serviceName, region, account))
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(vms))
	for _, vm := range vms {
		known[vm.ID] = struct{}{}
	}
	for id := range services {
		if _, ok := known[id]; !ok {
			if err := s.store.Deregister(ctx, id); err != nil {
				return fmt.Errorf("deregister removed PVE VM %s: %w", id, err)
			}
		}
	}

	on, off, unavailable := 0, 0, 0
	for _, vm := range vms {
		if strings.EqualFold(vm.Status, "running") {
			on++
		} else {
			off++
		}
		ip, port, err := s.overrides(ctx, vm.ID, vm.IP, vm.OSType)
		if err != nil {
			return err
		}
		if ip == "" {
			unavailable++
			continue // Preserve any prior service while Guest Agent is temporarily unavailable.
		}
		stat, tag := "off", "OFF"
		if strings.EqualFold(vm.Status, "running") {
			stat, tag = "on", "ON"
		}
		meta := map[string]string{
			"iid": vm.ID, "name": vm.Name, "region": region, "group": vm.Node,
			"instance": fmt.Sprintf("%s:%d", ip, port), "account": account,
			"vendor": config.VendorDisplay("pve"), "os": vm.OSType,
			"cpu": strconv.Itoa(vm.CPU) + "核", "mem": formatMemory(vm.Memory),
			"exp": "-", "stat": stat, "node": vm.Node, "vmid": strconv.Itoa(vm.VMID),
		}
		registration := store.ServiceRegistration{ID: vm.ID, Name: serviceName, Address: ip, Port: port, Tags: []string{tag, vm.OSType, region}, Meta: meta, Check: &store.ServiceCheck{TCP: fmt.Sprintf("%s:%d", ip, port), Interval: "60s"}}
		if err := s.store.Register(ctx, registration); err != nil {
			return fmt.Errorf("register PVE VM %s: %w", vm.ID, err)
		}
	}
	msg := fmt.Sprintf("PVE ECS同步成功！总数：%d，开机：%d，关机：%d", len(vms), on, off)
	if unavailable > 0 {
		msg += fmt.Sprintf("，IP不可用：%d", unavailable)
	}
	return s.store.PutKV(ctx, "ConsulManager/record/jobs/pve/"+account+"/ecs/"+region, map[string]any{"count": len(vms), "update": time.Now().Format("01.02/15:04"), "status": 20000, "on": on, "off": off, "ip_unavailable": unavailable, "msg": msg})
}

func (s *PVE) overrides(ctx context.Context, id, fallback, osType string) (string, int, error) {
	var custom struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	}
	_, err := s.store.GetValue(ctx, "ConsulManager/assets/sync_ecs_custom/"+id, &custom)
	if err != nil {
		return "", 0, err
	}
	ip := fallback
	if custom.IP != "" {
		ip = custom.IP
	}
	port := custom.Port
	if port == 0 {
		port = 9100
		if osType == "windows" {
			port = 9182
		}
	}
	return ip, port, nil
}

func unsupportedVendor(vendor string) error {
	return fmt.Errorf("cloud provider %q is not registered", vendor)
}

func formatMemory(bytes uint64) string {
	return strconv.FormatUint(bytes/(1024*1024*1024), 10) + "GB"
}

var _ Service = (*PVE)(nil)
