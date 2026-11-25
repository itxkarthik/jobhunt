package feed

import (
	"context"
	"crypto/hmac"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var secret = []byte("0123456789abcdef-test-secret")

// fastWebhook returns a sender with no backoff delay and a fixed clock.
func fastWebhook(t *testing.T, url string, secret []byte) *Webhook {
	t.Helper()
	w, err := NewWebhook(url, secret, "jobhunt-test")
	if err != nil {
		t.Fatal(err)
	}
	w.backoff = func(int) time.Duration { return 0 }
	w.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return w
}

// server answers each request with the next status in codes (the last one repeats).
func server(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		w.WriteHeader(codes[min(n, len(codes)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestWebhookSignedDelivery(t *testing.T) {
	body := []byte(`{"v":1,"type":"run"}` + "\n")
	var got *http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	if err := fastWebhook(t, srv.URL+"/hook", secret).Send(context.Background(), "run1", body); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPost || got.URL.Path != "/hook" || string(gotBody) != string(body) {
		t.Fatalf("got %s %s %q", got.Method, got.URL.Path, gotBody)
	}
	h := got.Header
	for k, want := range map[string]string{
		"Content-Type":  "application/x-ndjson",
		HeaderDelivery:  "run1",
		HeaderVersion:   "1",
		HeaderTimestamp: "1800000000",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	if !verify(secret, h.Get(HeaderTimestamp), gotBody, h.Get(HeaderSignature)) {
		t.Error("signature does not verify")
	}
}

func TestWebhookUnsigned(t *testing.T) {
	var sig atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig.Store(r.Header.Get(HeaderSignature))
	}))
	defer srv.Close()
	if err := fastWebhook(t, srv.URL, nil).Send(context.Background(), "run1", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if s := sig.Load().(string); s != "" {
		t.Errorf("unsigned webhook sent signature %q", s)
	}
}

func TestWebhookRetries(t *testing.T) {
	tests := []struct {
		name      string
		codes     []int
		wantCalls int32
		wantCode  int // 0 = success
	}{
		{"ok", []int{200}, 1, 0},
		{"2xx is success", []int{204}, 1, 0},
		{"500 then ok", []int{500, 200}, 2, 0},
		{"429 then ok", []int{429, 429, 202}, 3, 0},
		{"408 is retried", []int{408, 200}, 2, 0},
		{"gives up after 4", []int{503}, 4, 503},
		{"400 is not retried", []int{400}, 1, 400},
		{"401 is not retried", []int{401, 200}, 1, 401},
		{"redirect is not followed", []int{302}, 1, 302},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, calls := server(t, tt.codes...)
			err := fastWebhook(t, srv.URL, secret).Send(context.Background(), "run1", []byte("x\n"))
			if calls.Load() != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			se, ok := errors.AsType[*StatusError](err)
			if !ok || se.Code != tt.wantCode {
				t.Fatalf("err = %v, want HTTP %d", err, tt.wantCode)
			}
		})
	}
}

func TestWebhookRedirectTargetNotHit(t *testing.T) {
	target, hits := server(t, 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if err := fastWebhook(t, srv.URL, secret).Send(context.Background(), "run1", []byte("x\n")); err == nil {
		t.Fatal("redirect treated as success")
	}
	if hits.Load() != 0 {
		t.Error("signed batch was forwarded to the redirect target")
	}
}

func TestWebhookErrorsHideURLSecrets(t *testing.T) {
	srv, _ := server(t, 200)
	url := srv.URL + "/hooks/secret-token?key=also-secret"
	srv.Close() // connection refused on every attempt
	err := fastWebhook(t, url, nil).Send(context.Background(), "run1", []byte("x\n"))
	if err == nil {
		t.Fatal("want error from closed server")
	}
	if msg := err.Error(); strings.Contains(msg, "secret") || strings.Contains(msg, "/hooks") {
		t.Errorf("error leaks the URL path or query: %s", msg)
	}
	if se, _ := errors.AsType[*StatusError](err); se != nil {
		t.Errorf("network error reported as HTTP status: %v", err)
	}
}

func TestWebhookCancelDuringBackoff(t *testing.T) {
	srv, calls := server(t, 503)
	w := fastWebhook(t, srv.URL, nil)
	w.backoff = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Send(ctx, "run1", []byte("x\n")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestNewWebhook(t *testing.T) {
	tests := []struct {
		url       string
		secret    string
		ok        bool
		plaintext bool
		display   string
	}{
		{"https://hooks.example.com/x?token=t", "", true, false, "https://hooks.example.com"},
		{"http://example.com/x", "", true, true, "http://example.com"},
		{"http://localhost:8080/x", "", true, false, "http://localhost:8080"},
		{"http://127.0.0.1:9/x", "", true, false, "http://127.0.0.1:9"},
		{"http://[::1]:9/x", "", true, false, "http://[::1]:9"},
		{"https://example.com", string(secret), true, false, "https://example.com"},
		{"https://example.com", "short", false, false, ""},
		{"ftp://example.com/x", "", false, false, ""},
		{"file:///etc/passwd", "", false, false, ""},
		{"javascript:alert(1)", "", false, false, ""},
		{"/relative/path", "", false, false, ""},
		{"https://user:pass@example.com/x", "", false, false, ""},
		{"", "", false, false, ""},
	}
	for _, tt := range tests {
		w, err := NewWebhook(tt.url, []byte(tt.secret), "ua")
		if (err == nil) != tt.ok {
			t.Errorf("NewWebhook(%q) err = %v, want ok=%v", tt.url, err, tt.ok)
			continue
		}
		if err == nil && (w.Plaintext() != tt.plaintext || w.String() != tt.display) {
			t.Errorf("NewWebhook(%q): plaintext=%v display=%q, want %v %q", tt.url, w.Plaintext(), w.String(), tt.plaintext, tt.display)
		}
	}
}

// verify checks a signature the way a receiver should: recompute it with
// Sign and compare in constant time.
func verify(secret []byte, ts string, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, ts, body)), []byte(signature))
}

func TestVerify(t *testing.T) {
	body := []byte("x\n")
	sig := Sign(secret, "100", body)
	tests := []struct {
		name   string
		secret []byte
		ts     string
		body   string
		sig    string
		ok     bool
	}{
		{"valid", secret, "100", "x\n", sig, true},
		{"tampered body", secret, "100", "y\n", sig, false},
		{"replayed with new timestamp", secret, "200", "x\n", sig, false},
		{"wrong secret", []byte("another-secret-value"), "100", "x\n", sig, false},
		{"missing", secret, "100", "x\n", "", false},
	}
	for _, tt := range tests {
		if got := verify(tt.secret, tt.ts, []byte(tt.body), tt.sig); got != tt.ok {
			t.Errorf("%s: verify = %v, want %v", tt.name, got, tt.ok)
		}
	}
}
