package engine

import (
	"sync"
	"testing"

	"github.com/saidtaylan/netwatch/internal/cluster"
)

// ── shouldAlert ───────────────────────────────────────────────────────────────

func TestShouldAlert_Standalone(t *testing.T) {
	e := &Engine{}
	// No cluster manager → always alert
	if !e.shouldAlert(Target{Name: "any-target"}) {
		t.Error("standalone: shouldAlert must return true")
	}
}

func TestShouldAlert_IsolatedMode(t *testing.T) {
	mgr := &cluster.Manager{}
	mgr.SetIsolated(true)

	e := &Engine{clusterMgr: mgr}
	if e.shouldAlert(Target{Name: "any-target"}) {
		t.Error("isolated mode: shouldAlert must return false")
	}
}

func TestShouldAlert_NotResponsible(t *testing.T) {
	mgr := cluster.NewTestManager("node-3", []string{"node-1", "node-2", "node-3"})
	e := &Engine{clusterMgr: mgr}

	// Find a target node-3 is NOT responsible for
	for _, tid := range []string{"db", "cache", "api", "auth", "search", "queue", "payments", "orders", "x", "y", "z", "1", "2"} {
		if !mgr.IsResponsible(tid) {
			if e.shouldAlert(Target{Name: tid}) {
				t.Errorf("shouldAlert(%q): node-3 is not responsible but got true", tid)
			}
			return
		}
	}
	t.Skip("all test targets landed on node-3 — extend set")
}

func TestShouldAlert_Responsible(t *testing.T) {
	mgr := cluster.NewTestManager("node-1", []string{"node-1", "node-2", "node-3"})
	e := &Engine{clusterMgr: mgr}

	// Find a target node-1 IS responsible for
	for _, tid := range []string{"db", "cache", "api", "auth", "search", "queue", "payments", "orders", "x", "y", "z", "1", "2"} {
		if mgr.IsResponsible(tid) {
			if !e.shouldAlert(Target{Name: tid}) {
				t.Errorf("shouldAlert(%q): node-1 is responsible but got false", tid)
			}
			return
		}
	}
	t.Skip("no target hashed to node-1 — extend set")
}

// ── computeScope ──────────────────────────────────────────────────────────────

func TestComputeScope_Standalone(t *testing.T) {
	e := &Engine{}
	if got := e.computeScope("target", true); got != "STANDALONE" {
		t.Errorf("standalone: want STANDALONE got %q", got)
	}
}

func TestComputeScope_Global(t *testing.T) {
	mgr := cluster.NewTestManager("node-1", []string{"node-1", "node-2", "node-3"})
	mgr.SetPeerState("node-2", "target", cluster.GossipPayload{TargetID: "target", State: "hard_down", NodeName: "node-2"})
	mgr.SetPeerState("node-3", "target", cluster.GossipPayload{TargetID: "target", State: "hard_down", NodeName: "node-3"})

	e := &Engine{clusterMgr: mgr}
	// local=down, both peers=down → GLOBAL
	if got := e.computeScope("target", true); got != "GLOBAL" {
		t.Errorf("all-down: want GLOBAL got %q", got)
	}
}

func TestComputeScope_NodeLocal(t *testing.T) {
	mgr := cluster.NewTestManager("node-1", []string{"node-1", "node-2", "node-3"})
	mgr.SetPeerState("node-2", "target", cluster.GossipPayload{TargetID: "target", State: "up", NodeName: "node-2"})
	mgr.SetPeerState("node-3", "target", cluster.GossipPayload{TargetID: "target", State: "up", NodeName: "node-3"})

	e := &Engine{clusterMgr: mgr}
	// local=down, peers=up → NODE_LOCAL
	if got := e.computeScope("target", true); got != "NODE_LOCAL" {
		t.Errorf("only-local-down: want NODE_LOCAL got %q", got)
	}
}

func TestComputeScope_Partial(t *testing.T) {
	mgr := cluster.NewTestManager("node-1", []string{"node-1", "node-2", "node-3"})
	mgr.SetPeerState("node-2", "target", cluster.GossipPayload{TargetID: "target", State: "hard_down", NodeName: "node-2"})
	mgr.SetPeerState("node-3", "target", cluster.GossipPayload{TargetID: "target", State: "up", NodeName: "node-3"})

	e := &Engine{clusterMgr: mgr}
	// local=down, node-2=down, node-3=up → PARTIAL
	if got := e.computeScope("target", true); got != "PARTIAL" {
		t.Errorf("mixed: want PARTIAL got %q", got)
	}
}

func TestComputeScope_NoPeerData(t *testing.T) {
	mgr := cluster.NewTestManager("node-1", []string{"node-1", "node-2"})
	// No peer states set

	e := &Engine{clusterMgr: mgr}
	if got := e.computeScope("target", true); got != "NODE_LOCAL" {
		t.Errorf("no peer data, local down: want NODE_LOCAL got %q", got)
	}
}

// ── Helpers for testing (defined in cluster package via test helpers) ─────────

// stubClusterManager is a minimal Manager stand-in when we can't use cluster.NewTestManager.
type stubEngine struct {
	Engine
	mu sync.RWMutex
}
