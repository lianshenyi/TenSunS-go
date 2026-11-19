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

	"github.com/lianshenyi/TenSunS-go/internal/demodata"
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
	values, services, err := demodata.New("ConsulManager/jms/alicloud/main/node_id")
	if err != nil {
		return err
	}
	for path, value := range values {
		if err := s.SetKV(path, value); err != nil {
			return fmt.Errorf("seed %s: %w", path, err)
		}
	}
	for _, svc := range services {
		s.SetService(svc.ID, svc.Name, svc.Address, svc.Port, svc.Tags, svc.Meta)
	}
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
