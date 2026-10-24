// Package huaweicloud provides the fixture-only Huawei Cloud adapter.
package huaweicloud

import (
	"github.com/lianshenyi/TenSunS-go/internal/cloud/fixture"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// NewFixtureProvider creates a Huawei Cloud Provider backed by its isolated
// desensitized fixture namespace.
func NewFixtureProvider(st store.Store) fixture.Provider {
	return fixture.Provider{Store: st, Vendor: "huaweicloud"}
}
