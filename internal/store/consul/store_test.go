package consul

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

func newTestStore(t *testing.T) (*Store, *consulmock.Server, *httptest.Server) {
	t.Helper()
	mock := consulmock.New()
	if err := mock.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mock.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL+"/v1", "test-token", ts.Client()), mock, ts
}

func TestCheckAPIAndKV(t *testing.T) {
	st, mock, _ := newTestStore(t)
	ctx := context.Background()
	if err := CheckAPI(ctx, st.baseURL, "test-token", st.client); err != nil {
		t.Fatal(err)
	}
	var skey struct {
		SK string `json:"sk"`
	}
	found, err := st.GetValue(ctx, "ConsulManager/assets/secret/skey", &skey)
	if err != nil || !found || len(skey.SK) != 32 {
		t.Fatalf("GetValue = %+v, %v, %v", skey, found, err)
	}
	var missing map[string]any
	found, err = st.GetValue(ctx, "ConsulManager/assets/missing", &missing)
	if err != nil || found {
		t.Fatalf("missing key = %v, %v", found, err)
	}

	dict, err := st.GetKVDict(ctx, "ConsulManager/jobs/alicloud/main")
	if err != nil {
		t.Fatal(err)
	}
	if len(dict) != 4 {
		t.Fatalf("job dict len = %d", len(dict))
	}
	var spec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(dict["ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou"], &spec); err != nil || spec.ID == "" {
		t.Fatalf("decode job: %v, %+v", err, spec)
	}
	keys, err := st.GetKeys(ctx, "ConsulManager/jobs")
	if err != nil || len(keys) != 4 {
		t.Fatalf("keys = %v, %v", keys, err)
	}

	if err := st.PutKV(ctx, "test/中文", map[string]string{"value": "<&中文"}); err != nil {
		t.Fatal(err)
	}
	raw, ok := mock.GetKV("test/中文")
	if !ok {
		t.Fatal("PutKV did not reach mock")
	}
	if bytes.Contains(raw, []byte("\\u003c")) || bytes.Contains(raw, []byte("\\u4e2d")) {
		t.Fatalf("PutKV escaped JSON: %s", raw)
	}
}

func TestServicesAndRegistration(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	services, err := st.Services(ctx, `Service == "alicloud_main_ecs" and "cn-hangzhou" in Tags`)
	if err != nil || len(services) != 2 {
		t.Fatalf("services = %d, %v", len(services), err)
	}
	if services["i-demo0001"].Meta["os"] != "linux" {
		t.Fatalf("unexpected service: %+v", services["i-demo0001"])
	}
	reg := store.ServiceRegistration{
		ID: "i-new", Name: "alicloud_main_ecs", Address: "10.0.0.99", Port: 9100,
		Tags:  []string{"ON", "linux", "cn-hangzhou"},
		Meta:  map[string]string{"iid": "i-new"},
		Check: &store.ServiceCheck{TCP: "10.0.0.99:9100", Interval: "60s"},
	}
	if err := st.Register(ctx, reg); err != nil {
		t.Fatal(err)
	}
	services, _ = st.Services(ctx, `ID == "i-new"`)
	if len(services) != 1 {
		t.Fatalf("registered service missing: %v", services)
	}
	if err := st.Deregister(ctx, "i-new"); err != nil {
		t.Fatal(err)
	}
	services, _ = st.Services(ctx, `ID == "i-new"`)
	if len(services) != 0 {
		t.Fatal("service was not deregistered")
	}
}

func TestTokenHeaderIsSent(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Consul-Token")
		_, _ = w.Write([]byte(`{"sk":"0123456789abcdef0123456789abcdef"}`))
	}))
	defer ts.Close()
	st := New(ts.URL+"/v1", "secret-token", ts.Client())
	var out map[string]any
	if _, err := st.GetValue(context.Background(), "x", &out); err != nil {
		t.Fatal(err)
	}
	if got != "secret-token" {
		t.Fatalf("token header = %q", got)
	}
}
