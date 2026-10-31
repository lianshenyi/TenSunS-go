// Package jumpserver synchronizes Consul ECS services to JumpServer assets.
package jumpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
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

// Service aligns with flask-consul/units/jms/sync_jms.py: it builds one
// JumpServer node per Consul resource group under the configured node,
// deletes assets that disappeared from the cloud, creates or updates the
// rest, cleans up duplicate IPs and empty group nodes, and records the
// JMS-side asset count.
type Service struct {
	store  store.Store
	cipher *cryptox.Cipher
	http   *http.Client
	logger *slog.Logger
}

type ecsAccess struct {
	Protocols []string
	AdminUser string
}

// ecsInfo maps an OS name to its configured access.
type ecsInfo map[string]ecsAccess

// customRules maps a hostname keyword to per-OS access overrides.
type customRules map[string]ecsInfo

// jmsNode is the subset of a JumpServer node object used for the children
// listing (value is the node name, id the node UUID).
type jmsNode struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// jmsAsset is the shared V2 asset / V3 host shape from the list endpoint.
type jmsAsset struct {
	ID           string          `json:"id"`
	IP           string          `json:"ip"`
	Address      string          `json:"address"`
	Hostname     string          `json:"hostname"`
	Name         string          `json:"name"`
	Protocols    json.RawMessage `json:"protocols"`
	Nodes        []string        `json:"nodes"`
	NodesDisplay []string        `json:"nodes_display"`
}

// jmsTreeNode is one entry of the children tree endpoint.
type jmsTreeNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Meta struct {
		Node *struct {
			ID string `json:"id"`
		} `json:"node"`
		Data *struct {
			ID string `json:"id"`
		} `json:"data"`
	} `json:"meta"`
}

// assetIP prefers the V2 "ip" field over the V3 "address".
func (a jmsAsset) assetIP() string {
	if a.IP != "" {
		return a.IP
	}
	return a.Address
}

// assetName prefers "hostname" (V2) over "name" (V3).
func (a jmsAsset) assetName() string {
	if a.Hostname != "" {
		return a.Hostname
	}
	if a.Name != "" {
		return a.Name
	}
	return "NAMENOTFOUND"
}

// nodePath returns the first node path, mirroring the baseline's
// nodes_display[0] with a nodes[0] fallback.
func (a jmsAsset) nodePath() string {
	if len(a.NodesDisplay) > 0 {
		return a.NodesDisplay[0]
	}
	if len(a.Nodes) > 0 {
		return a.Nodes[0]
	}
	return ""
}

// groupName extracts the last node path segment. Assets under 未分组 report
// 无 so an ECS group of 无 matches them, like the baseline.
func (a jmsAsset) groupName() string {
	path := a.nodePath()
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	if path == "未分组" {
		return "无"
	}
	return path
}

func New(st store.Store, cipher *cryptox.Cipher, client *http.Client) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	return &Service{store: st, cipher: cipher, http: client, logger: slog.Default()}
}

// Run mirrors sync_jms.run: ensure group nodes, reconcile assets, clean
// duplicate IPs and empty nodes, then record the JMS-side asset count. Any
// failure also writes a status=50000 record so the first-sync and list
// contracts have a visible failure marker.
func (s *Service) Run(ctx context.Context, vendor, account string) error {
	now := time.Now().Format("0102/15:04")
	recordPath := "ConsulManager/record/jms/" + vendor + "/" + account
	fail := func(detail string, cause error) error {
		return s.recordFailure(ctx, recordPath, now, detail, cause)
	}

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
		return fail("未知ERROR", fmt.Errorf("JumpServer configuration is incomplete"))
	}
	token, err := s.cipher.Decrypt(jms.Token)
	if err != nil {
		return fail("未知ERROR", fmt.Errorf("decrypt JumpServer token: %w", err))
	}
	ver := jms.Ver
	if ver == "" {
		ver = "V2"
	}
	var node struct {
		NodeID string `json:"node_id"`
	}
	found, err = s.store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/node_id", &node)
	if err != nil {
		return err
	}
	if !found || node.NodeID == "" {
		return fail("未知ERROR", fmt.Errorf("JumpServer node configuration is missing"))
	}
	info, err := s.ecsInfo(ctx, vendor, account)
	if err != nil {
		return fail("未知ERROR", err)
	}
	custom, err := s.customInfo(ctx, vendor, account)
	if err != nil {
		return fail("未知ERROR", err)
	}
	base := strings.TrimRight(jms.URL, "/")

	nodeMap, detail, err := s.ensureNodes(ctx, token, base, node.NodeID, vendor, account)
	if err != nil {
		return fail(detail, err)
	}
	services, err := s.store.Services(ctx, fmt.Sprintf("Service == %q", vendor+"_"+account+"_ecs"))
	if err != nil {
		return fail("未知ERROR", err)
	}
	assets, detail, err := s.listAssets(ctx, token, base, node.NodeID)
	if err != nil {
		return fail(detail, err)
	}
	s.deleteStaleAssets(ctx, token, base, assets, services)
	if err := s.upsertAssets(ctx, token, base, ver, nodeMap, assets, services, info, custom, account); err != nil {
		return fail("未知ERROR", err)
	}
	if detail, err := s.deleteDuplicateIPs(ctx, token, base, node.NodeID, services); err != nil {
		return fail(detail, err)
	}
	if detail, err := s.deleteEmptyNodes(ctx, token, base, node.NodeID); err != nil {
		return fail(detail, err)
	}
	count, detail, err := s.assetCount(ctx, token, base, node.NodeID)
	if err != nil {
		return fail(detail, err)
	}
	return s.store.PutKV(ctx, recordPath, map[string]any{
		"count":  count,
		"update": now,
		"status": 20000,
		"msg":    fmt.Sprintf("同步资源成功！总数：%d", count),
	})
}

// recordFailure writes the status=50000 record the baseline produces when a
// sync cannot complete. detail must never carry credentials; JumpServer error
// bodies are used verbatim like the baseline.
func (s *Service) recordFailure(ctx context.Context, recordPath, now, detail string, cause error) error {
	if putErr := s.store.PutKV(ctx, recordPath, map[string]any{
		"count": "失败", "update": now, "status": 50000, "msg": "同步资源失败！" + detail,
	}); putErr != nil {
		return fmt.Errorf("%v (record the failure: %w)", cause, putErr)
	}
	return cause
}

// ensureNodes mirrors create_node: read the Consul group KV, append the
// 未分组 pseudo group, create any missing child node, and return the
// value→id mapping of the children after creation. Unlike the baseline, a
// failed children listing aborts immediately instead of half-executing.
func (s *Service) ensureNodes(ctx context.Context, token, base, nodeID, vendor, account string) (map[string]string, string, error) {
	children, detail, err := s.listChildren(ctx, token, base, nodeID)
	if err != nil {
		return nil, detail, err
	}
	existing := make(map[string]bool, len(children))
	for _, child := range children {
		existing[child.Value] = true
	}
	groups := map[string]bool{"未分组": true}
	var groupKV map[string]string
	found, err := s.store.GetValue(ctx, "ConsulManager/assets/"+vendor+"/group/"+account, &groupKV)
	if err != nil {
		return nil, "未知ERROR", err
	}
	if found {
		for _, name := range groupKV {
			groups[name] = true
		}
	}
	for name := range groups {
		if existing[name] {
			continue
		}
		status, err := s.write(ctx, token, http.MethodPost, base+"/api/v1/assets/nodes/"+nodeID+"/children/", map[string]any{"value": name})
		if err != nil {
			s.logger.Warn("JumpServer node create failed; the URL must be a direct address", "node", name, "error", err)
			continue
		}
		if status != http.StatusCreated {
			s.logger.Warn("JumpServer node create returned status", "node", name, "status", status)
		}
	}
	children, detail, err = s.listChildren(ctx, token, base, nodeID)
	if err != nil {
		return nil, detail, err
	}
	nodeMap := make(map[string]string, len(children))
	for _, child := range children {
		nodeMap[child.Value] = child.ID
	}
	return nodeMap, "", nil
}

// deleteStaleAssets removes every asset under the configured node whose IP is
// no longer in the Consul ECS list. Assets without any IP are removed too.
func (s *Service) deleteStaleAssets(ctx context.Context, token, base string, assets []jmsAsset, services map[string]store.Service) {
	ecsIPs := make(map[string]bool, len(services))
	for _, service := range services {
		if service.Address != "" {
			ecsIPs[service.Address] = true
		}
	}
	for _, asset := range assets {
		if ip := asset.assetIP(); ip != "" && ecsIPs[ip] {
			continue
		}
		s.deleteAsset(ctx, token, base, asset.ID)
	}
}

// deleteAsset deletes one asset through the shared assets endpoint used by
// both API versions. 404 means the asset is already gone; other failures
// only warn, matching the baseline's log-only delete handling.
func (s *Service) deleteAsset(ctx context.Context, token, base, id string) {
	status, err := s.write(ctx, token, http.MethodDelete, base+"/api/v1/assets/assets/"+id+"/", nil)
	if err != nil {
		s.logger.Warn("JumpServer asset delete failed", "asset", id, "error", err)
		return
	}
	if status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound {
		s.logger.Warn("JumpServer asset delete returned status", "asset", id, "status", status)
	}
}

// upsertAssets creates missing assets and updates assets whose name or group
// changed, per the baseline contract: protocol, port, admin-user and comment
// drift alone never triggers an update. Per-asset failures only warn, like
// the baseline's log-and-continue loop.
func (s *Service) upsertAssets(ctx context.Context, token, base, ver string, nodeMap map[string]string, assets []jmsAsset, services map[string]store.Service, info ecsInfo, custom customRules, account string) error {
	byIP := make(map[string]jmsAsset, len(assets))
	for _, asset := range assets {
		if ip := asset.assetIP(); ip != "" {
			byIP[ip] = asset
		}
	}
	ungrouped, ok := nodeMap["未分组"]
	if !ok {
		return fmt.Errorf("JumpServer 未分组 node was not created")
	}
	for id, service := range services {
		if service.Address == "" {
			continue
		}
		access, err := info.forOS(service.Meta["os"])
		if err != nil {
			return fmt.Errorf("JumpServer access configuration for %s: %w", id, err)
		}
		if override, ok := custom.override(service.Meta["name"], service.Meta["os"]); ok {
			access = override
		}
		group := service.Meta["group"]
		nodeID := nodeMap[group]
		if nodeID == "" {
			nodeID = ungrouped
		}
		payload := map[string]any{
			"is_active": true,
			"domain":    "",
			"nodes":     []string{nodeID},
			"comment":   fmt.Sprintf("%s %s %s %s", service.Meta["vendor"], account, service.Meta["region"], id),
		}
		if strings.EqualFold(ver, "V3") {
			protocols, err := v3Protocols(access.Protocols, existingProtocols(assets, service.Address))
			if err != nil {
				return fmt.Errorf("JumpServer protocols for %s: %w", id, err)
			}
			payload["address"] = service.Address
			payload["name"] = service.Meta["name"]
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
		existing, exists := byIP[service.Address]
		if !exists {
			endpoint := base + "/api/v1/assets/assets/"
			if strings.EqualFold(ver, "V3") {
				endpoint = base + "/api/v1/assets/hosts/"
			}
			s.writeAsset(ctx, token, http.MethodPost, endpoint, payload, service.Meta["name"])
			continue
		}
		if existing.assetName() == service.Meta["name"] && existing.groupName() == group {
			continue
		}
		// The baseline updates through the version-specific detail endpoint:
		// V2 assets and V3 hosts (its V3 branch reassigns ecs_url to hosts).
		endpoint := base + "/api/v1/assets/assets/" + existing.ID + "/"
		if strings.EqualFold(ver, "V3") {
			endpoint = base + "/api/v1/assets/hosts/" + existing.ID + "/"
		}
		s.writeAsset(ctx, token, http.MethodPut, endpoint, payload, service.Meta["name"])
	}
	return nil
}

// writeAsset performs one create/update and warns on failure without aborting
// the sync, mirroring the baseline's per-asset exception handling.
func (s *Service) writeAsset(ctx context.Context, token, method, endpoint string, payload map[string]any, name string) {
	status, err := s.write(ctx, token, method, endpoint, payload)
	if err != nil {
		s.logger.Warn("JumpServer asset write failed", "name", name, "error", err)
		return
	}
	if status < 200 || status >= 300 {
		s.logger.Warn("JumpServer asset write returned status", "name", name, "status", status)
	}
}

// deleteDuplicateIPs removes same-IP duplicates whose hostname does not match
// the ECS name for that IP. The first asset of a duplicated IP is a delete
// candidate too, exactly like the baseline.
func (s *Service) deleteDuplicateIPs(ctx context.Context, token, base, nodeID string, services map[string]store.Service) (string, error) {
	ecsNames := make(map[string]string, len(services))
	for _, service := range services {
		if service.Address != "" {
			ecsNames[service.Address] = service.Meta["name"]
		}
	}
	assets, detail, err := s.listAssets(ctx, token, base, nodeID)
	if err != nil {
		return detail, err
	}
	first := make(map[string]jmsAsset, len(assets))
	candidates := make(map[string]jmsAsset)
	for _, asset := range assets {
		ip := asset.assetIP()
		if ip == "" {
			continue
		}
		if previous, ok := first[ip]; ok {
			candidates[previous.ID] = previous
			candidates[asset.ID] = asset
			continue
		}
		first[ip] = asset
	}
	for _, asset := range candidates {
		if asset.assetName() != ecsNames[asset.assetIP()] {
			s.deleteAsset(ctx, token, base, asset.ID)
		}
	}
	return "", nil
}

// deleteEmptyNodes removes child nodes whose tree name reports zero assets,
// mirroring del_node.
func (s *Service) deleteEmptyNodes(ctx context.Context, token, base, nodeID string) (string, error) {
	var tree []jmsTreeNode
	detail, err := s.getJSON(ctx, token, base+"/api/v1/assets/nodes/children/tree/?id="+nodeID, &tree)
	if err != nil {
		return detail, err
	}
	for _, node := range tree {
		if !strings.HasSuffix(node.Name, " (0)") {
			continue
		}
		id := ""
		if node.Meta.Node != nil && node.Meta.Node.ID != "" {
			id = node.Meta.Node.ID
		} else if node.Meta.Data != nil && node.Meta.Data.ID != "" {
			id = node.Meta.Data.ID
		}
		if id == "" {
			s.logger.Warn("JumpServer empty node has no id in meta", "name", node.Name)
			continue
		}
		status, err := s.write(ctx, token, http.MethodDelete, base+"/api/v1/assets/nodes/"+id+"/", nil)
		if err != nil {
			s.logger.Warn("JumpServer node delete failed", "node", id, "error", err)
			continue
		}
		if status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound {
			s.logger.Warn("JumpServer node delete returned status", "node", id, "status", status)
		}
	}
	return "", nil
}

// listChildren returns the child nodes of nodeID. When JumpServer answers
// with an error dict instead of a list the detail is returned so the caller
// can record it like the baseline's create_node.
func (s *Service) listChildren(ctx context.Context, token, base, nodeID string) ([]jmsNode, string, error) {
	var children []jmsNode
	detail, err := s.getJSON(ctx, token, base+"/api/v1/assets/nodes/"+nodeID+"/children/", &children)
	return children, detail, err
}

// listAssets returns the assets under the configured node.
func (s *Service) listAssets(ctx context.Context, token, base, nodeID string) ([]jmsAsset, string, error) {
	var assets []jmsAsset
	detail, err := s.getJSON(ctx, token, base+"/api/v1/assets/assets/?node="+nodeID, &assets)
	return assets, detail, err
}

// assetCount reads the JMS-side asset count under the configured node via the
// paged list endpoint, matching the baseline record source.
func (s *Service) assetCount(ctx context.Context, token, base, nodeID string) (int, string, error) {
	var page struct {
		Count int `json:"count"`
	}
	detail, err := s.getJSON(ctx, token, base+"/api/v1/assets/assets/?node="+nodeID+"&limit=1&offset=1", &page)
	if err != nil {
		return 0, detail, err
	}
	return page.Count, "", nil
}

// getJSON performs an authenticated GET and decodes the JSON body. Errors and
// undecodable bodies yield the JumpServer error detail when present.
func (s *Service) getJSON(ctx context.Context, token, endpoint string, out any) (string, error) {
	resp, err := s.do(ctx, token, http.MethodGet, endpoint, nil)
	if err != nil {
		return "未知ERROR", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "未知ERROR", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return detailFrom(raw), fmt.Errorf("JumpServer GET %s returned %s", endpoint, resp.Status)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return detailFrom(raw), fmt.Errorf("decode JumpServer GET %s: %w", endpoint, err)
	}
	return "", nil
}

// detailFrom extracts the error detail from a JumpServer error body, bounded
// so records stay short. The baseline defaults to 未知ERROR when absent.
func detailFrom(raw []byte) string {
	var body struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Detail != "" {
		if len(body.Detail) > 200 {
			return body.Detail[:200]
		}
		return body.Detail
	}
	return "未知ERROR"
}

// do builds an authenticated request. The token only travels in the
// Authorization header; it never reaches URLs, bodies or logs.
func (s *Service) do(ctx context.Context, token, method, endpoint string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/json")
	return s.http.Do(req)
}

// write performs an authenticated write and returns the response status.
func (s *Service) write(ctx context.Context, token, method, endpoint string, body any) (int, error) {
	resp, err := s.do(ctx, token, method, endpoint, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// ecsInfo loads the access configuration, account-level first with the global
// ConsulManager/jms/ecs_info as fallback, like the baseline.
func (s *Service) ecsInfo(ctx context.Context, vendor, account string) (ecsInfo, error) {
	for _, path := range []string{
		"ConsulManager/jms/" + vendor + "/" + account + "/ecs_info",
		"ConsulManager/jms/ecs_info",
	} {
		var accountInfo map[string]json.RawMessage
		found, err := s.store.GetValue(ctx, path, &accountInfo)
		if err != nil {
			return nil, err
		}
		if !found || len(accountInfo) == 0 {
			continue
		}
		access, err := parseAccessMap(accountInfo)
		if err != nil {
			return nil, fmt.Errorf("invalid ecs_info: %w", err)
		}
		return access, nil
	}
	return nil, fmt.Errorf("JumpServer ecs_info configuration is missing")
}

// customInfo loads the custom access rules, account-level first with the
// global ConsulManager/jms/custom_ecs_info as fallback, like the baseline.
func (s *Service) customInfo(ctx context.Context, vendor, account string) (customRules, error) {
	for _, path := range []string{
		"ConsulManager/jms/" + vendor + "/" + account + "/custom_ecs_info",
		"ConsulManager/jms/custom_ecs_info",
	} {
		var raw map[string]json.RawMessage
		found, err := s.store.GetValue(ctx, path, &raw)
		if err != nil {
			return nil, err
		}
		if !found || len(raw) == 0 {
			continue
		}
		rules := make(customRules, len(raw))
		for name, entry := range raw {
			var accessMap map[string]json.RawMessage
			if err := json.Unmarshal(entry, &accessMap); err != nil {
				return nil, fmt.Errorf("invalid custom_ecs_info.%s: %w", name, err)
			}
			access, err := parseAccessMap(accessMap)
			if err != nil {
				return nil, fmt.Errorf("invalid custom_ecs_info.%s: %w", name, err)
			}
			rules[name] = access
		}
		return rules, nil
	}
	return customRules{}, nil
}

// parseAccessMap decodes one {"os": [["proto/port"], "user"]} access map.
func parseAccessMap(raw map[string]json.RawMessage) (ecsInfo, error) {
	access := make(ecsInfo, len(raw))
	for osType, entry := range raw {
		var value []json.RawMessage
		if err := json.Unmarshal(entry, &value); err != nil || len(value) != 2 {
			return nil, fmt.Errorf("invalid entry for %s", osType)
		}
		var protocols []string
		var adminUser string
		if err := json.Unmarshal(value[0], &protocols); err != nil || len(protocols) == 0 {
			return nil, fmt.Errorf("invalid protocols for %s", osType)
		}
		if err := json.Unmarshal(value[1], &adminUser); err != nil {
			return nil, fmt.Errorf("invalid admin user for %s", osType)
		}
		access[strings.ToLower(osType)] = ecsAccess{Protocols: protocols, AdminUser: adminUser}
	}
	return access, nil
}

// override applies the baseline keyword matching: case-insensitive substring
// over the hostname. When several keywords match, the last one wins, which
// the baseline derives from JSON insertion order; Go sorts the keys so the
// outcome is deterministic. A matching rule without an entry for the asset's
// OS keeps the defaults instead of aborting the sync.
func (rules customRules) override(hostname, osType string) (ecsAccess, bool) {
	lower := strings.ToLower(hostname)
	var matches []string
	for name := range rules {
		if strings.Contains(lower, strings.ToLower(name)) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return ecsAccess{}, false
	}
	sort.Strings(matches)
	access, ok := rules[matches[len(matches)-1]][strings.ToLower(osType)]
	if !ok {
		return ecsAccess{}, false
	}
	return access, true
}

func (info ecsInfo) forOS(osType string) (ecsAccess, error) {
	access, ok := info[strings.ToLower(osType)]
	if !ok {
		return ecsAccess{}, fmt.Errorf("no configuration for OS %q", osType)
	}
	return access, nil
}

func existingProtocols(assets []jmsAsset, address string) json.RawMessage {
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

var _ Syncer = (*Service)(nil)
