// Package store defines the data access boundary used by the HTTP API and
// job runtime. The Consul implementation lives in package consul.
package store

import (
	"context"
	"encoding/json"
)

// Service is the subset of Consul's agent service response used by TenSunS.
// JSON tags match Consul's wire format so responses can be decoded directly.
type Service struct {
	ID                string            `json:"ID"`
	Name              string            `json:"Service"`
	Tags              []string          `json:"Tags"`
	Meta              map[string]string `json:"Meta"`
	Address           string            `json:"Address"`
	Port              int               `json:"Port"`
	TaggedAddresses   map[string]any    `json:"TaggedAddresses,omitempty"`
	Weights           map[string]int    `json:"Weights,omitempty"`
	EnableTagOverride bool              `json:"EnableTagOverride,omitempty"`
}

// ServiceCheck is the TCP check shape used by resource registrations.
type ServiceCheck struct {
	TCP      string `json:"TCP"`
	Interval string `json:"Interval"`
}

// ServiceRegistration is the payload sent to /agent/service/register.
type ServiceRegistration struct {
	ID      string            `json:"ID"`
	Name    string            `json:"Name"`
	Address string            `json:"Address"`
	Port    int               `json:"Port"`
	Tags    []string          `json:"Tags"`
	Meta    map[string]string `json:"Meta"`
	Check   *ServiceCheck     `json:"Check,omitempty"`
}

// HealthCheck is the subset needed by the future resource/JMS views.
type HealthCheck struct {
	Status string `json:"Status"`
	Output string `json:"Output"`
}

// HealthInstance mirrors Consul's /health/service response.
type HealthInstance struct {
	Node    map[string]any `json:"Node"`
	Service Service        `json:"Service"`
	Checks  []HealthCheck  `json:"Checks"`
}

// Store is the Consul-facing contract from the migration baseline.
type Store interface {
	GetValue(ctx context.Context, path string, out any) (bool, error)
	GetKVDict(ctx context.Context, path string) (map[string]json.RawMessage, error)
	GetKeys(ctx context.Context, path string) ([]string, error)
	PutKV(ctx context.Context, path string, value any) error
	DeleteKV(ctx context.Context, path string) error

	Services(ctx context.Context, filter string) (map[string]Service, error)
	HealthService(ctx context.Context, name string) ([]HealthInstance, error)
	Register(ctx context.Context, svc ServiceRegistration) error
	Deregister(ctx context.Context, id string) error
}
