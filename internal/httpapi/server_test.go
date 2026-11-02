package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/auth"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/jobs"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

const testPassword = "test-password"

type fixture struct {
	api    *httptest.Server
	mock   *consulmock.Server
	store  *consul.Store
	sched  *scheduler.Scheduler
	cipher *cryptox.Cipher
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mock := consulmock.New()
	if err := mock.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	consulServer := httptest.NewServer(mock.Handler())
	t.Cleanup(consulServer.Close)
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(scheduler.ExecutorFunc(func(context.Context, scheduler.JobSpec) error { return nil }), logger)
	sched.SetClock(func() time.Time { return time.Date(2026, 10, 23, 14, 0, 0, 0, time.Local) })
	if _, _, err := jobs.LoadFromConsul(context.Background(), st, sched, logger); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.New(testPassword, cryptox.NewTokenSigner("0123456789abcdef0123456789abcdef"))
	apiServer := httptest.NewServer(New(st, cipher, authSvc, sched, logger).Handler())
	t.Cleanup(func() {
		apiServer.Close()
		sched.Close()
	})
	return &fixture{api: apiServer, mock: mock, store: st, sched: sched, cipher: cipher}
}

func (f *fixture) request(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.api.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return resp.StatusCode, out
}

func (f *fixture) login(t *testing.T) string {
	t.Helper()
	status, body := f.request(t, http.MethodPost, "/api/user/login", "", map[string]any{
		"username": "admin", "password": testPassword,
	})
	if status != http.StatusOK || body["code"].(float64) != 20000 {
		t.Fatalf("login status=%d body=%v", status, body)
	}
	data := body["data"].(map[string]any)
	token, _ := data["token"].(string)
	if !strings.HasPrefix(token, "Bearer ") {
		t.Fatalf("token missing Bearer prefix: %q", token)
	}
	return token
}

func TestLoginAndAuthContract(t *testing.T) {
	f := newFixture(t)
	status, body := f.request(t, http.MethodPost, "/api/user/login", "", map[string]any{
		"username": "admin", "password": "wrong",
	})
	if status != http.StatusOK || body["code"].(float64) != 40000 || body["data"] != "密码错误！" {
		t.Fatalf("wrong password response: %d %v", status, body)
	}
	token := f.login(t)
	status, body = f.request(t, http.MethodGet, "/api/user/info", token, nil)
	if status != http.StatusOK || body["code"].(float64) != 20000 {
		t.Fatalf("info response: %d %v", status, body)
	}
	info := body["data"].(map[string]any)
	if info["name"] != "admin" || info["avatar"] != "/harbor-mark.svg" {
		t.Fatalf("info = %v", info)
	}
	status, body = f.request(t, http.MethodGet, "/api/user/info", "", nil)
	if status != http.StatusOK || body["code"].(float64) != 50000 {
		t.Fatalf("unauthenticated info: %d %v", status, body)
	}
	status, body = f.request(t, http.MethodPost, "/api/user/logout", "", nil)
	if status != http.StatusOK || body["code"].(float64) != 20000 {
		t.Fatalf("logout: %d %v", status, body)
	}
}

func TestJobsContract(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	status, body := f.request(t, http.MethodGet, `/api/jobs?query_dict=%7B%22vendor%22%3A%22%22%2C%22account%22%3A%22%22%2C%22itype%22%3A%22%22%7D`, token, nil)
	if status != http.StatusOK || body["code"].(float64) != 20000 {
		t.Fatalf("jobs: %d %v", status, body)
	}
	allJobs := body["all_jobs"].([]any)
	if len(allJobs) != 4 {
		t.Fatalf("all_jobs len = %d", len(allJobs))
	}
	var ecs map[string]any
	for _, item := range allJobs {
		row := item.(map[string]any)
		if row["jobid"] == "alicloud/main/ecs/cn-hangzhou" {
			ecs = row
		}
	}
	if ecs == nil {
		t.Fatal("ECS job missing")
	}
	if ecs["vendor"] != "阿里云" || ecs["region"] != "华东1(杭州)" || ecs["interval"].(float64) != 10 {
		t.Fatalf("ECS job = %v", ecs)
	}
	if ecs["count"].(float64) != 2 || ecs["on"].(float64) != 1 || ecs["off"].(float64) != 1 {
		t.Fatalf("ECS record = %v", ecs)
	}
	if ecs["runtime"] != "10.23/14:12" || ecs["nextime"] != "10.23/14:10" {
		t.Fatalf("times = runtime %v nextime %v", ecs["runtime"], ecs["nextime"])
	}
	if got := body["vendor_list"].([]any); len(got) != 1 || got[0] != "阿里云" {
		t.Fatalf("vendor_list = %v", got)
	}
	if got := body["itype_list"].([]any); len(got) != 4 {
		t.Fatalf("itype_list = %v", got)
	}

	_, filtered := f.request(t, http.MethodGet, `/api/jobs?query_dict=%7B%22vendor%22%3A%22%E9%98%BF%E9%87%8C%E4%BA%91%22%2C%22account%22%3A%22%22%2C%22itype%22%3A%22ecs%22%7D`, token, nil)
	if got := filtered["all_jobs"].([]any); len(got) != 1 {
		t.Fatalf("filtered jobs = %v", got)
	}
	_, empty := f.request(t, http.MethodGet, `/api/jobs?query_dict=%7B%22vendor%22%3A%22%22%2C%22account%22%3A%22missing%22%2C%22itype%22%3A%22%22%7D`, token, nil)
	if got := empty["all_jobs"].([]any); len(got) != 0 {
		t.Fatalf("empty filter jobs = %v", got)
	}
}

func TestResourceAndEditReadContracts(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	_, cloud := f.request(t, http.MethodGet, "/api/edit/cloud", token, nil)
	accounts := cloud["cloud_dict"].(map[string]any)["alicloud"].([]any)
	if len(accounts) != 1 || accounts[0] != "main" {
		t.Fatalf("cloud_dict = %v", cloud["cloud_dict"])
	}
	_, find := f.request(t, http.MethodGet, "/api/edit/find?vendor=alicloud&account=main&region=cn-hangzhou", token, nil)
	restype := find["restype"].([]any)
	if len(restype) != 4 || restype[0] != "group" || restype[3] != "redis" {
		t.Fatalf("restype = %v", restype)
	}
	intervals := find["interval"].(map[string]any)
	if intervals["proj_interval"].(float64) != 60 || intervals["ecs_interval"].(float64) != 10 {
		t.Fatalf("intervals = %v", intervals)
	}

	_, res := f.request(t, http.MethodGet, "/api/nodes/res?job_id=alicloud%2Fmain%2Fecs%2Fcn-hangzhou", token, nil)
	resList := res["res_list"].([]any)
	if len(resList) != 2 {
		t.Fatalf("res_list = %v", resList)
	}
	if resList[0].(map[string]any)["iid"] != "i-demo0001" {
		t.Fatalf("first ECS = %v", resList[0])
	}
	_, empty := f.request(t, http.MethodGet, "/api/nodes/res?job_id=", token, nil)
	if len(empty["res_list"].([]any)) != 0 {
		t.Fatalf("empty res_list = %v", empty)
	}
	_, ecsJobs := f.request(t, http.MethodGet, "/api/nodes/jobecs", token, nil)
	if got := ecsJobs["jobecs"].([]any); len(got) != 1 || got[0] != "alicloud/main/ecs/cn-hangzhou" {
		t.Fatalf("jobecs = %v", got)
	}
	_, rdsJobs := f.request(t, http.MethodGet, "/api/rds/jobrds", token, nil)
	if got := rdsJobs["jobrds"].([]any); len(got) != 1 {
		t.Fatalf("jobrds = %v", got)
	}

	if err := f.mock.SetKV("ConsulManager/assets/sync_ecs_custom/i-demo0001", map[string]any{"ip": "10.0.0.15", "port": 2222}); err != nil {
		t.Fatal(err)
	}
	_, custom := f.request(t, http.MethodGet, "/api/nodes/cstecsconf?iid=i-demo0001", token, nil)
	cfg := custom["cst_ecs"].(map[string]any)
	if cfg["iid"] != "i-demo0001" || cfg["ip"] != "10.0.0.15" || cfg["port"].(float64) != 2222 || cfg["ipswitch"] != true || cfg["portswitch"] != true {
		t.Fatalf("custom config = %v", cfg)
	}
	_, customList := f.request(t, http.MethodGet, "/api/nodes/cstecslist?jobecs_name=alicloud%2Fmain%2Fecs%2Fcn-hangzhou&checked=true", token, nil)
	if got := customList["res_list"].([]any); len(got) != 1 {
		t.Fatalf("custom list = %v", got)
	}
	_, allList := f.request(t, http.MethodGet, "/api/nodes/cstecslist?jobecs_name=alicloud%2Fmain%2Fecs%2Fcn-hangzhou&checked=false", token, nil)
	if got := allList["res_list"].([]any); len(got) != 2 {
		t.Fatalf("all list = %v", got)
	}
}

func TestThirdPartyCommitValidation(t *testing.T) {
	validHost := func() map[string]any {
		return map[string]any{"name": "edge-1", "spec": "2C4G", "ip": "2001:db8::1", "port": 9100}
	}
	tests := []struct {
		name  string
		hosts []map[string]any
		want  string
	}{
		{name: "missing name", hosts: []map[string]any{{"spec": "2C4G", "ip": "10.0.0.1", "port": 9100}}, want: "名称、规格、IP和端口不能为空"},
		{name: "invalid IP", hosts: []map[string]any{{"name": "edge-1", "spec": "2C4G", "ip": "bad", "port": 9100}}, want: "IP格式错误"},
		{name: "invalid port", hosts: []map[string]any{{"name": "edge-1", "spec": "2C4G", "ip": "10.0.0.1", "port": 65536}}, want: "端口必须在1-65535之间"},
		{name: "duplicate name", hosts: []map[string]any{validHost(), validHost()}, want: "机器名称重复"},
		{name: "duplicate endpoint", hosts: []map[string]any{validHost(), {"name": "edge-2", "spec": "4C8G", "ip": "2001:0db8:0:0:0:0:0:1", "port": 9100}}, want: "机器IP和端口重复"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			_, body := f.request(t, http.MethodPost, "/api/edit/commit", f.login(t), map[string]any{"vendor": "third_party", "hosts": test.hosts})
			if body["code"] != float64(40000) || !strings.Contains(body["data"].(string), test.want) {
				t.Fatalf("response = %v", body)
			}
		})
	}
}

func TestThirdPartyCommitRequiresAuthentication(t *testing.T) {
	f := newFixture(t)
	status, body := f.request(t, http.MethodPost, "/api/edit/commit", "", map[string]any{"vendor": "third_party", "hosts": []any{}})
	if status != http.StatusOK || body["code"] != float64(50000) {
		t.Fatalf("unauthenticated response = %d %v", status, body)
	}
}

func TestThirdPartyCommitReplacesCompleteCollection(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	hosts := []map[string]any{
		{"name": "edge-v6", "spec": "2C4G", "ip": "2001:db8::1", "port": 9100, "project": "合作方"},
		{"name": "edge-v4", "spec": "4C8G", "ip": "10.0.0.2", "port": 2222, "account": "partner"},
	}
	_, saved := f.request(t, http.MethodPost, "/api/edit/commit", token, map[string]any{"vendor": "third_party", "hosts": hosts})
	if saved["code"] != float64(20000) || saved["data"].(map[string]any)["count"] != float64(2) {
		t.Fatalf("save response = %v", saved)
	}
	services, err := f.store.Services(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	v6ID := thirdPartyServiceID("edge-v6")
	v6 := services[v6ID]
	if v6.Name != "third_party_default_ecs" || v6.Meta["region"] != "global" || v6.Meta["instance"] != "[2001:db8::1]:9100" {
		t.Fatalf("IPv6 service = %#v", v6)
	}
	if _, found := f.mock.GetKV("ConsulManager/jobs/third_party/default/ecs/global"); found {
		t.Fatal("third-party commit must not create a job")
	}
	_, replaced := f.request(t, http.MethodPost, "/api/edit/commit", token, map[string]any{"vendor": "third_party", "hosts": hosts[:1]})
	if replaced["code"] != float64(20000) {
		t.Fatalf("replace response = %v", replaced)
	}
	services, err = f.store.Services(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := services[thirdPartyServiceID("edge-v4")]; found {
		t.Fatal("stale third-party service was not removed")
	}
	_, resources := f.request(t, http.MethodGet, "/api/nodes/res?job_id=third_party%2Fdefault%2Fecs%2Fglobal", token, nil)
	rows := resources["res_list"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "edge-v6" {
		t.Fatalf("third-party resources = %v", rows)
	}
}

func TestJMSContracts(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	_, list := f.request(t, http.MethodGet, `/api/jms/list?query_dict=%7B%22vendor%22%3A%22%22%2C%22account%22%3A%22%22%7D`, token, nil)
	rows := list["ecs_list"].([]any)
	if len(rows) != 1 {
		t.Fatalf("ecs_list = %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["vendor"] != "阿里云" || row["account"] != "main" {
		t.Fatalf("row = %v", row)
	}
	if row["count_linux"].(float64) != 1 || row["count_win"].(float64) != 1 || row["count_ecs"].(float64) != 2 {
		t.Fatalf("counts = %v", row)
	}
	if row["count_mem"] != "24.0GB" || row["count_cpu"] != "12核" || row["count_on"].(float64) != 1 || row["count_off"].(float64) != 1 {
		t.Fatalf("resource totals = %v", row)
	}
	if row["sync"] != true || row["interval"] != "3分钟" || row["runtime"] != "1023/14:10" || row["nextime"] != "10.23/14:03" {
		t.Fatalf("JMS job fields = %v", row)
	}
	if row["count_sync"].(float64) != 2 {
		t.Fatalf("count_sync = %v", row["count_sync"])
	}

	_, cfg := f.request(t, http.MethodGet, "/api/jms/config", token, nil)
	jmsCfg := cfg["jms_config"].(map[string]any)
	if jmsCfg["url"] != "https://jumpserver.example.com" || jmsCfg["token"] != "demo-jumpserver-admin-token" || jmsCfg["ver"] != "V3" {
		t.Fatalf("jms config = %v", jmsCfg)
	}
	if jmsCfg["linuxport"] != "22" || jmsCfg["linuxuid"] != "root" || jmsCfg["winport"] != "3389" {
		t.Fatalf("jms ecs info = %v", jmsCfg)
	}

	_, switched := f.request(t, http.MethodPost, "/api/jms/switch", token, map[string]any{
		"switch_dict": map[string]any{"vendor": "阿里云", "account": "main", "sync": true},
	})
	if switched["nodeid"] != "00000000-0000-0000-0000-000000000001" || switched["interval"].(float64) != 3 {
		t.Fatalf("switch = %v", switched)
	}

	customJSON := `{"prod-web": {"linux": [["ssh/2222"], "root"]}}`
	_, saved := f.request(t, http.MethodPost, "/api/jms/config", token, map[string]any{
		"jms_config": map[string]any{
			"url": "https://jumpserver.example.com", "token": "new-token", "ver": "V3",
			"linuxport": "22", "linuxuid": "root", "winport": "3389", "winuid": "Administrator",
			"custom_ecs_info": customJSON,
		},
	})
	if saved["code"].(float64) != 20000 || saved["data"] != "配置完成" {
		t.Fatalf("save config = %v", saved)
	}
	_, reread := f.request(t, http.MethodGet, "/api/jms/config", token, nil)
	if reread["jms_config"].(map[string]any)["token"] != "new-token" {
		t.Fatalf("token did not round trip: %v", reread)
	}
	raw, ok := f.mock.GetKV("ConsulManager/jms/jms_info")
	if !ok || bytes.Contains(raw, []byte("new-token")) {
		t.Fatalf("JMS token was not encrypted in Consul: %s", raw)
	}
}

func TestJobUpdateAndDelete(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	_, updated := f.request(t, http.MethodPost, "/api/jobs", token, map[string]any{
		"job_dict": map[string]any{"dialogStatus": "update", "jobid": "alicloud/main/ecs/cn-hangzhou", "interval": "15"},
	})
	if updated["code"].(float64) != 20000 {
		t.Fatalf("update = %v", updated)
	}
	raw, ok := f.mock.GetKV("ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou")
	if !ok || !bytes.Contains(raw, []byte(`"minutes":15`)) {
		t.Fatalf("updated KV = %s", raw)
	}
	next, ok := f.sched.NextRun("alicloud/main/ecs/cn-hangzhou")
	if !ok || !next.Equal(time.Date(2026, 10, 23, 14, 15, 0, 0, time.Local)) {
		t.Fatalf("updated next = %v", next)
	}
	_, deleted := f.request(t, http.MethodDelete, "/api/jobs?job_id=alicloud%2Fmain%2Fecs%2Fcn-hangzhou", token, nil)
	if deleted["code"].(float64) != 20000 {
		t.Fatalf("delete = %v", deleted)
	}
	if _, ok := f.mock.GetKV("ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou"); ok {
		t.Fatal("job KV still exists after delete")
	}
	if _, ok := f.sched.NextRun("alicloud/main/ecs/cn-hangzhou"); ok {
		t.Fatal("job still scheduled after delete")
	}
}
