package pve

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestTLSVerificationModes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/resources" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer server.Close()

	base := Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "token-secret"}
	defaultClient, err := NewClient(base, IPSelection{}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultClient.ListQEMUVMs(context.Background()); err == nil {
		t.Fatal("default TLS verification accepted a self-signed certificate")
	}

	trustedClient, err := NewClient(base, IPSelection{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trustedClient.ListQEMUVMs(context.Background()); err != nil {
		t.Fatalf("trusted private CA request failed: %v", err)
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	disabled := false
	insecureClient, err := NewClient(Credentials{APIBaseURL: base.APIBaseURL, TokenID: base.TokenID, TokenSecret: base.TokenSecret, TLSVerify: &disabled}, IPSelection{}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insecureClient.ListQEMUVMs(context.Background()); err != nil {
		t.Fatalf("explicit TLS override did not accept self-signed HTTPS: %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "TLS certificate verification disabled") || strings.Contains(got, base.TokenSecret) || strings.Contains(got, base.TokenID) || strings.Contains(got, "Authorization") {
		t.Fatalf("TLS override warning leaked credentials or was missing: %q", got)
	}

	if defaultClient.http.Transport == http.DefaultTransport || insecureClient.http.Transport == http.DefaultTransport || defaultClient.http.Transport == insecureClient.http.Transport {
		t.Fatal("PVE accounts must use independent cloned transports")
	}
	_, err = NewClient(Credentials{APIBaseURL: "http://pve.example:8006/api2/json", TokenID: "id", TokenSecret: "secret", TLSVerify: &disabled}, IPSelection{}, http.DefaultClient)
	if err == nil {
		t.Fatal("TLS override accepted a cleartext HTTP API URL")
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
