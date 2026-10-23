package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
)

type jobRow struct {
	Region   string `json:"region"`
	Vendor   string `json:"vendor"`
	Account  string `json:"account"`
	IType    string `json:"itype"`
	Interval int    `json:"interval"`
	JobID    string `json:"jobid"`
	Nextime  string `json:"nextime"`
	On       any    `json:"on"`
	Off      any    `json:"off"`
	Count    any    `json:"count"`
	Runtime  any    `json:"runtime"`
}

type jobsResponse struct {
	Code        int      `json:"code"`
	AllJobs     []jobRow `json:"all_jobs"`
	VendorList  []string `json:"vendor_list"`
	AccountList []string `json:"account_list"`
	ITypeList   []string `json:"itype_list"`
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listJobs(w, r)
	case http.MethodPost:
		s.mutateJob(w, r)
	case http.MethodDelete:
		s.deleteJob(w, r)
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	query, err := queryDict(r)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if vendor := query["vendor"]; vendor != "" {
		if internal, ok := config.InternalVendor(vendor); ok {
			query["vendor"] = internal
		}
	}
	specs, err := s.loadJobSpecs(r)
	if err != nil {
		s.businessError(w, err)
		return
	}
	records, err := s.store.GetKVDict(r.Context(), "ConsulManager/record/jobs")
	if err != nil {
		s.businessError(w, err)
		return
	}

	jobs := make([]jobRow, 0, len(specs))
	vendors := map[string]struct{}{}
	accounts := map[string]struct{}{}
	itypes := map[string]struct{}{}
	for _, spec := range specs {
		parts := strings.Split(spec.ID, "/")
		if len(parts) < 3 {
			s.logger.Warn("skip malformed job id", "id", spec.ID)
			continue
		}
		vendor, account, itype := parts[0], parts[1], parts[2]
		info := map[string]string{"vendor": vendor, "account": account, "itype": itype}
		if !matchesQuery(query, info) {
			continue
		}
		region := spec.Region()
		count := any("无")
		runtime := any("无")
		var on any = 0
		var off any = 0
		if raw, ok := records["ConsulManager/record/jobs/"+spec.ID]; ok {
			var record struct {
				Count  any `json:"count"`
				Update any `json:"update"`
				On     any `json:"on"`
				Off    any `json:"off"`
			}
			if err := json.Unmarshal(raw, &record); err == nil {
				if record.Count != nil {
					count = record.Count
				}
				if record.Update != nil {
					runtime = record.Update
				}
				if record.On != nil {
					on = record.On
				}
				if record.Off != nil {
					off = record.Off
				}
			} else {
				s.logger.Warn("decode job record failed", "id", spec.ID, "error", err)
			}
		}
		nextime := "无"
		if s.jobs != nil {
			if next, ok := s.jobs.NextRun(spec.ID); ok {
				nextime = formatNext(next)
			}
		}
		vendorDisplay := config.VendorDisplay(vendor)
		jobs = append(jobs, jobRow{
			Region:   config.RegionDisplay(vendor, region),
			Vendor:   vendorDisplay,
			Account:  account,
			IType:    itype,
			Interval: spec.Minutes,
			JobID:    spec.ID,
			Nextime:  nextime,
			On:       on,
			Off:      off,
			Count:    count,
			Runtime:  runtime,
		})
		vendors[vendorDisplay] = struct{}{}
		accounts[account] = struct{}{}
		itypes[itype] = struct{}{}
	}
	writeJSON(w, http.StatusOK, jobsResponse{
		Code:        20000,
		AllJobs:     jobs,
		VendorList:  sortedKeys(vendors),
		AccountList: sortedKeys(accounts),
		ITypeList:   sortedKeys(itypes),
	})
}

func (s *Server) mutateJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobDict map[string]any `json:"job_dict"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		s.businessError(w, err)
		return
	}
	status := stringValue(req.JobDict["dialogStatus"])
	switch status {
	case "update":
		jobID := stringValue(req.JobDict["jobid"])
		interval, err := intValue(req.JobDict["interval"])
		if err != nil || jobID == "" || interval <= 0 {
			s.businessError(w, fmt.Errorf("invalid update job request"))
			return
		}
		var raw map[string]any
		found, err := s.store.GetValue(r.Context(), "ConsulManager/jobs/"+jobID, &raw)
		if err != nil {
			s.businessError(w, err)
			return
		}
		if !found || raw == nil {
			s.businessError(w, fmt.Errorf("job %s KV not found", jobID))
			return
		}
		raw["minutes"] = interval
		if err := s.store.PutKV(r.Context(), "ConsulManager/jobs/"+jobID, raw); err != nil {
			s.businessError(w, err)
			return
		}
		if s.jobs != nil {
			if err := s.jobs.UpdateInterval(jobID, interval); err != nil {
				s.businessError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": "更新成功！"})
	case "create":
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 50000,
			"data": "Go 迁移阶段尚未实现：新增云账户任务需要资源组同步适配器。",
		})
	case "run":
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 50000,
			"data": "Go 迁移阶段尚未实现：云同步任务执行器尚未接入。",
		})
	default:
		s.businessError(w, fmt.Errorf("unknown dialogStatus %q", status))
	}
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	jobID := requiredQuery(r, "job_id")
	if jobID == "" {
		s.businessError(w, fmt.Errorf("job_id is empty"))
		return
	}
	if s.jobs != nil {
		s.jobs.Remove(jobID)
	}
	if err := s.store.DeleteKV(r.Context(), "ConsulManager/jobs/"+jobID); err != nil {
		s.businessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": "删除成功！"})
}

func (s *Server) loadJobSpecs(r *http.Request) ([]scheduler.JobSpec, error) {
	rawJobs, err := s.store.GetKVDict(r.Context(), "ConsulManager/jobs")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rawJobs))
	for key := range rawJobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	specs := make([]scheduler.JobSpec, 0, len(keys))
	for _, key := range keys {
		var spec scheduler.JobSpec
		if err := json.Unmarshal(rawJobs[key], &spec); err != nil {
			s.logger.Warn("skip malformed job", "key", key, "error", err)
			continue
		}
		if spec.ID == "" {
			spec.ID = strings.TrimPrefix(key, "ConsulManager/jobs/")
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func matchesQuery(query, info map[string]string) bool {
	for key, expected := range query {
		if expected == "" {
			continue
		}
		if info[key] != expected {
			return false
		}
	}
	return true
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func intValue(v any) (int, error) {
	switch value := v.(type) {
	case float64:
		return int(value), nil
	case int:
		return value, nil
	case json.Number:
		n, err := value.Int64()
		return int(n), err
	case string:
		n, err := strconv.Atoi(value)
		return n, err
	default:
		return 0, fmt.Errorf("not an integer: %T", v)
	}
}

func boolValue(v any) (bool, error) {
	switch value := v.(type) {
	case bool:
		return value, nil
	case string:
		return strconv.ParseBool(value)
	default:
		return false, fmt.Errorf("not a boolean: %T", v)
	}
}
