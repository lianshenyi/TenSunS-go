package jumpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccessKeySignatureIsDeterministicAndBindsRequestTarget(t *testing.T) {
	creds := credentials{authType: accessKeyAuthType, keyID: "key-id", secret: "secret-value"}
	now := time.Date(2026, time.October, 31, 7, 20, 0, 0, time.UTC)
	first, err := http.NewRequest(http.MethodGet, "https://jumpserver.example/api/v1/assets/assets/?node=node-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := http.NewRequest(http.MethodGet, "https://jumpserver.example/api/v1/assets/assets/?node=node-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.sign(first, now); err != nil {
		t.Fatal(err)
	}
	if err := creds.sign(second, now); err != nil {
		t.Fatal(err)
	}
	if first.Header.Get("Authorization") != second.Header.Get("Authorization") {
		t.Fatal("same request and clock produced different signatures")
	}
	if first.Header.Get("Date") != "Sat, 31 Oct 2026 07:20:00 GMT" || first.Header.Get("Accept") != "application/json" {
		t.Fatalf("required signature headers = %v", first.Header)
	}
	tampered, err := http.NewRequest(http.MethodGet, "https://jumpserver.example/api/v1/assets/assets/?node=node-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.sign(tampered, now); err != nil {
		t.Fatal(err)
	}
	if tampered.Header.Get("Authorization") == first.Header.Get("Authorization") {
		t.Fatal("query-string tampering did not change the signature")
	}
}

func TestAccessKeyExpiredDateAnd401DoNotExposeCredentials(t *testing.T) {
	const secret = "must-not-appear"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date, err := http.ParseTime(r.Header.Get("Date"))
		if err != nil || time.Since(date) > 5*time.Minute {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		t.Fatal("expected stale signed request")
	}))
	defer server.Close()
	svc := New(nil, nil, server.Client())
	svc.now = func() time.Time { return time.Now().Add(-6 * time.Minute) }
	err := svc.request(context.Background(), credentials{authType: accessKeyAuthType, keyID: "key-id", secret: secret}, http.MethodGet, server.URL+"/api/v1/users/profile/", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("401 error = %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "key-id") {
		t.Fatalf("credentials leaked in error: %v", err)
	}
}
