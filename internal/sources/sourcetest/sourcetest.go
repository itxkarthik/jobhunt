// Package sourcetest serves recorded fixtures to source parsers in tests.
package sourcetest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Request is one request the fake server received.
type Request struct {
	Path   string
	Query  map[string][]string
	Header http.Header
}

// Server serves fixture files by URL path; unknown paths return 404.
type Server struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []Request
}

// Serve starts a server mapping URL paths to fixture files.
func Serve(t *testing.T, routes map[string]string) *Server {
	t.Helper()
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, Request{Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone()})
		s.mu.Unlock()
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("fixture %s: %v", file, err)
			w.WriteHeader(500)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

// Requests returns what the server received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Client is a fast, unthrottled client for tests.
func Client() *sources.Client {
	c := sources.NewClient("jobhunt-test", rate.Inf)
	c.Retries = 0
	return c
}
