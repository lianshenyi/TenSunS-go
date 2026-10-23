// Package jumpserver will contain the V2/V3 API client and asset sync flow.
package jumpserver

import "context"

// Syncer is the typed job entry point for JumpServer synchronization.
type Syncer interface {
	Run(ctx context.Context, vendor, account string) error
}
