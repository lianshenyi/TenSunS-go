package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind is the typed job kind encoded in the legacy func string.
type Kind string

const (
	KindGroup Kind = "group"
	KindECS   Kind = "ecs"
	KindRDS   Kind = "rds"
	KindRedis Kind = "redis"
	KindJMS   Kind = "jms"
)

// JobSpec is the on-disk task shape in Consul. Keep field names and JSON
// tags identical to the Python APScheduler config so rollback is possible.
type JobSpec struct {
	ID              string `json:"id"`
	Func            string `json:"func"`
	Args            []any  `json:"args"`
	Minutes         int    `json:"minutes"`
	Trigger         string `json:"trigger,omitempty"`
	ReplaceExisting bool   `json:"replace_existing,omitempty"`
}

// ParsedFunc is the typed translation of func, e.g.
// "__main__:alicloud.ecs" -> KindECS with Vendor "alicloud".
type ParsedFunc struct {
	Kind   Kind
	Vendor string
}

// ParseFunc recognizes the five legacy func forms. Unknown func strings are
// returned as an error and must be skipped by the loader, never deleted.
func ParseFunc(fn string) (ParsedFunc, error) {
	module, name, ok := strings.Cut(fn, ":")
	if !ok || module != "__main__" || name == "" {
		return ParsedFunc{}, fmt.Errorf("unsupported func %q", fn)
	}
	if name == "sync_jms.run" {
		return ParsedFunc{Kind: KindJMS}, nil
	}
	vendor, action, ok := strings.Cut(name, ".")
	if !ok || vendor == "" || action == "" {
		return ParsedFunc{}, fmt.Errorf("unsupported func %q", fn)
	}
	switch action {
	case "group":
		return ParsedFunc{Kind: KindGroup, Vendor: vendor}, nil
	case "ecs":
		return ParsedFunc{Kind: KindECS, Vendor: vendor}, nil
	case "rds":
		return ParsedFunc{Kind: KindRDS, Vendor: vendor}, nil
	case "redis":
		return ParsedFunc{Kind: KindRedis, Vendor: vendor}, nil
	default:
		return ParsedFunc{}, fmt.Errorf("unsupported func %q", fn)
	}
}

// DecodeJobSpec accepts both a raw KV value and a decoded JSON object.
func DecodeJobSpec(raw json.RawMessage) (JobSpec, error) {
	var spec JobSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return JobSpec{}, err
	}
	if spec.ID == "" {
		return JobSpec{}, fmt.Errorf("job id is empty")
	}
	if spec.Minutes <= 0 {
		return JobSpec{}, fmt.Errorf("job %s has invalid minutes %d", spec.ID, spec.Minutes)
	}
	return spec, nil
}

// Account returns args[0] when present.
func (s JobSpec) Account() string {
	if len(s.Args) > 0 {
		if v, ok := s.Args[0].(string); ok {
			return v
		}
	}
	return ""
}

// JMSVendor returns args[0] for a JMS task.
func (s JobSpec) JMSVendor() string { return s.Account() }

// JMSAccount returns args[1] for a JMS task.
func (s JobSpec) JMSAccount() string {
	if len(s.Args) > 1 {
		if v, ok := s.Args[1].(string); ok {
			return v
		}
	}
	return ""
}

// Region returns args[1], or "none" for resource-group tasks.
func (s JobSpec) Region() string {
	if len(s.Args) > 1 {
		if v, ok := s.Args[1].(string); ok && v != "" {
			return v
		}
	}
	return "none"
}

// ExternalIP returns args[2] with a false default for two-argument legacy ECS
// jobs.
func (s JobSpec) ExternalIP() bool {
	if len(s.Args) > 2 {
		if v, ok := s.Args[2].(bool); ok {
			return v
		}
	}
	return false
}
