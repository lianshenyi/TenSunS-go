package apicheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSmokeContracts(t *testing.T) {
	tests := []struct {
		name, path, body, failure string
	}{
		{name: "valid"},
		{"login rejected", "/api/user/login", `{"code":40000,"data":"sensitive-body-marker"}`, "/api/user/login"},
		{"avatar differs", "/api/user/info", `{"code":20000,"data":{"name":"admin","roles":["admin"],"avatar":null}}`, "avatar"},
		{"roles differ", "/api/user/info", `{"code":20000,"data":{"name":"admin","roles":["user"],"avatar":"/harbor-mark.svg"}}`, "roles"},
		{"nextime invalid", "/api/jobs", strings.ReplaceAll(validJobs, `"nextime":"无"`, `"nextime":"bad date"`), "nextime"},
		{"interval is a float", "/api/jobs", strings.ReplaceAll(validJobs, `"interval":10`, `"interval":10.0`), "interval"},
		{"on count is a boolean", "/api/jobs", strings.ReplaceAll(validJobs, `"on":0`, `"on":false`), "on must"},
		{"vendor list has duplicates", "/api/jobs", strings.ReplaceAll(validJobs, `"vendor_list":["阿里云"]`, `"vendor_list":["阿里云","阿里云"]`), "vendor_list"},
		{"cloud does not match jobs", "/api/edit/cloud", `{"code":20000,"cloud_dict":{"alicloud":["other"]}}`, "cloud_dict accounts"},
		{"resource field missing", "/api/nodes/res", `{"code":20000,"res_list":[{"iid":"i-test"}]}`, "res_list[0]"},
		{"JMS array null", "/api/jms/list", `{"code":20000,"ecs_list":null}`, "ecs_list"},
		{"JMS sync not boolean", "/api/jms/list", strings.ReplaceAll(validJMS, `"sync":false`, `"sync":0`), "sync"},
		{"JMS secret present", "/api/jms/config", `{"code":20000,"jms_config":{"access_key_secret":"sensitive-body-marker"}}`, "access_key_secret"},
		{"JMS token present even if null", "/api/jms/config", `{"code":20000,"jms_config":{"token":null}}`, "token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := map[string]string{
				"/api/user/login": `{"code":20000,"data":{"token":"Bearer sample"}}`,
				"/api/user/info":  `{"code":20000,"data":{"name":"admin","roles":["admin"],"avatar":"/harbor-mark.svg"}}`,
				"/api/jobs":       validJobs,
				"/api/edit/cloud": `{"code":20000,"cloud_dict":{"alicloud":["main"]}}`,
				"/api/nodes/res":  `{"code":20000,"res_list":[{"iid":"i-test","name":"test","region":"test","account":"main","vendor":"阿里云","stat":"on"}]}`,
				"/api/jms/list":   validJMS,
				"/api/jms/config": `{"code":20000,"jms_config":{}}`,
			}
			if tt.path != "" {
				responses[tt.path] = tt.body
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/user/login" {
					var input map[string]string
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil || r.Method != http.MethodPost ||
						input["username"] != "admin" || input["password"] != "pass" {
						t.Error("unexpected login request")
					}
				} else if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sample" {
					t.Errorf("unexpected authenticated request to %s", r.URL.Path)
				}
				if r.URL.Path == "/api/nodes/res" && r.URL.Query().Get("job_id") != "alicloud/main/ecs/cn-hangzhou" {
					t.Error("resource query did not use the first ECS job ID")
				}
				if body, ok := responses[r.URL.Path]; ok {
					_, _ = w.Write([]byte(body))
				} else {
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := New(server.URL + "/")
			err := client.Login("pass")
			if err == nil {
				err = client.CheckUserInfo(true)
			}
			if err == nil {
				err = client.CheckSmoke()
			}
			if tt.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.failure) || strings.Contains(err.Error(), "sensitive-body-marker") {
				t.Fatalf("got error %v, want a redacted %q error", err, tt.failure)
			}
		})
	}
}

const validJobs = `{"code":20000,"vendor_list":["阿里云"],"account_list":["main"],"itype_list":["ecs"],
"all_jobs":[{"region":"华东1","vendor":"阿里云","account":"main","itype":"ecs","jobid":"alicloud/main/ecs/cn-hangzhou",
"nextime":"无","runtime":"无","interval":10,"on":0,"off":1,"count":"无"}]}`

const validJMS = `{"code":20000,"vendor_list":["阿里云"],"account_list":["main"],"ecs_list":[{"vendor":"阿里云",
"account":"main","count_linux":1,"count_win":0,"count_ecs":1,"count_on":1,"count_off":0,
"count_mem":"8GB","count_cpu":"4核","sync":false,"nextime":"11.19/10:45"}]}`
