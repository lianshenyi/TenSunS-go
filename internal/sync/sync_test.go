package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/alicloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/awscloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/huaweicloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/tencentcloud"
	"github.com/lianshenyi/TenSunS-go/internal/config"
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
	capture := &capturingStore{Store: s.Store, registrations: make(map[string]store.ServiceRegistration)}
	s.Store = capture
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
	assertECS(t, got, "alicloud", "cn-hangzhou", capture.registrations["i-1"])
	assertGroupRDSRedis(t, s, mock, "alicloud", "main", "cn-hangzhou", capture.registrations)
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

func assertECS(t *testing.T, got store.Service, vendor, region string, registration store.ServiceRegistration) {
	t.Helper()
	if got.Name != vendor+"_main_ecs" || got.Address != "10.0.0.1" || got.Port != 9100 || registration.Check == nil || registration.Check.TCP != "10.0.0.1:9100" || registration.Check.Interval != "60s" {
		t.Fatalf("unexpected ecs service: %+v", got)
	}
	wantMeta := map[string]string{"iid": "i-1", "name": "web", "region": config.RegionDisplay(vendor, region), "group": "生产", "instance": "10.0.0.1:9100", "account": "main", "vendor": config.VendorDisplay(vendor), "os": "linux", "cpu": "2核", "mem": "4GB", "exp": "按量", "stat": "on", "team": "ops"}
	for key, want := range wantMeta {
		if got.Meta[key] != want {
			t.Fatalf("ecs meta[%q]=%q, want %q: %+v", key, got.Meta[key], want, got.Meta)
		}
	}
	if got.Meta["bad-key"] != "" || len(got.Tags) != 3 || got.Tags[0] != "ON" || got.Tags[1] != "linux" || got.Tags[2] != region {
		t.Fatalf("unexpected ecs tags/meta: %+v", got)
	}
}

func assertGroupRDSRedis(t *testing.T, s *ConsulService, mock *consulmock.Server, vendor, account, region string, registrations map[string]store.ServiceRegistration) {
	t.Helper()
	raw, ok := mock.GetKV(groupPath(vendor, account))
	if !ok || string(raw) != `{"rg-1":"生产"}` {
		t.Fatalf("group fixture was not persisted: %s", raw)
	}
	for _, check := range []struct {
		kind string
		id   string
		meta map[string]string
		tags []string
		port int
	}{
		{"rds", "rm-1", map[string]string{"iid": "rm-1", "name": "mysql", "region": config.RegionDisplay(vendor, region), "group": "生产", "instance": "db.local:3306", "account": account, "vendor": config.VendorDisplay(vendor), "itype": "主实例", "disk": "20GB", "cpu": "2核", "mem": "4GB", "ver": "8.0", "domain": "db.local", "exp": "按量", "stat": "on", "team": "无"}, []string{"ON", "主实例", "8.0", region}, 3306},
		{"redis", "r-1", map[string]string{"iid": "r-1", "name": "cache", "region": config.RegionDisplay(vendor, region), "group": "生产", "instance": "cache.local:6379", "account": account, "vendor": config.VendorDisplay(vendor), "itype": "主从", "mem": "1024MB", "ver": "6.0", "ip": "10.0.0.3", "exp": "-", "stat": "on"}, []string{"ON", "主从", "6.0", region}, 6379},
	} {
		services, err := s.Store.Services(context.Background(), `Service == "`+vendor+`_`+account+`_`+check.kind+`" and "`+region+`" in Tags and Meta.account == "`+account+`"`)
		if err != nil || len(services) != 1 {
			t.Fatalf("%s services=%v err=%v", check.kind, services, err)
		}
		got := services[check.id]
		registration := registrations[check.id]
		if got.Port != check.port || registration.Check == nil || registration.Check.TCP != got.Address+":"+strconv.Itoa(check.port) || registration.Check.Interval != "60s" || !slices.Equal(got.Tags, check.tags) {
			t.Fatalf("unexpected %s service: %+v", check.kind, got)
		}
		for key, want := range check.meta {
			if got.Meta[key] != want {
				t.Fatalf("%s meta[%q]=%q, want %q: %+v", check.kind, key, got.Meta[key], want, got.Meta)
			}
		}
	}
}

func TestPhaseTwoFixtureProvidersWriteAllResourceKinds(t *testing.T) {
	for _, tc := range []struct {
		vendor   string
		provider cloud.Provider
	}{
		{"tencent_cloud", nil},
		{"huaweicloud", nil},
		{"awscloud", nil},
	} {
		t.Run(tc.vendor, func(t *testing.T) {
			s, mock := setup(t)
			capture := &capturingStore{Store: s.Store, registrations: make(map[string]store.ServiceRegistration)}
			s.Store = capture
			switch tc.vendor {
			case "tencent_cloud":
				tc.provider = tencentcloud.NewFixtureProvider(s.Store)
			case "huaweicloud":
				tc.provider = huaweicloud.NewFixtureProvider(s.Store)
			case "awscloud":
				tc.provider = awscloud.NewFixtureProvider(s.Store)
			}
			s.Providers.Register(tc.vendor, tc.provider)
			if err := mock.SetKV("ConsulManager/fixtures/"+tc.vendor+"/main", fixture()); err != nil {
				t.Fatal(err)
			}
			if err := s.Group(context.Background(), tc.vendor, "main"); err != nil {
				t.Fatal(err)
			}
			if err := s.ECS(context.Background(), tc.vendor, "main", "cn-hangzhou", false); err != nil {
				t.Fatal(err)
			}
			if err := s.RDS(context.Background(), tc.vendor, "main", "cn-hangzhou"); err != nil {
				t.Fatal(err)
			}
			if err := s.Redis(context.Background(), tc.vendor, "main", "cn-hangzhou"); err != nil {
				t.Fatal(err)
			}
			if err := s.Group(context.Background(), tc.vendor, "main"); err != nil {
				t.Fatal(err)
			}
			if err := s.ECS(context.Background(), tc.vendor, "main", "cn-hangzhou", false); err != nil {
				t.Fatal(err)
			}
			if err := s.RDS(context.Background(), tc.vendor, "main", "cn-hangzhou"); err != nil {
				t.Fatal(err)
			}
			if err := s.Redis(context.Background(), tc.vendor, "main", "cn-hangzhou"); err != nil {
				t.Fatal(err)
			}
			services, err := s.Store.Services(context.Background(), `Service == "`+tc.vendor+`_main_ecs" and "cn-hangzhou" in Tags and Meta.account == "main"`)
			if err != nil || len(services) != 1 {
				t.Fatalf("services=%v err=%v", services, err)
			}
			assertECS(t, services["i-1"], tc.vendor, "cn-hangzhou", capture.registrations["i-1"])
			assertGroupRDSRedis(t, s, mock, tc.vendor, "main", "cn-hangzhou", capture.registrations)
		})
	}
}

func TestPhaseTwoPartialWriteRerunsConverge(t *testing.T) {
	for _, vendor := range []string{"tencent_cloud", "huaweicloud", "awscloud"} {
		t.Run(vendor, func(t *testing.T) {
			s, mock := setup(t)
			s.Providers.Register(vendor, fixtureProviderFor(vendor, s.Store))
			if err := mock.SetKV("ConsulManager/fixtures/"+vendor+"/main", fixture()); err != nil {
				t.Fatal(err)
			}
			s.Store = &failingStore{Store: s.Store, fail: true}
			if err := s.ECS(context.Background(), vendor, "main", "cn-hangzhou", false); err == nil {
				t.Fatal("expected partial write failure")
			}
			if err := s.ECS(context.Background(), vendor, "main", "cn-hangzhou", false); err != nil {
				t.Fatal(err)
			}
			services, err := s.Store.Services(context.Background(), `Service == "`+vendor+`_main_ecs" and "cn-hangzhou" in Tags and Meta.account == "main"`)
			if err != nil || len(services) != 1 {
				t.Fatalf("services=%v err=%v", services, err)
			}
		})
	}
}

type capturingStore struct {
	store.Store
	registrations map[string]store.ServiceRegistration
}

func (s *capturingStore) Register(ctx context.Context, registration store.ServiceRegistration) error {
	s.registrations[registration.ID] = registration
	return s.Store.Register(ctx, registration)
}

func TestPhaseTwoFixturesClassifyAllFailureModes(t *testing.T) {
	for _, vendor := range []string{"tencent_cloud", "huaweicloud", "awscloud"} {
		for _, failure := range []string{"auth", "rate_limit", "temporary", "http_503", "timeout"} {
			t.Run(vendor+"/"+failure, func(t *testing.T) {
				s, mock := setup(t)
				provider := &countingProvider{Provider: fixtureProviderFor(vendor, s.Store)}
				s.Providers.Register(vendor, provider)
				if err := mock.SetKV("ConsulManager/fixtures/"+vendor+"/main", map[string]any{"error": failure}); err != nil {
					t.Fatal(err)
				}
				err := s.ECS(context.Background(), vendor, "main", "cn-hangzhou", false)
				if err == nil {
					t.Fatal("expected provider error")
				}
				wantCalls := 3
				if failure == "auth" {
					wantCalls = 1
				}
				if provider.calls != wantCalls {
					t.Fatalf("calls=%d, want %d", provider.calls, wantCalls)
				}
				raw, ok := mock.GetKV(recordPath(vendor, "main", "ecs", "cn-hangzhou"))
				if !ok {
					t.Fatal("missing failure record")
				}
				var record map[string]any
				if json.Unmarshal(raw, &record) != nil || record["status"] != float64(50000) {
					t.Fatalf("record=%s", raw)
				}
			})
		}
	}
}

type countingProvider struct {
	cloud.Provider
	calls int
}

func (p *countingProvider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	p.calls++
	return p.Provider.ECS(ctx, account, region, externalIP)
}

func fixtureProviderFor(vendor string, st store.Store) cloud.Provider {
	switch vendor {
	case "tencent_cloud":
		return tencentcloud.NewFixtureProvider(st)
	case "huaweicloud":
		return huaweicloud.NewFixtureProvider(st)
	case "awscloud":
		return awscloud.NewFixtureProvider(st)
	default:
		return alicloud.FixtureProvider{Store: st}
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

type temporaryProvider struct {
	calls    int
	failures int
	err      error
}

func (p *temporaryProvider) Group(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (p *temporaryProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	p.calls++
	if p.calls <= p.failures {
		return nil, p.err
	}
	return map[string]model.ECS{"i": {Name: "ok", OSType: "linux", IP: "10.0.0.1"}}, nil
}
func (p *temporaryProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}
func (p *temporaryProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

func TestHTTP5xxRetriesThenSucceeds(t *testing.T) {
	s, _ := setup(t)
	p := &temporaryProvider{failures: 2, err: cloud.HTTPStatusError{StatusCode: 503}}
	s.Providers.Register("temporary", p)
	if err := s.ECS(context.Background(), "temporary", "main", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 {
		t.Fatalf("calls=%d, want 3", p.calls)
	}
}

func TestTemporaryErrorRetriesThreeTimesThenWritesFailureRecord(t *testing.T) {
	s, mock := setup(t)
	p := &temporaryProvider{failures: 3, err: cloud.ErrTemporary}
	s.Providers.Register("temporary", p)
	err := s.ECS(context.Background(), "temporary", "main", "cn-hangzhou", false)
	if !errors.Is(err, cloud.ErrTemporary) {
		t.Fatalf("err=%v", err)
	}
	if p.calls != 3 {
		t.Fatalf("calls=%d, want 3", p.calls)
	}
	raw, ok := mock.GetKV("ConsulManager/record/jobs/temporary/main/ecs/cn-hangzhou")
	if !ok {
		t.Fatal("missing failure record")
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["status"] != float64(50000) {
		t.Fatalf("record=%v", record)
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
