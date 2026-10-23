package consul

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"
)

const skeyPath = "ConsulManager/assets/secret/skey"

// EnsureSKey returns the existing AES key from Consul and only generates a new
// one when the KV is absent. Regenerating skey would make every stored AK/SK
// and JumpServer token undecryptable, so this function never overwrites it.
func EnsureSKey(ctx context.Context, st *Store, logger *slog.Logger) (string, error) {
	var current struct {
		SK string `json:"sk"`
	}
	found, err := st.GetValue(ctx, skeyPath, &current)
	if err != nil {
		return "", fmt.Errorf("read skey: %w", err)
	}
	if found {
		if len([]byte(current.SK)) != 32 {
			return "", fmt.Errorf("existing skey is invalid: expected 32 bytes, got %d", len([]byte(current.SK)))
		}
		return current.SK, nil
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate skey: %w", err)
	}
	key := hex.EncodeToString(buf)
	if err := st.PutKV(ctx, skeyPath, map[string]string{"sk": key}); err != nil {
		return "", fmt.Errorf("write skey: %w", err)
	}
	backup := "ConsulManager/assets/secret/bak-skey" + time.Now().Format("2006-01-02_15-04-05")
	if err := st.PutKV(ctx, backup, map[string]string{"sk": key}); err != nil {
		return "", fmt.Errorf("write skey backup: %w", err)
	}
	if logger != nil {
		logger.Warn("initialized new AES skey", "path", skeyPath, "backup", backup)
	}
	return key, nil
}
