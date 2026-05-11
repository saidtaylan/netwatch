package cluster

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Phase 13 Step 1 — Config field tests ──────────────────────────────────────

func TestConfig_EffectiveReplicationFactor_DefaultsToThree(t *testing.T) {
	c := Config{}
	if got := c.effectiveReplicationFactor(); got != 3 {
		t.Fatalf("default factor: want 3, got %d", got)
	}
}

func TestConfig_EffectiveReplicationFactor_ExplicitOverride(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{1, 1},
		{3, 3},
		{5, 5},
		{999, 999},
	}
	for _, c := range cases {
		cfg := Config{ProbeReplicationFactor: c.in}
		if got := cfg.effectiveReplicationFactor(); got != c.want {
			t.Errorf("factor=%d: want %d, got %d", c.in, c.want, got)
		}
	}
}

func TestConfig_Validate_RejectsNegativeFactor(t *testing.T) {
	cfg := Config{
		Enabled:                true,
		NodeName:               "n1",
		ProbeReplicationFactor: -1,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative factor, got nil")
	}
	if !strings.Contains(err.Error(), "probe_replication_factor") {
		t.Fatalf("error should mention probe_replication_factor, got: %v", err)
	}
}

func TestConfig_Validate_ZeroFactorIsValid(t *testing.T) {
	// Zero means "use default" — must validate cleanly.
	cfg := Config{
		Enabled:                true,
		NodeName:               "n1",
		ProbeReplicationFactor: 0,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero factor should be valid, got: %v", err)
	}
}

func TestConfig_Validate_AcceptsAnyZoneString(t *testing.T) {
	// Zone is free-form. Empty, ascii, unicode — all must pass.
	cases := []string{"", "istanbul", "us-east-1a", "böl-1", "🇹🇷"}
	for _, z := range cases {
		cfg := Config{Enabled: true, NodeName: "n1", Zone: z}
		if err := cfg.Validate(); err != nil {
			t.Errorf("zone %q should be valid, got: %v", z, err)
		}
	}
}

func TestConfig_Validate_DisabledClusterSkipsAllChecks(t *testing.T) {
	// When disabled, even nonsense values must not error — operators may
	// leave junk in commented-out sections.
	cfg := Config{
		Enabled:                false,
		NodeName:               "",
		ProbeReplicationFactor: -42,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled cluster should bypass validation, got: %v", err)
	}
}

// ── Phase 13 Step 2 — NodeMeta + zoneOf tests ────────────────────────────────

func TestNodeMeta_IncludesZoneWhenSet(t *testing.T) {
	mgr := &Manager{cfg: Config{NodeName: "node-a", Zone: "istanbul"}}
	d := &gossipDelegate{mgr: mgr}

	raw := d.NodeMeta(512)
	if raw == nil {
		t.Fatal("NodeMeta returned nil")
	}
	var got nodeMeta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Node != "node-a" {
		t.Errorf("Node: want node-a, got %q", got.Node)
	}
	if got.Zone != "istanbul" {
		t.Errorf("Zone: want istanbul, got %q", got.Zone)
	}
}

func TestNodeMeta_OmitsZoneWhenEmpty(t *testing.T) {
	mgr := &Manager{cfg: Config{NodeName: "node-a"}}
	d := &gossipDelegate{mgr: mgr}

	raw := d.NodeMeta(512)
	if strings.Contains(string(raw), "zone") {
		t.Errorf("empty zone should be omitted from payload, got: %s", raw)
	}
}

func TestNodeMeta_LimitOverflowFallsBackToNodeOnly(t *testing.T) {
	// Force overflow: pass a tiny limit that fits {"node":"a"} but not a zone.
	mgr := &Manager{cfg: Config{NodeName: "a", Zone: "this-zone-is-too-long-for-limit"}}
	d := &gossipDelegate{mgr: mgr}

	raw := d.NodeMeta(15) // {"node":"a"} = 12 bytes; the full payload is >40.
	if raw == nil {
		t.Fatal("expected fallback payload, got nil")
	}
	if strings.Contains(string(raw), "zone") {
		t.Errorf("over-limit zone should be dropped, got: %s", raw)
	}
	var got nodeMeta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("fallback payload must be valid JSON, got: %v", err)
	}
	if got.Node != "a" {
		t.Errorf("Node: want a, got %q", got.Node)
	}
}

func TestNodeMeta_LimitTooSmallForAnyPayload(t *testing.T) {
	// Limit below {"node":"x"} → must return nil rather than malformed bytes.
	mgr := &Manager{cfg: Config{NodeName: "x"}}
	d := &gossipDelegate{mgr: mgr}

	if raw := d.NodeMeta(3); raw != nil {
		t.Errorf("expected nil for impossibly small limit, got: %s", raw)
	}
}

func TestZoneOf_SelfReturnsLocalZone(t *testing.T) {
	// Before memberlist is up, zoneOf must still answer for the local node
	// from cfg — otherwise startup-time callers see "" unnecessarily.
	mgr := &Manager{cfg: Config{NodeName: "self", Zone: "ankara"}}
	if got := mgr.zoneOf("self"); got != "ankara" {
		t.Errorf("zoneOf(self): want ankara, got %q", got)
	}
}

func TestZoneOf_UnknownNodeReturnsEmpty(t *testing.T) {
	mgr := &Manager{cfg: Config{NodeName: "self"}}
	if got := mgr.zoneOf("ghost"); got != "" {
		t.Errorf("unknown node should return empty zone, got %q", got)
	}
}

func TestZoneOf_LocalNodeFastPathDoesNotPanic(t *testing.T) {
	// Manager constructed without memberlist (test path) must not crash.
	mgr := &Manager{cfg: Config{NodeName: "n", Zone: "z"}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zoneOf panicked: %v", r)
		}
	}()
	_ = mgr.zoneOf("n")
	_ = mgr.zoneOf("other")
}

func TestZoneOf_ExportedAlias(t *testing.T) {
	mgr := &Manager{cfg: Config{NodeName: "self", Zone: "izmir"}}
	if mgr.ZoneOf("self") != mgr.zoneOf("self") {
		t.Error("ZoneOf must mirror zoneOf")
	}
}
