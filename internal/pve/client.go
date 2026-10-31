// Package pve implements the small read-only subset of the Proxmox VE API
// needed to inventory QEMU virtual machines and their guest-agent addresses.
package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

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
	return &Client{base: base, tokenID: creds.TokenID, tokenSecret: creds.TokenSecret, http: secureClient, selection: selection}, nil
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
		if item.Status == "running" {
			vm.IP = c.guestIPv4(ctx, item.Node, item.VMID)
		}
		result = append(result, vm)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *Client) guestIPv4(ctx context.Context, node string, vmid int) string {
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
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid)
	if err := c.get(ctx, path, &response); err != nil {
		return ""
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
		return ""
	}
	return selectIPv4(c.selection, candidates)
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
		return fmt.Errorf("PVE GET %s returned %s", endpoint, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode PVE response for %s: %w", endpoint, err)
	}
	return nil
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
