package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/auth"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/jobs"
	"github.com/lianshenyi/TenSunS-go/internal/jumpserver"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

// jmsAPIStub is a minimal JumpServer stub for the POST /api/jms/sync handler
// tests. It records every request, the Authorization header it carried, and
// the bodies of asset/node writes, so the test can assert the token travels
// only in the header and never in a payload.
type jmsAPIStub struct {
	mu             sync.Mutex
	requests       []string
	authHeaders    []string
	assetBodies    []map[string]any
	nodeBodies     []map[string]any
	nodes          []map[string]any
	nextNode       int
	failChildren   bool
	failAssetWrite bool
}

func (s *jmsAPIStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	if auth := r.Header.Get("Authorization"); auth != "" {
		s.authHeaders = append(s.authHeaders, auth)
	}
	switch {
	case r.URL.Path == "/api/v1/assets/assets/" && r.Method == http.MethodGet && r.URL.Query().Get("limit") != "":
		writeJSONStub(w, map[string]any{"count": len(s.assetBodies), "results": []any{}})
	case r.URL.Path == "/api/v1/assets/assets/" && r.Method == http.MethodGet:
		writeJSONStub(w, []any{})
	case r.URL.Path == "/api/v1/assets/assets/" && r.Method == http.MethodPost:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.assetBodies = append(s.assetBodies, payload)
		if s.failAssetWrite {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"detail":"asset write exploded"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/api/v1/assets/nodes/children/tree/" && r.Method == http.MethodGet:
		writeJSONStub(w, []any{})
	case strings.HasSuffix(r.URL.Path, "/children/") && r.Method == http.MethodGet:
		if s.failChildren {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"detail":"node listing exploded"}`)
			return
		}
		writeJSONStub(w, s.nodes)
	case strings.HasSuffix(r.URL.Path, "/children/") && r.Method == http.MethodPost:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.nodeBodies = append(s.nodeBodies, payload)
		s.nextNode++
		s.nodes = append(s.nodes, map[string]any{"id": fmt.Sprintf("n-%d", s.nextNode), "value": payload["value"]})
		w.WriteHeader(http.StatusCreated)
	default:
		http.NotFound(w, r)
	}
}

func writeJSONStub(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// jmsAPIFixture wires the full production path: consul mock, a real scheduler
// whose executor is jobs.Runtime{JMS: jumpserver.New(...)} — the same wiring as
// cmd/tensuns/main.go — and the API server.
type jmsAPIFixture struct {
	api   *httptest.Server
	st    *consul.Store
	sched *scheduler.Scheduler
	stub  *jmsAPIStub
}

func newJMSAPIFixture(t *testing.T, stub *jmsAPIStub) *jmsAPIFixture {
	t.Helper()
	jmsServer := httptest.NewServer(stub)
	t.Cleanup(jmsServer.Close)
	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	t.Cleanup(consulServer.Close)
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	token, err := cipher.Encrypt("jumpserver-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutKV(ctx, "ConsulManager/jms/jms_info", map[string]any{
		"url": jmsServer.URL, "token": token, "ver": "V2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutKV(ctx, "ConsulManager/jms/ecs_info", map[string]any{
		"linux":   []any{[]string{"ssh/22"}, "root"},
		"windows": []any{[]string{"rdp/3389"}, "windows-template"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Register(ctx, store.ServiceRegistration{
		ID: "i-1", Name: "alicloud_main_ecs", Address: "10.0.0.20",
		Meta: map[string]string{"name": "app-01", "os": "linux", "group": "prod", "region": "lab", "vendor": "阿里云"},
	}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(&jobs.Runtime{JMS: jumpserver.New(st, cipher, jmsServer.Client())}, logger)
	authSvc := auth.New(testPassword, cryptox.NewTokenSigner("0123456789abcdef0123456789abcdef"))
	apiServer := httptest.NewServer(New(st, cipher, authSvc, sched, logger).Handler())
	t.Cleanup(func() {
		apiServer.Close()
		sched.Close()
	})
	return &jmsAPIFixture{api: apiServer, st: st, sched: sched, stub: stub}
}

func jmsRequest(t *testing.T, api *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, api.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := api.Client().Do(req)
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

func jmsLogin(t *testing.T, api *httptest.Server) string {
	t.Helper()
	status, body := jmsRequest(t, api, http.MethodPost, "/api/user/login", "", map[string]any{
		"username": "admin", "password": testPassword,
	})
	if status != http.StatusOK || body["code"].(float64) != 20000 {
		t.Fatalf("login status=%d body=%v", status, body)
	}
	token, _ := body["data"].(map[string]any)["token"].(string)
	return token
}

func (f *jmsAPIFixture) getValue(t *testing.T, path string, out any) bool {
	t.Helper()
	found, err := f.st.GetValue(context.Background(), path, out)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestJMSSyncRejectsEmptyNodeID(t *testing.T) {
	stub := &jmsAPIStub{}
	f := newJMSAPIFixture(t, stub)
	token := jmsLogin(t, f.api)
	status, body := jmsRequest(t, f.api, http.MethodPost, "/api/jms/sync", token, map[string]any{
		"jms_sync": map[string]any{"vendor": "阿里云", "account": "main", "nodeid": "", "interval": 3},
	})
	if status != http.StatusOK || body["code"].(float64) != 50000 || body["data"] != "新节点ID不能为空" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if f.getValue(t, "ConsulManager/jms/alicloud/main/node_id", &map[string]any{}) {
		t.Fatal("node_id KV was written despite the rejected request")
	}
	if f.getValue(t, "ConsulManager/jms/jobs/alicloud/main", &scheduler.JobSpec{}) {
		t.Fatal("jobs KV was written despite the rejected request")
	}
	if _, ok := f.sched.NextRun("alicloud/main/jms"); ok {
		t.Fatal("scheduler holds a task despite the rejected request")
	}
	if len(stub.requests) != 0 {
		t.Fatalf("JumpServer was contacted despite the rejected request: %v", stub.requests)
	}
}

func TestJMSSyncEndToEndFirstSync(t *testing.T) {
	stub := &jmsAPIStub{}
	f := newJMSAPIFixture(t, stub)
	token := jmsLogin(t, f.api)
	status, body := jmsRequest(t, f.api, http.MethodPost, "/api/jms/sync", token, map[string]any{
		"jms_sync": map[string]any{"vendor": "阿里云", "account": "main", "nodeid": "node-1", "interval": 3},
	})
	if status != http.StatusOK || body["code"].(float64) != 20000 || body["data"] != "【alicloud/main】同步JumpServer功能开启！首次同步完成" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	var node map[string]any
	if !f.getValue(t, "ConsulManager/jms/alicloud/main/node_id", &node) {
		t.Fatal("node_id KV was not written")
	}
	if node["node_id"] != "node-1" || node["interval"] != float64(3) {
		t.Fatalf("node_id KV = %#v", node)
	}
	var job scheduler.JobSpec
	if !f.getValue(t, "ConsulManager/jms/jobs/alicloud/main", &job) {
		t.Fatal("jobs KV was not written")
	}
	wantJob := scheduler.JobSpec{
		ID: "alicloud/main/jms", Func: "__main__:sync_jms.run", Args: []any{"alicloud", "main"},
		Minutes: 3, Trigger: "interval", ReplaceExisting: true,
	}
	if !reflect.DeepEqual(job, wantJob) {
		t.Fatalf("jobs KV = %+v, want %+v", job, wantJob)
	}
	if _, ok := f.sched.NextRun("alicloud/main/jms"); !ok {
		t.Fatal("scheduler does not hold the registered task")
	}
	var record map[string]any
	if !f.getValue(t, "ConsulManager/record/jms/alicloud/main", &record) {
		t.Fatal("record KV was not written")
	}
	if record["status"] != float64(20000) || record["count"] != float64(1) || record["msg"] != "同步资源成功！总数：1" {
		t.Fatalf("record = %#v", record)
	}
	// The stub must have received the ungrouped node plus the asset creation.
	if len(stub.assetBodies) != 1 {
		t.Fatalf("asset creations = %d, want 1 (%v)", len(stub.assetBodies), stub.requests)
	}
	asset := stub.assetBodies[0]
	if asset["ip"] != "10.0.0.20" || asset["hostname"] != "app-01" || asset["admin_user"] != "root" || asset["platform"] != "Linux" {
		t.Fatalf("asset payload = %#v", asset)
	}
	nodeCreated := false
	for _, payload := range stub.nodeBodies {
		if payload["value"] == "未分组" {
			nodeCreated = true
		}
	}
	if !nodeCreated {
		t.Fatalf("未分组 node was not created; node bodies = %v", stub.nodeBodies)
	}
	// Credential hygiene: the token appears in the Authorization header and
	// nowhere else.
	if len(stub.authHeaders) == 0 {
		t.Fatal("JumpServer requests carried no Authorization header")
	}
	for _, authHeader := range stub.authHeaders {
		if authHeader != "Token jumpserver-token" {
			t.Fatalf("Authorization header = %q", authHeader)
		}
	}
	written, _ := json.Marshal(append(append([]map[string]any{}, stub.assetBodies...), stub.nodeBodies...))
	if strings.Contains(string(written), "jumpserver-token") {
		t.Fatalf("token leaked into a JumpServer payload: %s", written)
	}
}

func TestJMSSyncFirstSyncFailureRollsBack(t *testing.T) {
	stub := &jmsAPIStub{failChildren: true}
	f := newJMSAPIFixture(t, stub)
	token := jmsLogin(t, f.api)
	status, body := jmsRequest(t, f.api, http.MethodPost, "/api/jms/sync", token, map[string]any{
		"jms_sync": map[string]any{"vendor": "阿里云", "account": "main", "nodeid": "node-1", "interval": 3},
	})
	if status != http.StatusOK || body["code"].(float64) != 50000 || body["data"] != "【alicloud/main】同步功能开启失败，请查看后端日志！" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if f.getValue(t, "ConsulManager/jms/jobs/alicloud/main", &scheduler.JobSpec{}) {
		t.Fatal("jobs KV survived the failed first sync")
	}
	if _, ok := f.sched.NextRun("alicloud/main/jms"); ok {
		t.Fatal("scheduler still holds the task after the failed first sync")
	}
	if !f.getValue(t, "ConsulManager/jms/alicloud/main/node_id", &map[string]any{}) {
		t.Fatal("node_id KV missing; it is written before the first sync runs")
	}
	var record map[string]any
	if !f.getValue(t, "ConsulManager/record/jms/alicloud/main", &record) {
		t.Fatal("failure record was not written")
	}
	if record["status"] != float64(50000) || record["count"] != "失败" || record["msg"] != "同步资源失败！node listing exploded" {
		t.Fatalf("failure record = %#v", record)
	}
}

func TestJMSSyncAssetWriteFailureRollsBackWithoutFalseSuccess(t *testing.T) {
	stub := &jmsAPIStub{failAssetWrite: true}
	f := newJMSAPIFixture(t, stub)
	token := jmsLogin(t, f.api)
	status, body := jmsRequest(t, f.api, http.MethodPost, "/api/jms/sync", token, map[string]any{
		"jms_sync": map[string]any{"vendor": "阿里云", "account": "main", "nodeid": "node-1", "interval": 3},
	})
	if status != http.StatusOK || body["code"].(float64) != 50000 {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if f.getValue(t, "ConsulManager/jms/jobs/alicloud/main", &scheduler.JobSpec{}) {
		t.Fatal("jobs KV survived an asset write failure")
	}
	if _, ok := f.sched.NextRun("alicloud/main/jms"); ok {
		t.Fatal("scheduler retained a task after an asset write failure")
	}
	var record map[string]any
	if !f.getValue(t, "ConsulManager/record/jms/alicloud/main", &record) {
		t.Fatal("failure record was not written")
	}
	if record["status"] != float64(50000) || record["count"] != "失败" || !strings.Contains(stringValue(record["msg"]), "asset write exploded") {
		t.Fatalf("failure record = %#v", record)
	}
}
