package jumpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestV2MockContractPayloadUsesECSInfo(t *testing.T) {
	payload := runContractSync(t, "V2", "linux", map[string]any{
		"ip": "10.0.0.20", "hostname": "app-01", "platform": "Linux", "protocols": []any{"ssh/2222"}, "admin_user": "ops", "domain": "",
	})
	if payload["address"] != "10.0.0.20" || payload["name"] != "app-01" || payload["platform"] != "Linux" {
		t.Fatalf("V2 identity payload = %#v", payload)
	}
	assertJSONField(t, payload, "protocols", []any{"ssh/2222"})
	if payload["admin_user"] != "ops" || payload["domain"] != "" {
		t.Fatalf("V2 access payload = %#v", payload)
	}
}

func TestQAV3PayloadIncludesProtocolsAndAccounts(t *testing.T) {
	payload := runContractSync(t, "V3", "windows", map[string]any{
		"address": "10.0.0.20", "name": "app-01", "platform": "5", "domain": "",
		"protocols": []any{map[string]any{"name": "rdp", "port": float64(3389)}, map[string]any{"name": "sftp", "port": float64(3389)}},
		"accounts":  []any{map[string]any{"template": "windows-template"}},
	})
	assertJSONField(t, payload, "protocols", []any{map[string]any{"name": "rdp", "port": float64(3389)}, map[string]any{"name": "sftp", "port": float64(3389)}})
	assertJSONField(t, payload, "accounts", []any{map[string]any{"template": "windows-template"}})
	if payload["platform"] != "5" || payload["domain"] != "" {
		t.Fatalf("V3 payload = %#v", payload)
	}
}

func TestV2V3AssetCreateUpdateHTTPContracts(t *testing.T) {
	tests := []struct {
		name          string
		version       string
		existing      bool
		wantMethod    string
		wantAssetPath string
	}{
		{name: "v2_create", version: "V2", wantMethod: http.MethodPost, wantAssetPath: "/api/v1/assets/assets/"},
		{name: "v2_update", version: "V2", existing: true, wantMethod: http.MethodPut, wantAssetPath: "/api/v1/assets/asset-1/"},
		{name: "v3_create", version: "V3", wantMethod: http.MethodPost, wantAssetPath: "/api/v1/assets/hosts/"},
		{name: "v3_update", version: "V3", existing: true, wantMethod: http.MethodPut, wantAssetPath: "/api/v1/assets/asset-1/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, path := runAssetHTTPContract(t, tt.version, tt.existing)
			if method != tt.wantMethod || path != tt.wantAssetPath {
				t.Fatalf("asset request = %s %s, want %s %s", method, path, tt.wantMethod, tt.wantAssetPath)
			}
		})
	}
}

// TestQAExistingAssetUsesDocumentedUpdateEndpoint keeps the QA reproduction
// command focused on the two update branches while the matrix above covers all
// create/update combinations.
func TestQAExistingAssetUsesDocumentedUpdateEndpoint(t *testing.T) {
	for _, version := range []string{"V2", "V3"} {
		t.Run(version, func(t *testing.T) {
			method, path := runAssetHTTPContract(t, version, true)
			if method != http.MethodPut || path != "/api/v1/assets/asset-1/" {
				t.Fatalf("asset update = %s %s, want PUT /api/v1/assets/asset-1/", method, path)
			}
		})
	}
}

func runAssetHTTPContract(t *testing.T, version string, existing bool) (string, string) {
	t.Helper()
	var method, path string
	jms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/assets/assets/":
			if existing {
				_, _ = io.WriteString(w, `[{"id":"asset-1","ip":"10.0.0.20"}]`)
				return
			}
			_, _ = io.WriteString(w, "[]")
		case r.Method == http.MethodPost || r.Method == http.MethodPut:
			method, path = r.Method, r.URL.Path
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer jms.Close()

	st, cipher, ctx := setupContractStore(t, jms.URL, version, "linux")
	if err := New(st, cipher, jms.Client()).Run(ctx, "pve", "lab"); err != nil {
		t.Fatal(err)
	}
	if method == "" {
		t.Fatal("JumpServer did not receive an asset write request")
	}
	return method, path
}

func runContractSync(t *testing.T, version, osType string, want map[string]any) map[string]any {
	t.Helper()
	var got map[string]any
	jms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/assets/assets/":
			_, _ = io.WriteString(w, "[]")
		case r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode contract payload: %v", err)
			}
			if version == "V2" && r.URL.Path != "/api/v1/assets/assets/" {
				t.Errorf("V2 create path = %q", r.URL.Path)
			}
			if version == "V3" && r.URL.Path != "/api/v1/assets/hosts/" {
				t.Errorf("V3 create path = %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer jms.Close()

	st, cipher, ctx := setupContractStore(t, jms.URL, version, osType)
	if err := st.PutKV(ctx, "ConsulManager/jms/pve/lab/ecs_info", map[string]any{"linux": []any{[]string{"ssh/2222"}, "ops"}, "windows": []any{[]string{"rdp/3389"}, "windows-template"}}); err != nil {
		t.Fatal(err)
	}
	if err := New(st, cipher, jms.Client()).Run(ctx, "pve", "lab"); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("JumpServer did not receive an asset create request")
	}
	for key, expected := range want {
		if !reflect.DeepEqual(got[key], expected) {
			t.Fatalf("%s = %#v, want %#v; full payload=%#v", key, got[key], expected, got)
		}
	}
	return got
}

func setupContractStore(t *testing.T, jmsURL, version, osType string) (store.Store, *cryptox.Cipher, context.Context) {
	t.Helper()
	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	t.Cleanup(consulServer.Close)
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	accessKeyID, err := cipher.Encrypt("jumpserver-access-key")
	if err != nil {
		t.Fatal(err)
	}
	accessKeySecret, err := cipher.Encrypt("jumpserver-access-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for path, value := range map[string]any{
		"ConsulManager/jms/jms_info":        map[string]any{"url": jmsURL, "auth_type": "access_key", "access_key_id": accessKeyID, "access_key_secret": accessKeySecret, "ver": version},
		"ConsulManager/jms/pve/lab/node_id": map[string]any{"node_id": "node-1"},
		"ConsulManager/jms/ecs_info":        map[string]any{"linux": []any{[]string{"ssh/22"}, "root"}, "windows": []any{[]string{"rdp/3389"}, "windows-template"}},
	} {
		if err := st.PutKV(ctx, path, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Register(ctx, store.ServiceRegistration{ID: "pve-node-a-101", Name: "pve_lab_ecs", Address: "10.0.0.20", Meta: map[string]string{"name": "app-01", "os": osType, "vendor": "Proxmox VE", "region": "lab"}}); err != nil {
		t.Fatal(err)
	}
	return st, cipher, ctx
}

func assertJSONField(t *testing.T, object map[string]any, key string, want any) {
	t.Helper()
	actualJSON, err := json.Marshal(object[key])
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualJSON) != string(wantJSON) {
		t.Fatalf("%s = %s, want %s", key, actualJSON, wantJSON)
	}
}
