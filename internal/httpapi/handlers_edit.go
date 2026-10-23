package httpapi

import (
	"net/http"
	"strings"
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
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 50000,
			"data": "Go 迁移阶段尚未实现：编辑云资源需要云适配器与任务创建流程。",
		})
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) editCloud(w http.ResponseWriter, r *http.Request) {
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
