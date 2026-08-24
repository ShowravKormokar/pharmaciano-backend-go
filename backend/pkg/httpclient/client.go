// Package httpclient is a retrying, timeout-bounded HTTP client for outbound calls (AI provider, future SMS/email gateways, webhook delivery).
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// NoRetries, passed as Options.MaxRetries, explicitly requests zero retries
// (fail on the first attempt).
const NoRetries = -1

// Client is a retrying, timeout-bounded HTTP client.
type Client struct {
	name         string
	base         *url.URL
	http         *http.Client
	headers      http.Header
	userAgent    string
	maxRetries   int
	backoff      time.Duration
	maxBackoff   time.Duration
	maxRespBytes int64
	onRetry      func(attempt int, err error, statusCode int)
}

// Options configures a Client.
type Options struct {
	Name         string        // used in error messages; default "httpclient"
	BaseURL      string        // "" is fine if every call supplies an absolute URL
	Timeout      time.Duration // per attempt; default 30s
	MaxRetries   int           // 0 = default (3); NoRetries (-1) = explicitly zero
	BaseBackoff  time.Duration // exponential base; default 200ms
	MaxBackoff   time.Duration // cap on backoff growth; default 30s
	DefaultHead  http.Header   // headers merged into every request
	UserAgent    string        // default "go-httpclient/<name>"
	MaxIdleConns int           // default 32

	// MaxResponseBytes caps how much of a response body is buffered into memory.
	MaxResponseBytes int64

	// BlockPrivateNetworks, when true, refuses to connect (including via redirect) to loopback/private/link-local addresses.
	BlockPrivateNetworks bool

	// MaxRedirects caps how many redirects a single call follows. Default 5. Set to a negative value to follow zero redirects (surface the 3xx response directly instead).
	MaxRedirects int

	// OnRetry, if set, is called just before each retry sleep — for logging/metrics at the call site without coupling this package to any specific logger or metrics library.
	OnRetry func(attempt int, err error, statusCode int)
}

// New builds a Client. Sensible defaults tuned for LLM APIs (30s timeout, 3 retries, 10 MiB response cap).
func New(o Options) (*Client, error) {
	switch {
	case o.MaxRetries == 0:
		o.MaxRetries = 3
	case o.MaxRetries == NoRetries:
		o.MaxRetries = 0
	case o.MaxRetries < 0:
		return nil, fmt.Errorf("httpclient: MaxRetries must be >= 0, or the NoRetries sentinel (%d); got %d", NoRetries, o.MaxRetries)
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.BaseBackoff == 0 {
		o.BaseBackoff = 200 * time.Millisecond
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.MaxIdleConns == 0 {
		o.MaxIdleConns = 32
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 10 << 20 // 10 MiB
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}

	name := coalesce(o.Name, "httpclient")

	transport := &http.Transport{
		MaxIdleConns:        o.MaxIdleConns,
		MaxIdleConnsPerHost: o.MaxIdleConns,
		IdleConnTimeout:     90 * time.Second,
	}
	if o.BlockPrivateNetworks {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				host = addr
			}
			ips, lookupErr := net.LookupIP(host)
			if lookupErr == nil {
				for _, ip := range ips {
					if isPrivateOrLoopback(ip) {
						return nil, fmt.Errorf("httpclient: refusing to connect to private/loopback address %s", ip)
					}
				}
			}
			return dialer.DialContext(ctx, network, addr)
		}
	}

	httpClient := &http.Client{Timeout: o.Timeout, Transport: transport}
	if o.MaxRedirects < 0 {
		httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		maxRedirects := o.MaxRedirects
		blockPrivate := o.BlockPrivateNetworks
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("httpclient: stopped after %d redirects", maxRedirects)
			}
			if blockPrivate {
				if ips, err := net.LookupIP(req.URL.Hostname()); err == nil {
					for _, ip := range ips {
						if isPrivateOrLoopback(ip) {
							return errors.New("httpclient: redirect to private/loopback address blocked")
						}
					}
				}
			}
			return nil
		}
	}

	c := &Client{
		name:         name,
		headers:      o.DefaultHead.Clone(),
		userAgent:    coalesce(o.UserAgent, "go-httpclient/"+name),
		maxRetries:   o.MaxRetries,
		backoff:      o.BaseBackoff,
		maxBackoff:   o.MaxBackoff,
		maxRespBytes: o.MaxResponseBytes,
		onRetry:      o.OnRetry,
		http:         httpClient,
	}
	if o.BaseURL != "" {
		u, err := url.Parse(o.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("httpclient: invalid base url: %w", err)
		}
		c.base = u
	}
	return c, nil
}

// Close releases idle connections. Call on graceful shutdown.
func (c *Client) Close() { c.http.CloseIdleConnections() }

type StatusError struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("httpclient: exhausted retries, last status %d: %s", e.StatusCode, truncateBytes(e.Body, 512))
}

// Response is the caller-friendly outcome of Do.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// DecodeJSON parses r.Body into `out`.
func (r *Response) DecodeJSON(out any) error {
	if len(r.Body) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(r.Body, out)
}

// The response body is fully read (capped at Options.MaxResponseBytes) and
// returned; the caller does not need to close anything.
func (c *Client) Do(ctx context.Context, req *http.Request) (*Response, error) {
	if req.URL == nil {
		return nil, errors.New("httpclient: request URL is nil")
	}
	c.applyDefaults(req)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.jitteredBackoff(attempt)):
			}
		}

		reqCopy := req.Clone(ctx)
		if req.Body != nil {
			body, err := readAndReset(req)
			if err != nil {
				return nil, err
			}
			reqCopy.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := c.http.Do(reqCopy)
		if err != nil {
			lastErr = err
			if !isRetriable(err) {
				return nil, err
			}
			if c.onRetry != nil {
				c.onRetry(attempt+1, err, 0)
			}
			continue
		}

		limited := io.LimitReader(resp.Body, c.maxRespBytes+1)
		body, readErr := io.ReadAll(limited)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if int64(len(body)) > c.maxRespBytes {
			return nil, fmt.Errorf("%s: response body exceeds %d byte limit", c.name, c.maxRespBytes)
		}

		if shouldRetryStatus(resp.StatusCode) {
			statusErr := &StatusError{StatusCode: resp.StatusCode, Body: body, Header: resp.Header}
			if attempt < c.maxRetries {
				if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(ra):
					}
				}
				if c.onRetry != nil {
					c.onRetry(attempt+1, nil, resp.StatusCode)
				}
				lastErr = statusErr
				continue
			}
			// Final attempt and still retryable — surface as an error
			// rather than a "successful" response, so callers get a
			// consistent contract regardless of whether retries were
			// exhausted by a transport error or a retryable status.
			return nil, statusErr
		}

		return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}, nil
	}
	return nil, fmt.Errorf("%s: exhausted %d retries: %w", c.name, c.maxRetries, lastErr)
}

// ReqOption mutates a request before it is sent — for per-call headers that
// aren't known at Client construction time (idempotency keys, per-request
// signatures).
type ReqOption func(*http.Request)

// WithHeader sets a single header on the outgoing request.
func WithHeader(key, value string) ReqOption {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

// GetJSON performs GET and JSON-decodes a 2xx response into `out`.
func (c *Client) GetJSON(ctx context.Context, path string, out any, opts ...ReqOption) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	for _, o := range opts {
		o(req)
	}
	return c.doJSON(ctx, req, out)
}

// PostJSON marshals `in` to JSON, POSTs it, and decodes a 2xx response into `out`.
func (c *Client) PostJSON(ctx context.Context, path string, in, out any, opts ...ReqOption) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%s: marshal request: %w", c.name, err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, o := range opts {
		o(req)
	}
	return c.doJSON(ctx, req, out)
}

// ---- internals

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	isAbsolute := strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
	u := path
	switch {
	case c.base != nil && !isAbsolute:
		ref, err := url.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("%s: parse path %q: %w", c.name, path, err)
		}
		u = c.base.ResolveReference(ref).String()
	case c.base == nil && !isAbsolute:
		return nil, fmt.Errorf("%s: path %q is not an absolute URL and no BaseURL is configured", c.name, path)
	}
	return http.NewRequestWithContext(ctx, method, u, body)
}

func (c *Client) applyDefaults(req *http.Request) {
	if req.Header == nil {
		req.Header = http.Header{}
	}
	for k, vals := range c.headers {
		if req.Header.Get(k) == "" {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
}

func (c *Client) doJSON(ctx context.Context, req *http.Request, out any) error {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: unexpected status %d: %s", c.name, resp.StatusCode, truncateBytes(resp.Body, 512))
	}
	if out == nil {
		return nil
	}
	return resp.DecodeJSON(out)
}

// maxBackoffShift bounds the exponent before MaxBackoff even needs to clamp
// — without this, a large MaxRetries could shift a time.Duration (int64
// nanoseconds) into overflow/negative territory, which would then make
// rand.Int63n panic on a non-positive argument.
const maxBackoffShift = 20

func (c *Client) jitteredBackoff(attempt int) time.Duration {
	shift := attempt - 1
	if shift > maxBackoffShift {
		shift = maxBackoffShift
	}
	base := c.backoff * time.Duration(int64(1)<<uint(shift))
	if base <= 0 || base > c.maxBackoff {
		base = c.maxBackoff
	}
	if base <= 0 {
		return 0
	}
	half := int64(base) / 2
	if half <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(half))
}

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	// Fallback for errors that don't cleanly unwrap to a syscall errno on
	// every platform/Go version.
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF")
}

func shouldRetryStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code != http.StatusNotImplemented && code != http.StatusHTTPVersionNotSupported
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := time.ParseDuration(h + "s"); err == nil {
		return secs
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func readAndReset(r *http.Request) ([]byte, error) {
	if r.GetBody != nil {
		body, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(body)
	}
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func isPrivateOrLoopback(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
