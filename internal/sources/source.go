// Package sources defines the Source plugin interface and the shared HTTP
// plumbing (honest User-Agent, rate limiting, retries) every source uses.
package sources

import (
	"context"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Source is one job provider.
type Source interface {
	Name() string
	Supports(f model.Filters) bool // skip sources that can't serve this country/mode
	Fetch(ctx context.Context, f model.Filters) ([]model.Job, error)
}

// Readier is implemented by sources that need configuration (API keys).
// A non-nil error means "skip with this message".
type Readier interface {
	Ready() error
}

// Linker is implemented by sources that can produce a browser search URL for
// the current filters, shown when the source is skipped or fails.
type Linker interface {
	SearchURL(f model.Filters) string
}

// MinTTLer lets a source impose a minimum cache TTL, e.g. when its terms ask
// for infrequent polling.
type MinTTLer interface {
	MinTTL() time.Duration
}

// RateLimited sets the per-source request rate (default 1 req/s).
type RateLimited interface {
	Rate() rate.Limit
}

// CacheKeyer lets a source say which parts of the filters affect its request.
// Sources that always fetch the same feed and filter locally return a
// constant, so one cached fetch serves every search.
type CacheKeyer interface {
	CacheKey(f model.Filters) string
}

// ErrNotConfigured is returned by Ready when credentials are missing.
type ErrNotConfigured struct{ Msg string }

func (e ErrNotConfigured) Error() string { return e.Msg }
