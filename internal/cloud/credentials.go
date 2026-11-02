package cloud

import (
	"context"
	"fmt"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// Credentials are decrypted only for the lifetime of an outbound cloud API
// request. Callers must never log either field.
type Credentials struct {
	AccessKey string
	SecretKey string
}

// SecretResolver reads the legacy encrypted AK/SK shape from Consul.
type SecretResolver struct {
	Store  store.Store
	Cipher *cryptox.Cipher
}

// Resolve decrypts the AK/SK for one cloud account.
func (r SecretResolver) Resolve(ctx context.Context, vendor, account string) (Credentials, error) {
	if r.Store == nil || r.Cipher == nil {
		return Credentials{}, fmt.Errorf("cloud credentials resolver is not configured")
	}
	var encrypted struct {
		AK string `json:"ak"`
		SK string `json:"sk"`
	}
	found, err := r.Store.GetValue(ctx, "ConsulManager/assets/"+vendor+"/aksk/"+account, &encrypted)
	if err != nil {
		return Credentials{}, fmt.Errorf("read %s AK/SK: %w", vendor, err)
	}
	if !found || encrypted.AK == "" || encrypted.SK == "" {
		return Credentials{}, fmt.Errorf("%s AK/SK for account %q is missing", vendor, account)
	}
	ak, err := r.Cipher.Decrypt(encrypted.AK)
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt %s access key: %w", vendor, err)
	}
	sk, err := r.Cipher.Decrypt(encrypted.SK)
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt %s secret key: %w", vendor, err)
	}
	return Credentials{AccessKey: ak, SecretKey: sk}, nil
}
