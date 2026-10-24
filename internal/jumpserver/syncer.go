// Package jumpserver implements the compatibility V2/V3 asset sync flow.
package jumpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

var (
	ErrAuthentication = errors.New("jumpserver authentication failed")
	ErrRateLimited    = errors.New("jumpserver rate limited")
	ErrTemporary      = errors.New("jumpserver temporary failure")
)

type Syncer interface {
	Run(context.Context, string, string) error
}

// Client reads legacy configuration and reconciles JumpServer via its real API.
type Client struct {
	Store  store.Store
	Cipher *cryptox.Cipher
	HTTP   *http.Client
	Now    func() time.Time
	locks  sync.Map
}
type settings struct {
	URL, Token, Version, Node, User string
	Protocols                       []string
	ECS                             map[string][]json.RawMessage
	Custom                          orderedCustom
}
type host struct{ ID, Name, IP, Group, OS, Region string }
type asset struct {
	ID, Name, IP, Group string
	Protocols           any
}

// orderedCustom retains JSON object order because later matching keys override
// earlier ones in the legacy configuration contract.
type orderedCustom []customOverride

type customOverride struct {
	Keyword string
	Info    map[string][]json.RawMessage
}

func (c *orderedCustom) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("custom_ecs_info must be an object")
	}
	var entries []customOverride
	for decoder.More() {
		keywordToken, err := decoder.Token()
		if err != nil {
			return err
		}
		keyword, ok := keywordToken.(string)
		if !ok {
			return errors.New("custom_ecs_info key must be a string")
		}
		var info map[string][]json.RawMessage
		if err := decoder.Decode(&info); err != nil {
			return err
		}
		entries = append(entries, customOverride{Keyword: keyword, Info: info})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*c = entries
	return nil
}

func (c *Client) Run(ctx context.Context, vendor, account string) error {
	if c.Store == nil || c.Cipher == nil {
		return errors.New("jumpserver dependencies are not configured")
	}
	v, _ := c.locks.LoadOrStore(vendor+"/"+account, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := c.run(ctx, vendor, account); err != nil {
		_ = c.record(ctx, vendor, account, 0, 50000, "同步资源失败！"+err.Error())
		return err
	}
	return nil
}

func (c *Client) run(ctx context.Context, vendor, account string) error {
	cfg, err := c.settings(ctx, vendor, account)
	if err != nil {
		return err
	}
	instances, err := c.Store.HealthService(ctx, vendor+"_"+account+"_ecs")
	if err != nil {
		return fmt.Errorf("read ECS services: %w", err)
	}
	current := map[string]host{}
	for _, i := range instances {
		m := i.Service.Meta
		if m != nil && i.Service.Address != "" {
			current[i.Service.Address] = host{i.Service.ID, m["name"], i.Service.Address, m["group"], strings.ToLower(m["os"]), m["region"]}
		}
	}
	nodes, err := c.children(ctx, cfg)
	if err != nil {
		return err
	}
	groups := map[string]struct{}{"未分组": {}}
	var groupKV map[string]string
	if _, err := c.Store.GetValue(ctx, "ConsulManager/assets/"+vendor+"/group/"+account, &groupKV); err != nil {
		return err
	}
	for _, group := range groupKV {
		groups[group] = struct{}{}
	}
	for group := range groups {
		if nodes[group] == "" {
			_ = c.post(ctx, cfg, "/api/v1/assets/nodes/"+cfg.Node+"/children/", map[string]any{"value": group})
		}
	}
	nodes, err = c.children(ctx, cfg)
	if err != nil {
		return err
	}
	if nodes["未分组"] == "" {
		return errors.New("JumpServer did not create 未分组 node")
	}
	old, err := c.assets(ctx, cfg, "")
	if err != nil {
		return err
	}
	known := make(map[string]asset, len(old))
	for _, oldAsset := range old {
		selected, exists := known[oldAsset.IP]
		if !exists || (selected.Name != current[oldAsset.IP].Name && oldAsset.Name == current[oldAsset.IP].Name) {
			known[oldAsset.IP] = oldAsset
		}
	}
	for _, oldAsset := range old {
		ip := oldAsset.IP
		if _, ok := current[ip]; !ok {
			if err := c.delete(ctx, cfg, "/api/v1/assets/assets/"+oldAsset.ID+"/"); err != nil {
				return err
			}
		}
	}
	for ip, h := range current {
		node := nodes[h.Group]
		if node == "" {
			node = nodes["未分组"]
		}
		if a, ok := known[ip]; ok {
			wanted := h.Group
			if wanted == "无" {
				wanted = "未分组"
			}
			if a.Name != h.Name || a.Group != wanted {
				if err := c.put(ctx, cfg, "/api/v1/assets/assets/"+a.ID+"/", c.payload(cfg, h, node, vendor, account)); err != nil {
					return err
				}
			}
			continue
		}
		path := "/api/v1/assets/assets/"
		if cfg.Version == "V3" {
			path = "/api/v1/assets/hosts/"
		}
		if err := c.post(ctx, cfg, path, c.payload(cfg, h, node, vendor, account)); err != nil {
			return err
		}
	}
	if err := c.deleteDuplicateIPs(ctx, cfg, current); err != nil {
		return err
	}
	if err := c.deleteEmptyNodes(ctx, cfg); err != nil {
		return err
	}
	final, err := c.assets(ctx, cfg, "limit=1&offset=1")
	if err != nil {
		return err
	}
	return c.record(ctx, vendor, account, len(final), 20000, fmt.Sprintf("同步资源成功！总数：%d", len(final)))
}

func (c *Client) settings(ctx context.Context, vendor, account string) (settings, error) {
	var node struct {
		ID string `json:"node_id"`
	}
	found, err := c.Store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/node_id", &node)
	if err != nil || !found || node.ID == "" {
		return settings{}, fmt.Errorf("read node_id: %w", err)
	}
	var info struct {
		URL   string `json:"url"`
		Token string `json:"token"`
		Ver   string `json:"ver"`
	}
	found, err = c.Store.GetValue(ctx, "ConsulManager/jms/jms_info", &info)
	if err != nil || !found || info.URL == "" {
		return settings{}, fmt.Errorf("read jms_info: %w", err)
	}
	token, err := c.Cipher.Decrypt(info.Token)
	if err != nil {
		return settings{}, fmt.Errorf("decrypt jms token: %w", err)
	}
	var ecsInfo map[string][]json.RawMessage
	found, err = c.Store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/ecs_info", &ecsInfo)
	if err != nil || !found || len(ecsInfo) == 0 {
		_, err = c.Store.GetValue(ctx, "ConsulManager/jms/ecs_info", &ecsInfo)
	}
	if err != nil {
		return settings{}, err
	}
	var custom orderedCustom
	found, err = c.Store.GetValue(ctx, "ConsulManager/jms/"+vendor+"/"+account+"/custom_ecs_info", &custom)
	if err != nil || !found || len(custom) == 0 {
		found, err = c.Store.GetValue(ctx, "ConsulManager/jms/custom_ecs_info", &custom)
	}
	if err != nil {
		return settings{}, fmt.Errorf("read custom_ecs_info: %w", err)
	}
	return settings{URL: strings.TrimRight(info.URL, "/"), Token: token, Version: strings.ToUpper(info.Ver), Node: node.ID, User: user(ecsInfo, "linux"), Protocols: protocol(ecsInfo, "linux"), ECS: ecsInfo, Custom: custom}, nil
}
func protocol(info map[string][]json.RawMessage, os string) []string {
	if len(info[os]) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(info[os][0], &out)
	return out
}
func user(info map[string][]json.RawMessage, os string) string {
	if len(info[os]) < 2 {
		return ""
	}
	var out string
	_ = json.Unmarshal(info[os][1], &out)
	return strings.TrimSpace(out)
}
func (c *Client) children(ctx context.Context, cfg settings) (map[string]string, error) {
	var data []map[string]any
	if err := c.get(ctx, cfg, "/api/v1/assets/nodes/"+cfg.Node+"/children/", &data); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, v := range data {
		n, _ := v["value"].(string)
		if n != "" {
			out[n] = fmt.Sprint(v["id"])
		}
	}
	return out, nil
}
func (c *Client) assets(ctx context.Context, cfg settings, q string) ([]asset, error) {
	path := "/api/v1/assets/assets/?node=" + cfg.Node
	if q != "" {
		path += "&" + q
	}
	var data []map[string]any
	if err := c.get(ctx, cfg, path, &data); err != nil {
		return nil, err
	}
	var out []asset
	for _, v := range data {
		ip := text(v, "address")
		if ip == "" {
			ip = text(v, "ip")
		}
		if ip == "" {
			continue
		}
		name := text(v, "name")
		if name == "" {
			name = text(v, "hostname")
		}
		out = append(out, asset{ID: fmt.Sprint(v["id"]), Name: name, IP: ip, Group: node(v), Protocols: v["protocols"]})
	}
	return out, nil
}
func text(v map[string]any, key string) string { s, _ := v[key].(string); return s }
func node(v map[string]any) string {
	for _, key := range []string{"nodes_display", "nodes"} {
		if a, ok := v[key].([]any); ok && len(a) > 0 {
			return last(fmt.Sprint(a[0]))
		}
	}
	return ""
}
func last(s string) string { a := strings.Split(strings.Trim(s, "/"), "/"); return a[len(a)-1] }
func (c *Client) payload(cfg settings, h host, node, vendor, account string) map[string]any {
	protocols := protocol(cfg.ECS, h.OS)
	adminUser := user(cfg.ECS, h.OS)
	for _, custom := range cfg.Custom {
		if strings.Contains(strings.ToLower(h.Name), strings.ToLower(custom.Keyword)) {
			protocols = protocol(custom.Info, h.OS)
			adminUser = user(custom.Info, h.OS)
		}
	}
	if len(protocols) == 0 {
		protocols, adminUser = cfg.Protocols, cfg.User
	}
	if len(protocols) == 0 {
		protocols = []string{"ssh/22"}
	}
	port := 22
	if _, configuredPort, found := strings.Cut(protocols[0], "/"); found {
		if parsed, err := strconv.Atoi(configuredPort); err == nil {
			port = parsed
		}
	}
	comment := fmt.Sprintf("%s %s %s %s", config.VendorDisplay(vendor), account, h.Region, h.ID)
	if cfg.Version == "V3" {
		return map[string]any{"address": h.IP, "name": h.Name, "protocols": []map[string]any{{"name": strings.Split(protocols[0], "/")[0], "port": port}, {"name": "sftp", "port": port}}, "platform": map[bool]string{true: "5", false: "1"}[h.OS == "windows"], "is_active": true, "domain": "", "accounts": []map[string]string{{"template": adminUser}}, "nodes": []string{node}, "comment": comment}
	}
	return map[string]any{"ip": h.IP, "hostname": h.Name, "protocols": protocols, "platform": strings.Title(h.OS), "is_active": true, "domain": "", "admin_user": adminUser, "nodes": []string{node}, "comment": comment}
}

func (c *Client) deleteDuplicateIPs(ctx context.Context, cfg settings, current map[string]host) error {
	assets, err := c.assets(ctx, cfg, "")
	if err != nil {
		return err
	}
	byIP := make(map[string][]asset)
	for _, asset := range assets {
		byIP[asset.IP] = append(byIP[asset.IP], asset)
	}
	for ip, duplicates := range byIP {
		if len(duplicates) < 2 {
			continue
		}
		wanted, exists := current[ip]
		if !exists {
			continue
		}
		for _, duplicate := range duplicates {
			if duplicate.Name != wanted.Name {
				if err := c.delete(ctx, cfg, "/api/v1/assets/assets/"+duplicate.ID+"/"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *Client) deleteEmptyNodes(ctx context.Context, cfg settings) error {
	var tree []struct {
		Meta struct {
			Node struct {
				ID string `json:"id"`
			} `json:"node"`
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"meta"`
		Text string `json:"text"`
	}
	if err := c.get(ctx, cfg, "/api/v1/assets/nodes/children/tree/?id="+cfg.Node, &tree); err != nil {
		return err
	}
	for _, child := range tree {
		if !strings.HasSuffix(child.Text, " (0)") {
			continue
		}
		id := child.Meta.Node.ID
		if id == "" {
			id = child.Meta.Data.ID
		}
		if id != "" {
			if err := c.delete(ctx, cfg, "/api/v1/assets/nodes/"+id+"/"); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Client) get(ctx context.Context, s settings, p string, o any) error {
	return c.do(ctx, s, http.MethodGet, p, nil, o)
}
func (c *Client) post(ctx context.Context, s settings, p string, b any) error {
	return c.do(ctx, s, http.MethodPost, p, b, nil)
}
func (c *Client) put(ctx context.Context, s settings, p string, b any) error {
	return c.do(ctx, s, http.MethodPut, p, b, nil)
}
func (c *Client) delete(ctx context.Context, s settings, p string) error {
	return c.do(ctx, s, http.MethodDelete, p, nil, nil)
}
func (c *Client) do(ctx context.Context, s settings, method, path string, body, out any) error {
	var err error
	for i := 0; i < 3; i++ {
		err = c.once(ctx, s, method, path, body, out)
		if err == nil || !retryable(err) {
			return err
		}
		if i < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i+1) * 10 * time.Millisecond):
			}
		}
	}
	return err
}
func (c *Client) once(ctx context.Context, s settings, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		r = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, s.URL+path, r)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Token "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, e := client.Do(req)
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) {
			return ErrTemporary
		}
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrAuthentication
	}
	if resp.StatusCode == 429 {
		return ErrRateLimited
	}
	if resp.StatusCode >= 500 {
		return ErrTemporary
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jumpserver %s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
func retryable(e error) bool {
	return errors.Is(e, ErrRateLimited) || errors.Is(e, ErrTemporary) || errors.Is(e, context.DeadlineExceeded)
}
func (c *Client) record(ctx context.Context, v, a string, count, status int, msg string) error {
	return c.Store.PutKV(ctx, "ConsulManager/record/jms/"+v+"/"+a, map[string]any{"count": count, "update": c.now().Format("0102/15:04"), "status": status, "msg": msg})
}
func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
