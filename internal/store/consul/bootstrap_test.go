package consul

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
)

func TestEnsureSKeyKeepsExistingKey(t *testing.T) {
	mock := consulmock.New()
	_ = mock.SeedDemo()
	ts := httptest.NewServer(mock.Handler())
	defer ts.Close()
	st := New(ts.URL+"/v1", "", ts.Client())
	key, err := EnsureSKey(context.Background(), st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || key != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("EnsureSKey = %q, %v", key, err)
	}
	keys, _ := st.GetKeys(context.Background(), "ConsulManager/assets/secret/bak-skey")
	if len(keys) != 0 {
		t.Fatalf("existing key should not create backup: %v", keys)
	}
}

func TestEnsureSKeyCreatesOnlyWhenAbsent(t *testing.T) {
	mock := consulmock.New()
	ts := httptest.NewServer(mock.Handler())
	defer ts.Close()
	st := New(ts.URL+"/v1", "", ts.Client())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	key, err := EnsureSKey(context.Background(), st, logger)
	if err != nil || len(key) != 32 {
		t.Fatalf("EnsureSKey = %q, %v", key, err)
	}
	keys, _ := st.GetKeys(context.Background(), "ConsulManager/assets/secret/bak-skey")
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "ConsulManager/assets/secret/bak-skey") {
		t.Fatalf("backup keys = %v", keys)
	}
	second, err := EnsureSKey(context.Background(), st, logger)
	if err != nil || second != key {
		t.Fatalf("second EnsureSKey changed key: %q -> %q, %v", key, second, err)
	}
	if err := mock.SetKV("ConsulManager/assets/secret/skey", map[string]string{"sk": "bad"}); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSKey(context.Background(), st, logger); err == nil {
		t.Fatal("expected invalid existing skey error")
	}
}
