package main

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestSeedDemo(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(consulmock.New())
	defer server.Close()
	target := consul.New(server.URL+"/v1/", "", server.Client())
	if err := seedDemo(ctx, target); err != nil {
		t.Fatal(err)
	}

	keys, err := target.GetKeys(ctx, "ConsulManager/")
	if err != nil || len(keys) != 14 {
		t.Fatalf("read %d keys, want 14: %v", len(keys), err)
	}
	services, err := target.Services(ctx, "")
	if err != nil || len(services) != 4 || services["i-demo0002"].Meta["stat"] != "off" {
		t.Fatalf("unexpected demo services: count=%d, error=%v", len(services), err)
	}

	var node map[string]any
	if found, err := target.GetValue(ctx, "ConsulManager/jms/jobs/alicloud/main/node_id", &node); err != nil || !found {
		t.Fatalf("legacy seeder's node_id is missing: %v", err)
	}

	var key, credentials map[string]string
	for path, dest := range map[string]*map[string]string{
		"ConsulManager/assets/secret/skey": &key,
		"ConsulManager/jms/jms_info":       &credentials,
	} {
		if found, err := target.GetValue(ctx, path, dest); err != nil || !found {
			t.Fatalf("read %s: found=%v, error=%v", path, found, err)
		}
	}
	cipher, err := cryptox.NewCipher(key["sk"])
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{
		"access_key_id":     "demo-jumpserver-access-key",
		"access_key_secret": "demo-jumpserver-access-secret",
	} {
		if plain, err := cipher.Decrypt(credentials[field]); err != nil || plain != want {
			t.Fatalf("%s did not decrypt to the demo value: %v", field, err)
		}
	}
}
