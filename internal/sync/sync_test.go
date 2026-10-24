package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/alicloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	consulstore "github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func setup(t *testing.T) (*ConsulService, *consulmock.Server) {
	t.Helper()
	mock := consulmock.New()
	httpServer := httptest.NewServer(mock.Handler())
	t.Cleanup(httpServer.Close)
	st := consulstore.New(httpServer.URL+"/v1", "", nil)
	registry := cloud.NewRegistry()
	registry.Register("alicloud", alicloud.FixtureProvider{Store: st})
	return &ConsulService{Store: st, Providers: registry, Now: func() time.Time { return time.Date(2026, 10, 24, 10, 0, 0, 0, time.Local) }}, mock
}

func fixture() map[string]any {
	return map[string]any{
		"groups": map[string]string{"rg-1": "生产"},
		"ecs":    map[string]any{"i-1": map[string]any{"name": "web", "group": "生产", "ostype": "linux", "status": "Running", "region": "cn-hangzhou", "ip": "10.0.0.1", "cpu": "2核", "mem": "4GB", "exp": "按量", "ecstag": map[string]string{"team": "ops", "bad-key": "ignored"}}},
		"rds":    map[string]any{"rm-1": map[string]any{"name": "mysql", "domain": "db.local", "ip": "db.local", "port": 3306, "region": "cn-hangzhou", "group": "生产", "status": "Running", "itype": "主实例", "ver": "8.0", "exp": "按量", "cpu": "2核", "mem": "4GB", "disk": "20GB"}},
		"redis":  map[string]any{"r-1": map[string]any{"name": "cache", "domain": "cache.local", "ip": "10.0.0.3", "port": 6379, "region": "cn-hangzhou", "group": "生产", "status": "Running", "itype": "主从", "ver": "6.0", "mem": "1024MB", "exp": "-"}},
	}
}

func TestFixtureSyncWritesCompatibleResourcesAndIsIdempotent(t *testing.T) {
	s, mock := setup(t)
	if err := mock.SetKV("ConsulManager/fixtures/alicloud/main", fixture()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Group(ctx, "alicloud", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.ECS(ctx, "alicloud", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	if err := s.RDS(ctx, "alicloud", "main", "cn-hangzhou"); err != nil {
		t.Fatal(err)
	}
	if err := s.Redis(ctx, "alicloud", "main", "cn-hangzhou"); err != nil {
		t.Fatal(err)
	}
	if err := s.ECS(ctx, "alicloud", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	services, err := s.Store.Services(ctx, `Service == "alicloud_main_ecs" and "cn-hangzhou" in Tags and Meta.account == "main"`)
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%v err=%v", services, err)
	}
	got := services["i-1"]
	if got.Meta["team"] != "ops" || got.Meta["bad-key"] != "" || got.Meta["stat"] != "on" || got.Port != 9100 {
		t.Fatalf("unexpected ecs service: %+v", got)
	}
	raw, ok := mock.GetKV("ConsulManager/record/jobs/alicloud/main/ecs/cn-hangzhou")
	if !ok {
		t.Fatal("missing execution record")
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["status"] != float64(20000) || record["count"] != float64(1) {
		t.Fatalf("record=%v", record)
	}
}

func TestAuthenticationFailureWritesFailureRecordWithoutRetry(t *testing.T) {
	s, mock := setup(t)
	p := &authProvider{}
	s.Providers.Register("alicloud", p)
	err := s.ECS(context.Background(), "alicloud", "main", "cn-hangzhou", false)
	if !errors.Is(err, cloud.ErrAuthentication) {
		t.Fatalf("err=%v", err)
	}
	if p.calls != 1 {
		t.Fatalf("calls=%d, want 1", p.calls)
	}
	raw, ok := mock.GetKV("ConsulManager/record/jobs/alicloud/main/ecs/cn-hangzhou")
	if !ok {
		t.Fatal("missing failure record")
	}
	var record map[string]any
	_ = json.Unmarshal(raw, &record)
	if record["status"] != float64(50000) || record["count"] != "无" {
		t.Fatalf("record=%v", record)
	}
}

type authProvider struct{ calls int }

func (p *authProvider) Group(context.Context, string) (map[string]string, error) { return nil, nil }
func (p *authProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	p.calls++
	return nil, cloud.ErrAuthentication
}
func (p *authProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}
func (p *authProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

type retryProvider struct{ calls int }

func (p *retryProvider) Group(context.Context, string) (map[string]string, error) { return nil, nil }
func (p *retryProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	p.calls++
	if p.calls < 3 {
		return nil, cloud.ErrRateLimited
	}
	return map[string]model.ECS{"i": {Name: "ok", OSType: "linux", IP: "10.0.0.1"}}, nil
}
func (p *retryProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}
func (p *retryProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

func TestRateLimitRetriesThenSucceeds(t *testing.T) {
	s, _ := setup(t)
	p := &retryProvider{}
	s.Providers.Register("retry", p)
	if err := s.ECS(context.Background(), "retry", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 {
		t.Fatalf("calls=%d, want 3", p.calls)
	}
}

func TestTimeoutRetriesThenSucceeds(t *testing.T) {
	s, _ := setup(t)
	p := &timeoutProvider{}
	s.Providers.Register("timeout", p)
	if err := s.ECS(context.Background(), "timeout", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("calls=%d, want 2", p.calls)
	}
}

type timeoutProvider struct{ calls int }

func (p *timeoutProvider) Group(context.Context, string) (map[string]string, error) { return nil, nil }
func (p *timeoutProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	p.calls++
	if p.calls == 1 {
		return nil, context.DeadlineExceeded
	}
	return map[string]model.ECS{"i": {Name: "ok", OSType: "linux", IP: "10.0.0.1"}}, nil
}
func (p *timeoutProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}
func (p *timeoutProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

type failingStore struct {
	store.Store
	fail bool
}

func (s *failingStore) Register(ctx context.Context, svc store.ServiceRegistration) error {
	if s.fail {
		s.fail = false
		return errors.New("injected register failure")
	}
	return s.Store.Register(ctx, svc)
}

func TestPartialWriteFailsThenRerunConverges(t *testing.T) {
	s, mock := setup(t)
	if err := mock.SetKV("ConsulManager/fixtures/alicloud/main", fixture()); err != nil {
		t.Fatal(err)
	}
	s.Store = &failingStore{Store: s.Store, fail: true}
	if err := s.ECS(context.Background(), "alicloud", "main", "cn-hangzhou", false); err == nil {
		t.Fatal("expected partial write failure")
	}
	if err := s.ECS(context.Background(), "alicloud", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	services, err := s.Store.Services(context.Background(), `Service == "alicloud_main_ecs" and "cn-hangzhou" in Tags and Meta.account == "main"`)
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%v err=%v", services, err)
	}
}
