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
}
type host struct{ ID, Name, IP, Group, OS, Region string }
type asset struct {
	ID, Name, Group string
	Protocols       any
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
	for ip, oldAsset := range old {
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
		if a, ok := old[ip]; ok {
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
	return settings{URL: strings.TrimRight(info.URL, "/"), Token: token, Version: strings.ToUpper(info.Ver), Node: node.ID, User: user(ecsInfo, "linux"), Protocols: protocol(ecsInfo, "linux"), ECS: ecsInfo}, nil
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
func (c *Client) assets(ctx context.Context, cfg settings, q string) (map[string]asset, error) {
	path := "/api/v1/assets/assets/?node=" + cfg.Node
	if q != "" {
		path += "&" + q
	}
	var data []map[string]any
	if err := c.get(ctx, cfg, path, &data); err != nil {
		return nil, err
	}
	out := map[string]asset{}
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
		out[ip] = asset{fmt.Sprint(v["id"]), name, node(v), v["protocols"]}
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
	if len(protocols) == 0 {
		protocols, adminUser = cfg.Protocols, cfg.User
	}
	if len(protocols) == 0 {
		protocols = []string{"ssh/22"}
	}
	port := 22
	_, _ = fmt.Sscanf(protocols[0], "%*[^/]/%d", &port)
	comment := fmt.Sprintf("%s %s %s %s", config.VendorDisplay(vendor), account, h.Region, h.ID)
	if cfg.Version == "V3" {
		return map[string]any{"address": h.IP, "name": h.Name, "protocols": []map[string]any{{"name": strings.Split(protocols[0], "/")[0], "port": port}, {"name": "sftp", "port": port}}, "platform": map[bool]string{true: "5", false: "1"}[h.OS == "windows"], "is_active": true, "domain": "", "accounts": []map[string]string{{"template": adminUser}}, "nodes": []string{node}, "comment": comment}
	}
	return map[string]any{"ip": h.IP, "hostname": h.Name, "protocols": protocols, "platform": strings.Title(h.OS), "is_active": true, "domain": "", "admin_user": adminUser, "nodes": []string{node}, "comment": comment}
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
