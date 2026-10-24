// Package tencentcloud provides the fixture-only Tencent Cloud adapter.
package tencentcloud

import (
	"github.com/lianshenyi/TenSunS-go/internal/cloud/fixture"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// NewFixtureProvider creates a Tencent Cloud Provider backed by its isolated
// desensitized fixture namespace.
func NewFixtureProvider(st store.Store) fixture.Provider {
	return fixture.Provider{Store: st, Vendor: "tencent_cloud"}
}
