package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
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
		s.editCommit(w, r)
	default:
		s.methodNotAllowed(w)
	}
}

// editJob is the POST /api/edit/commit payload under "editJob". Intervals are
// decoded as json.Number because the frontend interval inputs are plain text
// fields and may carry either numbers or numeric strings.
type editJob struct {
	Vendor          string      `json:"vendor"`
	Account         string      `json:"account"`
	Region          string      `json:"region"`
	ResourceTypes   []string    `json:"restype"`
	ExternalIP      bool        `json:"isextip"`
	ProjectInterval json.Number `json:"proj_interval"`
	ECSInterval     json.Number `json:"ecs_interval"`
	RDSInterval     json.Number `json:"rds_interval"`
	RedisInterval   json.Number `json:"redis_interval"`
	// AK/SK override fields belong to the shared legacy form. The four cloud
	// vendors honor them through akskswitch; PVE ignores them because its
	// credentials are the token trio below.
	AKSwitch bool   `json:"akskswitch"`
	AK       string `json:"ak"`
	SK       string `json:"sk"`
	// PVE token credentials. The frontend deletes all four fields from the
	// payload when "更新 PVE 连接配置" is off, so each field uses
	// presence-based overlay: nil keeps the stored value, non-nil overwrites.
	APIBaseURL  *string `json:"api_base_url"`
	TokenID     *string `json:"token_id"`
	TokenSecret *string `json:"token_secret"`
	TLSVerify   *bool   `json:"tls_verify"`
}

const (
	thirdPartyVendor = "third_party"
	thirdPartyRegion = "global"
	defaultAccount   = "default"
)

type thirdPartyHost struct {
	Name    string `json:"name"`
	Spec    string `json:"spec"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Account string `json:"account"`
	Project string `json:"project"`
}

// editCommit implements POST /api/edit/commit for every registered vendor:
// PVE and the four cloud vendors. Unknown vendors are an explicit error, not
// the migration placeholder.
func (s *Server) editCommit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EditJob editJob          `json:"editJob"`
		Vendor  string           `json:"vendor"`
		Hosts   []thirdPartyHost `json:"hosts"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		s.businessError(w, err)
		return
	}
	if req.Vendor == thirdPartyVendor {
		hosts, err := validateThirdPartyHosts(req.Hosts)
		if err != nil {
			s.validationError(w, err.Error())
			return
		}
		if err := s.replaceThirdPartyServices(r, hosts); err != nil {
			s.businessError(w, fmt.Errorf("replace third-party ECS services: %w", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": map[string]int{"count": len(hosts)}})
		return
	}
	job := req.EditJob
	if _, ok := config.Vendors[job.Vendor]; !ok {
		s.businessError(w, fmt.Errorf("unsupported vendor %q", job.Vendor))
		return
	}
	if err := s.commitEdit(r, job); err != nil {
		s.businessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 20000,
		"data": job.Vendor + "/" + job.Account + "/" + job.Region + "：编辑成功！",
	})
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
		group := host.Project
		if group == "" {
			group = "手工录入"
		}
		meta := map[string]string{"iid": id, "name": host.Name, "region": thirdPartyRegion, "group": group,
			"instance": net.JoinHostPort(host.IP, strconv.Itoa(host.Port)), "account": host.Account,
			"vendor": "第三方", "project": host.Project, "os": "linux", "cpu": host.Spec, "mem": "无", "exp": "无", "stat": "on"}
		if err := s.store.Register(r.Context(), store.ServiceRegistration{ID: id, Name: thirdPartyVendor + "_" + host.Account + "_ecs", Address: host.IP, Port: host.Port,
			Tags: []string{"ON", "linux", thirdPartyRegion}, Meta: meta,
			Check: &store.ServiceCheck{TCP: net.JoinHostPort(host.IP, strconv.Itoa(host.Port)), Interval: "60s"}}); err != nil {
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

// commitEdit mirrors the legacy Python edit_cloud semantics: credential fields
// are handled per vendor, the group task is kept and follows proj_interval,
// selected resource tasks are created or updated, and unselected ones are
// removed from both Consul and the in-memory scheduler.
func (s *Server) commitEdit(r *http.Request, job editJob) error {
	ctx := r.Context()
	if job.Account == "" || job.Region == "" {
		return fmt.Errorf("account and region are required")
	}
	proj, err := job.ProjectInterval.Int64()
	if err != nil || proj <= 0 {
		return fmt.Errorf("proj_interval must be a positive integer")
	}
	for _, resourceType := range job.ResourceTypes {
		if !slices.Contains([]string{"group", "ecs", "rds", "redis"}, resourceType) {
			return fmt.Errorf("unsupported resource type %q", resourceType)
		}
	}
	intervals := map[string]int{"ecs": 0, "rds": 0, "redis": 0}
	for _, resourceType := range []string{"ecs", "rds", "redis"} {
		if !slices.Contains(job.ResourceTypes, resourceType) {
			continue
		}
		raw := job.ECSInterval
		switch resourceType {
		case "rds":
			raw = job.RDSInterval
		case "redis":
			raw = job.RedisInterval
		}
		n, err := raw.Int64()
		if err != nil || n <= 0 {
			return fmt.Errorf("%s_interval must be a positive integer", resourceType)
		}
		intervals[resourceType] = int(n)
	}
	if job.Vendor == "pve" {
		if err := s.overlayPVECredentials(ctx, job); err != nil {
			return err
		}
	} else if err := s.overlayCloudCredentials(ctx, job); err != nil {
		return err
	}

	specs, err := s.loadJobSpecs(r)
	if err != nil {
		return err
	}
	byID := make(map[string]scheduler.JobSpec, len(specs))
	for _, spec := range specs {
		byID[spec.ID] = spec
	}

	// The resource-group task must exist; the legacy Python crashes when it
	// does not, Go returns an explicit error instead.
	groupID := job.Vendor + "/" + job.Account + "/group"
	group, ok := byID[groupID]
	if !ok {
		return fmt.Errorf("group task %s not found; the account must keep its resource-group task", groupID)
	}
	if group.Minutes != int(proj) {
		group.Minutes = int(proj)
		if err := s.store.PutKV(ctx, "ConsulManager/jobs/"+groupID, group); err != nil {
			return fmt.Errorf("store group task %s: %w", groupID, err)
		}
		if s.jobs != nil {
			if err := s.jobs.UpdateInterval(groupID, int(proj)); err != nil {
				return fmt.Errorf("update group task interval %s: %w", groupID, err)
			}
		}
	}

	for _, resourceType := range []string{"ecs", "rds", "redis"} {
		id := job.Vendor + "/" + job.Account + "/" + resourceType + "/" + job.Region
		if !slices.Contains(job.ResourceTypes, resourceType) {
			if s.jobs != nil {
				s.jobs.Remove(id)
			}
			if err := s.store.DeleteKV(ctx, "ConsulManager/jobs/"+id); err != nil {
				return fmt.Errorf("delete task %s: %w", id, err)
			}
			continue
		}
		if err := s.upsertEditedTask(ctx, byID, job, resourceType, id, intervals[resourceType]); err != nil {
			return err
		}
	}
	return nil
}

// upsertEditedTask updates an existing task in place (interval, and for ECS
// also isextip) or creates a missing selected task without running it now.
func (s *Server) upsertEditedTask(ctx context.Context, byID map[string]scheduler.JobSpec, job editJob, resourceType, id string, interval int) error {
	if spec, ok := byID[id]; ok {
		minutesChanged := spec.Minutes != interval
		argsChanged := false
		if resourceType == "ecs" {
			account, region := spec.Account(), spec.Region()
			if account == "" {
				account = job.Account
			}
			if region == "" || region == "none" {
				region = job.Region
			}
			args := []any{account, region}
			for len(args) < 3 {
				args = append(args, false)
			}
			if len(spec.Args) != 3 || spec.ExternalIP() != job.ExternalIP {
				args[2] = job.ExternalIP
				argsChanged = true
			}
			spec.Args = args
		}
		if !minutesChanged && !argsChanged {
			return nil
		}
		spec.Minutes = interval
		if err := s.store.PutKV(ctx, "ConsulManager/jobs/"+id, spec); err != nil {
			return fmt.Errorf("store task %s: %w", id, err)
		}
		if s.jobs == nil {
			return nil
		}
		if argsChanged {
			// Replacing the runner reschedules from now, matching the legacy
			// modify_job with new args.
			if err := s.jobs.Upsert(ctx, spec); err != nil {
				return fmt.Errorf("reschedule task %s: %w", id, err)
			}
			return nil
		}
		if err := s.jobs.UpdateInterval(id, interval); err != nil {
			return fmt.Errorf("update task interval %s: %w", id, err)
		}
		return nil
	}
	// Selected but missing: create the task and write its KV, but do not run
	// it immediately (Upsert only schedules the next interval fire).
	spec := scheduler.JobSpec{
		ID:      id,
		Func:    "__main__:" + job.Vendor + "." + resourceType,
		Args:    []any{job.Account, job.Region},
		Minutes: interval, Trigger: "interval", ReplaceExisting: true,
	}
	if resourceType == "ecs" {
		spec.Args = append(spec.Args, job.ExternalIP)
	}
	if err := s.store.PutKV(ctx, "ConsulManager/jobs/"+id, spec); err != nil {
		return fmt.Errorf("store task %s: %w", id, err)
	}
	if s.jobs != nil {
		if err := s.jobs.Upsert(ctx, spec); err != nil {
			return fmt.Errorf("schedule task %s: %w", id, err)
		}
	}
	return nil
}

// overlayPVECredentials overwrites only the PVE credential fields present in
// the request; absent fields keep the stored values. A missing tls_verify
// must not reset the stored value to true.
func (s *Server) overlayPVECredentials(ctx context.Context, job editJob) error {
	if job.APIBaseURL == nil && job.TokenID == nil && job.TokenSecret == nil && job.TLSVerify == nil {
		return nil
	}
	if job.APIBaseURL != nil && *job.APIBaseURL == "" {
		return fmt.Errorf("api_base_url must not be empty when updating PVE credentials")
	}
	if job.TokenID != nil && *job.TokenID == "" {
		return fmt.Errorf("token_id must not be empty when updating PVE credentials")
	}
	if job.TokenSecret != nil && *job.TokenSecret == "" {
		return fmt.Errorf("token_secret must not be empty when updating PVE credentials")
	}
	path := "ConsulManager/assets/pve/api/" + job.Account
	creds := map[string]any{}
	found, err := s.store.GetValue(ctx, path, &creds)
	if err != nil {
		return fmt.Errorf("read PVE credentials: %w", err)
	}
	if !found || creds == nil {
		return fmt.Errorf("PVE account %q is not configured", job.Account)
	}
	if job.APIBaseURL != nil {
		creds["api_base_url"] = *job.APIBaseURL
	}
	if job.TokenID != nil {
		creds["token_id"] = *job.TokenID
	}
	if job.TokenSecret != nil {
		encrypted, err := s.cipher.Encrypt(*job.TokenSecret)
		if err != nil {
			return fmt.Errorf("encrypt PVE token secret: %w", err)
		}
		creds["token_secret"] = encrypted
	}
	if job.TLSVerify != nil {
		creds["tls_verify"] = *job.TLSVerify
	}
	if err := s.store.PutKV(ctx, path, creds); err != nil {
		return fmt.Errorf("store PVE credentials: %w", err)
	}
	return nil
}

// overlayCloudCredentials mirrors the legacy put_aksk semantics for the four
// cloud vendors. With akskswitch on, ConsulManager/assets/<vendor>/aksk/<account>
// is overwritten with freshly AES-encrypted AK/SK in the same shape
// accounts.Creator.storeCredentials writes and the sync layer reads; with the
// switch off the stored credentials keep their values. An enabled switch with
// an empty AK or SK is an explicit error: silently persisting empty values
// would break the next sync.
func (s *Server) overlayCloudCredentials(ctx context.Context, job editJob) error {
	if !job.AKSwitch {
		return nil
	}
	if job.AK == "" || job.SK == "" {
		return fmt.Errorf("ak and sk must not be empty when akskswitch is on")
	}
	ak, err := s.cipher.Encrypt(job.AK)
	if err != nil {
		return fmt.Errorf("encrypt access key: %w", err)
	}
	sk, err := s.cipher.Encrypt(job.SK)
	if err != nil {
		return fmt.Errorf("encrypt secret key: %w", err)
	}
	path := "ConsulManager/assets/" + job.Vendor + "/aksk/" + job.Account
	if err := s.store.PutKV(ctx, path, map[string]string{"ak": ak, "sk": sk}); err != nil {
		return fmt.Errorf("store cloud credentials: %w", err)
	}
	return nil
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

// loadThirdPartyHosts reconstructs the full cross-account edit baseline so a
// subsequent replacement cannot silently remove hosts in another account.
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
		account := strings.TrimSpace(service.Meta["account"])
		if account == "" {
			account = defaultAccount
		}
		hosts = append(hosts, thirdPartyHost{Name: service.Meta["name"], Spec: service.Meta["cpu"], IP: service.Address, Port: service.Port, Account: account, Project: service.Meta["project"]})
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
