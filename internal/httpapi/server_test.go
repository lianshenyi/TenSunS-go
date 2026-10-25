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

	"github.com/lianshenyi/TenSunS-go/internal/accounts"
	"github.com/lianshenyi/TenSunS-go/internal/auth"
	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/jobs"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	syncservice "github.com/lianshenyi/TenSunS-go/internal/sync"
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
	return newFixtureWithProvider(t, nil)
}

type httpFakeProvider struct{}

func (httpFakeProvider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{"group-1": "研发"}, nil
}
func (httpFakeProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return nil, nil
}
func (httpFakeProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}
func (httpFakeProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

func newFixtureWithProvider(t *testing.T, provider cloud.Provider) *fixture {
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
	var api *Server
	if provider == nil {
		api = New(st, cipher, authSvc, sched, logger)
	} else {
		registry := cloud.NewRegistry()
		registry.Register("fake", provider)
		cloudSync := syncservice.NewCloud(registry, st)
		api = NewWithAccountCreator(st, cipher, authSvc, sched, accounts.NewCreator(st, cipher, cloudSync, sched), logger)
	}
	apiServer := httptest.NewServer(api.Handler())
	t.Cleanup(func() {
		apiServer.Close()
		sched.Close()
	})
	return &fixture{api: apiServer, mock: mock, store: st, sched: sched, cipher: cipher}
}

func TestCreateCloudAccountWithFakeProvider(t *testing.T) {
	f := newFixtureWithProvider(t, httpFakeProvider{})
	token := f.login(t)
	_, body := f.request(t, http.MethodPost, "/api/jobs", token, map[string]any{
		"job_dict": map[string]any{
			"dialogStatus": "create", "vendor": "fake", "account": "demo", "ak": "access", "sk": "secret",
			"restype": []string{"group", "ecs", "rds"}, "region": []string{"test-1"}, "proj_interval": 60,
			"ecs_interval": 10, "rds_interval": 20, "redis_interval": 20, "isextip": true,
		},
	})
	if body["code"].(float64) != 20000 {
		t.Fatalf("create response = %v", body)
	}
	var groups map[string]string
	if found, err := f.store.GetValue(context.Background(), "ConsulManager/assets/fake/group/demo", &groups); err != nil || !found || groups["group-1"] != "研发" {
		t.Fatalf("groups found=%v err=%v value=%v", found, err, groups)
	}
	var credentials map[string]string
	if found, err := f.store.GetValue(context.Background(), "ConsulManager/assets/fake/aksk/demo", &credentials); err != nil || !found {
		t.Fatalf("credentials found=%v err=%v", found, err)
	}
	if plain, err := f.cipher.Decrypt(credentials["sk"]); err != nil || plain != "secret" {
		t.Fatalf("credential plaintext=%q err=%v", plain, err)
	}
	for _, id := range []string{"fake/demo/group", "fake/demo/ecs/test-1", "fake/demo/rds/test-1"} {
		if _, found := f.mock.GetKV("ConsulManager/jobs/" + id); !found {
			t.Fatalf("missing task %s", id)
		}
		if _, found := f.sched.NextRun(id); !found {
			t.Fatalf("task not scheduled %s", id)
		}
	}
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
