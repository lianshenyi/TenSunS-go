package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	syncservice "github.com/lianshenyi/TenSunS-go/internal/sync"
)

// pveEditFixture seeds a real PVE account through POST /api/jobs (the create
// chain) so the edit tests operate on the exact KV shape production writes.
func pveEditFixture(t *testing.T, restype []string) *fixture {
	t.Helper()
	f := newFixtureWithProvider(t, nil, func(st *consul.Store, cipher *cryptox.Cipher) syncservice.Service {
		return syncservice.NewPVE(st, cipher, nil)
	})
	token := f.login(t)
	_, body := f.request(t, http.MethodPost, "/api/jobs", token, map[string]any{
		"job_dict": map[string]any{
			"dialogStatus": "create", "vendor": "pve", "account": "demo",
			"api_base_url": "https://pve.example:8006/api2/json", "token_id": "old-token!id", "token_secret": "old-secret",
			"tls_verify": false, "restype": restype, "region": []string{"lab-cluster"},
			"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20, "isextip": false,
		},
	})
	if body["code"].(float64) != 20000 {
		t.Fatalf("setup create = %v", body)
	}
	return f
}

func pveCreds(t *testing.T, f *fixture) map[string]any {
	t.Helper()
	creds := map[string]any{}
	found, err := f.store.GetValue(context.Background(), "ConsulManager/assets/pve/api/demo", &creds)
	if err != nil || !found {
		t.Fatalf("pve credentials found=%v err=%v", found, err)
	}
	return creds
}

func jobKV(t *testing.T, f *fixture, id string) map[string]any {
	t.Helper()
	raw, ok := f.mock.GetKV("ConsulManager/jobs/" + id)
	if !ok {
		t.Fatalf("missing job KV %s", id)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode job KV %s: %v", id, err)
	}
	return out
}

func editCommit(t *testing.T, f *fixture, token string, job map[string]any) map[string]any {
	t.Helper()
	_, body := f.request(t, http.MethodPost, "/api/edit/commit", token, map[string]any{"editJob": job})
	return body
}

func TestPVEEditOverwritesCredentialsWhenSwitchOn(t *testing.T) {
	f := pveEditFixture(t, []string{"group", "ecs"})
	token := f.login(t)
	body := editCommit(t, f, token, map[string]any{
		"vendor": "pve", "account": "demo", "region": "lab-cluster",
		"restype": []string{"group", "ecs"}, "isextip": false,
		"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": false, "ak": "", "sk": "",
		"api_base_url": "https://new.pve.example:8006/api2/json",
		"token_id":     "new-token!id", "token_secret": "new-secret", "tls_verify": true,
	})
	if body["code"].(float64) != 20000 || body["data"] != "pve/demo/lab-cluster：编辑成功！" {
		t.Fatalf("edit response = %v", body)
	}
	if strings.Contains(body["data"].(string), "new-secret") {
		t.Fatalf("token secret echoed in response: %v", body["data"])
	}
	creds := pveCreds(t, f)
	if creds["api_base_url"] != "https://new.pve.example:8006/api2/json" || creds["token_id"] != "new-token!id" {
		t.Fatalf("pve credentials = %#v", creds)
	}
	if creds["tls_verify"] != true {
		t.Fatalf("tls_verify not overwritten: %#v", creds)
	}
	if plain, err := f.cipher.Decrypt(creds["token_secret"].(string)); err != nil || plain != "new-secret" {
		t.Fatalf("token secret plaintext=%q err=%v", plain, err)
	}
	if creds["cluster"] != "lab-cluster" {
		t.Fatalf("cluster field lost: %#v", creds)
	}
}

func TestPVEEditKeepsCredentialsWhenSwitchOff(t *testing.T) {
	f := pveEditFixture(t, []string{"group", "ecs"})
	token := f.login(t)
	// Switch off: the frontend deletes all four PVE fields from the payload.
	// Intervals arrive as strings from the plain-text inputs; isextip changes.
	body := editCommit(t, f, token, map[string]any{
		"vendor": "pve", "account": "demo", "region": "lab-cluster",
		"restype": []string{"group", "ecs"}, "isextip": true,
		"proj_interval": "60", "ecs_interval": "25", "rds_interval": "20", "redis_interval": "20",
		"akskswitch": false, "ak": "", "sk": "",
	})
	if body["code"].(float64) != 20000 {
		t.Fatalf("edit response = %v", body)
	}
	creds := pveCreds(t, f)
	if creds["api_base_url"] != "https://pve.example:8006/api2/json" || creds["token_id"] != "old-token!id" {
		t.Fatalf("credentials overwritten without switch: %#v", creds)
	}
	if plain, err := f.cipher.Decrypt(creds["token_secret"].(string)); err != nil || plain != "old-secret" {
		t.Fatalf("token secret plaintext=%q err=%v", plain, err)
	}
	// tls_verify was stored as false and must not be reset to true.
	if creds["tls_verify"] != false {
		t.Fatalf("tls_verify reset without explicit value: %#v", creds)
	}
	ecs := jobKV(t, f, "pve/demo/ecs/lab-cluster")
	if ecs["minutes"].(float64) != 25 {
		t.Fatalf("ecs minutes = %v", ecs["minutes"])
	}
	args := ecs["args"].([]any)
	if len(args) != 3 || args[0] != "demo" || args[1] != "lab-cluster" || args[2] != true {
		t.Fatalf("ecs args = %#v", args)
	}
	next, ok := f.sched.NextRun("pve/demo/ecs/lab-cluster")
	if !ok || !next.Equal(time.Date(2026, 10, 23, 14, 25, 0, 0, time.Local)) {
		t.Fatalf("ecs next run = %v ok=%v", next, ok)
	}
}

func TestPVEEditCreatesMissingTaskWithoutRunning(t *testing.T) {
	f := pveEditFixture(t, []string{"group"})
	token := f.login(t)
	body := editCommit(t, f, token, map[string]any{
		"vendor": "pve", "account": "demo", "region": "lab-cluster",
		"restype": []string{"group", "ecs"}, "isextip": false,
		"proj_interval": 60, "ecs_interval": 15, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": false, "ak": "", "sk": "",
	})
	if body["code"].(float64) != 20000 {
		t.Fatalf("edit response = %v", body)
	}
	ecs := jobKV(t, f, "pve/demo/ecs/lab-cluster")
	if ecs["id"] != "pve/demo/ecs/lab-cluster" || ecs["func"] != "__main__:pve.ecs" || ecs["minutes"].(float64) != 15 {
		t.Fatalf("new ecs task = %#v", ecs)
	}
	args := ecs["args"].([]any)
	if len(args) != 3 || args[0] != "demo" || args[1] != "lab-cluster" || args[2] != false {
		t.Fatalf("new ecs args = %#v", args)
	}
	if next, ok := f.sched.NextRun("pve/demo/ecs/lab-cluster"); !ok || !next.Equal(time.Date(2026, 10, 23, 14, 15, 0, 0, time.Local)) {
		t.Fatalf("new ecs task not scheduled: next=%v ok=%v", next, ok)
	}
	// Created but not executed immediately: no execution record exists.
	if _, ok := f.mock.GetKV("ConsulManager/record/jobs/pve/demo/ecs/lab-cluster"); ok {
		t.Fatal("new task was executed immediately")
	}
}

func TestPVEEditRemovesUncheckedTasks(t *testing.T) {
	f := pveEditFixture(t, []string{"group", "ecs"})
	token := f.login(t)
	body := editCommit(t, f, token, map[string]any{
		"vendor": "pve", "account": "demo", "region": "lab-cluster",
		"restype": []string{"group"}, "isextip": false,
		"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": false, "ak": "", "sk": "",
	})
	if body["code"].(float64) != 20000 {
		t.Fatalf("edit response = %v", body)
	}
	if _, ok := f.mock.GetKV("ConsulManager/jobs/pve/demo/ecs/lab-cluster"); ok {
		t.Fatal("unchecked ecs task KV still exists")
	}
	if _, ok := f.sched.NextRun("pve/demo/ecs/lab-cluster"); ok {
		t.Fatal("unchecked ecs task still scheduled")
	}
	// The group task is kept.
	if _, ok := f.mock.GetKV("ConsulManager/jobs/pve/demo/group"); !ok {
		t.Fatal("group task KV was removed")
	}
	if _, ok := f.sched.NextRun("pve/demo/group"); !ok {
		t.Fatal("group task no longer scheduled")
	}
}

func TestPVEEditRejectsMissingGroupTask(t *testing.T) {
	f := pveEditFixture(t, []string{"group", "ecs"})
	token := f.login(t)
	_, deleted := f.request(t, http.MethodDelete, "/api/jobs?job_id=pve%2Fdemo%2Fgroup", token, nil)
	if deleted["code"].(float64) != 20000 {
		t.Fatalf("delete group task = %v", deleted)
	}
	body := editCommit(t, f, token, map[string]any{
		"vendor": "pve", "account": "demo", "region": "lab-cluster",
		"restype": []string{"group", "ecs"}, "isextip": false,
		"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": false, "ak": "", "sk": "",
	})
	if body["code"].(float64) != 50000 {
		t.Fatalf("edit without group task = %v", body)
	}
}

// TestEditCommitKeepsCloudPlaceholder is the regression assertion for the
// four cloud vendors: they must still return the migration placeholder.
func TestEditCommitKeepsCloudPlaceholder(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	body := editCommit(t, f, token, map[string]any{
		"vendor": "alicloud", "account": "main", "region": "cn-hangzhou",
		"restype": []string{"group", "ecs", "rds", "redis"}, "isextip": true,
		"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": true, "ak": "access", "sk": "secret",
	})
	if body["code"].(float64) != 50000 || body["data"] != "Go 迁移阶段尚未实现：编辑云资源需要云适配器与任务创建流程。" {
		t.Fatalf("cloud edit response = %v", body)
	}
	ecs := jobKV(t, f, "alicloud/main/ecs/cn-hangzhou")
	if ecs["minutes"].(float64) != 10 {
		t.Fatalf("cloud task mutated: %#v", ecs)
	}
	if _, ok := f.mock.GetKV("ConsulManager/assets/alicloud/aksk/main"); ok {
		t.Fatal("cloud credentials were written")
	}
}
