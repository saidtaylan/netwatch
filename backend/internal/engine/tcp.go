package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
)

// tcp.go — the TCP Checker. A target is "up" if a TCP connection to its
// host:port can be established within the probe deadline. This is the simplest
// and most common probe type; it implements the Checker interface (Run /
// ValidateOptions / ParseAddr) from protocol.go.

// tcpOptions is reserved for future fields (e.g. banner matching).
type tcpOptions struct{}

// tcpChecker implements Checker for tcp targets.
type tcpChecker struct{}

// Run probes a TCP target by dialing addr (a "host:port" string) using the
// context's deadline. It returns (true, nil) when the connection succeeds (the
// socket is closed immediately — establishing it is the health signal), or
// (false, err) when the dial fails or times out. The options argument is unused
// for TCP. Called by the engine's probe loop on every interval.
func (c *tcpChecker) Run(ctx context.Context, addr string, _ json.RawMessage) (bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

// ValidateOptions checks the target's raw JSON options at config-load time.
// TCP takes no options, so it accepts empty/null and otherwise rejects any
// unknown field (DisallowUnknownFields), returning an error that fails config
// validation early rather than at probe time.
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

// ParseAddr splits a "host:port" target into its host and port components,
// used to populate the HOST and PORT alert env variables. Returns an error when
// addr is not a valid host:port pair.
func (c *tcpChecker) ParseAddr(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("tcp: cannot parse host:port from %q: %w", addr, err)
	}
	return host, port, nil
}
