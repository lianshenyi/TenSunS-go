package cloud

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func TestSecretResolverDecryptsLegacyAKSK(t *testing.T) {
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ak, _ := cipher.Encrypt("access-key")
	sk, _ := cipher.Encrypt("secret-key")
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	if err := mock.SetKV("ConsulManager/assets/tencent_cloud/aksk/main", map[string]string{"ak": ak, "sk": sk}); err != nil {
		t.Fatal(err)
	}
	got, err := (SecretResolver{Store: consul.New(server.URL+"/v1", "", server.Client()), Cipher: cipher}).Resolve(context.Background(), "tencent_cloud", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKey != "access-key" || got.SecretKey != "secret-key" {
		t.Fatalf("credentials = %#v", got)
	}
}

func TestSecretResolverRejectsMissingAndMalformedValues(t *testing.T) {
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	mock := consulmock.New()
	server := httptest.NewServer(mock.Handler())
	defer server.Close()
	resolver := SecretResolver{Store: consul.New(server.URL+"/v1", "", server.Client()), Cipher: cipher}
	if _, err := resolver.Resolve(context.Background(), "awscloud", "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing error = %v", err)
	}
	if err := mock.SetKV("ConsulManager/assets/awscloud/aksk/bad", map[string]string{"ak": "not-base64", "sk": "not-base64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "awscloud", "bad"); err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("decrypt error = %v", err)
	}
}
