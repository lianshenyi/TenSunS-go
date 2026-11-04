// Package pve implements the small read-only subset of the Proxmox VE API
// needed to inventory QEMU virtual machines and their guest-agent addresses.
package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// guestAgentConcurrency bounds how many guest-agent IP queries run at once.
// The production PVE host adds a fixed ~3s latency to every request, so a
// serial per-VM query turns a 12-VM sync into ~39s and trips the frontend's
// request timeout. Eight in flight keeps the sync fast without hammering PVE.
const guestAgentConcurrency = 8

// defaultGuestAgentTimeout bounds one guest-agent query. It must stay above
// the ~3s per-request latency observed on the production PVE host.
const defaultGuestAgentTimeout = 5 * time.Second

// guestAgentMaxIndividualFailures caps full WARN lines per sync; any further
// failures are folded into one summary line so a large fleet cannot flood
// the log.
const guestAgentMaxIndividualFailures = 5

// Credentials are the only PVE values required at runtime. TokenSecret must
// never be logged or persisted in plaintext by callers.
type Credentials struct {
	APIBaseURL  string
	TokenID     string
	TokenSecret string
	// TLSVerify is nil for legacy account records and means verification remains
	// enabled. Only an explicit false permits a self-signed HTTPS endpoint.
	TLSVerify *bool
}

// IPSelection controls deterministic IPv4 selection when a guest has several
// interfaces. Empty priority lists fall back to the lexically first usable IP.
type IPSelection struct {
	NICPriority  []string `json:"nic_priority"`
	CIDRPriority []string `json:"cidr_priority"`
}

// VM is the normalized PVE QEMU inventory used by the Consul writer.
type VM struct {
	ID     string
	VMID   int
	Name   string
	Node   string
	Status string
	OSType string
	CPU    int
	Memory uint64
	IP     string
}

type candidate struct{ nic, ip string }

// Client makes authenticated, read-only PVE API requests.
type Client struct {
	base        *url.URL
	tokenID     string
	tokenSecret string
	http        *http.Client
	selection   IPSelection
	// guestAgentTimeout bounds one guest-agent query. NewClient sets the
	// production default; tests shorten it to keep the suite fast.
	guestAgentTimeout time.Duration
}

// NewClient requires an HTTPS API base URL, normally
// https://pve.example:8006/api2/json. Enforcing TLS here prevents a production
// configuration mistake from silently leaking an API token over clear text.
func NewClient(creds Credentials, selection IPSelection, client *http.Client) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(creds.APIBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse PVE API base URL: %w", err)
	}
	if base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("PVE API base URL must be an absolute HTTPS URL")
	}
	if strings.TrimSpace(creds.TokenID) == "" || strings.TrimSpace(creds.TokenSecret) == "" {
		return nil, fmt.Errorf("PVE API token ID and secret are required")
	}
	tlsVerify := creds.TLSVerify == nil || *creds.TLSVerify
	secureClient, err := secureHTTPClient(client, tlsVerify)
	if err != nil {
		return nil, err
	}
	if !tlsVerify {
		// Deliberately log only the destination host. API tokens and secrets must
		// never reach diagnostics, including when TLS verification is overridden.
		slog.Warn("PVE TLS certificate verification disabled", "host", base.Host)
	}
	return &Client{base: base, tokenID: creds.TokenID, tokenSecret: creds.TokenSecret, http: secureClient, selection: selection, guestAgentTimeout: defaultGuestAgentTimeout}, nil
}

// secureHTTPClient makes an independent client and transport for one account.
// A custom RoundTripper cannot be inspected or safely cloned, so it is refused.
func secureHTTPClient(client *http.Client, tlsVerify bool) (*http.Client, error) {
	if client == nil {
		client = http.DefaultClient
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	standardTransport, ok := transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("PVE HTTP client must use a standard transport")
	}
	if tlsVerify && standardTransport.TLSClientConfig != nil && standardTransport.TLSClientConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("PVE HTTP client must not disable TLS certificate or hostname verification")
	}

	copyClient := *client
	copyTransport := standardTransport.Clone()
	if copyTransport.TLSClientConfig != nil {
		copyTransport.TLSClientConfig = copyTransport.TLSClientConfig.Clone()
	} else {
		copyTransport.TLSClientConfig = &tls.Config{}
	}
	copyTransport.TLSClientConfig.InsecureSkipVerify = !tlsVerify //nolint:gosec // REQ-PVE-006 permits this only for an explicit account setting.
	copyClient.Transport = copyTransport
	return &copyClient, nil
}

// ListQEMUVMs lists QEMU VMs and obtains their current agent-reported IPv4.
// A stopped VM or an unavailable guest agent deliberately yields an empty IP;
// it is inventory data, not a failure of the whole account sync.
func (c *Client) ListQEMUVMs(ctx context.Context) ([]VM, error) {
	var inventory struct {
		Data []struct {
			Type   string `json:"type"`
			VMID   int    `json:"vmid"`
			Name   string `json:"name"`
			Node   string `json:"node"`
			Status string `json:"status"`
			CPU    int    `json:"maxcpu"`
			Memory uint64 `json:"maxmem"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/cluster/resources?type=vm", &inventory); err != nil {
		return nil, err
	}
	result := make([]VM, 0, len(inventory.Data))
	for _, item := range inventory.Data {
		if item.Type != "qemu" || item.VMID <= 0 || item.Node == "" {
			continue
		}
		vm := VM{ID: "pve-" + item.Node + "-" + fmt.Sprint(item.VMID), VMID: item.VMID, Name: item.Name, Node: item.Node, Status: item.Status, CPU: item.CPU, Memory: item.Memory, OSType: "linux"}
		result = append(result, vm)
	}
	c.fillGuestIPv4s(ctx, result)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// fillGuestIPv4s resolves the agent-reported IPv4 of every running VM with
// bounded concurrency and a per-VM timeout. A failed query deliberately
// leaves the IP empty — it is inventory data, not a failure of the whole
// account sync — and is summarized as WARN logs. A canceled parent context
// aborts the remaining queries without logging.
func (c *Client) fillGuestIPv4s(ctx context.Context, vms []VM) {
	type query struct {
		index int
	}
	var queries []query
	for i := range vms {
		if vms[i].Status == "running" {
			queries = append(queries, query{index: i})
		}
	}
	if len(queries) == 0 {
		return
	}

	jobs := make(chan query, len(queries))
	for _, q := range queries {
		jobs <- q
	}
	close(jobs)

	var (
		failuresMu sync.Mutex
		failures   []guestAgentFailure
		wg         sync.WaitGroup
	)
	for range min(guestAgentConcurrency, len(queries)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				vm := vms[q.index]
				ip, failure := c.guestIPv4(ctx, vm)
				vm.IP = ip
				vms[q.index] = vm
				if failure != nil {
					failuresMu.Lock()
					failures = append(failures, *failure)
					failuresMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// Deterministic log order, and a cap so a large fleet cannot flood the log.
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].node != failures[j].node {
			return failures[i].node < failures[j].node
		}
		return failures[i].vmid < failures[j].vmid
	})
	for i := range failures {
		if i == guestAgentMaxIndividualFailures {
			slog.Warn("further PVE guest-agent IP query failures omitted from log", "skipped", len(failures)-i)
			break
		}
		f := failures[i]
		slog.Warn("PVE guest-agent IP query failed", "node", f.node, "vmid", f.vmid, "vm", f.name, "http_status", f.status, "detail", f.detail)
	}
}

// guestAgentFailure carries enough of one failed guest-agent query for a
// WARN line. detail must never contain credentials; it is redacted on the
// way in.
type guestAgentFailure struct {
	node   string
	vmid   int
	name   string
	status int
	detail string
}

func (c *Client) guestIPv4(ctx context.Context, vm VM) (string, *guestAgentFailure) {
	queryCtx, cancel := context.WithTimeout(ctx, c.guestAgentTimeout)
	defer cancel()

	var response struct {
		Data struct {
			Result []struct {
				Name        string `json:"name"`
				IPAddresses []struct {
					Address string `json:"ip-address"`
				} `json:"ip-addresses"`
			} `json:"result"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(vm.Node), vm.VMID)
	if err := c.get(queryCtx, path, &response); err != nil {
		if ctx.Err() != nil {
			// The whole sync is being canceled (e.g. the caller went away);
			// that cancellation is surfaced by the caller, not logged here.
			return "", nil
		}
		return "", c.guestAgentFailure(vm, err)
	}
	candidates := make([]candidate, 0)
	for _, iface := range response.Data.Result {
		for _, address := range iface.IPAddresses {
			parsed, err := netip.ParseAddr(address.Address)
			if err != nil || !parsed.Is4() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified() {
				continue
			}
			candidates = append(candidates, candidate{nic: iface.Name, ip: parsed.String()})
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	return selectIPv4(c.selection, candidates), nil
}

func (c *Client) guestAgentFailure(vm VM, err error) *guestAgentFailure {
	f := &guestAgentFailure{node: vm.Node, vmid: vm.VMID, name: vm.Name}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		f.status = httpErr.StatusCode
		f.detail = httpErr.Body
	} else {
		f.detail = err.Error()
	}
	// Defensive redaction: the PVE token never appears in a response, but
	// diagnostics must stay credential-free even if PVE echoes one back.
	f.detail = redactSecrets(f.detail, c.tokenID, c.tokenSecret)
	return f
}

func redactSecrets(text, tokenID, tokenSecret string) string {
	for _, secret := range []string{tokenSecret, tokenID} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

func (c *Client) get(ctx context.Context, endpoint string, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + endpoint
	if index := strings.IndexByte(u.Path, '?'); index >= 0 {
		u.RawQuery = u.Path[index+1:]
		u.Path = u.Path[:index]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.tokenSecret)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &HTTPError{Endpoint: endpoint, Status: resp.Status, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode PVE response for %s: %w", endpoint, err)
	}
	return nil
}

// HTTPError carries a non-2xx PVE response for diagnostics. Body is the
// first 1 KiB of the response; credentials travel in the Authorization
// request header, never in the response body.
type HTTPError struct {
	Endpoint   string
	Status     string // Full status line, e.g. "500 Internal Server Error".
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("PVE GET %s returned %s", e.Endpoint, e.Status)
}

// selectIPv4 returns the first address matching CIDR priority, then NIC
// priority, then lexical order. Invalid configured CIDRs are ignored so a
// typo cannot make an otherwise usable VM disappear.
func selectIPv4(selection IPSelection, candidates []candidate) string {
	byIP := func(items []candidate) string {
		sort.Slice(items, func(i, j int) bool {
			if items[i].ip == items[j].ip {
				return items[i].nic < items[j].nic
			}
			return items[i].ip < items[j].ip
		})
		return items[0].ip
	}
	for _, cidr := range selection.CIDRPriority {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue
		}
		matches := make([]candidate, 0)
		for _, item := range candidates {
			if ip, err := netip.ParseAddr(item.ip); err == nil && prefix.Contains(ip) {
				matches = append(matches, item)
			}
		}
		if len(matches) > 0 {
			return byIP(matches)
		}
	}
	for _, nic := range selection.NICPriority {
		matches := make([]candidate, 0)
		for _, item := range candidates {
			if item.nic == nic {
				matches = append(matches, item)
			}
		}
		if len(matches) > 0 {
			return byIP(matches)
		}
	}
	return byIP(candidates)
}
