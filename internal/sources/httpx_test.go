package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func testClient() *Client {
	c := NewClient("jobhunt-test", rate.Inf)
	c.Backoff = time.Millisecond
	return c
}

func TestGetRetries(t *testing.T) {
	tests := []struct {
		name      string
		codes     []int // response code per attempt
		wantErr   bool
		wantCalls int32
	}{
		{"ok", []int{200}, false, 1},
		{"503 then ok", []int{503, 200}, false, 2},
		{"429 twice then ok", []int{429, 429, 200}, false, 3},
		{"500 exhausts retries", []int{500, 500, 500, 500}, true, 3},
		{"404 not retried", []int{404, 200}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.WriteHeader(tt.codes[n-1])
				w.Write([]byte("body"))
			}))
			defer srv.Close()
			_, err := testClient().Get(context.Background(), srv.URL, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if calls.Load() != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
			var se *StatusError
			if tt.wantErr && !errors.As(err, &se) {
				t.Errorf("want *StatusError, got %T", err)
			}
		})
	}
}

func TestStatusErrorRedactsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := testClient().Get(context.Background(), srv.URL+"/x?app_id=secret1&app_key=secret2&q=go", nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks credentials: %v", err)
	}
}

// A redirect must not carry key headers to another host (or port); one
// that stays on the same host keeps them.
func TestRedirectKeepsKeysOnOrigin(t *testing.T) {
	var got http.Header
	record := func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		got = r.Header.Clone()
		_, _ = w.Write([]byte("{}"))
	}
	api := httptest.NewServer(http.HandlerFunc(record))
	defer api.Close()
	other := httptest.NewServer(http.HandlerFunc(record))
	defer other.Close()
	keys := map[string]string{"x-api-key": "k1", "X-RapidAPI-Key": "k2", "Authorization": "Bearer k3"}
	tests := []struct {
		name, to string
		keep     bool
	}{
		{"same host", api.URL + "/final", true},
		{"other port", other.URL + "/final", false},
		{"other host", strings.Replace(other.URL, "127.0.0.1", "localhost", 1) + "/final", false},
	}
	for _, tt := range tests {
		got = nil
		var v any
		if err := testClient().GetJSON(context.Background(), api.URL+"/?to="+tt.to, keys, &v); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if kept := got.Get("x-api-key") != "" || got.Get("X-RapidAPI-Key") != ""; kept != tt.keep {
			t.Errorf("%s: key headers kept = %v, want %v", tt.name, kept, tt.keep)
		}
		if !tt.keep && got.Get("Authorization") != "" {
			t.Errorf("%s: Authorization reached another host", tt.name)
		}
		if got.Get("User-Agent") == "" {
			t.Errorf("%s: User-Agent was dropped", tt.name)
		}
	}
}

func TestErrorsRedactKeys(t *testing.T) {
	ctx := context.Background()
	q := "?app_id=secret1&app_key=secret2&q=go"
	// A body that isn't JSON: the decode error quotes the URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	var v any
	err := testClient().GetJSON(ctx, srv.URL+"/x"+q, nil, &v)
	srv.Close()
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("decode error leaks credentials: %v", err)
	}
	// A transport failure: *url.Error carries the URL.
	err = testClient().GetJSON(ctx, srv.URL+"/x"+q, nil, &v) // server closed
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("transport error leaks credentials: %v", err)
	}
	got := RedactURLs(`decode https://api.example.com/jobs?app_key=abc&q=go: bad; see "http://x.io/?token=t1"`)
	if strings.Contains(got, "abc") || strings.Contains(got, "t1") || !strings.Contains(got, "q=go") {
		t.Errorf("RedactURLs = %s", got)
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct{ in, want string }{
		{"<p>Hello <b>world</b></p><p>Two</p>", "Hello world\n\nTwo"},
		{"&lt;p&gt;escaped&lt;/p&gt;", "escaped"},
		{"a&amp;b&nbsp;c", "a&b c"},
		{"<ul><li>one</li><li>two</li></ul>", "one\n\ntwo"},
	}
	for _, tt := range tests {
		if got := StripHTML(tt.in); got != tt.want {
			t.Errorf("StripHTML(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	for _, s := range []string{"2026-10-21T09:15:00Z", "2026-10-21T09:15:00", "Wed, 21 Oct 2026 09:15:00 +0000", "2026-10-21"} {
		if ParseTime(s).IsZero() {
			t.Errorf("ParseTime(%q) failed", s)
		}
	}
}
