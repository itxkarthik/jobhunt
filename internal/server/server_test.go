package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

const testToken = "test-token-0123456789abcdef"

func newTestServer(t *testing.T, mod func(*Options)) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	opts := Options{
		Store: st,
		Search: func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
			return &search.Result{RunAt: time.Now()}, nil
		},
		Defaults: config.Defaults{Countries: []string{"IN"}},
		Token:    testToken,
	}
	if mod != nil {
		mod(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

// req builds a request that passes every check by default; tests then
// break one thing at a time.
func req(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Authorization", "Bearer "+testToken)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestSecurityChecks(t *testing.T) {
	s, _ := newTestServer(t, nil)
	tests := []struct {
		name string
		mod  func(*http.Request)
		want int
	}{
		{"valid request", func(*http.Request) {}, http.StatusOK},
		{"host localhost", func(r *http.Request) { r.Host = "localhost:7777" }, http.StatusOK},
		{"host ::1", func(r *http.Request) { r.Host = "[::1]:7777" }, http.StatusOK},
		{"host without port", func(r *http.Request) { r.Host = "127.0.0.1" }, http.StatusOK},
		{"host case-insensitive", func(r *http.Request) { r.Host = "LOCALHOST:7777" }, http.StatusOK},
		// DNS rebinding: evil.example resolves to 127.0.0.1 but keeps its Host.
		{"rebinding host", func(r *http.Request) { r.Host = "evil.example:7777" }, http.StatusForbidden},
		{"rebinding host without port", func(r *http.Request) { r.Host = "evil.example" }, http.StatusForbidden},
		{"loopback-looking suffix", func(r *http.Request) { r.Host = "127.0.0.1.evil.example" }, http.StatusForbidden},
		{"empty host", func(r *http.Request) { r.Host = "" }, http.StatusForbidden},
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope-nope-nope-nope") }, http.StatusUnauthorized},
		{"token prefix", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken[:10]) }, http.StatusUnauthorized},
		{"token plus extra", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken+"x") }, http.StatusUnauthorized},
		{"token without scheme", func(r *http.Request) { r.Header.Set("Authorization", testToken) }, http.StatusUnauthorized},
		{"token in query", func(r *http.Request) {
			r.Header.Del("Authorization")
			r.URL.RawQuery = "token=" + testToken
		}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := req(http.MethodGet, "/api/health", "")
			tt.mod(r)
			w := serve(s, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body)
			}
			if w.Code == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without WWW-Authenticate")
			}
			if w.Code != http.StatusOK && !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("error body is not JSON: %s", w.Body)
			}
		})
	}
}

func TestCrossOriginWritesBlocked(t *testing.T) {
	s, _ := newTestServer(t, nil)
	tests := []struct {
		name   string
		header map[string]string
		want   int
	}{
		// 405: the request passed CSRF checks and reached the router.
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusMethodNotAllowed},
		{"non-browser client", nil, http.StatusMethodNotAllowed},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site (other port)", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := req(http.MethodPost, "/api/health", "{}")
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			if w := serve(s, r); w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t, nil)
	bad := req(http.MethodGet, "/api/health", "")
	bad.Host = "evil.example"
	noToken := req(http.MethodGet, "/api/health", "")
	noToken.Header.Del("Authorization")
	for name, r := range map[string]*http.Request{
		"api":        req(http.MethodGet, "/api/health", ""),
		"index":      req(http.MethodGet, "/", ""),
		"bad host":   bad,
		"no token":   noToken,
		"api 404":    req(http.MethodGet, "/api/nope", ""),
		"page 404":   req(http.MethodGet, "/nope", ""),
		"api method": req(http.MethodDelete, "/api/health", ""),
	} {
		w := serve(s, r)
		h := w.Header()
		for k, want := range map[string]string{
			"Content-Security-Policy": csp,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		} {
			if h.Get(k) != want {
				t.Errorf("%s: %s = %q, want %q", name, k, h.Get(k), want)
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Host != "evil.example" && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: API response is cacheable", name)
		}
	}
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP too weak: %s", csp)
	}
}

func TestIndexNeedsNoToken(t *testing.T) {
	s, _ := newTestServer(t, nil)
	r := req(http.MethodGet, "/", "")
	r.Header.Del("Authorization")
	w := serve(s, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/api/") {
		t.Fatalf("index = %d %q", w.Code, w.Body)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	s, _ := newTestServer(t, nil)
	w := serve(s, req(http.MethodGet, "/api/does-not-exist", ""))
	var body map[string]string
	if w.Code != http.StatusNotFound || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] == "" {
		t.Fatalf("got %d %q", w.Code, w.Body)
	}
}

func TestExtraHosts(t *testing.T) {
	s, _ := newTestServer(t, func(o *Options) { o.Hosts = []string{"jobs.lan", "[fd00::1]"} })
	for host, want := range map[string]int{
		"jobs.lan:7777":  http.StatusOK,
		"[fd00::1]:7777": http.StatusOK,
		"other.lan:7777": http.StatusForbidden,
	} {
		r := req(http.MethodGet, "/api/health", "")
		r.Host = host
		if w := serve(s, r); w.Code != want {
			t.Errorf("host %s: status %d, want %d", host, w.Code, want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok := func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
		return nil, nil
	}
	for name, o := range map[string]Options{
		"short token": {Store: st, Search: ok, Token: "short"},
		"no store":    {Search: ok, Token: testToken},
		"no search":   {Store: st, Token: testToken},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%s: New accepted invalid options", name)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true,
		"localhost:7777": true,
		"[::1]:7777":     true,
		"127.0.0.2:7777": true,
		"0.0.0.0:7777":   false,
		":7777":          false,
		"[::]:7777":      false,
		"192.168.1.5:80": false,
		"127.0.0.1":      false, // no port
	} {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestServeAndShutdown(t *testing.T) {
	s, _ := newTestServer(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health = %d %s", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not shut down")
	}
}

func TestWrongMethodIsJSON405(t *testing.T) {
	s, _ := newTestServer(t, nil)
	w := serve(s, req(http.MethodDelete, "/api/health", ""))
	if w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Header().Get("Allow"), "GET") ||
		!strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("got %d Allow=%q %s", w.Code, w.Header().Get("Allow"), w.Body)
	}
}

// A request that ignores cancellation must not stop the server exiting:
// after shutdownWait the connections are closed.
func TestServeForcesShutdown(t *testing.T) {
	old := shutdownWait
	shutdownWait = 200 * time.Millisecond
	t.Cleanup(func() { shutdownWait = old })

	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, _ := newTestServer(t, func(o *Options) {
		o.Search = func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
			close(started)
			<-release // deliberately ignores ctx
			return &search.Result{}, nil
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	go func() {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			"http://"+ln.Addr().String()+"/api/search", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		if resp, err := http.DefaultClient.Do(r); err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil after a forced shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve hung on a request that ignores cancellation")
	}
}

func TestStaticUI(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>jobhunt</title>")},
		"js/app.js":  {Data: []byte("export {};")},
		"app.css":    {Data: []byte("body{}")},
		"f/a.woff2":  {Data: []byte("wOF2")},
		"l/a.txt":    {Data: []byte("licence")},
	}
	s, _ := newTestServer(t, func(o *Options) { o.Static = ui })
	tests := []struct {
		name, path, host string
		token            bool
		want             int
	}{
		{"index without a token", "/", "127.0.0.1:7777", false, http.StatusOK},
		{"module", "/js/app.js", "localhost:7777", false, http.StatusOK},
		{"no directory listing", "/js/", "127.0.0.1:7777", false, http.StatusNotFound},
		{"missing file", "/nope.js", "127.0.0.1:7777", false, http.StatusNotFound},
		{"foreign host", "/", "evil.example", false, http.StatusForbidden},
		{"api still needs the token", "/api/meta", "127.0.0.1:7777", false, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		r := req(http.MethodGet, tt.path, "")
		r.Host = tt.host
		if !tt.token {
			r.Header.Del("Authorization")
		}
		w := serve(s, r)
		if w.Code != tt.want {
			t.Errorf("%s: GET %s = %d, want %d", tt.name, tt.path, w.Code, tt.want)
		}
		if w.Header().Get("Content-Security-Policy") != csp {
			t.Errorf("%s: missing CSP", tt.name)
		}
		if tt.want == http.StatusOK && w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", tt.name, w.Header().Get("Cache-Control"))
		}
	}
	for p, want := range map[string]string{
		"/": "text/html; charset=utf-8", "/js/app.js": "text/javascript; charset=utf-8", "/app.css": "text/css; charset=utf-8",
		"/f/a.woff2": "font/woff2", "/l/a.txt": "text/plain; charset=utf-8",
	} {
		if got := serve(s, req(http.MethodGet, p, "")).Header().Get("Content-Type"); got != want {
			t.Errorf("GET %s: Content-Type = %q, want %q", p, got, want)
		}
	}
	if w := serve(s, req(http.MethodGet, "/", "")); !strings.Contains(w.Body.String(), "<title>jobhunt</title>") {
		t.Errorf("index body = %q", w.Body.String())
	}
	// Regression: the UI's catch-all must not answer unknown API paths.
	for _, tt := range []struct {
		method, path, allow string
		want                int
	}{
		{http.MethodGet, "/api/does-not-exist", "", http.StatusNotFound},
		{http.MethodPost, "/api/does-not-exist", "", http.StatusNotFound},
		{http.MethodPut, "/api/meta", "GET, HEAD", http.StatusMethodNotAllowed},
	} {
		w := serve(s, req(tt.method, tt.path, ""))
		if w.Code != tt.want || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Allow") != tt.allow {
			t.Errorf("%s %s = %d %q, Allow %q; want %d JSON, Allow %q", tt.method, tt.path, w.Code,
				w.Header().Get("Content-Type"), w.Header().Get("Allow"), tt.want, tt.allow)
		}
	}
}
