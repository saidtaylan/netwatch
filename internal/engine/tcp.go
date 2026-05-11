package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
)

// tcpOptions is reserved for future fields (e.g. banner matching).
type tcpOptions struct{}

// tcpChecker implements Checker for tcp targets.
type tcpChecker struct{}

func (c *tcpChecker) Run(ctx context.Context, addr string, _ json.RawMessage) (bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

func (c *tcpChecker) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts tcpOptions
	if err := dec.Decode(&opts); err != nil {
		return fmt.Errorf("invalid tcp options: %w", err)
	}
	return nil
}

func (c *tcpChecker) ParseAddr(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("tcp: cannot parse host:port from %q: %w", addr, err)
	}
	return host, port, nil
}
