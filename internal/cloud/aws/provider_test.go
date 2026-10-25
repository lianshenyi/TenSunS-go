package aws

import (
	"context"
	"testing"
)

func TestProviderGroupUsesLegacyDefaultWithoutCredentials(t *testing.T) {
	p := &Provider{load: func(context.Context, string) (credentialsValue, error) {
		t.Fatal("Group must not load AWS credentials")
		return credentialsValue{}, nil
	}}
	groups, err := p.Group(context.Background(), "demo")
	if err != nil || len(groups) != 1 || groups["1"] != "1" {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
}

func TestProviderCancellationSkipsCredentialLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	p := &Provider{load: func(context.Context, string) (credentialsValue, error) { called = true; return credentialsValue{}, nil }}
	if _, err := p.ECS(ctx, "demo", "us-east-1", false); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
