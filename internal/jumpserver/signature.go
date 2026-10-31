package jumpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	accessKeyAuthType   = "access_key"
	legacyTokenAuthType = "private_token_legacy"
	signatureHeaders    = "(request-target) accept date"
)

type credentials struct {
	authType string
	keyID    string
	secret   string
}

func (c credentials) sign(req *http.Request, now time.Time) error {
	if c.authType != accessKeyAuthType {
		return fmt.Errorf("unsupported JumpServer authentication type %q", c.authType)
	}
	if strings.TrimSpace(c.keyID) == "" || strings.TrimSpace(c.secret) == "" {
		return fmt.Errorf("JumpServer Access Key configuration is incomplete")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	target := req.URL.EscapedPath()
	if target == "" {
		target = "/"
	}
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	signingString := strings.Join([]string{
		"(request-target): " + strings.ToLower(req.Method) + " " + target,
		"accept: " + req.Header.Get("Accept"),
		"date: " + req.Header.Get("Date"),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(c.secret))
	_, _ = mac.Write([]byte(signingString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	req.Header.Set("Authorization", fmt.Sprintf(`Signature keyId=%q,algorithm="hmac-sha256",headers=%q,signature=%q`, c.keyID, signatureHeaders, signature))
	return nil
}
