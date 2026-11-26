// Package sources defines the Source plugin interface and the shared HTTP
// plumbing (honest User-Agent, rate limiting, retries) every source uses.
package sources

import (
	"context"
	"fmt"
	"strings"
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

// MinTTLer imposes a hard minimum cache TTL because the source's terms ask
// for infrequent polling. Even --refresh honors it.
type MinTTLer interface {
	MinTTL() time.Duration
}

// DefaultTTLer suggests a cache TTL for heavy or slowly changing sources.
// Config's per-source ttl overrides it, and --refresh bypasses it.
type DefaultTTLer interface {
	DefaultTTL() time.Duration
}

// RateLimited sets the per-source request rate (default 1 req/s).
type RateLimited interface {
	Rate() rate.Limit
}

// Timeouter lets slow multi-request sources (many company boards) ask for a
// longer fetch timeout than the global default.
type Timeouter interface {
	Timeout() time.Duration
}

// CacheKeyer lets a source say which parts of the filters affect its request.
// Sources that always fetch the same feed and filter locally return a
// constant, so one cached fetch serves every search.
type CacheKeyer interface {
	CacheKey(f model.Filters) string
}

// Splitter is a source made of parts fetched on their own, like an ATS's
// company boards. The engine caches each part separately, so searches
// that need different sets of parts share the cached ones, and calls
// FetchPart instead of Fetch.
type Splitter interface {
	Parts(f model.Filters) []string
	FetchPart(ctx context.Context, part string) ([]model.Job, error)
}

// NotConfiguredError is returned by Ready when credentials are missing.
type NotConfiguredError struct{ Msg string }

func (e NotConfiguredError) Error() string { return e.Msg }

// PartialError reports that some sub-requests failed (e.g. a few company
// boards are down) while others succeeded. Fetch returns it alongside the
// jobs it did get; the engine treats the source as ok with a warning.
type PartialError struct {
	Failed []string // e.g. board slugs
	Total  int
	Err    error // first underlying error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%d/%d failed (%s): %v", len(e.Failed), e.Total, strings.Join(firstN(e.Failed, 5), ", "), e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], "…")
	}
	return xs
}
