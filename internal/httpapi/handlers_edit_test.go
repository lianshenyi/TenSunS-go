package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
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

// cloudVendors drives one httptest per scenario for each of the four cloud
// vendors, using the stable vendor keys from the config registry. The alicloud
// entry uses a region the demo seed does not contain, so "create missing task"
// assertions start from a clean slate.
var cloudVendors = []struct{ vendor, account, region string }{
	{"alicloud", "main", "cn-shanghai"},
	{"awscloud", "main", "us-east-1"},
	{"huaweicloud", "main", "cn-north-4"},
	{"tencent_cloud", "main", "ap-shanghai"},
}

// cloudEditFixture seeds a cloud account's job KVs in the exact shapes the
// create chain writes (accounts.taskSpecs) and registers them in the in-memory
// scheduler, so edit assertions cover both Consul and the scheduler. seedCreds
// also stores an old AK/SK pair to prove overwrite and keep-unchanged
// semantics.
func cloudEditFixture(t *testing.T, vendor, account, region string, restype []string, seedCreds bool) *fixture {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	specs := []scheduler.JobSpec{{
		ID: vendor + "/" + account + "/group", Func: "__main__:" + vendor + ".group",
		Args: []any{account}, Minutes: 60, Trigger: "interval", ReplaceExisting: true,
	}}
	for _, resourceType := range []string{"ecs", "rds", "redis"} {
		if !slices.Contains(restype, resourceType) {
			continue
		}
		args := []any{account, region}
		if resourceType == "ecs" {
			args = append(args, false)
		}
		specs = append(specs, scheduler.JobSpec{
			ID:   vendor + "/" + account + "/" + resourceType + "/" + region,
			Func: "__main__:" + vendor + "." + resourceType, Args: args, Minutes: 10,
			Trigger: "interval", ReplaceExisting: true,
		})
	}
	for _, spec := range specs {
		if err := f.store.PutKV(ctx, "ConsulManager/jobs/"+spec.ID, spec); err != nil {
			t.Fatalf("seed job %s: %v", spec.ID, err)
		}
		if err := f.sched.Upsert(ctx, spec); err != nil {
			t.Fatalf("seed scheduler %s: %v", spec.ID, err)
		}
	}
	if seedCreds {
		oldAK, err := f.cipher.Encrypt("old-access")
		if err != nil {
			t.Fatal(err)
		}
		oldSK, err := f.cipher.Encrypt("old-secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutKV(ctx, "ConsulManager/assets/"+vendor+"/aksk/"+account, map[string]string{"ak": oldAK, "sk": oldSK}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// cloudCreds returns the decrypted AK/SK pair stored for a cloud account.
func cloudCreds(t *testing.T, f *fixture, vendor, account string) (string, string) {
	t.Helper()
	stored := map[string]string{}
	found, err := f.store.GetValue(context.Background(), "ConsulManager/assets/"+vendor+"/aksk/"+account, &stored)
	if err != nil || !found {
		t.Fatalf("cloud credentials found=%v err=%v", found, err)
	}
	ak, err := f.cipher.Decrypt(stored["ak"])
	if err != nil {
		t.Fatalf("decrypt ak: %v", err)
	}
	sk, err := f.cipher.Decrypt(stored["sk"])
	if err != nil {
		t.Fatalf("decrypt sk: %v", err)
	}
	return ak, sk
}

// cloudEditJob builds the editJob payload the frontend sends for a cloud
// vendor. Intervals stay strings to keep the plain-text interval input path
// covered.
func cloudEditJob(vendor, account, region string, restype []string, isextip, akskswitch bool, ak, sk string) map[string]any {
	return map[string]any{
		"vendor": vendor, "account": account, "region": region,
		"restype": restype, "isextip": isextip,
		"proj_interval": "60", "ecs_interval": "25", "rds_interval": "20", "redis_interval": "20",
		"akskswitch": akskswitch, "ak": ak, "sk": sk,
	}
}

func TestCloudEditOverwritesCredentialsWhenSwitchOn(t *testing.T) {
	for _, c := range cloudVendors {
		t.Run(c.vendor, func(t *testing.T) {
			f := cloudEditFixture(t, c.vendor, c.account, c.region, []string{"group", "ecs"}, true)
			token := f.login(t)
			body := editCommit(t, f, token, cloudEditJob(c.vendor, c.account, c.region, []string{"group", "ecs"}, false, true, "new-access", "new-secret"))
			if body["code"].(float64) != 20000 || body["data"] != c.vendor+"/"+c.account+"/"+c.region+"：编辑成功！" {
				t.Fatalf("edit response = %v", body)
			}
			ak, sk := cloudCreds(t, f, c.vendor, c.account)
			if ak != "new-access" || sk != "new-secret" {
				t.Fatalf("credentials not overwritten: ak=%q sk=%q", ak, sk)
			}
			// The string interval from the plain-text input is applied.
			if ecs := jobKV(t, f, c.vendor+"/"+c.account+"/ecs/"+c.region); ecs["minutes"].(float64) != 25 {
				t.Fatalf("ecs minutes = %v", ecs["minutes"])
			}
		})
	}
}

func TestCloudEditKeepsCredentialsWhenSwitchOff(t *testing.T) {
	for _, c := range cloudVendors {
		t.Run(c.vendor, func(t *testing.T) {
			f := cloudEditFixture(t, c.vendor, c.account, c.region, []string{"group", "ecs"}, true)
			token := f.login(t)
			body := editCommit(t, f, token, cloudEditJob(c.vendor, c.account, c.region, []string{"group", "ecs"}, false, false, "", ""))
			if body["code"].(float64) != 20000 {
				t.Fatalf("edit response = %v", body)
			}
			ak, sk := cloudCreds(t, f, c.vendor, c.account)
			if ak != "old-access" || sk != "old-secret" {
				t.Fatalf("credentials changed without switch: ak=%q sk=%q", ak, sk)
			}
		})
	}
}

func TestCloudEditRejectsEmptyKeysWhenSwitchOn(t *testing.T) {
	f := cloudEditFixture(t, "alicloud", "main", "cn-shanghai", []string{"group", "ecs"}, true)
	token := f.login(t)
	for _, empty := range []map[string]any{
		cloudEditJob("alicloud", "main", "cn-shanghai", []string{"group", "ecs"}, false, true, "", "secret"),
		cloudEditJob("alicloud", "main", "cn-shanghai", []string{"group", "ecs"}, false, true, "access", ""),
	} {
		body := editCommit(t, f, token, empty)
		if body["code"].(float64) != 50000 {
			t.Fatalf("edit with empty key = %v", body)
		}
		// No silent empty-value write: the stored credentials keep their values.
		ak, sk := cloudCreds(t, f, "alicloud", "main")
		if ak != "old-access" || sk != "old-secret" {
			t.Fatalf("credentials mutated on rejected edit: ak=%q sk=%q", ak, sk)
		}
	}
}

func TestCloudEditCreatesMissingTaskWithoutRunning(t *testing.T) {
	for _, c := range cloudVendors {
		t.Run(c.vendor, func(t *testing.T) {
			f := cloudEditFixture(t, c.vendor, c.account, c.region, []string{"group"}, false)
			token := f.login(t)
			body := editCommit(t, f, token, cloudEditJob(c.vendor, c.account, c.region, []string{"group", "ecs"}, true, false, "", ""))
			if body["code"].(float64) != 20000 {
				t.Fatalf("edit response = %v", body)
			}
			id := c.vendor + "/" + c.account + "/ecs/" + c.region
			ecs := jobKV(t, f, id)
			if ecs["id"] != id || ecs["func"] != "__main__:"+c.vendor+".ecs" || ecs["minutes"].(float64) != 25 {
				t.Fatalf("new ecs task = %#v", ecs)
			}
			args := ecs["args"].([]any)
			if len(args) != 3 || args[0] != c.account || args[1] != c.region || args[2] != true {
				t.Fatalf("new ecs args = %#v", args)
			}
			// Created but not executed immediately: no execution record exists.
			if _, ok := f.mock.GetKV("ConsulManager/record/jobs/" + id); ok {
				t.Fatal("new task was executed immediately")
			}
			if next, ok := f.sched.NextRun(id); !ok || !next.Equal(time.Date(2026, 10, 23, 14, 25, 0, 0, time.Local)) {
				t.Fatalf("new ecs task not scheduled: next=%v ok=%v", next, ok)
			}
		})
	}
}

func TestCloudEditRemovesUncheckedTasks(t *testing.T) {
	for _, c := range cloudVendors {
		t.Run(c.vendor, func(t *testing.T) {
			f := cloudEditFixture(t, c.vendor, c.account, c.region, []string{"group", "ecs"}, false)
			token := f.login(t)
			body := editCommit(t, f, token, cloudEditJob(c.vendor, c.account, c.region, []string{"group"}, false, false, "", ""))
			if body["code"].(float64) != 20000 {
				t.Fatalf("edit response = %v", body)
			}
			id := c.vendor + "/" + c.account + "/ecs/" + c.region
			if _, ok := f.mock.GetKV("ConsulManager/jobs/" + id); ok {
				t.Fatal("unchecked ecs task KV still exists")
			}
			if _, ok := f.sched.NextRun(id); ok {
				t.Fatal("unchecked ecs task still scheduled")
			}
			// The group task is kept.
			groupID := c.vendor + "/" + c.account + "/group"
			if _, ok := f.mock.GetKV("ConsulManager/jobs/" + groupID); !ok {
				t.Fatal("group task KV was removed")
			}
			if _, ok := f.sched.NextRun(groupID); !ok {
				t.Fatal("group task no longer scheduled")
			}
		})
	}
}

// TestEditCommitRejectsUnknownVendor asserts an unknown vendor is an explicit
// error, never the migration placeholder.
func TestEditCommitRejectsUnknownVendor(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	body := editCommit(t, f, token, map[string]any{
		"vendor": "oracle", "account": "main", "region": "eu-1",
		"restype": []string{"group"}, "isextip": false,
		"proj_interval": 60, "ecs_interval": 10, "rds_interval": 20, "redis_interval": 20,
		"akskswitch": false, "ak": "", "sk": "",
	})
	if body["code"].(float64) != 50000 {
		t.Fatalf("unknown vendor response = %v", body)
	}
	if body["data"] == "Go 迁移阶段尚未实现：编辑云资源需要云适配器与任务创建流程。" {
		t.Fatal("unknown vendor fell back to the migration placeholder")
	}
}
