package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/store"
)

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	stype := strings.TrimPrefix(r.URL.Path, "/api/edit/")
	switch r.Method {
	case http.MethodGet:
		switch stype {
		case "cloud":
			s.editCloud(w, r)
		case "find":
			s.editFind(w, r)
		default:
			s.notFound(w, r)
		}
	case http.MethodPost:
		if stype != "commit" {
			s.notFound(w, r)
			return
		}
		var request thirdPartyCommitRequest
		if err := decodeJSONBody(r, &request); err != nil {
			s.validationError(w, "Json解析错误，请检查！")
			return
		}
		if request.Vendor == "third_party" {
			s.commitThirdParty(w, r, request)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 50000,
			"data": "Go 迁移阶段尚未实现：编辑云资源需要云适配器与任务创建流程。",
		})
	default:
		s.methodNotAllowed(w)
	}
}

const (
	thirdPartyVendor = "third_party"
	thirdPartyRegion = "global"
	defaultAccount   = "default"
)

type thirdPartyCommitRequest struct {
	Vendor string           `json:"vendor"`
	Hosts  []thirdPartyHost `json:"hosts"`
}

type thirdPartyHost struct {
	Name    string `json:"name"`
	Spec    string `json:"spec"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Account string `json:"account"`
	Project string `json:"project"`
}

func (s *Server) commitThirdParty(w http.ResponseWriter, r *http.Request, request thirdPartyCommitRequest) {
	hosts, err := validateThirdPartyHosts(request.Hosts)
	if err != nil {
		s.validationError(w, err.Error())
		return
	}
	if err := s.replaceThirdPartyServices(r, hosts); err != nil {
		s.businessError(w, fmt.Errorf("replace third-party ECS services: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": map[string]int{"count": len(hosts)}})
}

func validateThirdPartyHosts(hosts []thirdPartyHost) ([]thirdPartyHost, error) {
	seenNames := make(map[string]struct{}, len(hosts))
	seenEndpoints := make(map[string]struct{}, len(hosts))
	for index := range hosts {
		host := &hosts[index]
		host.Name = strings.TrimSpace(host.Name)
		host.Spec = strings.TrimSpace(host.Spec)
		host.IP = strings.TrimSpace(host.IP)
		host.Account = strings.TrimSpace(host.Account)
		host.Project = strings.TrimSpace(host.Project)
		if host.Name == "" || host.Spec == "" || host.IP == "" || host.Port == 0 {
			return nil, fmt.Errorf("第%d台机器的名称、规格、IP和端口不能为空", index+1)
		}
		address, err := netip.ParseAddr(host.IP)
		if err != nil {
			return nil, fmt.Errorf("第%d台机器的IP格式错误", index+1)
		}
		if host.Port < 1 || host.Port > 65535 {
			return nil, fmt.Errorf("第%d台机器的端口必须在1-65535之间", index+1)
		}
		host.IP = address.String()
		if host.Account == "" {
			host.Account = defaultAccount
		}
		if _, found := seenNames[host.Name]; found {
			return nil, fmt.Errorf("机器名称重复：%s", host.Name)
		}
		seenNames[host.Name] = struct{}{}
		endpoint := net.JoinHostPort(host.IP, strconv.Itoa(host.Port))
		if _, found := seenEndpoints[endpoint]; found {
			return nil, fmt.Errorf("机器IP和端口重复：%s", endpoint)
		}
		seenEndpoints[endpoint] = struct{}{}
	}
	return hosts, nil
}

func (s *Server) replaceThirdPartyServices(r *http.Request, hosts []thirdPartyHost) error {
	existing, err := s.store.Services(r.Context(), "")
	if err != nil {
		return fmt.Errorf("list existing services: %w", err)
	}
	current := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		id := thirdPartyServiceID(host.Name)
		current[id] = struct{}{}
		serviceName := thirdPartyVendor + "_" + host.Account + "_ecs"
		group := host.Project
		if group == "" {
			group = "手工录入"
		}
		meta := map[string]string{
			"iid": id, "name": host.Name, "region": thirdPartyRegion, "group": group,
			"instance": net.JoinHostPort(host.IP, strconv.Itoa(host.Port)), "account": host.Account,
			"vendor": "第三方", "project": host.Project, "os": "linux", "cpu": host.Spec, "mem": "无", "exp": "无", "stat": "on",
		}
		if err := s.store.Register(r.Context(), store.ServiceRegistration{
			ID: id, Name: serviceName, Address: host.IP, Port: host.Port,
			Tags: []string{"ON", "linux", thirdPartyRegion}, Meta: meta,
			Check: &store.ServiceCheck{TCP: net.JoinHostPort(host.IP, strconv.Itoa(host.Port)), Interval: "60s"},
		}); err != nil {
			return fmt.Errorf("register service %q: %w", id, err)
		}
	}
	for id, service := range existing {
		if !isThirdPartyService(service) {
			continue
		}
		if _, found := current[id]; found {
			continue
		}
		if err := s.store.Deregister(r.Context(), id); err != nil {
			return fmt.Errorf("deregister stale service %q: %w", id, err)
		}
	}
	return nil
}

func thirdPartyServiceID(name string) string {
	digest := sha256.Sum256([]byte(name))
	return thirdPartyVendor + "-" + hex.EncodeToString(digest[:])
}

func (s *Server) validationError(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusOK, map[string]any{"code": 40000, "data": message})
}

func (s *Server) editCloud(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("vendor") == thirdPartyVendor {
		hosts, err := s.loadThirdPartyHosts(r)
		if err != nil {
			s.businessError(w, fmt.Errorf("load third-party ECS services: %w", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": map[string]any{"hosts": hosts}})
		return
	}
	specs, err := s.loadJobSpecs(r)
	if err != nil {
		s.businessError(w, err)
		return
	}
	cloud := map[string]map[string]struct{}{}
	for _, spec := range specs {
		parts := strings.Split(spec.ID, "/")
		if len(parts) < 2 {
			continue
		}
		vendor, account := parts[0], parts[1]
		if cloud[vendor] == nil {
			cloud[vendor] = map[string]struct{}{}
		}
		cloud[vendor][account] = struct{}{}
	}
	result := make(map[string][]string, len(cloud))
	for vendor, accounts := range cloud {
		result[vendor] = sortedKeys(accounts)
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "cloud_dict": result})
}

// loadThirdPartyHosts reconstructs the complete edit baseline from every
// third-party ECS Service, so a subsequent full replacement cannot drop a
// host merely because it belongs to a different account.
func (s *Server) loadThirdPartyHosts(r *http.Request) ([]thirdPartyHost, error) {
	services, err := s.store.Services(r.Context(), "")
	if err != nil {
		return nil, err
	}
	hosts := make([]thirdPartyHost, 0)
	for _, service := range services {
		if !isThirdPartyService(service) {
			continue
		}
		meta := service.Meta
		account := strings.TrimSpace(meta["account"])
		if account == "" {
			account = defaultAccount
		}
		hosts = append(hosts, thirdPartyHost{
			Name:    meta["name"],
			Spec:    meta["cpu"],
			IP:      service.Address,
			Port:    service.Port,
			Account: account,
			Project: meta["project"],
		})
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Account != hosts[j].Account {
			return hosts[i].Account < hosts[j].Account
		}
		return hosts[i].Name < hosts[j].Name
	})
	return hosts, nil
}

func isThirdPartyService(service store.Service) bool {
	return service.Meta["vendor"] == "第三方" && strings.HasPrefix(service.Name, thirdPartyVendor+"_")
}

func (s *Server) editFind(w http.ResponseWriter, r *http.Request) {
	vendor := requiredQuery(r, "vendor")
	account := requiredQuery(r, "account")
	region := requiredQuery(r, "region")
	specs, err := s.loadJobSpecs(r)
	if err != nil {
		s.businessError(w, err)
		return
	}
	restype := []string{"group"}
	intervals := map[string]int{
		"proj_interval":  60,
		"ecs_interval":   10,
		"rds_interval":   20,
		"redis_interval": 20,
	}
	isextip := false
	for _, spec := range specs {
		switch spec.ID {
		case vendor + "/" + account + "/group":
			intervals["proj_interval"] = spec.Minutes
		case vendor + "/" + account + "/ecs/" + region:
			restype = append(restype, "ecs")
			intervals["ecs_interval"] = spec.Minutes
			if len(spec.Args) == 3 {
				isextip, _ = boolValue(spec.Args[2])
			}
		case vendor + "/" + account + "/rds/" + region:
			restype = append(restype, "rds")
			intervals["rds_interval"] = spec.Minutes
		case vendor + "/" + account + "/redis/" + region:
			restype = append(restype, "redis")
			intervals["redis_interval"] = spec.Minutes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":     20000,
		"restype":  restype,
		"interval": intervals,
		"isextip":  isextip,
	})
}
