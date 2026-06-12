package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// http.go — the HTTP/HTTPS Checker. A target is "up" when a request to its URL
// returns a status the operator accepts (default: any < 400) and, optionally,
// the body matches/avoids given substrings. Implements the Checker interface;
// the target is a full URL. Rich options (status operators, body assertions,
// redirect control) are parsed from the target's raw JSON options.

var allowedMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true, "PATCH": true,
}

// statusRule defines how to evaluate an HTTP response status code.
// Exactly one operator field must be set.
type statusRule struct {
	In      []int `json:"in,omitempty"`
	Lt      *int  `json:"lt,omitempty"`
	Lte     *int  `json:"lte,omitempty"`
	Gt      *int  `json:"gt,omitempty"`
	Gte     *int  `json:"gte,omitempty"`
	Between []int `json:"between,omitempty"`
}

// validate ensures exactly one status operator is set (in / lt / lte / gt / gte
// / between) and that `between` has exactly two bounds. Returns an error used at
// config-load time so a malformed expected_status is rejected early.
func (r *statusRule) validate() error {
	n := 0
	if len(r.In) > 0 {
		n++
	}
	if r.Lt != nil {
		n++
	}
	if r.Lte != nil {
		n++
	}
	if r.Gt != nil {
		n++
	}
	if r.Gte != nil {
		n++
	}
	if len(r.Between) > 0 {
		n++
	}
	if n == 0 {
		return fmt.Errorf("status_rule: no operator set (use: in, lt, lte, gt, gte, between)")
	}
	if n > 1 {
		return fmt.Errorf("status_rule: exactly one operator required, found %d", n)
	}
	if len(r.Between) > 0 && len(r.Between) != 2 {
		return fmt.Errorf("status_rule.between: requires exactly [min, max], got %d values", len(r.Between))
	}
	return nil
}

// matches reports whether an HTTP status code satisfies the rule. A nil rule
// (no expected_status configured) accepts any code below 400. Otherwise it
// applies the single configured operator (in / lt / lte / gt / gte / between).
func (r *statusRule) matches(code int) bool {
	if r == nil {
		return code < 400
	}
	if len(r.In) > 0 {
		for _, v := range r.In {
			if code == v {
				return true
			}
		}
		return false
	}
	if r.Lt != nil {
		return code < *r.Lt
	}
	if r.Lte != nil {
		return code <= *r.Lte
	}
	if r.Gt != nil {
		return code > *r.Gt
	}
	if r.Gte != nil {
		return code >= *r.Gte
	}
	if len(r.Between) == 2 {
		return code >= r.Between[0] && code <= r.Between[1]
	}
	return false
}

// httpOptions holds options for http/https targets.
type httpOptions struct {
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	ExpectedStatus  *statusRule       `json:"expected_status,omitempty"`
	BodyContains    string            `json:"body_contains,omitempty"`
	BodyNotContains string            `json:"body_not_contains,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	MaxRedirects    *int              `json:"max_redirects,omitempty"`
}

type ctxKeyFollow struct{}
type ctxKeyMaxHops struct{}

var followKey = ctxKeyFollow{}
var maxHopsKey = ctxKeyMaxHops{}

// httpChecker implements Checker for http/https targets.
type httpChecker struct {
	client *http.Client
}

// Run performs one HTTP request to addr (a URL) and decides up/down. It parses
// the JSON options (method, headers, body, expected_status, body_contains /
// body_not_contains, follow_redirects, max_redirects), issues the request under
// ctx's deadline, then checks the status against the rule and, when configured,
// reads up to 1 MiB of body to assert the contains/not-contains conditions.
// Returns (true, nil) when all checks pass, otherwise (false, err) describing
// the first failing check (transport error, bad status, or body mismatch).
func (c *httpChecker) Run(ctx context.Context, addr string, raw json.RawMessage) (bool, error) {
	var opts *httpOptions
	if len(raw) > 0 && string(raw) != "null" {
		var o httpOptions
		if err := json.Unmarshal(raw, &o); err != nil {
			return false, fmt.Errorf("http options: %w", err)
		}
		opts = &o
	}

	method := "GET"
	if opts != nil && opts.Method != "" {
		method = strings.ToUpper(opts.Method)
	}

	var body io.Reader
	if opts != nil && opts.Body != "" {
		body = strings.NewReader(opts.Body)
	}

	if opts != nil && opts.FollowRedirects != nil && !*opts.FollowRedirects {
		ctx = context.WithValue(ctx, followKey, false)
	}
	if opts != nil && opts.MaxRedirects != nil {
		ctx = context.WithValue(ctx, maxHopsKey, *opts.MaxRedirects)
	}

	req, err := http.NewRequestWithContext(ctx, method, addr, body)
	if err != nil {
		return false, err
	}
	if opts != nil {
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var rule *statusRule
	if opts != nil {
		rule = opts.ExpectedStatus
	}
	if !rule.matches(resp.StatusCode) {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	needsBodyCheck := opts != nil && (opts.BodyContains != "" || opts.BodyNotContains != "")
	if needsBodyCheck && method != "HEAD" {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return false, fmt.Errorf("http: body read: %w", err)
		}
		s := string(data)
		if opts.BodyContains != "" && !strings.Contains(s, opts.BodyContains) {
			return false, fmt.Errorf("http: body missing expected string %q", opts.BodyContains)
		}
		if opts.BodyNotContains != "" && strings.Contains(s, opts.BodyNotContains) {
			return false, fmt.Errorf("http: body contains forbidden string %q", opts.BodyNotContains)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return true, nil
}

// ValidateOptions checks the http options at config-load time: rejects unknown
// fields, validates the method is allowed, validates the expected_status rule,
// forbids body assertions with HEAD (no body), and requires max_redirects >= 0.
// Returns an error that fails config validation early.
func (c *httpChecker) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts httpOptions
	if err := dec.Decode(&opts); err != nil {
		return fmt.Errorf("invalid http options: %w", err)
	}
	if opts.Method != "" && !allowedMethods[strings.ToUpper(opts.Method)] {
		return fmt.Errorf("invalid method %q; allowed: GET POST PUT DELETE HEAD OPTIONS PATCH", opts.Method)
	}
	if opts.ExpectedStatus != nil {
		if err := opts.ExpectedStatus.validate(); err != nil {
			return err
		}
	}
	if strings.ToUpper(opts.Method) == "HEAD" && (opts.BodyContains != "" || opts.BodyNotContains != "") {
		return fmt.Errorf("body_contains/body_not_contains cannot be used with HEAD (no response body)")
	}
	if opts.MaxRedirects != nil && *opts.MaxRedirects < 0 {
		return fmt.Errorf("max_redirects must be >= 0")
	}
	return nil
}

// ParseAddr extracts the host and port from a URL for the alert env, defaulting
// the port to 443 for https and 80 otherwise when the URL omits it.
func (c *httpChecker) ParseAddr(addr string) (string, string, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return "", "", fmt.Errorf("http: cannot parse URL %q: %w", addr, err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port, nil
}
