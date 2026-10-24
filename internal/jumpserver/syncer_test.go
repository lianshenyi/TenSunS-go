package jumpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	consulstore "github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestClientSynchronizesV2AndV3(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			client, mock := testClient(t, server.URL, version)
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			jms.mu.Lock()
			if len(jms.assets) != 1 {
				t.Fatalf("assets = %#v", jms.assets)
			}
			if version == "V3" && jms.hostPosts != 1 {
				t.Fatalf("V3 host posts = %d", jms.hostPosts)
			}
			if version == "V2" && jms.assetPosts != 1 {
				t.Fatalf("V2 asset posts = %d", jms.assetPosts)
			}
			jms.mu.Unlock()
			raw, ok := mock.GetKV("ConsulManager/record/jms/alicloud/main")
			if !ok {
				t.Fatal("missing record")
			}
			var record map[string]any
			_ = json.Unmarshal(raw, &record)
			if record["status"] != float64(20000) || record["count"] != float64(1) {
				t.Fatalf("record = %s", raw)
			}
		})
	}
}

func TestClientClassifiesAuthenticationAndRetriesTemporary(t *testing.T) {
	server, jms := newJMS(t)
	defer server.Close()
	jms.failChildren = http.StatusServiceUnavailable
	client, mock := testClient(t, server.URL, "V3")
	if err := client.Run(context.Background(), "alicloud", "main"); err == nil {
		t.Fatal("temporary failure succeeded")
	}
	if jms.childrenCalls != 3 {
		t.Fatalf("temporary calls = %d", jms.childrenCalls)
	}
	raw, _ := mock.GetKV("ConsulManager/record/jms/alicloud/main")
	if !strings.Contains(string(raw), "50000") {
		t.Fatalf("failure record = %s", raw)
	}

	server2, jms2 := newJMS(t)
	defer server2.Close()
	jms2.failChildren = http.StatusUnauthorized
	client, _ = testClient(t, server2.URL, "V3")
	if err := client.Run(context.Background(), "alicloud", "main"); !errorsIs(err, ErrAuthentication) {
		t.Fatalf("auth error = %v", err)
	}
	if jms2.childrenCalls != 1 {
		t.Fatalf("auth calls = %d", jms2.childrenCalls)
	}
}

func TestClientAppliesAccountCustomOverridesInOrder(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			client, _ := testClient(t, server.URL, version)
			put(t, client.Store, "ConsulManager/jms/custom_ecs_info", json.RawMessage(`{"web":{"linux":[["ssh/2200"],"global"]}}`))
			put(t, client.Store, "ConsulManager/jms/alicloud/main/custom_ecs_info", json.RawMessage(`{"web":{"linux":[["ssh/2201"],"first"]},"eb-":{"linux":[["ssh/2222"],"last"]}}`))
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			jms.mu.Lock()
			defer jms.mu.Unlock()
			asset := jms.assets["10.0.0.1"]
			if version == "V3" {
				accounts := asset["accounts"].([]any)
				if accounts[0].(map[string]any)["template"] != "last" {
					t.Fatalf("accounts = %#v", accounts)
				}
				protocols := asset["protocols"].([]any)
				if protocols[0].(map[string]any)["port"] != float64(2222) {
					t.Fatalf("protocols = %#v", protocols)
				}
			} else if asset["admin_user"] != "last" || asset["protocols"].([]any)[0] != "ssh/2222" {
				t.Fatalf("asset = %#v", asset)
			}
		})
	}
}

func TestClientCleansDuplicateIPsAndEmptyNodes(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			client, _ := testClient(t, server.URL, version)
			jms.extraAssets = []map[string]any{
				{"id": "wrong", "address": "10.0.0.1", "name": "stale", "nodes_display": []any{"/parent/生产"}},
				{"id": "right", "address": "10.0.0.1", "name": "web-1", "nodes_display": []any{"/parent/生产"}},
			}
			jms.emptyNodes = []fakeNode{{ID: "v3-empty", Text: "empty (0)", V3: true}, {ID: "v2-empty", Text: "old (0)"}}
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			jms.mu.Lock()
			defer jms.mu.Unlock()
			if jms.deletedAssets["wrong"] != 1 || jms.deletedAssets["right"] != 0 {
				t.Fatalf("duplicate deletion = %#v", jms.deletedAssets)
			}
			if jms.deletedNodes["v3-empty"] != 1 || jms.deletedNodes["v2-empty"] != 1 {
				t.Fatalf("empty node deletion = %#v", jms.deletedNodes)
			}
		})
	}
}

func TestClientPartialFailureRerunsConvergeForV2AndV3(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			jms.failAssetPosts = 3
			client, mock := testClient(t, server.URL, version)
			if err := client.Run(context.Background(), "alicloud", "main"); err == nil {
				t.Fatal("partial failure succeeded")
			}
			if raw, _ := mock.GetKV("ConsulManager/record/jms/alicloud/main"); !strings.Contains(string(raw), "50000") {
				t.Fatalf("failure record = %s", raw)
			}
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			jms.mu.Lock()
			defer jms.mu.Unlock()
			if len(jms.assets) != 1 || jms.assetCalls != 4 {
				t.Fatalf("assets/calls = %#v / %d", jms.assets, jms.assetCalls)
			}
		})
	}
}

func TestClientUpdatesV2AndV3ThroughCanonicalAssetEndpoint(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			jms.strictUpdates = true
			jms.extraAssets = []map[string]any{{
				"id": "right", "address": "10.0.0.1", "name": "old-name",
				"nodes_display": []any{"/parent/旧分组"},
			}}
			client, _ := testClient(t, server.URL, version)
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			jms.mu.Lock()
			defer jms.mu.Unlock()
			if len(jms.updatePaths) != 1 || jms.updatePaths[0] != "/api/v1/assets/right/" {
				t.Fatalf("update paths = %#v", jms.updatePaths)
			}
			payload := jms.updateBodies[0]
			if version == "V3" {
				if payload["address"] != "10.0.0.1" || payload["name"] != "web-1" || payload["accounts"] == nil || payload["platform"] != "1" {
					t.Fatalf("V3 update payload = %#v", payload)
				}
			} else if payload["ip"] != "10.0.0.1" || payload["hostname"] != "web-1" || payload["admin_user"] != "root" || payload["platform"] != "Linux" {
				t.Fatalf("V2 update payload = %#v", payload)
			}
		})
	}
}

func TestClientRetriesRateLimitAndDeadlineButNotForbidden(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		deadline  bool
		wantCalls int
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, wantCalls: 3},
		{name: "deadline", deadline: true, wantCalls: 3},
		{name: "forbidden", status: http.StatusForbidden, wantCalls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, jms := newJMS(t)
			defer server.Close()
			jms.failChildren = tc.status
			client, mock := testClient(t, server.URL, "V3")
			var deadlineCalls int
			if tc.deadline {
				client.HTTP = &http.Client{Transport: deadlineTransport{calls: &deadlineCalls}}
			}
			if err := client.Run(context.Background(), "alicloud", "main"); err == nil {
				t.Fatal("sync unexpectedly succeeded")
			}
			calls := deadlineCalls
			if !tc.deadline {
				jms.mu.Lock()
				calls = jms.childrenCalls
				jms.mu.Unlock()
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			raw, ok := mock.GetKV("ConsulManager/record/jms/alicloud/main")
			if !ok {
				t.Fatal("missing failure record")
			}
			var record map[string]any
			if err := json.Unmarshal(raw, &record); err != nil || record["status"] != float64(50000) {
				t.Fatalf("record = %s err=%v", raw, err)
			}
		})
	}
}

type deadlineTransport struct{ calls *int }

func (t deadlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.calls++
	return nil, context.DeadlineExceeded
}

func testClient(t *testing.T, url, version string) (*Client, *consulmock.Server) {
	t.Helper()
	mock := consulmock.New()
	consul := httptest.NewServer(mock.Handler())
	t.Cleanup(consul.Close)
	st := consulstore.New(consul.URL+"/v1", "", nil)
	cipher, err := cryptox.NewCipher("12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	put(t, st, "ConsulManager/jms/jms_info", map[string]any{"url": url, "token": token, "ver": version})
	put(t, st, "ConsulManager/jms/ecs_info", map[string]any{"linux": []any{[]string{"ssh/22"}, "root"}, "windows": []any{[]string{"rdp/3389"}, "Administrator"}})
	put(t, st, "ConsulManager/jms/alicloud/main/node_id", map[string]any{"node_id": "parent"})
	put(t, st, "ConsulManager/assets/alicloud/group/main", map[string]string{"g1": "生产"})
	if err := st.Register(context.Background(), store.ServiceRegistration{ID: "i-1", Name: "alicloud_main_ecs", Address: "10.0.0.1", Port: 9100, Meta: map[string]string{"name": "web-1", "group": "生产", "os": "linux", "region": "华东1(杭州)"}}); err != nil {
		t.Fatal(err)
	}
	return &Client{Store: st, Cipher: cipher, HTTP: &http.Client{Timeout: time.Second}, Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC) }}, mock
}
func put(t *testing.T, st store.Store, key string, value any) {
	t.Helper()
	if err := st.PutKV(context.Background(), key, value); err != nil {
		t.Fatal(err)
	}
}
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := err.(unwrap)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

type fakeJMS struct {
	mu                                                 sync.Mutex
	nodes                                              map[string]string
	assets                                             map[string]map[string]any
	extraAssets                                        []map[string]any
	emptyNodes                                         []fakeNode
	deletedAssets, deletedNodes                        map[string]int
	childrenCalls, hostPosts, assetPosts, failChildren int
	failAssetPosts                                     int
	assetCalls                                         int
	strictUpdates                                      bool
	updatePaths                                        []string
	updateBodies                                       []map[string]any
}

type fakeNode struct {
	ID, Text string
	V3       bool
}

func newJMS(t *testing.T) (*httptest.Server, *fakeJMS) {
	state := &fakeJMS{nodes: map[string]string{}, assets: map[string]map[string]any{}, deletedAssets: map[string]int{}, deletedNodes: map[string]int{}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if strings.Contains(r.URL.Path, "/nodes/children/tree/") {
			out := make([]map[string]any, 0, len(state.emptyNodes))
			for _, node := range state.emptyNodes {
				meta := map[string]any{"data": map[string]any{"id": node.ID}}
				if node.V3 {
					meta = map[string]any{"node": map[string]any{"id": node.ID}}
				}
				out = append(out, map[string]any{"text": node.Text, "meta": meta})
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if strings.Contains(r.URL.Path, "/children/") {
			state.childrenCalls++
			if state.failChildren != 0 {
				w.WriteHeader(state.failChildren)
				return
			}
			if r.Method == http.MethodPost {
				var b map[string]any
				_ = json.NewDecoder(r.Body).Decode(&b)
				name := b["value"].(string)
				state.nodes[name] = "node-" + name
				w.WriteHeader(http.StatusCreated)
				return
			}
			out := []map[string]any{}
			for n, id := range state.nodes {
				out = append(out, map[string]any{"id": id, "value": n})
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if r.Method == http.MethodPost && (strings.Contains(r.URL.Path, "/assets/hosts/") || r.URL.Path == "/api/v1/assets/assets/") {
			state.assetCalls++
			if state.failAssetPosts > 0 {
				state.failAssetPosts--
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			ip := text(b, "address")
			if ip == "" {
				ip = text(b, "ip")
			}
			if strings.Contains(r.URL.Path, "hosts") {
				state.hostPosts++
			} else {
				state.assetPosts++
			}
			b["id"] = "asset-" + ip
			if _, ok := b["address"]; !ok {
				b["address"] = ip
				b["name"] = b["hostname"]
			}
			b["nodes_display"] = []any{"/parent/生产"}
			state.assets[ip] = b
			w.WriteHeader(http.StatusCreated)
			return
		}
		if strings.Contains(r.URL.Path, "/assets/assets/") && r.Method == http.MethodGet {
			out := []map[string]any{}
			for _, a := range state.assets {
				out = append(out, a)
			}
			out = append(out, state.extraAssets...)
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/assets/assets/") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/assets/assets/"), "/")
			state.deletedAssets[id]++
			filtered := state.extraAssets[:0]
			for _, asset := range state.extraAssets {
				if fmt.Sprint(asset["id"]) != id {
					filtered = append(filtered, asset)
				}
			}
			state.extraAssets = filtered
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/assets/nodes/") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/assets/nodes/"), "/")
			state.deletedNodes[id]++
			remaining := state.emptyNodes[:0]
			for _, node := range state.emptyNodes {
				if node.ID != id {
					remaining = append(remaining, node)
				}
			}
			state.emptyNodes = remaining
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPut {
			if state.strictUpdates && !strings.HasPrefix(r.URL.Path, "/api/v1/assets/") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/api/v1/assets/") || strings.HasPrefix(r.URL.Path, "/api/v1/assets/assets/") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.updatePaths = append(state.updatePaths, r.URL.Path)
			state.updateBodies = append(state.updateBodies, body)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})), state
}
