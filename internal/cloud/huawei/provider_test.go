package huawei

import (
	"context"
	"errors"
	"strings"
	"testing"

	epsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eps/v1/model"
)

func TestProviderGroupMapsEnterpriseProjectsAndSanitizesFailures(t *testing.T) {
	p := &Provider{load: func(context.Context, string) (credentials, error) { return credentials{}, nil }, group: func(credentials) ([]epsmodel.EpDetail, error) {
		return []epsmodel.EpDetail{{Id: "ep-1", Name: "platform"}}, nil
	}}
	groups, err := p.Group(context.Background(), "demo")
	if err != nil || groups["ep-1"] != "platform" {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	p.group = func(credentials) ([]epsmodel.EpDetail, error) { return nil, errors.New("signature=secret") }
	_, err = p.Group(context.Background(), "demo")
	if err == nil || !strings.Contains(err.Error(), "ListEnterpriseProject") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
}

func TestProviderCancellationSkipsCredentialLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	p := &Provider{load: func(context.Context, string) (credentials, error) { called = true; return credentials{}, nil }}
	if _, err := p.Group(ctx, "demo"); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
