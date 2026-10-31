// Package jumpserver synchronizes Consul ECS services to JumpServer assets.
package jumpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	services, err := s.store.Services(ctx, fmt.Sprintf("Service == %q", vendor+"_"+account+"_ecs"))
	if err != nil {
		return err
	}
	base := strings.TrimRight(jms.URL, "/")
	var existing []struct {
		ID      string `json:"id"`
		IP      string `json:"ip"`
		Address string `json:"address"`
	}
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
		payload := map[string]any{"address": service.Address, "name": service.Meta["name"], "is_active": true, "nodes": []string{node.NodeID}, "comment": fmt.Sprintf("%s %s %s %s", service.Meta["vendor"], account, service.Meta["region"], service.ID)}
		if strings.EqualFold(jms.Ver, "V3") {
			payload["platform"] = "1"
		} else {
			payload["ip"] = service.Address
			payload["hostname"] = service.Meta["name"]
			payload["platform"] = "Linux"
		}
		path, method := base+"/api/v1/assets/assets/", http.MethodPost
		if strings.EqualFold(jms.Ver, "V3") {
			path = base + "/api/v1/assets/hosts/"
		}
		if id := byAddress[service.Address]; id != "" {
			path += id + "/"
			method = http.MethodPut
		}
		if err := s.request(ctx, token, method, path, payload, nil); err != nil {
			return fmt.Errorf("sync JumpServer asset %s: %w", service.ID, err)
		}
	}
	return s.store.PutKV(ctx, "ConsulManager/record/jms/"+vendor+"/"+account, map[string]any{"count": len(services), "update": time.Now().Format("0102/15:04"), "status": 20000, "msg": fmt.Sprintf("同步资源成功！总数：%d", len(services))})
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
