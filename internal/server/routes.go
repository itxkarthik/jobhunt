package server

import (
	"net/http"
	"strings"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	s.apiRoutes(mux)
	if s.opts.Static != nil {
		mux.Handle("GET /", http.FileServerFS(s.opts.Static))
	} else {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("jobhunt is running. The JSON API is under /api/ (see docs/api.md).\n"))
		})
	}
	return jsonErrors(mux)
}

// jsonErrors turns the mux's own plain-text 404 and 405 answers for /api/
// paths into JSON errors, keeping the Allow header. (A catch-all "/api/"
// route can't do this: it would also swallow wrong-method requests.)
func jsonErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" || !strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		probe := &statusProbe{header: http.Header{}}
		h.ServeHTTP(probe, r)
		switch probe.code {
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "no such endpoint")
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", probe.header.Get("Allow"))
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		default: // e.g. a redirect to the cleaned path
			mux.ServeHTTP(w, r)
		}
	})
}

// statusProbe records what a handler would answer without sending it.
type statusProbe struct {
	header http.Header
	code   int
}

func (p *statusProbe) Header() http.Header         { return p.header }
func (p *statusProbe) Write(b []byte) (int, error) { return len(b), nil }
func (p *statusProbe) WriteHeader(code int)        { p.code = code }
