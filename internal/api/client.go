// Package api is a thin HTTP client for the adaa Admin API.
//
// It deliberately does not model the domain. Responses stay as JSON so that
// `--json` output is exactly what the API said, and the CLI never lags behind
// a field the server added.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxAttempts = 3

type Client struct {
	BaseURL   string
	Token     string
	UserAgent string
	HTTP      *http.Client
	// Debug receives one line per request when set.
	Debug io.Writer
}

func New(baseURL, token, userAgent string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

type Request struct {
	Method string
	Path   string
	Query  url.Values
	// Body is marshalled as JSON unless it is already a []byte.
	Body any
	// IdempotencyKey is generated for every mutation when empty. The same key
	// is reused across retries, which is what makes retrying a POST safe.
	IdempotencyKey string
	// Accept overrides the default JSON Accept header, for endpoints that
	// answer in other formats such as a DNS zone file.
	Accept string
}

type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Decode unmarshals the body, keeping numbers exact.
func (r *Response) Decode(v any) error {
	if len(r.Body) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(r.Body))
	d.UseNumber()
	return d.Decode(v)
}

// Do sends a request, retrying rate limits and brief outages when that is safe,
// and turns any non-2xx answer into a *Problem.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	var body []byte
	switch b := r.Body.(type) {
	case nil:
	case []byte:
		body = b
	default:
		var err error
		if body, err = json.Marshal(b); err != nil {
			return nil, err
		}
	}
	mutation := r.Method != http.MethodGet && r.Method != http.MethodHead
	key := r.IdempotencyKey
	if mutation && key == "" {
		key = NewIdempotencyKey()
	}
	return c.send(ctx, r.Method, r.Path, r.Query, r.Accept, func() (io.Reader, string) {
		if body == nil {
			return nil, ""
		}
		return bytes.NewReader(body), "application/json"
	}, key)
}

func (c *Client) send(ctx context.Context, method, path string, query url.Values, accept string, body func() (io.Reader, string), key string) (*Response, error) {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	for attempt := 1; ; attempt++ {
		rd, ctype := body()
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, err
		}
		if accept == "" {
			accept = "application/json, application/problem+json"
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", c.UserAgent)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}

		start := time.Now()
		resp, err := c.HTTP.Do(req)
		if err != nil {
			c.debugf("%s %s -> error after %s: %v", method, u, time.Since(start).Round(time.Millisecond), err)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &NetworkError{URL: c.BaseURL, Err: err}
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return nil, &NetworkError{URL: c.BaseURL, Err: err}
		}
		c.debugf("%s %s -> %d in %s", method, u, resp.StatusCode, time.Since(start).Round(time.Millisecond))

		if retryable(resp.StatusCode) && (method == http.MethodGet || key != "") && attempt < maxAttempts {
			wait := retryAfter(resp.Header, attempt)
			c.debugf("retrying in %s", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		out := &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
		if resp.StatusCode >= 400 {
			return out, parseProblem(resp.StatusCode, b)
		}
		return out, nil
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryAfter(h http.Header, attempt int) time.Duration {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return min(time.Duration(n)*time.Second, 30*time.Second)
		}
		if t, err := http.ParseTime(s); err == nil {
			return min(max(time.Until(t), 0), 30*time.Second)
		}
	}
	return time.Duration(attempt) * time.Second
}

func (c *Client) debugf(format string, args ...any) {
	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "[api] "+format+"\n", args...)
	}
}

// Upload sends a file as multipart/form-data, the way attachments are created.
func (c *Client) Upload(ctx context.Context, path, file, caption string) (*Response, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	key := NewIdempotencyKey()
	return c.send(ctx, http.MethodPost, path, nil, "", func() (io.Reader, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, _ := w.CreateFormFile("file", filepath.Base(file))
		_, _ = fw.Write(data)
		if caption != "" {
			_ = w.WriteField("caption", caption)
		}
		_ = w.Close()
		return &buf, w.FormDataContentType()
	}, key)
}

// Page is one page of a list endpoint.
type Page struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// List fetches up to limit items, following cursors. limit <= 0 means all.
func (c *Client) List(ctx context.Context, path string, query url.Values, limit int) (items []json.RawMessage, next string, err error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	for {
		pageSize := 200
		if limit > 0 {
			pageSize = min(limit-len(items), 200)
		}
		q.Set("limit", strconv.Itoa(pageSize))
		resp, err := c.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: q})
		if err != nil {
			return nil, "", err
		}
		var p Page
		if err := json.Unmarshal(resp.Body, &p); err != nil {
			return nil, "", fmt.Errorf("unexpected response from %s: %w", path, err)
		}
		items = append(items, p.Items...)
		if p.NextCursor == nil || *p.NextCursor == "" {
			return items, "", nil
		}
		if limit > 0 && len(items) >= limit {
			return items, *p.NextCursor, nil
		}
		q.Set("cursor", *p.NextCursor)
	}
}

func NewIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "cli-" + hex.EncodeToString(b)
}

// NetworkError means the API could not be reached at all.
type NetworkError struct {
	URL string
	Err error
}

func (e *NetworkError) Error() string {
	var dnsErr *net.DNSError
	if errors.As(e.Err, &dnsErr) {
		return fmt.Sprintf("could not reach %s: %s does not resolve", e.URL, dnsErr.Name)
	}
	if errors.Is(e.Err, context.DeadlineExceeded) || os.IsTimeout(e.Err) {
		return fmt.Sprintf("could not reach %s: timed out", e.URL)
	}
	return fmt.Sprintf("could not reach %s: %v", e.URL, e.Err)
}

func (e *NetworkError) Unwrap() error { return e.Err }
