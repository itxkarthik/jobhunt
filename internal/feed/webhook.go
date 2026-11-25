package feed

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Webhook request headers. A receiver should check the signature, reject
// stale timestamps, and dedupe on the delivery ID.
const (
	HeaderSignature = "X-Jobhunt-Signature" // "sha256=" + hex HMAC-SHA256 of "<timestamp>.<body>"
	HeaderTimestamp = "X-Jobhunt-Timestamp" // Unix seconds when this attempt was sent
	HeaderDelivery  = "X-Jobhunt-Delivery"  // run ID; the same across retries
	HeaderVersion   = "X-Jobhunt-Version"   // event schema version
)

// MinSecretLen is the shortest webhook signing secret accepted.
const MinSecretLen = 16

const (
	defaultAttempts = 4
	requestTimeout  = 15 * time.Second
	maxRetryWait    = time.Minute
)

// Webhook POSTs NDJSON batches to an HTTP(S) endpoint, signing each
// request when a secret is set and retrying transient failures.
type Webhook struct {
	url       string
	display   string // scheme://host only; the path or query may hold a token
	plaintext bool
	secret    []byte
	userAgent string
	client    *http.Client

	attempts int
	backoff  func(retry int) time.Duration
	now      func() time.Time
}

// NewWebhook validates rawURL and returns a sender. secret may be empty
// (unsigned); otherwise it must be at least MinSecretLen bytes.
func NewWebhook(rawURL string, secret []byte, userAgent string) (*Webhook, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("webhook URL must be an absolute http(s) URL")
	}
	if u.User != nil {
		return nil, errors.New("webhook URL must not contain credentials; use JOBHUNT_WEBHOOK_SECRET to sign requests instead")
	}
	if len(secret) > 0 && len(secret) < MinSecretLen {
		return nil, fmt.Errorf("webhook secret is too short: use at least %d characters", MinSecretLen)
	}
	return &Webhook{
		url:       u.String(),
		display:   u.Scheme + "://" + u.Host,
		plaintext: u.Scheme == "http" && !isLoopback(u.Hostname()),
		secret:    secret,
		userAgent: userAgent,
		client: &http.Client{
			Timeout: requestTimeout,
			// A redirect could send a signed batch somewhere else, and Go
			// turns a redirected POST into a GET. Treat it as an error.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		attempts: defaultAttempts,
		backoff:  func(retry int) time.Duration { return time.Second << retry },
		now:      time.Now,
	}, nil
}

// String returns the endpoint without its path or query, safe to print.
func (w *Webhook) String() string { return w.display }

// Plaintext reports whether batches would cross the network unencrypted.
func (w *Webhook) Plaintext() bool { return w.plaintext }

// Signed reports whether requests carry an HMAC signature.
func (w *Webhook) Signed() bool { return len(w.secret) > 0 }

// StatusError is a non-2xx webhook response.
type StatusError struct {
	Endpoint string
	Code     int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("webhook %s: HTTP %d", e.Endpoint, e.Code)
}

// Send delivers one batch, retrying network errors, 408, 429 and 5xx
// with exponential backoff (or Retry-After, up to a minute).
func (w *Webhook) Send(ctx context.Context, deliveryID string, body []byte) error {
	return w.SendAs(ctx, deliveryID, "application/x-ndjson", body)
}

// SendAs is Send with another content type, for payloads that aren't feed
// events (the digest is application/json). Signing is the same.
func (w *Webhook) SendAs(ctx context.Context, deliveryID, contentType string, body []byte) error {
	var err error
	for attempt := range w.attempts {
		if attempt > 0 {
			wait := w.backoff(attempt - 1)
			if ra, ok := errors.AsType[*retryAfterError](err); ok && ra.after > 0 {
				wait = ra.after
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(min(wait, maxRetryWait)):
			}
		}
		var retry bool
		retry, err = w.post(ctx, deliveryID, contentType, body)
		if err == nil {
			return nil
		}
		if !retry || ctx.Err() != nil {
			break
		}
	}
	if ra, ok := errors.AsType[*retryAfterError](err); ok {
		return ra.StatusError
	}
	return err
}

type retryAfterError struct {
	*StatusError
	after time.Duration
}

func (w *Webhook) post(ctx context.Context, deliveryID, contentType string, body []byte) (retry bool, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("webhook %s: %w", w.display, err)
	}
	ts := strconv.FormatInt(w.now().Unix(), 10)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", w.userAgent)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderDelivery, deliveryID)
	req.Header.Set(HeaderVersion, strconv.Itoa(Version))
	if w.Signed() {
		req.Header.Set(HeaderSignature, Sign(w.secret, ts, body))
	}
	resp, err := w.client.Do(req)
	if err != nil {
		// *url.Error repeats the full URL, which may contain a token.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return true, fmt.Errorf("webhook %s: %w", w.display, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused

	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return false, nil
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		se := &StatusError{Endpoint: w.display, Code: code}
		return true, &retryAfterError{StatusError: se, after: sources.ParseRetryAfter(resp.Header.Get("Retry-After"))}
	default:
		return false, &StatusError{Endpoint: w.display, Code: code}
	}
}

// Sign returns the signature header value for body sent at timestamp ts.
// Signing the timestamp too lets receivers reject replayed requests.
func Sign(secret []byte, ts string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte{'.'})
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
