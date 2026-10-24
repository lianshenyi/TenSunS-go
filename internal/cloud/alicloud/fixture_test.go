package alicloud

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	consulstore "github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestFixtureClassifiesTemporaryAndHTTP5xxErrors(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    error
	}{
		{name: "temporary", fixture: "temporary", want: cloud.ErrTemporary},
		{name: "http 503", fixture: "http_503", want: cloud.ErrTemporary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := consulmock.New()
			httpServer := httptest.NewServer(mock.Handler())
			t.Cleanup(httpServer.Close)
			st := consulstore.New(httpServer.URL+"/v1", "", nil)
			if err := mock.SetKV("ConsulManager/fixtures/alicloud/main", map[string]any{"error": tt.fixture}); err != nil {
				t.Fatal(err)
			}

			_, err := (FixtureProvider{Store: st}).load(context.Background(), "main")
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v, want errors.Is(_, %v)", err, tt.want)
			}
		})
	}
}

func TestFixtureHTTPStatusRejectsNon5xxValues(t *testing.T) {
	for _, value := range []string{"http_429", "http_600", "http_invalid", "server error"} {
		if _, ok := fixtureHTTPStatus(value); ok {
			t.Fatalf("fixtureHTTPStatus(%q) unexpectedly classified", value)
		}
	}
}
