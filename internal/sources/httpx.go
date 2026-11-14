package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// Client wraps http.Client with an honest User-Agent, a per-source rate
// limiter, and retries with exponential backoff on 429/5xx/network errors.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	Limiter   *rate.Limiter
	Retries   int           // extra attempts after the first
	Backoff   time.Duration // base delay, doubled each attempt
	MaxBody   int64
}

// NewClient returns a client allowing r requests/second (burst 1).
func NewClient(ua string, r rate.Limit) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: ua,
		Limiter:   rate.NewLimiter(r, 1),
		Retries:   2,
		Backoff:   500 * time.Millisecond,
		MaxBody:   32 << 20,
	}
}

// StatusError is returned for non-2xx responses after retries are exhausted.
type StatusError struct {
	Code int
	URL  string
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d %s", e.URL, e.Code, e.Body)
}

// Get fetches url and returns the body. headers may be nil.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			d := c.Backoff << (attempt - 1)
			var ra *retryAfterError
			if errors.As(lastErr, &ra) && ra.after > 0 {
				d = ra.after
			}
			if d > 30*time.Second {
				d = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
		}
		body, retry, err := c.do(ctx, url, headers)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	if ra, ok := errors.AsType[*retryAfterError](lastErr); ok {
		return nil, ra.StatusError
	}
	return nil, lastErr
}

// GetJSON fetches url and decodes the JSON body into out.
func (c *Client) GetJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	b, err := c.Get(ctx, url, headers)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

type retryAfterError struct {
	*StatusError
	after time.Duration
}

func (c *Client) do(ctx context.Context, url string, headers map[string]string) ([]byte, bool, error) {
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, false, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json, application/rss+xml, application/xml;q=0.9, */*;q=0.8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBody))
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, false, nil
	}
	snippet := string(body)
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	se := &StatusError{Code: resp.StatusCode, URL: redact(url), Body: snippet}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, &retryAfterError{StatusError: se, after: ParseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil, false, se
}

// ParseRetryAfter reads a Retry-After header (seconds or an HTTP date).
// It returns 0 when the header is missing or invalid.
func ParseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}
