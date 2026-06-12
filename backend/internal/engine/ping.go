package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ping.go — the ICMP ping Checker. A target is "up" when it answers an ICMPv4
// echo request within the deadline. Needs CAP_NET_RAW (or root / Windows admin)
// for the raw-socket fallback; tries an unprivileged ICMP socket first.
// Implements the Checker interface; the target is a hostname or IPv4 address.

const icmpProto = 1 // IANA ICMPv4 protocol number

// pingOptions is reserved for future fields (packet count, size, etc.).
type pingOptions struct{}

// pingChecker implements Checker for ping-type targets using ICMPv4 echo.
//
// Privilege requirements:
//   - Linux: unprivileged ICMP socket first (kernel ≥ 3.11 + ping_group_range);
//     falls back to raw socket which needs CAP_NET_RAW or root.
//   - Windows: Administrator rights when run as a plain process;
//     automatic when installed as a Windows Service.
type pingChecker struct{}

// Run sends a single ICMPv4 echo request to addr and waits for a matching
// reply. It resolves addr to an IPv4 address, opens an ICMP socket
// (unprivileged UDP first, raw-socket fallback), writes an echo packet tagged
// with a random id, and reads replies until one is an echo reply whose id
// matches — returning (true, nil). Any resolution, socket, send, receive or
// deadline error returns (false, err). Options are unused for ping. ctx carries
// the per-probe deadline.
func (c *pingChecker) Run(ctx context.Context, addr string, _ json.RawMessage) (bool, error) {
	ip, err := lookupIPv4(ctx, addr)
	if err != nil {
		return false, err
	}

	conn, privileged, err := dialICMP()
	if err != nil {
		return false, err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return false, fmt.Errorf("ping: set deadline: %w", err)
		}
	}

	id := uint16(rand.Uint32()) //nolint:gosec
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: int(id), Seq: 1, Data: []byte(BinaryName + "-ping")},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return false, fmt.Errorf("ping: marshal: %w", err)
	}

	var dst net.Addr
	if privileged {
		dst = &net.IPAddr{IP: ip}
	} else {
		dst = &net.UDPAddr{IP: ip}
	}
	if _, err := conn.WriteTo(wb, dst); err != nil {
		return false, fmt.Errorf("ping: send: %w", err)
	}

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return false, fmt.Errorf("ping: recv: %w", err)
		}
		payload := buf[:n]
		if privileged {
			if n < 20 {
				continue
			}
			ihl := int(buf[0]&0x0f) * 4
			if n < ihl {
				continue
			}
			payload = buf[ihl:n]
		}
		rm, err := icmp.ParseMessage(icmpProto, payload)
		if err != nil {
			continue
		}
		if rm.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if echo, ok := rm.Body.(*icmp.Echo); ok && uint16(echo.ID) == id {
			return true, nil
		}
	}
}

// ValidateOptions checks the ping options at config-load time. Ping takes no
// options, so it accepts empty/null and rejects any unknown field.
func (c *pingChecker) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts pingOptions
	if err := dec.Decode(&opts); err != nil {
		return fmt.Errorf("invalid ping options: %w", err)
	}
	return nil
}

// ParseAddr treats the whole target as the host (HOST) and reports "0" as the
// PORT for the alert env, since ICMP has no port. Errors on an empty addr.
func (c *pingChecker) ParseAddr(addr string) (string, string, error) {
	if addr == "" {
		return "", "", fmt.Errorf("ping: addr cannot be empty")
	}
	return addr, "0", nil
}

// lookupIPv4 resolves host to an IPv4 net.IP using ctx for cancellation.
func lookupIPv4(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, fmt.Errorf("ping: %q is IPv6; only IPv4 is supported", host)
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("ping: resolve %q: %w", host, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4, nil
			}
		}
	}
	return nil, fmt.Errorf("ping: %q has no IPv4 address (got: %v)", host, addrs)
}

// dialICMP opens an ICMP socket. Tries unprivileged (udp4) first, falls back to raw (ip4:icmp).
func dialICMP() (*icmp.PacketConn, bool, error) {
	if conn, err := icmp.ListenPacket("udp4", ""); err == nil {
		return conn, false, nil
	}
	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		return nil, false, fmt.Errorf(
			"ping: cannot open ICMP socket (needs CAP_NET_RAW, root, or Windows admin): %w", err)
	}
	return conn, true, nil
}
