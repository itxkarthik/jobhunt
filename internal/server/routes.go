package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// routes keeps the API in a mux of its own: in a shared one, the web UI's
// "GET /" catch-all would also match unknown /api/ paths and answer them
// with the file server's plain-text 404 instead of a JSON error.
func (s *Server) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	s.apiRoutes(api)

	root := http.NewServeMux()
	if s.opts.Static != nil {
		root.Handle("GET /", static(s.opts.Static))
	} else {
		root.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("jobhunt is running. The JSON API is under /api/ (see docs/api.md).\n"))
		})
	}
	apiHandler := jsonErrors(api)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		root.ServeHTTP(w, r)
	})
}

// jsonErrors turns the API mux's own plain-text 404 and 405 answers into
// JSON errors, keeping the Allow header. (A catch-all "/api/" route inside
// that mux can't do this: it would also swallow wrong-method requests.)
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

// static serves the web UI files. Directory listings are refused, and
// browsers revalidate each file so a new jobhunt binary's UI shows up
// without a hard refresh.
func static(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
