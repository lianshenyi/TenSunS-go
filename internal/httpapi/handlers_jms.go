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

type jmsRow struct {
	Vendor     string `json:"vendor"`
	Account    string `json:"account"`
	CountLinux int    `json:"count_linux"`
	CountWin   int    `json:"count_win"`
	CountMem   string `json:"count_mem"`
	CountCPU   string `json:"count_cpu"`
	CountECS   int    `json:"count_ecs"`
	CountOn    int    `json:"count_on"`
	CountOff   int    `json:"count_off"`
	Sync       bool   `json:"sync"`
	CountSync  any    `json:"count_sync"`
	Interval   string `json:"interval"`
	Runtime    any    `json:"runtime"`
	Nextime    string `json:"nextime"`
}

type jmsListResponse struct {
	Code        int      `json:"code"`
	ECSList     []jmsRow `json:"ecs_list"`
	VendorList  []string `json:"vendor_list"`
	AccountList []string `json:"account_list"`
}

func (s *Server) handleJMS(w http.ResponseWriter, r *http.Request) {
	stype := strings.TrimPrefix(r.URL.Path, "/api/jms/")
	switch r.Method {
	case http.MethodGet:
		switch stype {
		case "list":
			s.jmsList(w, r)
		case "config":
			s.jmsConfigGet(w, r)
		default:
			s.notFound(w, r)
		}
	case http.MethodPost:
		switch stype {
		case "config":
			s.jmsConfigPost(w, r)
		case "switch":
			s.jmsSwitch(w, r)
		case "sync":
			s.jmsSyncPlaceholder(w)
		default:
			s.notFound(w, r)
		}
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) jmsList(w http.ResponseWriter, r *http.Request) {
	var jmsInfo map[string]any
	found, err := s.store.GetValue(r.Context(), "ConsulManager/jms/jms_info", &jmsInfo)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if !found || len(jmsInfo) == 0 {
		writeJSON(w, http.StatusOK, jmsListResponse{
			Code:        20000,
			ECSList:     []jmsRow{},
			VendorList:  []string{},
			AccountList: []string{},
		})
		return
	}
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
	keys, err := s.store.GetKeys(r.Context(), "ConsulManager/jobs")
	if err != nil {
		s.businessError(w, err)
		return
	}
	groupKeys := make([]string, 0)
	for _, key := range keys {
		if strings.HasSuffix(key, "/group") {
			groupKeys = append(groupKeys, key)
		}
	}
	sort.Strings(groupKeys)

	rows := make([]jmsRow, 0, len(groupKeys))
	vendors := map[string]struct{}{}
	accounts := map[string]struct{}{}
	for _, key := range groupKeys {
		parts := strings.Split(key, "/")
		if len(parts) < 5 {
			continue
		}
		vendor, account := parts[2], parts[3]
		info := map[string]string{"vendor": vendor, "account": account}
		if !matchesQuery(query, info) {
			continue
		}
		services, err := s.store.Services(r.Context(), fmt.Sprintf("Service == %q", vendor+"_"+account+"_ecs"))
		if err != nil {
			s.businessError(w, err)
			return
		}
		var countLinux, countWin, countOn, countOff, countCPU int
		var countMem float64
		hasMem := false
		for _, service := range services {
			meta := service.Meta
			switch meta["os"] {
			case "linux":
				countLinux++
			case "windows":
				countWin++
			}
			if meta["stat"] == "off" {
				countOff++
			} else {
				countOn++
			}
			if cpu, ok := parseIntegerSuffix(meta["cpu"], "核"); ok {
				countCPU += cpu
			}
			if mem, ok := parseFloatSuffix(meta["mem"], "GB"); ok {
				countMem += mem
				hasMem = true
			}
		}
		memDisplay := "0GB"
		if hasMem {
			memDisplay = formatPythonFloat(countMem) + "GB"
		}

		row := jmsRow{
			Vendor:     config.VendorDisplay(vendor),
			Account:    account,
			CountLinux: countLinux,
			CountWin:   countWin,
			CountMem:   memDisplay,
			CountCPU:   strconv.Itoa(countCPU) + "核",
			CountECS:   len(services),
			CountOn:    countOn,
			CountOff:   countOff,
			Sync:       false,
			CountSync:  "无",
			Interval:   "无",
			Runtime:    "无",
			Nextime:    "无",
		}
		var job scheduler.JobSpec
		found, err := s.store.GetValue(r.Context(), "ConsulManager/jms/jobs/"+vendor+"/"+account, &job)
		if err != nil {
			s.businessError(w, err)
			return
		}
		if found && job.Minutes > 0 {
			row.Sync = true
			row.Interval = strconv.Itoa(job.Minutes) + "分钟"
			var record map[string]any
			recordFound, err := s.store.GetValue(r.Context(), "ConsulManager/record/jms/"+vendor+"/"+account, &record)
			if err != nil {
				s.businessError(w, err)
				return
			}
			if recordFound {
				if v, ok := record["update"]; ok {
					row.Runtime = v
				}
				if v, ok := record["count"]; ok {
					row.CountSync = v
				}
			}
			if s.jobs != nil {
				if next, ok := s.jobs.NextRun(vendor + "/" + account + "/jms"); ok {
					row.Nextime = formatNext(next)
				}
			}
		}
		rows = append(rows, row)
		vendors[row.Vendor] = struct{}{}
		accounts[account] = struct{}{}
	}
	writeJSON(w, http.StatusOK, jmsListResponse{
		Code:        20000,
		ECSList:     rows,
		VendorList:  sortedKeys(vendors),
		AccountList: sortedKeys(accounts),
	})
}

func (s *Server) jmsConfigGet(w http.ResponseWriter, r *http.Request) {
	var jmsInfo map[string]any
	jmsFound, err := s.store.GetValue(r.Context(), "ConsulManager/jms/jms_info", &jmsInfo)
	if err != nil {
		s.businessError(w, err)
		return
	}
	var ecsInfo map[string]any
	ecsFound, err := s.store.GetValue(r.Context(), "ConsulManager/jms/ecs_info", &ecsInfo)
	if err != nil {
		s.businessError(w, err)
		return
	}
	if !jmsFound || !ecsFound || len(jmsInfo) == 0 || len(ecsInfo) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "jms_config": map[string]any{}})
		return
	}
	var custom map[string]any
	_, _ = s.store.GetValue(r.Context(), "ConsulManager/jms/custom_ecs_info", &custom)
	encryptedToken := stringValue(jmsInfo["token"])
	token, err := s.cipher.Decrypt(encryptedToken)
	if err != nil {
		s.businessError(w, fmt.Errorf("decrypt jms token: %w", err))
		return
	}
	customJSON := ""
	if len(custom) > 0 {
		raw, err := json.MarshalIndent(custom, "", "        ")
		if err != nil {
			s.businessError(w, err)
			return
		}
		customJSON = string(raw)
	}
	ver := stringValue(jmsInfo["ver"])
	if ver == "" {
		ver = "V2"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 20000,
		"jms_config": map[string]any{
			"url":             stringValue(jmsInfo["url"]),
			"token":           token,
			"ver":             ver,
			"linuxport":       protocolPort(ecsInfo["linux"]),
			"linuxuid":        managementUser(ecsInfo["linux"]),
			"winport":         protocolPort(ecsInfo["windows"]),
			"winuid":          managementUser(ecsInfo["windows"]),
			"custom_ecs_info": customJSON,
		},
	})
}

func (s *Server) jmsConfigPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JMSConfig map[string]any `json:"jms_config"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		s.businessError(w, err)
		return
	}
	cfg := req.JMSConfig
	if cfg == nil {
		s.businessError(w, fmt.Errorf("jms_config is missing"))
		return
	}
	customJSON := stringValue(cfg["custom_ecs_info"])
	custom := map[string]any{}
	if customJSON != "" {
		if err := json.Unmarshal([]byte(customJSON), &custom); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"code": 50000, "data": "Json解析错误，请检查！"})
			return
		}
	}
	encrypted, err := s.cipher.Encrypt(stringValue(cfg["token"]))
	if err != nil {
		s.businessError(w, err)
		return
	}
	ver := stringValue(cfg["ver"])
	if ver == "" {
		ver = "V2"
	}
	jmsInfo := map[string]any{
		"url":   stringValue(cfg["url"]),
		"token": encrypted,
		"ver":   ver,
	}
	ecsInfo := map[string]any{
		"linux":   []any{[]string{"ssh/" + stringValue(cfg["linuxport"])}, stringValue(cfg["linuxuid"])},
		"windows": []any{[]string{"rdp/" + stringValue(cfg["winport"])}, stringValue(cfg["winuid"])},
	}
	if err := s.store.PutKV(r.Context(), "ConsulManager/jms/jms_info", jmsInfo); err != nil {
		s.businessError(w, err)
		return
	}
	if err := s.store.PutKV(r.Context(), "ConsulManager/jms/ecs_info", ecsInfo); err != nil {
		s.businessError(w, err)
		return
	}
	if err := s.store.PutKV(r.Context(), "ConsulManager/jms/custom_ecs_info", custom); err != nil {
		s.businessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": "配置完成"})
}

func (s *Server) jmsSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SwitchDict map[string]any `json:"switch_dict"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		s.businessError(w, err)
		return
	}
	vendorDisplay := stringValue(req.SwitchDict["vendor"])
	vendor, ok := config.InternalVendor(vendorDisplay)
	if !ok {
		s.businessError(w, fmt.Errorf("unknown vendor %q", vendorDisplay))
		return
	}
	account := stringValue(req.SwitchDict["account"])
	sync, err := boolValue(req.SwitchDict["sync"])
	if err != nil {
		s.businessError(w, err)
		return
	}
	if sync {
		var node map[string]any
		found, err := s.store.GetValue(r.Context(), "ConsulManager/jms/"+vendor+"/"+account+"/node_id", &node)
		if err != nil {
			s.businessError(w, err)
			return
		}
		interval := any(3)
		nodeID := ""
		if found && node != nil {
			if v, ok := node["interval"]; ok {
				interval = v
			}
			nodeID = stringValue(node["node_id"])
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code":     20000,
			"interval": interval,
			"nodeid":   nodeID,
		})
		return
	}
	if s.jobs != nil {
		s.jobs.Remove(vendor + "/" + account + "/jms")
	}
	if err := s.store.DeleteKV(r.Context(), "ConsulManager/jms/jobs/"+vendor+"/"+account); err != nil {
		s.businessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": fmt.Sprintf("【%s/%s】同步功能关闭！", vendor, account)})
}

func (s *Server) jmsSyncPlaceholder(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 50000,
		"data": "Go 迁移阶段尚未实现：JumpServer V2/V3 同步器尚未接入。",
	})
}

func protocolPort(value any) string {
	arr, ok := value.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	protocols, ok := arr[0].([]any)
	if !ok || len(protocols) == 0 {
		return ""
	}
	protocol := stringValue(protocols[0])
	if index := strings.LastIndex(protocol, "/"); index >= 0 {
		return protocol[index+1:]
	}
	return protocol
}

func managementUser(value any) string {
	arr, ok := value.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	return stringValue(arr[len(arr)-1])
}

func parseIntegerSuffix(value, suffix string) (int, bool) {
	if !strings.HasSuffix(value, suffix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(value, suffix))
	return n, err == nil
}

func parseFloatSuffix(value, suffix string) (float64, bool) {
	if !strings.HasSuffix(value, suffix) {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(value, suffix), 64)
	return n, err == nil
}

func formatPythonFloat(value float64) string {
	out := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(out, ".") {
		out += ".0"
	}
	return out
}
