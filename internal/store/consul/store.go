// Package consul implements store.Store on top of the Consul HTTP API.
package consul

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Store is a thin, non-caching Consul API client. Keeping it non-caching is
// intentional: Python reads Consul on every request and existing deployments
// may change KV outside TenSunS.
type Store struct {
	baseURL string
	token   string
	client  *http.Client
}

// New creates a Store. baseURL must include /v1 (for example
// http://127.0.0.1:8500/v1), matching consul_url in the Python deployment.
func New(baseURL, token string, client *http.Client) *Store {
	if client == nil {
		client = http.DefaultClient
	}
	return &Store{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  client,
	}
}

func (s *Store) endpoint(path string) string {
	return s.baseURL + "/" + strings.TrimLeft(path, "/")
}

func (s *Store) request(ctx context.Context, method, path string, query url.Values, body []byte) (*http.Response, error) {
	u := s.endpoint(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Consul-Token", s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.client.Do(req)
}

func (s *Store) GetValue(ctx context.Context, path string, out any) (bool, error) {
	resp, err := s.request(ctx, http.MethodGet, "kv/"+path, url.Values{"raw": {""}}, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("consul GET kv/%s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, fmt.Errorf("decode kv/%s: %w", path, err)
	}
	return true, nil
}

func (s *Store) GetKVDict(ctx context.Context, path string) (map[string]json.RawMessage, error) {
	resp, err := s.request(ctx, http.MethodGet, "kv/"+path, url.Values{"recurse": {""}}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return map[string]json.RawMessage{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("consul GET kv/%s?recurse: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var items []struct {
		Key   string  `json:"Key"`
		Value *string `json:"Value"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("decode kv/%s?recurse: %w", path, err)
	}
	out := make(map[string]json.RawMessage, len(items))
	for _, item := range items {
		if item.Value == nil {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(*item.Value)
		if err != nil {
			return nil, fmt.Errorf("decode value for %s: %w", item.Key, err)
		}
		var raw json.RawMessage
		if err := json.Unmarshal(decoded, &raw); err != nil {
			return nil, fmt.Errorf("decode JSON value for %s: %w", item.Key, err)
		}
		out[item.Key] = raw
	}
	return out, nil
}

func (s *Store) GetKeys(ctx context.Context, path string) ([]string, error) {
	resp, err := s.request(ctx, http.MethodGet, "kv/"+path, url.Values{"keys": {""}}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("consul GET kv/%s?keys: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return []string{}, nil
	}
	var keys []string
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, fmt.Errorf("decode kv/%s?keys: %w", path, err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func (s *Store) PutKV(ctx context.Context, path string, value any) error {
	body, err := marshalNoHTMLEscape(value)
	if err != nil {
		return err
	}
	resp, err := s.request(ctx, http.MethodPut, "kv/"+path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("consul PUT kv/%s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *Store) DeleteKV(ctx context.Context, path string) error {
	resp, err := s.request(ctx, http.MethodDelete, "kv/"+path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("consul DELETE kv/%s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *Store) Services(ctx context.Context, filter string) (map[string]store.Service, error) {
	q := url.Values{}
	if filter != "" {
		q.Set("filter", filter)
	}
	resp, err := s.request(ctx, http.MethodGet, "agent/services", q, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("consul GET agent/services: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out map[string]store.Service
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode agent/services: %w", err)
	}
	if out == nil {
		out = map[string]store.Service{}
	}
	return out, nil
}

func (s *Store) HealthService(ctx context.Context, name string) ([]store.HealthInstance, error) {
	resp, err := s.request(ctx, http.MethodGet, "health/service/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("consul GET health/service/%s: %s: %s", name, resp.Status, strings.TrimSpace(string(body)))
	}
	var out []store.HealthInstance
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode health/service/%s: %w", name, err)
	}
	if out == nil {
		out = []store.HealthInstance{}
	}
	return out, nil
}

func (s *Store) Register(ctx context.Context, svc store.ServiceRegistration) error {
	body, err := marshalNoHTMLEscape(svc)
	if err != nil {
		return err
	}
	resp, err := s.request(ctx, http.MethodPut, "agent/service/register", nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("consul service register %s: %s: %s", svc.ID, resp.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *Store) Deregister(ctx context.Context, id string) error {
	resp, err := s.request(ctx, http.MethodPut, "agent/service/deregister/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("consul service deregister %s: %s: %s", id, resp.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// CheckAPI performs the same startup probe as consul_manager.get_consul_ver:
// /operator/autopilot/state first, falling back to /operator/autopilot/health
// on 404 for older Consul versions.
func CheckAPI(ctx context.Context, baseURL, token string, client *http.Client) error {
	if client == nil {
		client = http.DefaultClient
	}
	base := strings.TrimRight(baseURL, "/")
	for _, path := range []string{"operator/autopilot/state", "operator/autopilot/health"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Consul-Token", token)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request consul %s: %w", path, err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return nil
		case http.StatusForbidden:
			return fmt.Errorf("consul authentication failed (403), check consul_token")
		case http.StatusNotFound:
			continue
		default:
			return fmt.Errorf("consul probe %s returned %s", path, resp.Status)
		}
	}
	return fmt.Errorf("consul is unreachable or too old (404 on autopilot APIs)")
}

// Ensure the implementation satisfies the interface at compile time.
var _ store.Store = (*Store)(nil)
