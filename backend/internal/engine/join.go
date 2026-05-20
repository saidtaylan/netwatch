package engine

// join.go — helpers used by the `init --cluster` and `join` CLI subcommands.
//
// Keep this file dependency-light: only crypto/rand and encoding/base64.
// Everything network- or memberlist-related lives in the cluster package.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateKeyringKey returns a base64-encoded 32-byte (AES-256) random key
// suitable for use as a cluster.keyring entry. The output is safe to embed in
// config.yaml without further encoding.
func GenerateKeyringKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// LocalClusterAddr returns this node's advertised gossip address as "host:port",
// or empty when cluster is disabled / not yet started. Used by the startup
// banner to print a ready-to-copy `netwatch join --addr <addr>` line.
func (e *Engine) LocalClusterAddr() string {
	if e.clusterMgr == nil {
		return ""
	}
	return e.clusterMgr.LocalAddr()
}

// ClusterPrimaryKey returns the base64-encoded primary AES keyring key, or
// empty when no keyring is configured. Treat as a secret.
func (e *Engine) ClusterPrimaryKey() string {
	if e.clusterMgr == nil {
		return ""
	}
	return e.clusterMgr.PrimaryKey()
}

// ClusterMemberCount returns the current alive member count, or 0 when cluster
// is disabled / not yet started.
func (e *Engine) ClusterMemberCount() int {
	if e.clusterMgr == nil {
		return 0
	}
	return e.clusterMgr.AliveCount()
}
