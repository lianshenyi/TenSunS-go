// Package jumpserver synchronizes Consul ECS services to JumpServer assets.
package jumpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Syncer is the typed job entry point for JumpServer synchronization.
type Syncer interface {
	Run(ctx context.Context, vendor, account string) error
}

// Service is a conservative JumpServer V2/V3 asset writer. It only reads
// Consul ECS records and creates or updates assets; it never executes commands
// on guests or exposes credentials in logs.
type Service struct {
	store  store.Store
	cipher *cryptox.Cipher
	http   *http.Client
}

type ecsAccess struct {
	Protocols []string
	AdminUser string
}

type ecsInfo map[string]ecsAccess

type existingAsset struct {
	ID        string          `json:"id"`
	IP        string          `json:"ip"`
	Address   string          `json:"address"`
	Protocols json.RawMessage `json:"protocols"`
}

func New(st store.Store, cipher *cryptox.Cipher, client *http.Client) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	return &Service{store: st, cipher: cipher, http: client}
}

func (s *Service) Run(ctx context.Context, vendor, account string) error {
	var jms struct {
		URL   string `json:"url"`
		Token string `json:"token"`
		Ver   string `json:"ver"`
	}
	found, err := s.store.GetValue(ctx, "ConsulManager/jms/jms_info", &jms)
	if err != nil {
		return err
	}
	if !found || jms.URL == "" || jms.Token == "" {
		return fmt.Errorf("JumpServer configuration is incomplete")
	}
	token, err := s.cipher.Decrypt(jms.Token)
	if err != nil {
		return fmt.Errorf("decrypt JumpServer token: %w", err)
	}
	var node struct {
		NodeID string `json:"node_id"`
	}
	found, err = s.store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/node_id", &node)
	if err != nil {
		return err
	}
	if !found || node.NodeID == "" {
		return fmt.Errorf("JumpServer node configuration is missing")
	}
	ecsInfo, err := s.ecsInfo(ctx, vendor, account)
	if err != nil {
		return err
	}
	services, err := s.store.Services(ctx, fmt.Sprintf("Service == %q", vendor+"_"+account+"_ecs"))
	if err != nil {
		return err
	}
	base := strings.TrimRight(jms.URL, "/")
	var existing []existingAsset
	if err := s.request(ctx, token, http.MethodGet, base+"/api/v1/assets/assets/?node="+node.NodeID, nil, &existing); err != nil {
		return err
	}
	byAddress := map[string]string{}
	for _, host := range existing {
		if host.IP != "" {
			byAddress[host.IP] = host.ID
		} else {
			byAddress[host.Address] = host.ID
		}
	}
	for _, service := range services {
		if service.Address == "" {
			continue
		}
		access, err := ecsInfo.forOS(service.Meta["os"])
		if err != nil {
			return fmt.Errorf("JumpServer access configuration for %s: %w", service.ID, err)
		}
		payload := map[string]any{"address": service.Address, "name": service.Meta["name"], "is_active": true, "domain": "", "nodes": []string{node.NodeID}, "comment": fmt.Sprintf("%s %s %s %s", service.Meta["vendor"], account, service.Meta["region"], service.ID)}
		if strings.EqualFold(jms.Ver, "V3") {
			protocols, err := v3Protocols(access.Protocols, existingProtocols(existing, service.Address))
			if err != nil {
				return fmt.Errorf("JumpServer protocols for %s: %w", service.ID, err)
			}
			payload["platform"] = v3Platform(service.Meta["os"])
			payload["protocols"] = protocols
			payload["accounts"] = []map[string]string{{"template": strings.TrimSpace(access.AdminUser)}}
		} else {
			payload["ip"] = service.Address
			payload["hostname"] = service.Meta["name"]
			payload["platform"] = v2Platform(service.Meta["os"])
			payload["protocols"] = access.Protocols
			payload["admin_user"] = strings.TrimSpace(access.AdminUser)
		}
		path, method := base+"/api/v1/assets/assets/", http.MethodPost
		if strings.EqualFold(jms.Ver, "V3") {
			path = base + "/api/v1/assets/hosts/"
		}
		if id := byAddress[service.Address]; id != "" {
			// Hosts is a V3 creation endpoint only. Both API versions update
			// existing assets through the shared assets endpoint.
			path = base + "/api/v1/assets/assets/" + id + "/"
			method = http.MethodPut
		}
		if err := s.request(ctx, token, method, path, payload, nil); err != nil {
			return fmt.Errorf("sync JumpServer asset %s: %w", service.ID, err)
		}
	}
	return s.store.PutKV(ctx, "ConsulManager/record/jms/"+vendor+"/"+account, map[string]any{"count": len(services), "update": time.Now().Format("0102/15:04"), "status": 20000, "msg": fmt.Sprintf("同步资源成功！总数：%d", len(services))})
}

func (s *Service) ecsInfo(ctx context.Context, vendor, account string) (ecsInfo, error) {
	var accountInfo map[string]json.RawMessage
	found, err := s.store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/ecs_info", &accountInfo)
	if err != nil {
		return nil, err
	}
	if !found || len(accountInfo) == 0 {
		if found, err = s.store.GetValue(ctx, "ConsulManager/jms/ecs_info", &accountInfo); err != nil {
			return nil, err
		} else if !found || len(accountInfo) == 0 {
			return nil, fmt.Errorf("JumpServer ecs_info configuration is missing")
		}
	}
	access := make(ecsInfo, len(accountInfo))
	for osType, raw := range accountInfo {
		var value []json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil || len(value) != 2 {
			return nil, fmt.Errorf("invalid ecs_info.%s", osType)
		}
		var protocols []string
		var adminUser string
		if err := json.Unmarshal(value[0], &protocols); err != nil || len(protocols) == 0 {
			return nil, fmt.Errorf("invalid ecs_info.%s protocols", osType)
		}
		if err := json.Unmarshal(value[1], &adminUser); err != nil || strings.TrimSpace(adminUser) == "" {
			return nil, fmt.Errorf("invalid ecs_info.%s admin user", osType)
		}
		access[strings.ToLower(osType)] = ecsAccess{Protocols: protocols, AdminUser: adminUser}
	}
	return access, nil
}

func (info ecsInfo) forOS(osType string) (ecsAccess, error) {
	access, ok := info[strings.ToLower(osType)]
	if !ok {
		return ecsAccess{}, fmt.Errorf("no configuration for OS %q", osType)
	}
	return access, nil
}

func existingProtocols(assets []existingAsset, address string) json.RawMessage {
	for _, asset := range assets {
		if asset.IP == address || asset.Address == address {
			return asset.Protocols
		}
	}
	return nil
}

func v3Protocols(configured []string, existing json.RawMessage) ([]map[string]any, error) {
	if len(configured) == 0 {
		return nil, fmt.Errorf("protocols are required")
	}
	name, port, err := parseProtocol(configured[0])
	if err != nil {
		return nil, err
	}
	var current []struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	if len(existing) > 0 && json.Unmarshal(existing, &current) == nil {
		for _, protocol := range current {
			if protocol.Name == "ssh" && protocol.Port != port {
				port = protocol.Port
				break
			}
		}
	}
	return []map[string]any{{"name": name, "port": port}, {"name": "sftp", "port": port}}, nil
}

func parseProtocol(value string) (string, int, error) {
	name, portText, ok := strings.Cut(value, "/")
	if !ok || strings.TrimSpace(name) == "" {
		return "", 0, fmt.Errorf("invalid protocol %q", value)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid protocol port in %q", value)
	}
	return name, port, nil
}

func v3Platform(osType string) string {
	if strings.EqualFold(osType, "windows") {
		return "5"
	}
	return "1"
}

func v2Platform(osType string) string {
	if strings.EqualFold(osType, "windows") {
		return "Windows"
	}
	return "Linux"
}

func (s *Service) request(ctx context.Context, token, method, endpoint string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("JumpServer %s %s returned %s", method, endpoint, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

var _ Syncer = (*Service)(nil)
