//go:build !windows

package cluster_test

import (
	"strings"
	"testing"

	"github.com/saidtaylan/netwatch/internal/cluster"
)

// TestConfigHashOf_SameBytesProduceSameHash verifies determinism.
func TestConfigHashOf_SameBytesProduceSameHash(t *testing.T) {
	data := []byte("timeout: 5\nmax_retries: 3\n")
	h1 := cluster.ConfigHashOf(data)
	h2 := cluster.ConfigHashOf(data)
	if h1 != h2 {
		t.Errorf("same input produced different hashes: %q vs %q", h1, h2)
	}
}

// TestConfigHashOf_DifferentBytesProduceDifferentHash verifies sensitivity to input changes.
func TestConfigHashOf_DifferentBytesProduceDifferentHash(t *testing.T) {
	d1 := []byte("timeout: 5\n")
	d2 := []byte("timeout: 10\n")
	h1 := cluster.ConfigHashOf(d1)
	h2 := cluster.ConfigHashOf(d2)
	if h1 == h2 {
		t.Errorf("different inputs produced same hash: %q", h1)
	}
}

// TestConfigHashOf_EmptyBytesNonEmpty verifies that empty input still produces a hash.
func TestConfigHashOf_EmptyBytesNonEmpty(t *testing.T) {
	h := cluster.ConfigHashOf([]byte{})
	if h == "" {
		t.Fatal("expected non-empty hash for empty bytes")
	}
}

// TestConfigHashOf_HashLength verifies the hash is exactly 16 hex chars.
func TestConfigHashOf_HashLength(t *testing.T) {
	data := []byte("some config content here")
	h := cluster.ConfigHashOf(data)
	if len(h) != 16 {
		t.Errorf("hash length: want 16, got %d (hash=%q)", len(h), h)
	}
}

// TestConfigHashOf_HashIsHex verifies the hash contains only hexadecimal characters.
func TestConfigHashOf_HashIsHex(t *testing.T) {
	data := []byte("port: 10240\nnode_alias: test\n")
	h := cluster.ConfigHashOf(data)
	for _, c := range h {
		if !isHexChar(c) {
			t.Errorf("non-hex character %q in hash %q", c, h)
		}
	}
}

// TestConfigHashOf_LargeConfigDeterministic verifies large inputs produce consistent hashes.
func TestConfigHashOf_LargeConfigDeterministic(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("  - name: target\n    type: tcp\n    target: 127.0.0.1:9000\n")
	}
	data := []byte(sb.String())
	h1 := cluster.ConfigHashOf(data)
	h2 := cluster.ConfigHashOf(data)
	if h1 != h2 {
		t.Errorf("large config: non-deterministic hash: %q vs %q", h1, h2)
	}
}

// TestConfigHashOf_SingleByteChange verifies a single byte change produces a different hash.
func TestConfigHashOf_SingleByteChange(t *testing.T) {
	d1 := []byte("port: 10240")
	d2 := []byte("port: 10241")
	h1 := cluster.ConfigHashOf(d1)
	h2 := cluster.ConfigHashOf(d2)
	if h1 == h2 {
		t.Errorf("single-byte change: expected different hashes, both %q", h1)
	}
}

// TestConfigHashOf_BinaryData verifies hashing works for arbitrary byte sequences.
func TestConfigHashOf_BinaryData(t *testing.T) {
	var data [256]byte
	for i := range data {
		data[i] = byte(i)
	}
	h := cluster.ConfigHashOf(data[:])
	if len(h) != 16 {
		t.Errorf("hash length: want 16, got %d", len(h))
	}
}

// TestConfigHashOf_NilInput verifies nil input does not panic.
func TestConfigHashOf_NilInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ConfigHashOf panicked with nil input: %v", r)
		}
	}()
	h := cluster.ConfigHashOf(nil)
	if h == "" {
		t.Fatal("expected non-empty hash for nil input")
	}
}

// isHexChar returns true when c is a valid hexadecimal digit.
func isHexChar(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
