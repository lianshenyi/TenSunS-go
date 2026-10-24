// Package awscloud provides the fixture-only AWS adapter.
package awscloud

import (
	"github.com/lianshenyi/TenSunS-go/internal/cloud/fixture"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// NewFixtureProvider creates an AWS Provider backed by its isolated
// desensitized fixture namespace.
func NewFixtureProvider(st store.Store) fixture.Provider {
	return fixture.Provider{Store: st, Vendor: "awscloud"}
}
