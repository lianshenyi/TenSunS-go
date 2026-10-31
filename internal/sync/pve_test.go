package sync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestPVEECSSyncRegistersUsableGuestIPsAndRecordsUnavailable(t *testing.T) {
	pveServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/cluster/resources":
			_, _ = io.WriteString(w, `{"data":[{"type":"qemu","vmid":101,"name":"app-01","node":"node-a","status":"running","maxcpu":4,"maxmem":8589934592},{"type":"qemu","vmid":102,"name":"no-agent","node":"node-a","status":"running","maxcpu":2,"maxmem":4294967296}]}`)
		case "/api2/json/nodes/node-a/qemu/101/agent/network-get-interfaces":
			_, _ = io.WriteString(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.20"}]}]}}`)
		case "/api2/json/nodes/node-a/qemu/102/agent/network-get-interfaces":
			http.Error(w, "guest agent unavailable", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pveServer.Close()

	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	defer consulServer.Close()
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := cipher.Encrypt("pve-token-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutKV(context.Background(), "ConsulManager/assets/pve/api/lab", map[string]any{"api_base_url": pveServer.URL + "/api2/json", "token_id": "reader@pve!sync", "token_secret": secret, "cluster": "lab-cluster", "ip_selection": map[string]any{"cidr_priority": []string{"10.0.0.0/8"}}}); err != nil {
		t.Fatal(err)
	}
	service := NewPVE(st, cipher, pveServer.Client())
	if err := service.ECS(context.Background(), "pve", "lab", "lab-cluster", false); err != nil {
		t.Fatal(err)
	}
	services, err := st.Services(context.Background(), `Service == "pve_lab_ecs"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("service count = %d: %#v", len(services), services)
	}
	vm := services["pve-node-a-101"]
	if vm.Address != "10.0.0.20" || vm.Meta["group"] != "node-a" || vm.Meta["vendor"] != "Proxmox VE" {
		t.Fatalf("registered VM = %#v", vm)
	}
	var record map[string]any
	found, err := st.GetValue(context.Background(), "ConsulManager/record/jobs/pve/lab/ecs/lab-cluster", &record)
	if err != nil || !found {
		t.Fatalf("record found=%v err=%v", found, err)
	}
	if record["count"] != float64(2) || record["ip_unavailable"] != float64(1) {
		raw, _ := json.Marshal(record)
		t.Fatalf("record = %s", raw)
	}
}
