package pve

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListQEMUVMsSelectsConfiguredIPv4AndToleratesMissingAgent(t *testing.T) {
	var authorization string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api2/json/cluster/resources":
			_, _ = io.WriteString(w, `{"data":[{"type":"qemu","vmid":101,"name":"web","node":"node-a","status":"running","maxcpu":2,"maxmem":2147483648},{"type":"qemu","vmid":102,"name":"off","node":"node-a","status":"stopped","maxcpu":1,"maxmem":1073741824},{"type":"lxc","vmid":103,"name":"skip","node":"node-a","status":"running"}]}`)
		case "/api2/json/nodes/node-a/qemu/101/agent/network-get-interfaces":
			_, _ = io.WriteString(w, `{"data":{"result":[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1"}]},{"name":"eth1","ip-addresses":[{"ip-address":"192.168.1.5"}]},{"name":"eth0","ip-addresses":[{"ip-address":"10.4.5.6"},{"ip-address":"169.254.1.1"}]}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "secret"}, IPSelection{NICPriority: []string{"eth1"}, CIDRPriority: []string{"10.0.0.0/8"}}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	vms, err := client.ListQEMUVMs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "PVEAPIToken=reader@pve!sync=secret" {
		t.Fatalf("authorization = %q", authorization)
	}
	if len(vms) != 2 || vms[0].IP != "10.4.5.6" || vms[1].IP != "" {
		t.Fatalf("VMs = %#v", vms)
	}
}

func TestNewClientRejectsCleartextAPIURL(t *testing.T) {
	_, err := NewClient(Credentials{APIBaseURL: "http://pve.example:8006/api2/json", TokenID: "id", TokenSecret: "secret"}, IPSelection{}, &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{}}})
	if err == nil {
		t.Fatal("NewClient accepted HTTP API URL")
	}
}

func TestQATLSBoundaryRejectsVerificationBypass(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // Deliberately verifies the rejection boundary.
	_, err := NewClient(Credentials{APIBaseURL: "https://pve.example:8006/api2/json", TokenID: "id", TokenSecret: "secret"}, IPSelection{}, client)
	if err == nil {
		t.Fatal("NewClient accepted a client that disables TLS certificate/hostname verification")
	}
}

func TestNewClientRejectsUninspectableTransport(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	_, err := NewClient(Credentials{APIBaseURL: "https://pve.example:8006/api2/json", TokenID: "id", TokenSecret: "secret"}, IPSelection{}, client)
	if err == nil {
		t.Fatal("NewClient accepted an uninspectable transport")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
