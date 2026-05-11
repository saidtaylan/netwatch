package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// dnsOptions holds options for dns-type targets.
type dnsOptions struct {
	// ExpectedIPs lists acceptable resolved addresses.
	// Any match → UP. Omit to treat any successful resolution as UP.
	ExpectedIPs []string `json:"expected_ips,omitempty"`

	// Nameserver overrides the system resolver. Format: "1.2.3.4" or "1.2.3.4:53".
	Nameserver string `json:"nameserver,omitempty"`
}

// dnsChecker implements Checker for dns-type targets.
type dnsChecker struct{}

func (c *dnsChecker) Run(ctx context.Context, addr string, raw json.RawMessage) (bool, error) {
	var opts dnsOptions
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return false, fmt.Errorf("dns options: %w", err)
		}
	}

	resolver := newResolver(opts.Nameserver)
	addrs, err := resolver.LookupHost(ctx, addr)
	if err != nil {
		return false, fmt.Errorf("dns: lookup %q: %w", addr, err)
	}
	if len(addrs) == 0 {
		return false, fmt.Errorf("dns: %q returned no addresses", addr)
	}
	if len(opts.ExpectedIPs) == 0 {
		return true, nil
	}

	want := make(map[string]struct{}, len(opts.ExpectedIPs))
	for _, ip := range opts.ExpectedIPs {
		want[strings.TrimSpace(ip)] = struct{}{}
	}
	for _, a := range addrs {
		if _, ok := want[a]; ok {
			return true, nil
		}
	}
	return false, fmt.Errorf("dns: %q resolved to %v — none matched expected %v", addr, addrs, opts.ExpectedIPs)
}

func (c *dnsChecker) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts dnsOptions
	if err := dec.Decode(&opts); err != nil {
		return fmt.Errorf("invalid dns options: %w", err)
	}
	for _, ip := range opts.ExpectedIPs {
		if net.ParseIP(strings.TrimSpace(ip)) == nil {
			return fmt.Errorf("dns options: expected_ips: %q is not a valid IP", ip)
		}
	}
	if opts.Nameserver != "" {
		host := opts.Nameserver
		if strings.Contains(host, ":") {
			h, _, err := net.SplitHostPort(host)
			if err != nil {
				return fmt.Errorf("dns options: nameserver %q invalid host:port: %w", host, err)
			}
			host = h
		}
		if net.ParseIP(host) == nil {
			return fmt.Errorf("dns options: nameserver %q must be an IP address", host)
		}
	}
	return nil
}

func (c *dnsChecker) ParseAddr(addr string) (string, string, error) {
	if addr == "" {
		return "", "", fmt.Errorf("dns: addr cannot be empty")
	}
	return addr, "53", nil
}

// newResolver builds a custom resolver when nameserver is set, otherwise returns the system default.
func newResolver(nameserver string) *net.Resolver {
	if nameserver == "" {
		return net.DefaultResolver
	}
	ns := nameserver
	if !strings.Contains(ns, ":") {
		ns += ":53"
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", ns)
		},
	}
}
