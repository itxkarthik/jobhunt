// Package server is jobhunt's local HTTP API (`jobhunt serve`). It backs
// the web UI and gives agent builders a documented local API over the
// search engine and the store.
//
// It renders text fetched from the open internet into a browser, so it is
// locked down by default:
//   - it listens on loopback only unless told otherwise;
//   - requests must carry an allowed Host header, which blocks DNS rebinding;
//   - every /api request needs the per-process bearer token, and unsafe
//     cross-origin requests are rejected (CSRF);
//   - responses carry a strict Content-Security-Policy and related headers.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// MinTokenLen is the shortest API token accepted.
const MinTokenLen = 16

const (
	maxBody = 1 << 20 // request bodies are small JSON documents
	csp     = "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// shutdownWait is how long in-flight requests get before connections are
// closed; a variable so tests can shorten it.
var shutdownWait = 5 * time.Second

// SearchOptions are the engine options a search request may set.
type SearchOptions struct {
	Refresh    bool
	NewOnly    bool
	ShowHidden bool
	Sort       string // "score" (default) or "date"
}

// SearchFunc runs one search. progress, if non-nil, is called as each
// source finishes, possibly from several goroutines.
type SearchFunc func(ctx context.Context, f model.Filters, o SearchOptions, progress func(search.Report)) (*search.Result, error)

// Options configure a Server.
type Options struct {
	Store    *store.Store
	Search   SearchFunc
	Sources  []sources.Source // enabled sources, for readiness in /api/sources
	Catalog  []SourceDesc     // every known source, so disabled ones are listed too
	Defaults config.Defaults  // applied to empty filter fields, as in the CLI
	Token    string           // bearer token for /api; at least MinTokenLen
	Hosts    []string         // extra allowed Host names besides loopback
	Static   fs.FS            // web UI files; nil serves a short notice at /
	Logger   *slog.Logger

	// CheckBoard fetches a new job board once before it's saved; nil
	// refuses new boards.
	CheckBoard func(ctx context.Context, b ats.Company) (int, error)
	// RefreshBoards makes the ATS sources pick up changed boards.
	RefreshBoards func(ctx context.Context) error
	// Stack is the user's languages in priority order, for outreach drafts.
	Stack []string
}

// SourceDesc describes a source whether or not it's enabled.
type SourceDesc struct {
	Name        string
	Description string
	Credential  string // config field it needs, e.g. "api_key"; "" if none
}

// Server serves the API and the web UI.
type Server struct {
	opts    Options
	hosts   map[string]bool
	handler http.Handler
	log     *slog.Logger
	// searching allows one search at a time, to stay polite to sources.
	searching chan struct{}
}

// New validates opts and builds the handler.
func New(opts Options) (*Server, error) {
	if len(opts.Token) < MinTokenLen {
		return nil, fmt.Errorf("API token must be at least %d characters", MinTokenLen)
	}
	if opts.Store == nil || opts.Search == nil {
		return nil, errors.New("server needs a store and a search function")
	}
	s := &Server{
		opts:      opts,
		hosts:     map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true},
		log:       opts.Logger,
		searching: make(chan struct{}, 1),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	for _, h := range opts.Hosts {
		s.hosts[strings.ToLower(strings.Trim(h, "[]"))] = true
	}
	s.handler = s.secure(s.routes())
	return s, nil
}

// Handler returns the full handler, with security middleware applied.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve serves on ln until ctx is done, then shuts down gracefully.
// Long-running handlers (event streams) see their context cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute, // a cold search can take a minute; event streams clear it
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return base },
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	cancel()
	// Give in-flight requests a moment to finish; ctx itself is already done.
	sctx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
	defer stop()
	if err := srv.Shutdown(sctx); err != nil {
		// Something is still running (e.g. a write to a client that stopped
		// reading). Close the connections rather than hang on exit.
		_ = srv.Close()
		if serr := <-errc; !errors.Is(serr, http.ErrServerClosed) {
			return serr
		}
		s.log.Warn("forced shutdown: requests were still running", "err", err)
		return nil
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// secure applies the security policy to every request.
func (s *Server) secure(next http.Handler) http.Handler {
	// Rejects cross-origin unsafe requests using Sec-Fetch-Site / Origin.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request blocked")
	}))
	inner := csrf.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		if !s.allowedHost(r.Host) {
			writeError(w, http.StatusForbidden, "host not allowed")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			if !s.authorized(r) {
				h.Set("WWW-Authenticate", `Bearer realm="jobhunt"`)
				writeError(w, http.StatusUnauthorized, "missing or invalid token")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		inner.ServeHTTP(w, r)
	})
}

// allowedHost checks the Host header (with or without a port) against
// loopback names and the configured extras. This defeats DNS rebinding:
// a hostile page on evil.example resolving to 127.0.0.1 still sends
// "Host: evil.example".
func (s *Server) allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return s.hosts[strings.ToLower(strings.Trim(host, "[]"))]
}

// authorized checks "Authorization: Bearer <token>" in constant time.
func (s *Server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

// IsLoopback reports whether addr ("host:port") binds only to loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
