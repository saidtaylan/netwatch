package integration

// storage_gossip_test.go — End-to-end test of GossipLWWStorage replicating
// changes across a 2-node memberlist cluster.
//
// Setup: spin up two real cluster.Manager instances connected via localhost
// UDP, wrap each with gossip.Storage backed by an in-memory MemoryStorage,
// then verify that a write on node-1 propagates to node-2's storage via
// memberlist broadcast.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/cluster"
	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// twoNodeCluster builds a 2-node cluster on free localhost ports with
// matching keyrings. Returns the two Managers + a cleanup func.
//
// Each Manager is wrapped with its own GossipLWWStorage backed by an
// in-memory MemoryStorage. Returns the storages too.
func twoNodeCluster(t *testing.T) (n1, n2 *cluster.Manager, s1, s2 *gossip.Storage, cleanup func()) {
	t.Helper()

	p1, p2 := pickFreePort(t), pickFreePort(t)
	key := []string{"YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY="} // 32B base64

	c1 := cluster.Config{
		Enabled:                true,
		NodeName:               "node-1",
		BindAddr:               "127.0.0.1",
		BindPort:               p1,
		AdvertiseAddr:          "127.0.0.1",
		Keyring:                key,
		ExpectedNodeCount:      2,
		MinQuorumRatio:         0.5,
		ProbeReplicationFactor: 2,
	}
	c2 := cluster.Config{
		Enabled:                true,
		NodeName:               "node-2",
		BindAddr:               "127.0.0.1",
		BindPort:               p2,
		AdvertiseAddr:          "127.0.0.1",
		Keyring:                key,
		ExpectedNodeCount:      2,
		MinQuorumRatio:         0.5,
		Peers:                  []string{fmt.Sprintf("127.0.0.1:%d", p1)},
		ProbeReplicationFactor: 2,
	}

	m1, err := cluster.New(c1)
	if err != nil {
		t.Fatalf("node-1: %v", err)
	}
	m2, err := cluster.New(c2)
	if err != nil {
		_ = m1.Leave(time.Second)
		t.Fatalf("node-2: %v", err)
	}

	// Wait for cluster to converge
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(m1.Snapshot().Members) == 2 && len(m2.Snapshot().Members) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(m1.Snapshot().Members) < 2 {
		_ = m1.Leave(time.Second)
		_ = m2.Leave(time.Second)
		t.Fatalf("cluster failed to converge: m1 sees %d members", len(m1.Snapshot().Members))
	}

	// Wait for IsolatedMode to clear — the quorum loop ticks every 5s,
	// so writes immediately after cluster formation get rejected with
	// ErrSplitBrain. This is the correct production behavior: don't accept
	// writes until quorum is confirmed.
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !m1.IsolatedMode() && !m2.IsolatedMode() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if m1.IsolatedMode() || m2.IsolatedMode() {
		_ = m1.Leave(time.Second)
		_ = m2.Leave(time.Second)
		t.Fatalf("quorum did not stabilise: m1.isolated=%v, m2.isolated=%v",
			m1.IsolatedMode(), m2.IsolatedMode())
	}

	// Wrap each with GossipLWWStorage
	mem1 := storage.NewMemoryStorage()
	mem2 := storage.NewMemoryStorage()

	s1 = gossip.NewStorage(mem1, m1, m1, "node-1")
	s2 = gossip.NewStorage(mem2, m2, m2, "node-2")

	// Wire receive paths
	m1.SetStorageChangeHandler(s1)
	m2.SetStorageChangeHandler(s2)

	cleanup = func() {
		_ = m1.Leave(time.Second)
		_ = m2.Leave(time.Second)
		_ = mem1.Close()
		_ = mem2.Close()
	}
	return m1, m2, s1, s2, cleanup
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// ── Tests ───────────────────────────────────────────────────────────────────

func TestStorageGossip_WriteReplicates(t *testing.T) {
	_, _, s1, s2, cleanup := twoNodeCluster(t)
	defer cleanup()

	ctx := context.Background()

	// Write on node-1
	v := s1.NextVersion()
	err := s1.Upsert(ctx, storage.TableSLOTargets, "db-primary",
		[]byte(`{"target_uptime":0.999}`), v)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify node-1 sees it immediately
	rec, err := s1.Get(ctx, storage.TableSLOTargets, "db-primary")
	if err != nil {
		t.Fatalf("local get: %v", err)
	}
	if string(rec.Payload) != `{"target_uptime":0.999}` {
		t.Errorf("local payload mismatch")
	}

	// Wait for node-2 to receive the broadcast
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := s2.Get(ctx, storage.TableSLOTargets, "db-primary")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	rec2, err := s2.Get(ctx, storage.TableSLOTargets, "db-primary")
	if err != nil {
		t.Fatalf("node-2 never received broadcast: %v", err)
	}
	if string(rec2.Payload) != `{"target_uptime":0.999}` {
		t.Errorf("node-2 payload mismatch: %q", string(rec2.Payload))
	}
	if rec2.Version.UpdatedBy != "node-1" {
		t.Errorf("origin attribution lost: %q", rec2.Version.UpdatedBy)
	}
}

func TestStorageGossip_DeleteReplicates(t *testing.T) {
	_, _, s1, s2, cleanup := twoNodeCluster(t)
	defer cleanup()

	ctx := context.Background()

	// Seed: write then delete on node-1
	_ = s1.Upsert(ctx, storage.TableSLOTargets, "ephemeral",
		[]byte(`v`), s1.NextVersion())

	// Wait for node-2 to see it
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s2.Get(ctx, storage.TableSLOTargets, "ephemeral"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Delete on node-1
	_ = s1.Delete(ctx, storage.TableSLOTargets, "ephemeral", s1.NextVersion())

	// Wait for tombstone to reach node-2
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := s2.Get(ctx, storage.TableSLOTargets, "ephemeral")
		if errors.Is(err, storage.ErrNotFound) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	_, err := s2.Get(ctx, storage.TableSLOTargets, "ephemeral")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("node-2 should see tombstone (ErrNotFound), got %v", err)
	}
}

func TestStorageGossip_BidirectionalWrites(t *testing.T) {
	_, _, s1, s2, cleanup := twoNodeCluster(t)
	defer cleanup()

	ctx := context.Background()

	// Both nodes write different records
	_ = s1.Upsert(ctx, storage.TableSLOTargets, "from-node-1",
		[]byte(`1`), s1.NextVersion())
	_ = s2.Upsert(ctx, storage.TableSLOTargets, "from-node-2",
		[]byte(`2`), s2.NextVersion())

	// Wait for both broadcasts to settle
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, e1 := s1.Get(ctx, storage.TableSLOTargets, "from-node-2")
		_, e2 := s2.Get(ctx, storage.TableSLOTargets, "from-node-1")
		if e1 == nil && e2 == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := s1.Get(ctx, storage.TableSLOTargets, "from-node-2"); err != nil {
		t.Errorf("node-1 should have node-2's write: %v", err)
	}
	if _, err := s2.Get(ctx, storage.TableSLOTargets, "from-node-1"); err != nil {
		t.Errorf("node-2 should have node-1's write: %v", err)
	}
}

func TestStorageGossip_LWWConflictResolution(t *testing.T) {
	_, _, s1, s2, cleanup := twoNodeCluster(t)
	defer cleanup()

	ctx := context.Background()

	// Both nodes write to the SAME key. Node-2's seq will be higher
	// because we call NextVersion() in sequence — this lets us verify
	// LWW deterministically.
	v1 := s1.NextVersion() // Seq=1 from node-1
	_ = s1.Upsert(ctx, storage.TableSLOTargets, "conflict",
		[]byte(`from-node-1`), v1)

	// Wait for node-2 to see node-1's write — this also bumps node-2's
	// observed seq counter so the next NextVersion() returns >v1.Seq.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s2.Get(ctx, storage.TableSLOTargets, "conflict"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	v2 := s2.NextVersion() // higher than v1 (observed bump)
	_ = s2.Upsert(ctx, storage.TableSLOTargets, "conflict",
		[]byte(`from-node-2-WINS`), v2)

	// Wait for node-1 to apply node-2's write
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := s1.Get(ctx, storage.TableSLOTargets, "conflict")
		if err == nil && string(rec.Payload) == `from-node-2-WINS` {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	rec, err := s1.Get(ctx, storage.TableSLOTargets, "conflict")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(rec.Payload) != `from-node-2-WINS` {
		t.Errorf("higher seq did not win: %q", string(rec.Payload))
	}
}

func TestStorageGossip_StatsTrack(t *testing.T) {
	_, _, s1, s2, cleanup := twoNodeCluster(t)
	defer cleanup()

	ctx := context.Background()

	// 3 writes on node-1
	for i := 0; i < 3; i++ {
		v := s1.NextVersion()
		_ = s1.Upsert(ctx, storage.TableSLOTargets,
			fmt.Sprintf("rec-%d", i), []byte(`v`), v)
	}

	// Wait for node-2 to receive all 3
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s2.Stats().TotalReceived >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stats := s2.Stats()
	if stats.TotalReceived < 3 {
		t.Errorf("expected >= 3 received, got %d", stats.TotalReceived)
	}
	if stats.HighestSeq < 3 {
		t.Errorf("expected HighestSeq >= 3, got %d", stats.HighestSeq)
	}
}
