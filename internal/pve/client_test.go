package pve

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

// TestListQEMUVMsQueriesGuestIPsConcurrently reproduces the production
// timeout: the PVE host adds a fixed latency to every request, and 12 running
// VMs queried serially take 12×latency and blow the frontend's 10s axios
// timeout. The sync must query guest agents with bounded concurrency.
func TestListQEMUVMsQueriesGuestIPsConcurrently(t *testing.T) {
	const vmCount = 12
	var (
		mu      sync.Mutex
		current int
		peak    int
		calls   int
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			vms := make([]string, 0, vmCount)
			for i := 1; i <= vmCount; i++ {
				vms = append(vms, fmt.Sprintf(`{"type":"qemu","vmid":%d,"name":"vm-%d","node":"node-a","status":"running","maxcpu":2,"maxmem":1073741824}`, i, i))
			}
			_, _ = io.WriteString(w, `{"data":[`+strings.Join(vms, ",")+`]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agent/network-get-interfaces") {
			mu.Lock()
			calls++
			current++
			if current > peak {
				peak = current
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				current--
				mu.Unlock()
			}()
			time.Sleep(300 * time.Millisecond)
			// /api2/json/nodes/node-a/qemu/101/agent/network-get-interfaces
			vmid := strings.Split(r.URL.Path, "/")[6]
			_, _ = io.WriteString(w, fmt.Sprintf(`{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.%s"}]}]}}`, vmid))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := NewClient(Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "test-secret"}, IPSelection{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	vms, err := client.ListQEMUVMs(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != vmCount || calls != vmCount {
		t.Fatalf("got %d VMs, %d agent calls", len(vms), calls)
	}
	expected := make(map[int]string, vmCount)
	for i := 1; i <= vmCount; i++ {
		expected[i] = fmt.Sprintf("10.0.0.%d", i)
	}
	for _, vm := range vms {
		if vm.IP != expected[vm.VMID] {
			t.Fatalf("VM %d IP = %q, want %q", vm.VMID, vm.IP, expected[vm.VMID])
		}
	}
	// 12 serial queries at 300ms plus the 300ms inventory call take ~3.9s;
	// bounded concurrency finishes in roughly two waves (~900ms).
	if elapsed >= 2500*time.Millisecond {
		t.Fatalf("guest IP queries took %v, want well under the serial %v", elapsed, (vmCount+1)*300*time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak < 4 {
		t.Fatalf("peak in-flight guest-agent requests = %d, want genuinely concurrent", peak)
	}
	if peak > 8 {
		t.Fatalf("peak in-flight guest-agent requests = %d, want bounded by 8", peak)
	}
}

// TestListQEMUVMsGuestIPTimeoutLogsSummarizedWarnings covers the per-VM
// timeout: a slow guest-agent endpoint must not stall the whole sync, the
// failure must be logged as WARN with node/vmid/name, a large fleet must be
// summarized instead of spamming one line per VM, and no credentials may
// reach the log.
func TestListQEMUVMsGuestIPTimeoutLogsSummarizedWarnings(t *testing.T) {
	const vmCount = 12
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			vms := make([]string, 0, vmCount)
			for i := 1; i <= vmCount; i++ {
				vms = append(vms, fmt.Sprintf(`{"type":"qemu","vmid":%d,"name":"vm-%d","node":"node-a","status":"running","maxcpu":2,"maxmem":1073741824}`, i, i))
			}
			_, _ = io.WriteString(w, `{"data":[`+strings.Join(vms, ",")+`]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agent/network-get-interfaces") {
			time.Sleep(time.Second) // far beyond the 200ms test timeout
			_, _ = io.WriteString(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.1"}]}]}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client, err := NewClient(Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "test-secret"}, IPSelection{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.guestAgentTimeout = 200 * time.Millisecond

	start := time.Now()
	vms, err := client.ListQEMUVMs(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != vmCount {
		t.Fatalf("got %d VMs, want %d", len(vms), vmCount)
	}
	for _, vm := range vms {
		if vm.IP != "" {
			t.Fatalf("VM %d IP = %q, want empty after timeout", vm.VMID, vm.IP)
		}
	}
	// 12 timeouts of 200ms over 8 workers take roughly two waves; a serial
	// run would take 12s because every endpoint sleeps for a full second.
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("timed-out sync took %v, want bounded by the per-VM timeout", elapsed)
	}
	logText := logs.String()
	if got := strings.Count(logText, "PVE guest-agent IP query failed"); got != guestAgentMaxIndividualFailures {
		t.Fatalf("individual WARN lines = %d, want %d; log:\n%s", got, guestAgentMaxIndividualFailures, logText)
	}
	if !strings.Contains(logText, "skipped=7") {
		t.Fatalf("summary line missing; log:\n%s", logText)
	}
	for _, want := range []string{"node=node-a", "vmid=1", "vm=vm-1", "http_status=0", "context deadline exceeded"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("WARN missing %q; log:\n%s", want, logText)
		}
	}
	if strings.Contains(logText, "test-secret") || strings.Contains(logText, "reader@pve!sync") || strings.Contains(logText, "Authorization") {
		t.Fatalf("WARN leaked credentials; log:\n%s", logText)
	}
}

// TestListQEMUVMsGuestIPHTTPErrorLogsStatusAndBody covers the diagnostics for
// a non-2xx guest-agent response: the WARN must carry the HTTP status and the
// PVE response body.
func TestListQEMUVMsGuestIPHTTPErrorLogsStatusAndBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api2/json/cluster/resources":
			_, _ = io.WriteString(w, `{"data":[{"type":"qemu","vmid":101,"name":"web","node":"node-a","status":"running","maxcpu":2,"maxmem":1073741824}]}`)
		case strings.HasSuffix(r.URL.Path, "/agent/network-get-interfaces"):
			http.Error(w, `{"data":null,"message":"hostname lookup failure"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client, err := NewClient(Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "test-secret"}, IPSelection{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	vms, err := client.ListQEMUVMs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 || vms[0].IP != "" {
		t.Fatalf("VMs = %#v, want one VM with empty IP", vms)
	}
	logText := logs.String()
	for _, want := range []string{"PVE guest-agent IP query failed", "node=node-a", "vmid=101", "vm=web", "http_status=500", "hostname lookup failure"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("WARN missing %q; log:\n%s", want, logText)
		}
	}
	if strings.Contains(logText, "test-secret") || strings.Contains(logText, "reader@pve!sync") {
		t.Fatalf("WARN leaked credentials; log:\n%s", logText)
	}
}

// TestListQEMUVMsGuestIPQueriesStopOnCanceledContext covers ctx cancellation:
// when the caller cancels mid-sync the pending guest-agent queries must stop
// promptly and must not be logged as per-VM failures.
func TestListQEMUVMsGuestIPQueriesStopOnCanceledContext(t *testing.T) {
	const vmCount = 12
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			vms := make([]string, 0, vmCount)
			for i := 1; i <= vmCount; i++ {
				vms = append(vms, fmt.Sprintf(`{"type":"qemu","vmid":%d,"name":"vm-%d","node":"node-a","status":"running","maxcpu":2,"maxmem":1073741824}`, i, i))
			}
			_, _ = io.WriteString(w, `{"data":[`+strings.Join(vms, ",")+`]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agent/network-get-interfaces") {
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.1"}]}]}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client, err := NewClient(Credentials{APIBaseURL: server.URL + "/api2/json", TokenID: "reader@pve!sync", TokenSecret: "test-secret"}, IPSelection{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	vms, err := client.ListQEMUVMs(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != vmCount {
		t.Fatalf("got %d VMs, want %d", len(vms), vmCount)
	}
	// A serial run would take ~3.9s; cancellation must abort the queries
	// instead of letting them run out their per-VM timeouts.
	if elapsed >= time.Second {
		t.Fatalf("canceled sync took %v, want prompt abort", elapsed)
	}
	if strings.Contains(logs.String(), "PVE guest-agent IP query failed") {
		t.Fatalf("canceled sync logged per-VM failures; log:\n%s", logs.String())
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
