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

	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	defer consulServer.Close()
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("jumpserver-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for path, value := range map[string]any{
		"ConsulManager/jms/jms_info":         map[string]any{"url": jms.URL, "token": token, "ver": version},
		"ConsulManager/jms/pve/lab/node_id":  map[string]any{"node_id": "node-1"},
		"ConsulManager/jms/ecs_info":         map[string]any{"linux": []any{[]string{"ssh/22"}, "root"}, "windows": []any{[]string{"rdp/3389"}, "windows-template"}},
		"ConsulManager/jms/pve/lab/ecs_info": map[string]any{"linux": []any{[]string{"ssh/2222"}, "ops"}, "windows": []any{[]string{"rdp/3389"}, "windows-template"}},
	} {
		if err := st.PutKV(ctx, path, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Register(ctx, store.ServiceRegistration{ID: "pve-node-a-101", Name: "pve_lab_ecs", Address: "10.0.0.20", Meta: map[string]string{"name": "app-01", "os": osType, "vendor": "Proxmox VE", "region": "lab"}}); err != nil {
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
