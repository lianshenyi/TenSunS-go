// Package consulmock is a small in-memory Consul HTTP fixture used by Go
// tests and by the appendix-B compatibility smoke script. It is not a
// production Consul replacement.
package consulmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

type Server struct {
	mu       sync.RWMutex
	kv       map[string][]byte
	services map[string]store.Service
}

func New() *Server {
	return &Server{
		kv:       make(map[string][]byte),
		services: make(map[string]store.Service),
	}
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) SetKV(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[path] = raw
	return nil
}

func (s *Server) SetKVBytes(path string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[path] = append([]byte(nil), value...)
}

func (s *Server) GetKV(path string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.kv[path]
	return append([]byte(nil), v...), ok
}

func (s *Server) SetService(id, name string, address string, port int, tags []string, meta map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services[id] = store.Service{
		ID: id, Name: name, Address: address, Port: port, Tags: tags, Meta: meta,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/v1/operator/autopilot/state":
		writeJSON(w, http.StatusOK, map[string]any{})
	case path == "/v1/operator/autopilot/health":
		writeJSON(w, http.StatusOK, map[string]any{})
	case strings.HasPrefix(path, "/v1/kv/"):
		s.handleKV(w, r, strings.TrimPrefix(path, "/v1/kv/"))
	case path == "/v1/agent/services":
		s.handleServices(w, r)
	case path == "/v1/agent/service/register":
		s.handleRegister(w, r)
	case strings.HasPrefix(path, "/v1/agent/service/deregister/"):
		s.handleDeregister(w, strings.TrimPrefix(path, "/v1/agent/service/deregister/"))
	case strings.HasPrefix(path, "/v1/agent/service/"):
		s.handleService(w, strings.TrimPrefix(path, "/v1/agent/service/"))
	case strings.HasPrefix(path, "/v1/health/service/"):
		writeJSON(w, http.StatusOK, []any{})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		if _, ok := q["raw"]; ok {
			s.mu.RLock()
			value, found := s.kv[key]
			s.mu.RUnlock()
			if !found {
				http.Error(w, "key not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(value)
			return
		}
		if _, ok := q["keys"]; ok {
			s.mu.RLock()
			keys := make([]string, 0)
			for k := range s.kv {
				if strings.HasPrefix(k, key) {
					keys = append(keys, k)
				}
			}
			s.mu.RUnlock()
			sort.Strings(keys)
			writeJSON(w, http.StatusOK, keys)
			return
		}
		s.mu.RLock()
		keys := make([]string, 0)
		for k := range s.kv {
			if strings.HasPrefix(k, key) {
				keys = append(keys, k)
			}
		}
		type pair struct {
			Key   string  `json:"Key"`
			Value *string `json:"Value"`
		}
		items := make([]pair, 0, len(keys))
		for _, k := range keys {
			encoded := base64.StdEncoding.EncodeToString(s.kv[k])
			items = append(items, pair{Key: k, Value: &encoded})
		}
		s.mu.RUnlock()
		sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
		writeJSON(w, http.StatusOK, items)
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.SetKVBytes(key, body)
		writeJSON(w, http.StatusOK, true)
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.kv, key)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, true)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

var (
	filterService = regexp.MustCompile(`Service\s*==\s*"([^"]*)"`)
	filterRegion  = regexp.MustCompile(`"([^"]*)"\s+in\s+Tags`)
	filterAccount = regexp.MustCompile(`Meta\.account\s*==\s*"([^"]*)"`)
	filterID      = regexp.MustCompile(`\bID\s*==\s*"([^"]*)"`)
)

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	serviceName := firstMatch(filterService, filter)
	region := firstMatch(filterRegion, filter)
	account := firstMatch(filterAccount, filter)
	id := firstMatch(filterID, filter)

	s.mu.RLock()
	out := make(map[string]map[string]any, len(s.services))
	for key, svc := range s.services {
		if id != "" && svc.ID != id {
			continue
		}
		if serviceName != "" && svc.Name != serviceName {
			continue
		}
		if region != "" && !contains(svc.Tags, region) {
			continue
		}
		if account != "" && svc.Meta["account"] != account {
			continue
		}
		out[key] = map[string]any{
			"ID":      svc.ID,
			"Service": svc.Name,
			"Tags":    svc.Tags,
			"Meta":    svc.Meta,
			"Address": svc.Address,
			"Port":    svc.Port,
		}
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleService(w http.ResponseWriter, id string) {
	s.mu.RLock()
	svc, ok := s.services[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ID":              svc.ID,
		"Service":         svc.Name,
		"Tags":            svc.Tags,
		"Meta":            svc.Meta,
		"Address":         svc.Address,
		"Port":            svc.Port,
		"TaggedAddresses": map[string]any{},
		"Weights":         map[string]int{"Passing": 1, "Warning": 1},
		"ContentHash":     "",
		"Datacenter":      "dc1",
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var reg store.ServiceRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.SetService(reg.ID, reg.Name, reg.Address, reg.Port, reg.Tags, reg.Meta)
	writeJSON(w, http.StatusOK, true)
}

func (s *Server) handleDeregister(w http.ResponseWriter, id string) {
	s.mu.Lock()
	delete(s.services, id)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, true)
}

// SeedDemo populates a dataset equivalent to one alicloud account, two ECS
// hosts, one RDS, one Redis, and an enabled JumpServer task.
func (s *Server) SeedDemo() error {
	const skey = "0123456789abcdef0123456789abcdef"
	seed := map[string]any{
		"ConsulManager/assets/secret/skey": map[string]string{"sk": skey},
		"ConsulManager/assets/alicloud/group/main": map[string]string{
			"rg-default": "默认资源组",
		},
		"ConsulManager/jobs/alicloud/main/group": map[string]any{
			"id": "alicloud/main/group", "func": "__main__:alicloud.group",
			"args": []any{"main"}, "minutes": 60, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou": map[string]any{
			"id": "alicloud/main/ecs/cn-hangzhou", "func": "__main__:alicloud.ecs",
			"args": []any{"main", "cn-hangzhou", false}, "minutes": 10, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/rds/cn-hangzhou": map[string]any{
			"id": "alicloud/main/rds/cn-hangzhou", "func": "__main__:alicloud.rds",
			"args": []any{"main", "cn-hangzhou"}, "minutes": 20, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/redis/cn-hangzhou": map[string]any{
			"id": "alicloud/main/redis/cn-hangzhou", "func": "__main__:alicloud.redis",
			"args": []any{"main", "cn-hangzhou"}, "minutes": 20, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/record/jobs/alicloud/main/group": map[string]any{
			"count": 2, "update": "10.23/14:10", "status": 20000, "msg": "同步资源组成功！总数：2",
		},
		"ConsulManager/record/jobs/alicloud/main/ecs/cn-hangzhou": map[string]any{
			"count": 2, "update": "10.23/14:12", "status": 20000, "on": 1, "off": 1, "msg": "ECS同步成功！总数：2，开机：1，关机：1",
		},
		"ConsulManager/jms/jobs/alicloud/main": map[string]any{
			"id": "alicloud/main/jms", "func": "__main__:sync_jms.run",
			"args": []any{"alicloud", "main"}, "minutes": 3, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/record/jms/alicloud/main": map[string]any{
			"count": 2, "update": "1023/14:10", "status": 20000, "msg": "同步资源成功！总数：2",
		},
		"ConsulManager/jms/alicloud/main/node_id": map[string]any{
			"node_id": "00000000-0000-0000-0000-000000000001", "interval": 3,
		},
		"ConsulManager/jms/ecs_info": map[string]any{
			"linux":   []any{[]string{"ssh/22"}, "root"},
			"windows": []any{[]string{"rdp/3389"}, "Administrator"},
		},
		"ConsulManager/jms/custom_ecs_info": map[string]any{},
	}
	cipher, err := cryptox.NewCipher(skey)
	if err != nil {
		return err
	}
	token, err := cipher.Encrypt("demo-jumpserver-admin-token")
	if err != nil {
		return err
	}
	seed["ConsulManager/jms/jms_info"] = map[string]any{
		"url": "https://jumpserver.example.com", "token": token, "ver": "V3",
	}
	for path, value := range seed {
		if err := s.SetKV(path, value); err != nil {
			return fmt.Errorf("seed %s: %w", path, err)
		}
	}
	s.SetService("i-demo0001", "alicloud_main_ecs", "10.0.0.11", 9100,
		[]string{"ON", "linux", "cn-hangzhou"},
		map[string]string{
			"iid": "i-demo0001", "name": "prod-web-01", "region": "华东1(杭州)",
			"group": "默认资源组", "instance": "10.0.0.11:9100", "account": "main",
			"vendor": "阿里云", "os": "linux", "cpu": "4核", "mem": "8GB",
			"exp": "按量", "stat": "on",
		})
	s.SetService("i-demo0002", "alicloud_main_ecs", "10.0.0.12", 9182,
		[]string{"OFF", "windows", "cn-hangzhou"},
		map[string]string{
			"iid": "i-demo0002", "name": "prod-win-01", "region": "华东1(杭州)",
			"group": "默认资源组", "instance": "10.0.0.12:9182", "account": "main",
			"vendor": "阿里云", "os": "windows", "cpu": "8核", "mem": "16GB",
			"exp": "按量", "stat": "off",
		})
	s.SetService("rm-demo0001", "alicloud_main_rds", "rm-demo0001.mysql.rds.aliyuncs.com", 3306,
		[]string{"ON", "主实例", "MySQL 8.0", "cn-hangzhou"},
		map[string]string{
			"iid": "rm-demo0001", "name": "prod-mysql", "region": "华东1(杭州)",
			"group": "默认资源组", "instance": "rm-demo0001.mysql.rds.aliyuncs.com:3306",
			"account": "main", "itype": "主实例", "vendor": "阿里云", "disk": "100GB",
			"cpu": "4核", "mem": "8GB", "ver": "MySQL 8.0", "domain": "rm-demo0001.mysql.rds.aliyuncs.com",
			"exp": "按量", "stat": "on",
		})
	s.SetService("r-demo0001", "alicloud_main_redis", "r-demo0001.redis.rds.aliyuncs.com", 6379,
		[]string{"ON", "主从", "Redis 6.0", "cn-hangzhou"},
		map[string]string{
			"iid": "r-demo0001", "name": "prod-redis", "region": "华东1(杭州)",
			"group": "默认资源组", "instance": "r-demo0001.redis.rds.aliyuncs.com:6379",
			"account": "main", "itype": "主从", "vendor": "阿里云", "mem": "4GB",
			"ver": "Redis 6.0", "exp": "按量", "stat": "on", "ip": "10.0.0.21",
		})
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstMatch(re *regexp.Regexp, value string) string {
	match := re.FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
