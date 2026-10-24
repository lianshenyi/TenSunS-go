package jumpserver

import (
	"context"
	"encoding/json"
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
			if err := client.Run(context.Background(), "alicloud", "main"); err != nil {
				t.Fatal(err)
			}
			if len(jms.assets) != 1 {
				t.Fatalf("rerun duplicated assets: %#v", jms.assets)
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
	childrenCalls, hostPosts, assetPosts, failChildren int
}

func newJMS(t *testing.T) (*httptest.Server, *fakeJMS) {
	state := &fakeJMS{nodes: map[string]string{}, assets: map[string]map[string]any{}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
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
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})), state
}
