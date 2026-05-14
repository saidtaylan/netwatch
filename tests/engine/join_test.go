//go:build !windows

package engine_test

import (
	"encoding/base64"
	"testing"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// TestGenerateKeyringKey_ReturnsNonEmpty verifies that GenerateKeyringKey returns a non-empty string.
func TestGenerateKeyringKey_ReturnsNonEmpty(t *testing.T) {
	key, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key == "" {
		t.Fatal("expected non-empty key string")
	}
}

// TestGenerateKeyringKey_NoError verifies no error in normal conditions.
func TestGenerateKeyringKey_NoError(t *testing.T) {
	_, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// TestGenerateKeyringKey_ValidBase64 verifies the output is valid standard base64.
func TestGenerateKeyringKey_ValidBase64(t *testing.T) {
	key, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("GenerateKeyringKey: %v", err)
	}
	_, decodeErr := base64.StdEncoding.DecodeString(key)
	if decodeErr != nil {
		t.Errorf("key is not valid base64: %v (key=%q)", decodeErr, key)
	}
}

// TestGenerateKeyringKey_Decoded32Bytes verifies the decoded key is exactly 32 bytes (AES-256).
func TestGenerateKeyringKey_Decoded32Bytes(t *testing.T) {
	key, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("GenerateKeyringKey: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded length: want 32, got %d", len(decoded))
	}
}

// TestGenerateKeyringKey_Unique verifies two successive calls return different keys.
func TestGenerateKeyringKey_Unique(t *testing.T) {
	k1, err1 := engine.GenerateKeyringKey()
	k2, err2 := engine.GenerateKeyringKey()
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err1, err2)
	}
	if k1 == k2 {
		t.Errorf("two calls returned identical keys: %q", k1)
	}
}

// TestGenerateKeyringKey_MultipleUnique verifies multiple successive calls all return unique values.
func TestGenerateKeyringKey_MultipleUnique(t *testing.T) {
	const n = 10
	keys := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		k, err := engine.GenerateKeyringKey()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if keys[k] {
			t.Errorf("duplicate key detected on call %d: %q", i, k)
		}
		keys[k] = true
	}
}

// TestGenerateKeyringKey_CompatibleWithClusterKeyring verifies the key passes cluster keyring validation
// by using it in a cluster config with enabled=true.
func TestGenerateKeyringKey_CompatibleWithClusterKeyring(t *testing.T) {
	key, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("GenerateKeyringKey: %v", err)
	}

	// Write a config with the generated key and verify it validates.
	p := writeConfig(t, `
port: "19200"
timeout: 5
cluster:
  enabled: true
  node_name: "test-node"
  bind_port: 17950
  keyring:
    - `+key+`
`)
	_, cfgErr := engine.ValidateConfigFile(p)
	if cfgErr != nil {
		t.Errorf("generated key failed cluster config validation: %v", cfgErr)
	}
}

// TestGenerateKeyringKey_Base64Encoding verifies standard (not URL) base64 encoding is used.
func TestGenerateKeyringKey_Base64Encoding(t *testing.T) {
	// Standard base64 uses + and / while URL base64 uses - and _.
	// We confirm StdEncoding round-trips without error.
	key, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("GenerateKeyringKey: %v", err)
	}
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("StdEncoding decode failed: %v", err)
	}
	reencoded := base64.StdEncoding.EncodeToString(b)
	if reencoded != key {
		t.Errorf("re-encode mismatch: original %q, re-encoded %q", key, reencoded)
	}
}
