package cluster

// Tests for the background re-join loop (auto-recovery after a node is evicted
// by a prolonged network partition). The loop runs for the manager's lifetime
// and, while the node is below target strength, re-attempts Join(peers).

import (
	"net"
	"testing"
	"time"
)

// rejoinFreePort grabs an ephemeral TCP port and releases it for memberlist.
func rejoinFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestRejoinInterval_DefaultAndOverride(t *testing.T) {
	m := &Manager{cfg: Config{}}
	if got := m.rejoinInterval(); got != 15*time.Second {
		t.Errorf("default rejoinInterval = %v, want 15s", got)
	}
	m.cfg.RejoinIntervalSec = 3
	if got := m.rejoinInterval(); got != 3*time.Second {
		t.Errorf("override rejoinInterval = %v, want 3s", got)
	}
}

func TestTargetStrength(t *testing.T) {
	// No expected count configured → minimum of 2 (this node + one peer).
	if got := (&Manager{cfg: Config{}}).targetStrength(); got != 2 {
		t.Errorf("targetStrength with no expected = %d, want 2", got)
	}
	// Expected count configured → that value.
	if got := (&Manager{cfg: Config{ExpectedNodeCount: 5}}).targetStrength(); got != 5 {
		t.Errorf("targetStrength with expected=5 = %d, want 5", got)
	}
	// expected=1 is degenerate (a single-node cluster has no peers); fall back to 2.
	if got := (&Manager{cfg: Config{ExpectedNodeCount: 1}}).targetStrength(); got != 2 {
		t.Errorf("targetStrength with expected=1 = %d, want 2", got)
	}
}

// TestRejoin_ConvergesWhenPeerAppearsLate proves the permanent loop actively
// re-joins while the node is under strength: node A starts alone (its only peer
// is not up yet), and once node B appears A converges to a 2-node cluster on its
// own — the same code path that recovers an evicted node after a partition heals.
func TestRejoin_ConvergesWhenPeerAppearsLate(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real memberlist sockets")
	}
	pA, pB := rejoinFreePort(t), rejoinFreePort(t)

	mkCfg := func(name string, bind int, peer int) Config {
		return Config{
			Enabled:           true,
			NodeName:          name,
			BindAddr:          "127.0.0.1",
			BindPort:          bind,
			Peers:             []string{net.JoinHostPort("127.0.0.1", itoa(peer))},
			ExpectedNodeCount: 2,
			RejoinIntervalSec: 1, // fast cadence for the test
		}
	}

	// Start A first — its peer (B) does not exist yet, so A is under strength
	// and its re-join loop is actively retrying.
	a, err := New(mkCfg("node-a", pA, pB))
	if err != nil {
		t.Fatalf("start node-a: %v", err)
	}
	defer a.Leave(time.Second)

	if a.list.NumMembers() != 1 {
		t.Fatalf("node-a should be alone before node-b starts, got %d members", a.list.NumMembers())
	}

	// Bring B up. A's loop (and B's startup join) should converge them.
	b, err := New(mkCfg("node-b", pB, pA))
	if err != nil {
		t.Fatalf("start node-b: %v", err)
	}
	defer b.Leave(time.Second)

	if !eventually(10*time.Second, func() bool {
		return a.list.NumMembers() == 2 && b.list.NumMembers() == 2
	}) {
		t.Fatalf("cluster did not converge: a=%d b=%d members",
			a.list.NumMembers(), b.list.NumMembers())
	}
}

// eventually polls cond every 200ms until it is true or the deadline passes.
func eventually(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return cond()
}
