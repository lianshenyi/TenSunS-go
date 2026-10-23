package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	stype := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	switch r.Method {
	case http.MethodGet:
		switch stype {
		case "group":
			s.nodeGroup(w, r)
		case "res":
			s.nodeRes(w, r)
		case "jobecs":
			s.jobKeys(w, r, "ecs", "jobecs")
		case "cstecsconf":
			s.customConfig(w, r, "ecs", "cst_ecs")
		case "cstecslist":
			s.customList(w, r, "ecs", "jobecs_name")
		default:
			s.notFound(w, r)
		}
	case http.MethodPost:
		switch stype {
		case "cstecs":
			s.customSavePlaceholder(w, "ECS")
		default:
			s.notFound(w, r)
		}
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) handleRDS(w http.ResponseWriter, r *http.Request) {
	stype := strings.TrimPrefix(r.URL.Path, "/api/rds/")
	switch r.Method {
	case http.MethodGet:
		switch stype {
		case "jobrds":
			s.jobKeys(w, r, "rds", "jobrds")
		case "cstrdsconf":
			s.customConfig(w, r, "rds", "cst_rds")
		case "cstrdslist":
			s.customList(w, r, "rds", "jobrds_name")
		default:
			s.notFound(w, r)
		}
	case http.MethodPost:
		switch stype {
		case "cstrds":
			s.customSavePlaceholder(w, "RDS")
		default:
			s.notFound(w, r)
		}
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) handleRedis(w http.ResponseWriter, r *http.Request) {
	stype := strings.TrimPrefix(r.URL.Path, "/api/redis/")
	switch r.Method {
	case http.MethodGet:
		switch stype {
		case "jobredis":
			s.jobKeys(w, r, "redis", "jobredis")
		case "cstredisconf":
			s.customConfig(w, r, "redis", "cst_redis")
		case "cstredislist":
			s.customList(w, r, "redis", "jobredis_name")
		default:
			s.notFound(w, r)
		}
	case http.MethodPost:
		switch stype {
		case "cstredis":
			s.customSavePlaceholder(w, "Redis")
		default:
			s.notFound(w, r)
		}
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) nodeGroup(w http.ResponseWriter, r *http.Request) {
	jobID := requiredQuery(r, "job_id")
	parts := strings.Split(jobID, "/")
	if len(parts) != 3 {
		s.businessError(w, fmt.Errorf("invalid group job_id %q", jobID))
		return
	}
	groups := map[string]string{}
	found, err := s.store.GetValue(r.Context(), "ConsulManager/assets/"+parts[0]+"/group/"+parts[1], &groups)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if !found {
		groups = map[string]string{}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	list := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		list = append(list, map[string]string{"gid": key, "gname": groups[key]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "group": list})
}

func (s *Server) nodeRes(w http.ResponseWriter, r *http.Request) {
	jobID := requiredQuery(r, "job_id")
	if jobID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "res_list": []map[string]string{}})
		return
	}
	list, err := s.resourceMetas(r, jobID)
	if err != nil {
		s.businessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "res_list": list})
}

func (s *Server) resourceMetas(r *http.Request, jobID string) ([]map[string]string, error) {
	parts := strings.Split(jobID, "/")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid resource job_id %q", jobID)
	}
	vendor, account, itype, region := parts[0], parts[1], parts[2], parts[3]
	filter := fmt.Sprintf("Service == %q and %q in Tags", vendor+"_"+account+"_"+itype, region)
	services, err := s.store.Services(r.Context(), filter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(services))
	for id := range services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	list := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		meta := services[id].Meta
		if meta == nil {
			meta = map[string]string{}
		}
		list = append(list, meta)
	}
	return list, nil
}

func (s *Server) jobKeys(w http.ResponseWriter, r *http.Request, kind, responseKey string) {
	keys, err := s.store.GetKeys(r.Context(), "ConsulManager/jobs")
	if err != nil {
		s.businessError(w, err)
		return
	}
	out := make([]string, 0)
	needle := "/" + kind + "/"
	for _, key := range keys {
		if !strings.Contains(key, needle) {
			continue
		}
		if index := strings.Index(key, "/jobs/"); index >= 0 {
			out = append(out, key[index+len("/jobs/"):])
		}
	}
	sort.Strings(out)
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, responseKey: out})
}

func (s *Server) customConfig(w http.ResponseWriter, r *http.Request, kind, responseKey string) {
	iid := requiredQuery(r, "iid")
	config := map[string]any{}
	found, err := s.store.GetValue(r.Context(), "ConsulManager/assets/sync_"+kind+"_custom/"+iid, &config)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if !found || config == nil {
		config = map[string]any{}
	}
	config["iid"] = iid
	config["ipswitch"] = valueNonEmpty(config["ip"])
	config["portswitch"] = valueNonEmpty(config["port"])
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, responseKey: config})
}

func (s *Server) customList(w http.ResponseWriter, r *http.Request, kind, jobParam string) {
	jobName := requiredQuery(r, jobParam)
	checked := requiredQuery(r, "checked")
	custom, err := s.store.GetKVDict(r.Context(), "ConsulManager/assets/sync_"+kind+"_custom/")
	if err != nil {
		s.businessError(w, err)
		return
	}
	customIDs := map[string]struct{}{}
	for key, raw := range custom {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
			continue
		}
		customIDs[key[strings.LastIndex(key, "/")+1:]] = struct{}{}
	}
	list, err := s.resourceMetas(r, jobName)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if checked == "false" {
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "res_list": list})
		return
	}
	filtered := make([]map[string]string, 0, len(list))
	for _, meta := range list {
		if _, ok := customIDs[meta["iid"]]; ok {
			filtered = append(filtered, meta)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "res_list": filtered})
}

func (s *Server) customSavePlaceholder(w http.ResponseWriter, kind string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 50000,
		"data": "Go 迁移阶段尚未实现：" + kind + " 自定义实例即时改写。",
	})
}

func valueNonEmpty(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	default:
		return true
	}
}
