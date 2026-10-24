// Package client is a small, typed client for the Porch management API (/api/v1).
//
// It implements the conventions Terraform relies on: bearer automation tokens, `X-Porch-Managed-By: terraform` on
// every mutation, `If-Match` with the resource ETag on PUT/PATCH/DELETE, an `Idempotency-Key` per logical create
// (reused across retries), retries with exponential backoff and RFC 9457 problem decoding.
//
// The client never logs or embeds request or response bodies in errors, so secrets cannot leak through it.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// TokenPrefix is the prefix of Porch automation tokens (ADR-0041).
const TokenPrefix = "porch_at_"

// ManagedBy is sent in X-Porch-Managed-By on every mutation.
const ManagedBy = "terraform"

const apiPrefix = "/api/v1"

// Config configures a Client.
type Config struct {
	// Endpoint is the public URL of the Porch installation, e.g. https://auth.example.com.
	Endpoint string
	// Token is an automation token (porch_at_…).
	Token string
	// CAFile optionally points to a PEM bundle trusted in addition to the system roots.
	CAFile string
	// UserAgent is sent on every request.
	UserAgent string
	// HTTPClient overrides the HTTP client (tests). CAFile is ignored when set.
	HTTPClient *http.Client
	// MaxRetries is the number of retries after the first attempt (default 4; negative disables retries).
	MaxRetries int
	// MinBackoff and MaxBackoff bound the exponential backoff (defaults 250ms and 8s).
	MinBackoff, MaxBackoff time.Duration
	// Timeout is the per-attempt timeout (default 60s).
	Timeout time.Duration
	// Observer, when set, is called after every attempt with method, path and status (never bodies or headers).
	Observer func(ctx context.Context, method, path string, status int, attempt int, err error)
}

// Client talks to one Porch installation.
type Client struct {
	base       *url.URL
	token      string
	userAgent  string
	hc         *http.Client
	maxRetries int
	minBackoff time.Duration
	maxBackoff time.Duration
	observer   func(ctx context.Context, method, path string, status int, attempt int, err error)

	// NewIdempotencyKey generates keys for creates (overridable in tests).
	NewIdempotencyKey func() string
	// Sleep waits between retries (overridable in tests).
	Sleep func(ctx context.Context, d time.Duration) error
}

// ValidateToken checks the automation token format without revealing it.
func ValidateToken(token string) error {
	if !strings.HasPrefix(token, TokenPrefix) {
		return fmt.Errorf("token must be a Porch automation token starting with %q", TokenPrefix)
	}
	if len(token) <= len(TokenPrefix) || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token is malformed")
	}
	return nil
}

// ParseEndpoint normalises an endpoint URL (scheme://host[:port][/base]) and returns the API base URL.
func ParseEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("endpoint must be an absolute http(s) URL such as https://auth.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("endpoint must not contain credentials, a query or a fragment")
	}
	p := strings.TrimSuffix(u.Path, "/")
	p = strings.TrimSuffix(p, apiPrefix)
	u.Path = p + apiPrefix
	u.RawPath = ""
	return u, nil
}

// New builds a client.
func New(cfg Config) (*Client, error) {
	base, err := ParseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if err := ValidateToken(cfg.Token); err != nil {
		return nil, err
	}
	hc := cfg.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("ca_file: %w", err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("ca_file: no PEM certificates found")
			}
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 60 * time.Second
		}
		hc = &http.Client{Transport: tr, Timeout: timeout,
			// The management API never redirects; following one could forward the bearer token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	c := &Client{base: base, token: cfg.Token, userAgent: cfg.UserAgent, hc: hc, maxRetries: cfg.MaxRetries,
		minBackoff: cfg.MinBackoff, maxBackoff: cfg.MaxBackoff, observer: cfg.Observer, NewIdempotencyKey: randomKey, Sleep: sleepCtx}
	if c.userAgent == "" {
		c.userAgent = "terraform-provider-porch"
	}
	if cfg.MaxRetries == 0 {
		c.maxRetries = 4
	} else if cfg.MaxRetries < 0 {
		c.maxRetries = 0
	}
	if c.minBackoff == 0 {
		c.minBackoff = 250 * time.Millisecond
	}
	if c.maxBackoff == 0 {
		c.maxBackoff = 8 * time.Second
	}
	return c, nil
}

// Endpoint returns the public origin the client talks to (without /api/v1).
func (c *Client) Endpoint() string {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, apiPrefix)
	return strings.TrimSuffix(u.String(), "/")
}

func randomKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "tf-" + base64.RawURLEncoding.EncodeToString(b)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// request describes one logical API call.
type request struct {
	method  string
	path    string // relative to /api/v1 (or an absolute path that already starts with /api/v1)
	query   url.Values
	body    any
	ifMatch string
	// create marks a POST that creates something: it carries an Idempotency-Key reused across retries.
	create bool
}

// response is a raw API response.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (r *response) etag() string { return r.header.Get("ETag") }

func (c *Client) url(path string, q url.Values) string {
	u := *c.base
	if strings.HasPrefix(path, c.base.Path+"/") {
		u.Path = path
	} else {
		u.Path = c.base.Path + path
	}
	u.RawPath = ""
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// retryPolicy reports whether a failed attempt may be repeated. Reads and deletes are safe; creates are safe because
// they carry an Idempotency-Key; PUT/PATCH are conditional (If-Match), so a repeat of an applied change would fail with
// 412 — they are only retried when the server certainly did not apply them (429, 503).
func retryable(req *request, status int, netErr bool, code string) bool {
	safe := req.method == http.MethodGet || req.method == http.MethodHead || req.method == http.MethodDelete ||
		(req.method == http.MethodPost && req.create)
	switch {
	case netErr:
		return safe
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		return true
	case status == http.StatusConflict && code == "idempotency_in_progress":
		return req.create
	case status >= 500 && status != http.StatusNotImplemented:
		return safe
	}
	return false
}

func (c *Client) backoff(attempt int, h http.Header) time.Duration {
	if h != nil {
		if s := h.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n >= 0 {
				return min(time.Duration(n)*time.Second, c.maxBackoff)
			}
		}
	}
	d := c.minBackoff << min(attempt, 20)
	if d <= 0 || d > c.maxBackoff {
		d = c.maxBackoff
	}
	// Full jitter in [d/2, d].
	half := int64(d / 2)
	if half > 0 {
		if n, err := rand.Int(rand.Reader, big.NewInt(half)); err == nil {
			return time.Duration(half + n.Int64())
		}
	}
	return d
}

// do performs a request with retries and returns the final response (status < 400) or an error (*APIError for
// problem responses).
func (c *Client) do(ctx context.Context, req *request) (*response, error) {
	var payload []byte
	if req.body != nil {
		var err error
		if payload, err = json.Marshal(req.body); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}
	key := ""
	if req.create {
		key = c.NewIdempotencyKey()
	}
	target := c.url(req.path, req.query)
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		hreq, err := http.NewRequestWithContext(ctx, req.method, target, rdr)
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Authorization", "Bearer "+c.token)
		hreq.Header.Set("User-Agent", c.userAgent)
		hreq.Header.Set("Accept", "application/json, application/problem+json")
		if payload != nil {
			hreq.Header.Set("Content-Type", "application/json")
		}
		if req.method != http.MethodGet && req.method != http.MethodHead {
			hreq.Header.Set("X-Porch-Managed-By", ManagedBy)
		}
		if req.ifMatch != "" {
			hreq.Header.Set("If-Match", req.ifMatch)
		}
		if key != "" {
			hreq.Header.Set("Idempotency-Key", key)
		}
		hresp, err := c.hc.Do(hreq)
		if err != nil {
			c.observe(ctx, req, 0, attempt, err)
			lastErr = &TransportError{Method: req.method, Path: req.path, Err: err}
			if ctx.Err() == nil && attempt < c.maxRetries && retryable(req, 0, true, "") {
				if serr := c.Sleep(ctx, c.backoff(attempt, nil)); serr != nil {
					return nil, lastErr
				}
				continue
			}
			return nil, lastErr
		}
		body, rerr := io.ReadAll(io.LimitReader(hresp.Body, 8<<20))
		_ = hresp.Body.Close()
		c.observe(ctx, req, hresp.StatusCode, attempt, rerr)
		if rerr != nil {
			lastErr = &TransportError{Method: req.method, Path: req.path, Err: rerr}
			if attempt < c.maxRetries && retryable(req, 0, true, "") {
				if serr := c.Sleep(ctx, c.backoff(attempt, nil)); serr != nil {
					return nil, lastErr
				}
				continue
			}
			return nil, lastErr
		}
		resp := &response{status: hresp.StatusCode, header: hresp.Header, body: body}
		if resp.status < 400 {
			return resp, nil
		}
		apiErr := decodeProblem(req.method, req.path, resp)
		lastErr = apiErr
		if attempt < c.maxRetries && retryable(req, resp.status, false, apiErr.Code) {
			if serr := c.Sleep(ctx, c.backoff(attempt, resp.header)); serr != nil {
				return nil, lastErr
			}
			continue
		}
		return resp, apiErr
	}
}

func (c *Client) observe(ctx context.Context, req *request, status, attempt int, err error) {
	if c.observer != nil {
		c.observer(ctx, req.method, req.path, status, attempt, err)
	}
}

// decode unmarshals a JSON response body.
func decode[T any](resp *response) (*T, error) {
	var out T
	if len(bytes.TrimSpace(resp.body)) == 0 {
		return &out, nil
	}
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// call performs req and decodes the body into T.
func call[T any](ctx context.Context, c *Client, req *request) (*T, *response, error) {
	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, resp, err
	}
	out, err := decode[T](resp)
	return out, resp, err
}

// etagFor returns the response ETag, falling back to the body version (idempotent replays carry no ETag header).
func etagFor(resp *response, version int64) string {
	if resp != nil {
		if e := resp.etag(); e != "" {
			return e
		}
	}
	if version > 0 {
		return strconv.Quote(strconv.FormatInt(version, 10))
	}
	return ""
}

// ETagOf formats a resource version as an ETag.
func ETagOf(version int64) string { return etagFor(nil, version) }

func esc(s string) string { return url.PathEscape(s) }

// createResult is the outcome of a create that may carry a secret.
type createResult struct {
	resp *response
	// withheld is true when an idempotent retry found that the original request succeeded but its secret-bearing
	// response was lost: the resource was read from Location instead and the secret is unavailable.
	withheld bool
}

// create performs a POST create. On `409 idempotent_secret_withheld` it reads the resource at the Location header.
func create[T any](ctx context.Context, c *Client, path string, body any) (*T, createResult, error) {
	resp, err := c.do(ctx, &request{method: http.MethodPost, path: path, body: body, create: true})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Code == "idempotent_secret_withheld" && resp != nil {
			loc := resp.header.Get("Location")
			if loc == "" {
				return nil, createResult{}, err
			}
			lu, perr := url.Parse(loc)
			if perr != nil || (lu.Host != "" && lu.Host != c.base.Host) {
				return nil, createResult{}, fmt.Errorf("unexpected Location %q in idempotent replay", loc)
			}
			out, gresp, gerr := call[T](ctx, c, &request{method: http.MethodGet, path: lu.Path})
			if gerr != nil {
				return nil, createResult{}, gerr
			}
			return out, createResult{resp: gresp, withheld: true}, nil
		}
		return nil, createResult{resp: resp}, err
	}
	out, err := decode[T](resp)
	return out, createResult{resp: resp}, err
}
