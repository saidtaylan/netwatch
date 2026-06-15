// Package cluster implements the gossip-based cluster layer using memberlist.
//
// Phase 6 scope: nodes join/leave, state changes are broadcast, peer states
// are tracked. Alerting decisions are NOT yet affected — that comes in Phase 8
// (consistent hashing + exactly-once alerting).
//
// When Config.Enabled is false, New() returns (nil, nil) and callers treat a
// nil *Manager as a no-op. No code path inside the engine package is opened.
package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"log/slog"
	"math"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"
)

// ── Config ────────────────────────────────────────────────────────────────────

// Config holds all cluster-related configuration fields.
// It is embedded as `cluster:` in the top-level engine Config.
type Config struct {
	// Enabled must be true to activate the cluster layer.
	// false (default) → New() returns nil; zero cluster code paths.
	Enabled bool `json:"enabled"`

	// NodeName uniquely identifies this node in the cluster.
	// Required when Enabled=true. Typically hostname or hostname:port.
	NodeName string `json:"node_name"`

	// BindAddr is the address memberlist listens on (default "0.0.0.0").
	BindAddr string `json:"bind_addr,omitempty"`

	// BindPort is the gossip port (default 7946).
	BindPort int `json:"bind_port,omitempty"`

	// AdvertiseAddr is the address peers use to reach this node.
	// Useful behind NAT or in container networks.
	AdvertiseAddr string `json:"advertise_addr,omitempty"`

	// AdvertisePort overrides the advertised port (defaults to BindPort).
	AdvertisePort int `json:"advertise_port,omitempty"`

	// Peers is a list of seed addresses (host:port) used on startup to join
	// an existing cluster. best-effort — no error if none are reachable.
	Peers []string `json:"peers,omitempty"`

	// RejoinIntervalSec controls how often the background re-join loop re-checks
	// membership and, when this node is below target strength, re-attempts
	// Join(Peers). This recovers a node that was evicted by a prolonged network
	// partition: once connectivity returns it rejoins on its own instead of
	// staying split-brained until restart. memberlist's Join is idempotent, and
	// the loop only calls it while under strength, so it is cheap when healthy.
	// 0 (default) → 15 seconds.
	RejoinIntervalSec int `json:"rejoin_interval_sec,omitempty"`

	// Keyring contains base64-encoded AES keys (decoded length must be 16, 24,
	// or 32 bytes). The first key is used for encryption; all keys are tried for
	// decryption (supports zero-downtime key rotation). Leave empty to disable
	// encryption (not recommended in production).
	Keyring []string `json:"keyring,omitempty"`

	// ExpectedNodeCount is the total expected cluster size, used for quorum
	// calculation in Phase 7. Has no effect in Phase 6.
	ExpectedNodeCount int `json:"expected_node_count,omitempty"`

	// MinQuorumRatio is the minimum fraction of nodes required for quorum
	// (Phase 7). Default 0.5 (simple majority). Has no effect in Phase 6.
	MinQuorumRatio float64 `json:"min_quorum_ratio,omitempty"`

	// Zone is an optional free-form label identifying the physical or logical
	// location of this node (e.g. "istanbul", "us-east-1a"). When set on at
	// least two nodes, Phase 13's prober selection prefers picking probers
	// from distinct zones for redundancy.
	//
	// Empty by default — operators must opt in by setting it on each node.
	// The agent never derives this from hostname or any other implicit source.
	//
	// Propagated to peers via memberlist NodeMeta (no extra gossip traffic).
	Zone string `json:"zone,omitempty"`

	// Region is an optional geographic label for this node (e.g. "eu-central",
	// "us-east", "asia-pacific"). Distinct from Zone:
	//   - Zone is used for failure-domain spread in prober selection (Phase 13).
	//   - Region is used for latency grouping and probe_from_regions filtering (P1.6).
	//
	// Propagated to peers via memberlist NodeMeta alongside Zone.
	Region string `json:"region,omitempty"`

	// HTTPPort is the HTTP port this node serves admin endpoints on (the
	// `port:` field at the top of config.yaml). Propagated to peers via
	// NodeMeta so any node can build a URL for any other node — required
	// for cross-node aggregation endpoints like /cluster/sync/aggregate
	// (effective-config diff across the cluster).
	HTTPPort string `json:"-"`

	// ProbeReplicationFactor caps the number of nodes that probe any single
	// target. Even if N nodes have the target in their config, only this many
	// (selected deterministically via the hash ring with zone-aware spread)
	// will run probes; the rest stay quiet and only consume gossip.
	//
	// 0 (default) → 3. To force every candidate node to probe (legacy
	// behaviour), set a value larger than the cluster size, e.g. 999.
	//
	// When candidate count ≤ factor all candidates probe — small clusters
	// keep the current behaviour with zero configuration.
	ProbeReplicationFactor int `json:"probe_replication_factor,omitempty"`

	// ProbeReplicationPercent, when > 0, expresses the prober count as a
	// percentage of the eligible candidate nodes instead of a fixed number —
	// useful for large clusters where a constant like 3 may be too few or too
	// many. The effective factor is ceil(percent/100 × candidates), at least 1,
	// and it takes precedence over ProbeReplicationFactor.
	//
	// Example: percent=10 on 100 candidates → 10 probers; on 20 → 2.
	//
	// Because it is derived from the candidate set every node already agrees on
	// (gossiped peer states), all nodes compute the same effective factor, so
	// the exactly-once / deterministic-assignment guarantees are preserved.
	ProbeReplicationPercent int `json:"probe_replication_percent,omitempty"`

	// ConfigSync holds gossip-based config drift detection settings (P1.5).
	// When nil or Enabled=false, no config hash is broadcast.
	ConfigSync *ConfigSyncConfig `json:"config_sync,omitempty"`

	// MinProbeConfirmations is the minimum number of probers that must
	// independently confirm hard_down before the responsible node dispatches
	// an alert. 0 or 1 (default) preserves the current behaviour — alert as
	// soon as any single prober declares hard_down. Set to 2 to require two
	// independent confirmations, which suppresses false positives caused by a
	// single prober losing connectivity to the target while other probers still
	// see it as up. Trade-off: higher values reduce false alerts but increase
	// detection latency by up to one probe_interval_sec.
	MinProbeConfirmations int `json:"min_probe_confirmations,omitempty"`
}

// effectiveReplicationFactor resolves the desired prober count for a target
// given the number of eligible candidate nodes. Precedence:
//   - ProbeReplicationPercent > 0 → ceil(percent/100 × candidates), at least 1
//   - else ProbeReplicationFactor when set
//   - else the default of 3
// Centralised so the default and the percent maths live in one place.
func (c Config) effectiveReplicationFactor(candidates int) int {
	if c.ProbeReplicationPercent > 0 {
		f := int(math.Ceil(float64(c.ProbeReplicationPercent) * float64(candidates) / 100.0))
		if f < 1 {
			f = 1
		}
		return f
	}
	if c.ProbeReplicationFactor > 0 {
		return c.ProbeReplicationFactor
	}
	return 3
}

// Validate checks required fields when cluster is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.NodeName == "" {
		return fmt.Errorf("cluster.node_name is required when cluster.enabled=true")
	}
	if c.BindPort != 0 && (c.BindPort < 1 || c.BindPort > 65535) {
		return fmt.Errorf("cluster.bind_port must be 1–65535, got %d", c.BindPort)
	}
	for i, k := range c.Keyring {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			return fmt.Errorf("cluster.keyring[%d]: base64 decode: %w", i, err)
		}
		if n := len(raw); n != 16 && n != 24 && n != 32 {
			return fmt.Errorf("cluster.keyring[%d]: decoded length %d is not 16, 24, or 32", i, n)
		}
	}
	if c.ProbeReplicationFactor < 0 {
		return fmt.Errorf("cluster.probe_replication_factor must be >= 0, got %d", c.ProbeReplicationFactor)
	}
	if c.ProbeReplicationPercent < 0 || c.ProbeReplicationPercent > 100 {
		return fmt.Errorf("cluster.probe_replication_percent must be 0–100, got %d", c.ProbeReplicationPercent)
	}
	// Zone is free-form text — no constraints. An empty value disables
	// zone-aware prober selection for this node specifically.
	return nil
}

// ── AntiEntropyProvider ───────────────────────────────────────────────────────

// AntiEntropyProvider is implemented by the engine layer and plugged in via
// Manager.SetStateProvider(). It lets the cluster layer request and apply full
// state snapshots during memberlist push-pull cycles without importing the
// engine package (which would create a dependency cycle).
type AntiEntropyProvider interface {
	// FullState returns a serialised snapshot of all locally known target
	// states. Called during LocalState(join=true) so a re-joining peer
	// receives the complete current picture.
	FullState() []byte

	// ApplyRemoteState merges a remote full-state snapshot into this engine.
	// Called during MergeRemoteState(join=true) after a push-pull re-join.
	ApplyRemoteState(buf []byte)

	// SetSyncing enables (true) or disables (false) sync mode.
	// While true the engine suppresses new alert dispatch to prevent alarm
	// storms during state reconciliation.
	SetSyncing(v bool)
}

// ── GossipPayload ─────────────────────────────────────────────────────────────

// GossipPayload is the unit of state exchanged between cluster nodes.
// It carries the latest known health state for one target from one node's
// perspective.
//
// TargetName and TargetType are included so that a primary node that does not
// probe this target locally can still dispatch a meaningful alert when it
// receives a hard_down state from a peer. Without them the primary would only
// know the target's ID, losing the human-readable name in the alert.
type GossipPayload struct {
	TargetID   string    `json:"target_id"`
	TargetName string    `json:"target_name,omitempty"` // display name (config: name:)
	TargetType string    `json:"target_type,omitempty"` // probe type: tcp/http/ping/dns/sql
	State      string    `json:"state"`                 // "up" | "hard_down" | "soft_down"
	Seq        uint64    `json:"seq"`                   // Lamport sequence from engine (0 for soft_down signals)
	ErrorCode  string    `json:"error_code,omitempty"`
	NodeName   string    `json:"node_name"`             // originating node
	// Latency is the last measured round-trip in seconds (P1.6).
	// 0 means not measured / not applicable (failure probes, bootstrap).
	Latency float64 `json:"latency,omitempty"`
	// RetryNum is set only when State=="soft_down". It indicates how many
	// retry attempts have occurred so far, giving co-probers a sense of urgency.
	// soft_down payloads are never stored in peerStates — they are transient
	// suspect signals that trigger immediate co-prober verification.
	RetryNum  int       `json:"retry_num,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ── MemberInfo ────────────────────────────────────────────────────────────────

// MemberInfo is the JSON-serializable view of a cluster member.
//
// Zone is sourced from memberlist NodeMeta (Phase 13) and is empty when the
// member has not declared one. Region (P1.6) is the geographic label used for
// latency grouping; also from NodeMeta.
type MemberInfo struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Port     uint16 `json:"port"`
	Status   string `json:"status"` // alive | suspect | dead | left
	Self     bool   `json:"self"`
	Zone     string `json:"zone,omitempty"`
	Region   string `json:"region,omitempty"`   // P1.6 geo latency grouping
	HTTPPort string `json:"http_port,omitempty"` // for cross-node aggregation URLs
}

// ── ClusterStateSnapshot ──────────────────────────────────────────────────────

// ClusterStateSnapshot is returned by Manager.Snapshot() for /cluster/state.
type ClusterStateSnapshot struct {
	LocalNode  string                               `json:"local_node"`
	Members    []MemberInfo                         `json:"members"`
	PeerStates map[string]map[string]GossipPayload  `json:"peer_states,omitempty"`
}

// ── broadcast (implements memberlist.Broadcast) ───────────────────────────────

type broadcast struct {
	payload GossipPayload
	data    []byte // pre-encoded JSON
}

// newBroadcast wraps a GossipPayload in a memberlist.Broadcast, pre-marshalling
// it to JSON once. Returns an error if the payload can't be encoded.
func newBroadcast(p GossipPayload) (*broadcast, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return &broadcast{payload: p, data: data}, nil
}

// Invalidates returns true when this broadcast supersedes an older one for the
// same (node, target) pair so the queue drops the stale entry.
func (b *broadcast) Invalidates(other memberlist.Broadcast) bool {
	ob, ok := other.(*broadcast)
	if !ok {
		return false
	}
	return b.payload.NodeName == ob.payload.NodeName &&
		b.payload.TargetID == ob.payload.TargetID &&
		b.payload.Seq >= ob.payload.Seq
}

// Message returns the encoded bytes memberlist gossips to peers.
func (b *broadcast) Message() []byte { return b.data }

// Finished is the memberlist callback invoked once the broadcast has been sent
// to enough peers; nothing to clean up here.
func (b *broadcast) Finished() {}

// ── gossipDelegate (implements memberlist.Delegate) ───────────────────────────

type gossipDelegate struct {
	mgr        *Manager
	broadcasts *memberlist.TransmitLimitedQueue
}

// nodeMeta is the JSON payload memberlist distributes for every node.
// Stays intentionally small — memberlist limits NodeMeta to 512 bytes by
// default and replicates it on every node update.
//
// Fields:
//   - Node   — the node's own NodeName; redundant with memberlist.Node.Name but
//               useful for debugging when receivers dump the raw payload.
//   - Zone   — optional zone label for Phase 13 prober selection (failure domain).
//   - Region — optional geographic label for P1.6 geo latency grouping.
type nodeMeta struct {
	Node     string `json:"node"`
	Zone     string `json:"zone,omitempty"`
	Region   string `json:"region,omitempty"` // P1.6: geographic region label
	HTTPPort string `json:"http_port,omitempty"`
}

// NodeMeta returns this node's metadata (zone, region, HTTP port) encoded for
// memberlist to attach to every gossip message, so peers learn each node's
// labels without extra round-trips. If the encoded meta would exceed memberlist's
// limit, optional fields are dropped (region first) to fit.
func (d *gossipDelegate) NodeMeta(limit int) []byte {
	full := nodeMeta{
		Node:     d.mgr.cfg.NodeName,
		Zone:     d.mgr.cfg.Zone,
		Region:   d.mgr.cfg.Region,
		HTTPPort: d.mgr.cfg.HTTPPort,
	}
	data, _ := json.Marshal(full)
	if len(data) > limit {
		// 512 byte limit — drop optional fields in priority order: Region,
		// then HTTPPort (cross-node aggregation degrades to "node-only" view),
		// then Zone (prober spread loses the failure-domain hint).
		full.Region = ""
		data, _ = json.Marshal(full)
		if len(data) > limit {
			full.HTTPPort = ""
			data, _ = json.Marshal(full)
			if len(data) > limit {
				full.Zone = ""
				data, _ = json.Marshal(full)
				if len(data) > limit {
					return nil
				}
			}
		}
	}
	return data
}

// NotifyMsg is called whenever a UDP gossip message arrives.
// It distinguishes between state payloads (legacy / normal) and config
// broadcasts (P1.5, identified by msg_type == "config").
func (d *gossipDelegate) NotifyMsg(b []byte) {
	if len(b) == 0 {
		return
	}
	// Peek at msg_type to support multiple message types on the same queue.
	// GossipPayload messages do not carry msg_type, so a missing/empty field
	// routes to the legacy state path — fully backward-compatible.
	var peek struct {
		MsgType string `json:"msg_type"`
	}
	if err := json.Unmarshal(b, &peek); err == nil {
		switch peek.MsgType {
		case msgTypeConfig:
			var cb ConfigBroadcast
			if err := json.Unmarshal(b, &cb); err != nil {
				slog.Warn("cluster: malformed config broadcast", "err", err)
				return
			}
			d.mgr.handleConfigBroadcast(cb)
			return
		case msgTypeConfigPush:
			go d.mgr.handleConfigPush(b)
			return
		case msgTypeMaintenance:
			go d.mgr.handleMaintenance(b)
			return
		case msgTypeStorageChange:
			go d.mgr.handleStorageChange(b)
			return
		}
	}
	var p GossipPayload
	if err := json.Unmarshal(b, &p); err != nil {
		slog.Warn("cluster: malformed gossip message", "len", len(b), "err", err)
		return
	}
	d.mgr.OnStateReceived(p)
}

// GetBroadcasts is called by memberlist to drain the outgoing broadcast queue.
func (d *gossipDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return d.broadcasts.GetBroadcasts(overhead, limit)
}

// LocalState serialises this node's state for memberlist push-pull cycles.
//
//   - join=true  (re-join): return the engine's full lastKnown map so the
//     incoming peer can reconcile without spurious alarms. Requires a registered
//     AntiEntropyProvider (wired up in Engine.Init).
//   - join=false (periodic): return peerStates — individual gossip payloads
//     from all known peers. This is the lightweight periodic exchange.
func (d *gossipDelegate) LocalState(join bool) []byte {
	// Read stateProvider under mu to avoid a data race with SetStateProvider(),
	// which writes under mu.Lock(). Memberlist starts its background goroutines
	// inside cluster.New, before Engine.Init calls SetStateProvider.
	d.mgr.mu.RLock()
	sp := d.mgr.stateProvider
	d.mgr.mu.RUnlock()
	if join && sp != nil {
		return sp.FullState()
	}
	// Periodic push-pull — exchange peer-states as before.
	d.mgr.mu.RLock()
	data, _ := json.Marshal(d.mgr.peerStates)
	d.mgr.mu.RUnlock()
	return data
}

// MergeRemoteState is called when a push-pull cycle receives a peer's state.
//
//   - join=true  (re-join): delegate full merge to AntiEntropyProvider; suppress
//     alarm dispatch while the merge is in progress to prevent alarm storms.
//   - join=false (periodic): process each payload through the normal Lamport-merge
//     path (OnStateReceived), same as UDP gossip messages.
func (d *gossipDelegate) MergeRemoteState(buf []byte, join bool) {
	if len(buf) == 0 {
		return
	}
	// Read stateProvider under mu (written under mu.Lock() by SetStateProvider).
	d.mgr.mu.RLock()
	sp := d.mgr.stateProvider
	d.mgr.mu.RUnlock()
	if join && sp != nil {
		// Re-join full sync — let the engine merge with alarm suppression.
		sp.SetSyncing(true)
		sp.ApplyRemoteState(buf)
		sp.SetSyncing(false)
		return
	}
	// Periodic push-pull — treat as individual gossip updates.
	var remote map[string]map[string]GossipPayload
	if err := json.Unmarshal(buf, &remote); err != nil {
		slog.Warn("cluster: malformed state from peer", "err", err)
		return
	}
	for _, targets := range remote {
		for _, p := range targets {
			d.mgr.OnStateReceived(p)
		}
	}
}

// ── eventDelegate (implements memberlist.EventDelegate) ───────────────────────

type eventDelegate struct {
	mgr *Manager
}

// NotifyJoin is the memberlist callback fired when a node joins. It rebuilds the
// hash ring, schedules a debounced prober recompute (the new member changes
// candidate sets), and re-broadcasts this node's inventory and config
// fingerprint so the newcomer catches up. The work runs in a goroutine because
// memberlist holds an internal lock during the callback that updateRing would
// otherwise deadlock on.
func (e *eventDelegate) NotifyJoin(node *memberlist.Node) {
	slog.Info("cluster member joined", "node", node.Name, "addr", node.Addr)
	// updateRing calls list.Members() which acquires nodeLock.RLock.
	// memberlist calls NotifyJoin while holding nodeLock.WLock, so calling
	// list.Members() here would deadlock. Run asynchronously instead.
	go func() {
		e.mgr.updateRing()
		// Phase 13: a new member changes the candidate sets — schedule a
		// debounced recompute so probe loops can be reassigned.
		e.mgr.scheduleRecompute()
		// Re-broadcast this node's target states so the new peer can learn
		// our inventory. Gossip broadcasts are UDP (fire-and-forget) so the
		// new node may have missed the original bootstrap and probe broadcasts.
		// Read inventoryRefreshHandler under mu to avoid a data race with
		// SetInventoryRefreshHandler() which writes under mu.Lock().
		e.mgr.mu.RLock()
		h := e.mgr.inventoryRefreshHandler
		e.mgr.mu.RUnlock()
		if h != nil {
			h.BroadcastInventory()
		}
		// Re-broadcast config fingerprint so the new peer learns our config hash.
		// Config_sync may be disabled on this node — broadcastConfigInfo is a no-op
		// when config_sync.enabled=false or delegate is nil.
		e.mgr.broadcastConfigInfo()
	}()
}

// NotifyLeave is the memberlist callback fired when a node leaves or is declared
// dead. It drops that node's peer states, rebuilds the ring (which may make this
// node the new primary/prober for targets the departed node owned), and
// schedules a recompute. Runs asynchronously to avoid the memberlist lock.
func (e *eventDelegate) NotifyLeave(node *memberlist.Node) {
	slog.Info("cluster member left", "node", node.Name, "addr", node.Addr)
	nodeName := node.Name
	go func() {
		e.mgr.mu.Lock()
		delete(e.mgr.peerStates, nodeName)
		e.mgr.mu.Unlock()
		e.mgr.updateRing()
		// Phase 13: removing a member may force this node into the prober
		// set for some target it previously left to a now-gone peer.
		e.mgr.scheduleRecompute()
	}()
}

// NotifyUpdate is the memberlist callback fired when a node's metadata changes
// (e.g. its zone label). It rebuilds the ring and schedules a recompute, since a
// changed zone can alter zone-aware prober selection. Runs asynchronously.
func (e *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	slog.Debug("cluster member updated", "node", node.Name)
	go func() {
		e.mgr.updateRing()
		// NodeMeta (zone) may have changed — recompute could yield different
		// zone-aware picks. Cheap enough to schedule on every update.
		e.mgr.scheduleRecompute()
	}()
}

// ── Manager ───────────────────────────────────────────────────────────────────

// PeerAlertHandler is implemented by the engine to let the cluster layer
// trigger an alert for a target that this node does not probe locally.
//
// When a primary node receives a hard_down gossip payload for a target that
// is not in its own config, it cannot reach the alert through the normal
// probe → processPending → shouldAlert path. Instead, OnStateReceived calls
// this handler so the engine can dispatch the alert on behalf of the peer.
//
// HasLocalProbe returns true when the engine already handles alerting for
// this target via its own probe loop — in that case the callback is skipped
// to avoid double-alerting.
type PeerAlertHandler interface {
	HasLocalProbe(targetID string) bool
	DispatchPeerAlert(payload GossipPayload)
}

// SoftDownNotifier is implemented by the engine to receive soft-down suspect
// signals from co-probers. When a prober enters soft-down for a target it
// broadcasts a State="soft_down" payload. Other probers for the same target
// receive this via OnStateReceived and call NotifyCoProberSoftDown so the
// engine can trigger an immediate verification probe — shortening detection
// latency and providing coverage if the original prober crashes before it
// reaches hard_down.
//
// The signal is fire-and-forget: soft_down payloads are never stored in
// peerStates (they carry no Lamport seq). Duplicate signals are dropped
// harmlessly by the buffered channel in the probe loop.
type SoftDownNotifier interface {
	NotifyCoProberSoftDown(targetID string)
}

// InventoryRefreshHandler is implemented by the engine to re-broadcast all
// local target states when a new peer joins the cluster. This ensures that
// late-joining nodes catch up with already-running probers, since gossip
// broadcasts are fire-and-forget (UDP) and early broadcasts may be missed.
type InventoryRefreshHandler interface {
	BroadcastInventory()
}

// Manager owns the memberlist instance and all cluster state.
type Manager struct {
	cfg      Config
	list     *memberlist.Memberlist
	delegate *gossipDelegate

	mu sync.RWMutex
	// peerStates: nodeName → targetID → latest GossipPayload from that node.
	// Protected by mu. Does NOT include this node's own state (the engine
	// holds that in lastKnown).
	peerStates map[string]map[string]GossipPayload

	// peerAlerted tracks the highest seq for which a peer-driven alert was
	// already dispatched. Prevents duplicate alerts when the same hard_down
	// payload arrives via both UDP gossip and TCP push/pull.
	// Protected by mu.
	peerAlerted map[string]uint64 // targetID → last alerted seq

	// isolated is set true when quorum is lost; cleared on recovery.
	// Written by runQuorumLoop, read by IsolatedMode().
	isolated atomic.Bool

	// stopQuorum cancels the quorum background goroutine.
	// Called by Leave() to prevent goroutine leak after shutdown.
	stopQuorum func()

	// ringMu protects ring. Updated on every NotifyJoin/Leave/Update event.
	ringMu sync.RWMutex
	// ring is the sorted list of alive node names used by the hash ring.
	// Kept sorted lexicographically so all nodes agree on the same ordering.
	ring []string

	// stateProvider bridges back to the engine for anti-entropy push-pull.
	// Set via SetStateProvider() after New() returns; nil until wired up.
	stateProvider AntiEntropyProvider

	// peerAlertHandler is set by the engine to handle alerts for targets this
	// node does not probe locally. nil until SetPeerAlertHandler is called.
	peerAlertHandler PeerAlertHandler

	// softDownNotifier is set by the engine to receive co-prober soft-down
	// suspect signals. nil until SetSoftDownNotifier is called.
	softDownNotifier SoftDownNotifier

	// configPushHandler is set by the engine to apply incoming shared-config
	// push payloads. nil until SetConfigPushHandler is called.
	configPushHandler ConfigPushHandler

	// maintenanceHandler is set by the engine to apply incoming maintenance
	// window set/cancel broadcasts. nil until SetMaintenanceHandler is called.
	maintenanceHandler MaintenanceHandler

	// storageChangeHandler is set by the engine to apply incoming storage
	// change broadcasts (SLO targets, apps, channels, etc.). Backed by
	// gossip.Storage.ApplyRemoteChange. nil until SetStorageChangeHandler.
	storageChangeHandler StorageChangeHandler

	// inventoryRefreshHandler is called on NotifyJoin so the engine
	// re-broadcasts its local target states to late-joining peers.
	// nil until SetInventoryRefreshHandler is called.
	inventoryRefreshHandler InventoryRefreshHandler

	// localTargetProvider is the engine-side inventory of target IDs in this
	// node's config. Used by CandidatesFor to include the local node in
	// candidate sets before its first state broadcast. nil until
	// SetLocalTargetProvider is called. Protected by mu.
	localTargetProvider LocalTargetProvider

	// assignmentListener receives StartProbing / StopProbing callbacks when
	// this node's prober responsibilities change. Set via
	// SetProberAssignmentListener. nil → no callbacks emitted. Protected by mu.
	assignmentListener ProberAssignmentListener

	// proberAssignments caches the result of the last recomputeProberAssignments
	// pass: targetID → "this node is one of the probers". Used to diff
	// against the new desired set so only changed targets get callbacks.
	// Protected by mu.
	proberAssignments map[string]bool

	// recomputeMu protects recomputeTimer. Separate from m.mu so timer resets
	// from gossip event handlers do not contend with the much hotter peerStates
	// lock.
	recomputeMu sync.Mutex
	// recomputeTimer is the debounce timer scheduleRecompute resets on every
	// membership / inventory event. nil until the first call.
	recomputeTimer *time.Timer

	// ── Config-sync state (P1.5) ──────────────────────────────────────────
	// cfgMu protects the local config info fields below. Kept separate from
	// the hot peerStates mutex (mu) to avoid contention.
	cfgMu          sync.RWMutex
	localCfgHash   string
	localCfgSize   int64
	localCfgLoadedAt time.Time
	// peerConfigs stores the most-recent ConfigBroadcast from each peer node.
	// Protected by mu (reuses the peerStates lock for simplicity).
	peerConfigs map[string]ConfigBroadcast
	// stopConfigSync cancels the runConfigSyncLoop goroutine on Leave().
	stopConfigSync func()

	// stopRejoin cancels the background re-join goroutine on Leave().
	stopRejoin func()

	// keyring is the live AES keyring used by memberlist for gossip encryption.
	// Non-nil only when the cluster was started with at least one key in Config.Keyring.
	// Used by KeyringAddKey / KeyringUseKey / KeyringRemoveKey for zero-downtime rotation.
	keyring *memberlist.Keyring

	// ── Test-only overrides ────────────────────────────────────────────────
	// These are populated exclusively by testhelpers.go's SetTestAliveSet /
	// SetTestZones / SetTestRegions. They let unit tests simulate membership
	// and metadata without standing up a real memberlist. nil in production.
	testAliveOverride  map[string]bool
	testZoneOverride   map[string]string
	testRegionOverride map[string]string // P1.6: region override for tests
}

// New creates and starts the cluster manager.
// Returns (nil, nil) when cfg.Enabled is false — callers treat nil as no-op.
func New(cfg Config) (*Manager, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	m := &Manager{
		cfg:         cfg,
		peerStates:  make(map[string]map[string]GossipPayload),
		peerAlerted: make(map[string]uint64),
	}

	mlCfg := memberlist.DefaultLANConfig()
	mlCfg.Name = cfg.NodeName
	mlCfg.Logger = log.New(os.Stderr, "", 0) // memberlist internal logs suppressed

	if cfg.BindAddr != "" {
		mlCfg.BindAddr = cfg.BindAddr
	}
	if cfg.BindPort > 0 {
		mlCfg.BindPort = cfg.BindPort
		// AdvertisePort defaults to BindPort in DefaultLANConfig, but only at
		// construction time. Sync it here so gossip pings use the correct port.
		mlCfg.AdvertisePort = cfg.BindPort
	}
	if cfg.AdvertiseAddr != "" {
		mlCfg.AdvertiseAddr = cfg.AdvertiseAddr
	}
	if cfg.AdvertisePort > 0 {
		// Explicit AdvertisePort overrides the BindPort-derived default.
		mlCfg.AdvertisePort = cfg.AdvertisePort
	}

	// AES keyring — optional but strongly recommended in production.
	if len(cfg.Keyring) > 0 {
		keys := make([][]byte, 0, len(cfg.Keyring))
		for i, k := range cfg.Keyring {
			raw, err := base64.StdEncoding.DecodeString(k)
			if err != nil {
				return nil, fmt.Errorf("cluster keyring[%d]: %w", i, err)
			}
			keys = append(keys, raw)
		}
		kr, err := memberlist.NewKeyring(keys, keys[0])
		if err != nil {
			return nil, fmt.Errorf("cluster keyring: %w", err)
		}
		mlCfg.Keyring = kr
		m.keyring = kr // stored for live key rotation via KeyringAddKey/UseKey/RemoveKey
	}

	delegate := &gossipDelegate{
		mgr: m,
		broadcasts: &memberlist.TransmitLimitedQueue{
			NumNodes:       func() int { return m.list.NumMembers() },
			RetransmitMult: mlCfg.RetransmitMult,
		},
	}
	m.delegate = delegate
	mlCfg.Delegate = delegate
	mlCfg.Events = &eventDelegate{mgr: m}

	list, err := memberlist.Create(mlCfg)
	if err != nil {
		return nil, fmt.Errorf("cluster create: %w", err)
	}
	// Assign under ringMu so that any concurrent updateRing() goroutine spawned
	// by the NotifyJoin callback during Create() sees a consistent list pointer.
	m.ringMu.Lock()
	m.list = list
	m.ringMu.Unlock()

	// Seed the ring with the local node before joining peers.
	m.updateRing()

	if len(cfg.Peers) > 0 {
		n, err := list.Join(cfg.Peers)
		if err != nil {
			slog.Warn("cluster join partial — continuing",
				"contacted", n, "of", len(cfg.Peers), "err", err)
		} else {
			slog.Info("cluster joined", "contacted", n, "peers", cfg.Peers)
		}
	}

	// Rebuild the ring now that Join() has exchanged full membership with peers.
	// The initial updateRing() call (above) only had the local node in the ring
	// because Join() had not yet completed. After Join(), list.Members() reflects
	// the full cluster view, so the ring is now populated correctly.
	m.updateRing()

	local := list.LocalNode()
	slog.Info("cluster started",
		"node", cfg.NodeName,
		"addr", net.JoinHostPort(local.Addr.String(), fmt.Sprintf("%d", local.Port)),
		"members", list.NumMembers(),
	)

	// Start quorum watcher only when expected_node_count is configured.
	// Without it there is nothing to calculate quorum against.
	if cfg.ExpectedNodeCount > 0 {
		m.startQuorumLoop()
	}

	// P1.5: start config-sync loop if enabled.
	m.startConfigSyncLoop()

	// Background rejoin loop — keeps the node converged on the cluster. It serves
	// two cases: (1) startup, when all nodes boot together and the initial Join()
	// is refused by peers not yet listening, and (2) recovery, when a prolonged
	// network partition causes peers to evict this node — once connectivity
	// returns it rejoins on its own instead of staying split-brained until a
	// restart. Unlike the old loop, it does NOT stop after the cluster first
	// forms; it runs for the manager's lifetime.
	if len(cfg.Peers) > 0 {
		m.startRejoinLoop(cfg.Peers)
	}

	return m, nil
}

// rejoinInterval returns the configured re-join cadence, defaulting to 15s.
func (m *Manager) rejoinInterval() time.Duration {
	if m.cfg.RejoinIntervalSec > 0 {
		return time.Duration(m.cfg.RejoinIntervalSec) * time.Second
	}
	return 15 * time.Second
}

// targetStrength is the alive-member count at or above which this node is
// considered fully converged. It is the expected cluster size when configured,
// otherwise 2 (this node plus at least one peer). Used to decide when the
// re-join loop should actively re-attempt Join.
func (m *Manager) targetStrength() int {
	if m.cfg.ExpectedNodeCount > 1 {
		return m.cfg.ExpectedNodeCount
	}
	return 2
}

// startRejoinLoop launches the background re-join goroutine and wires its
// cancel into Leave().
func (m *Manager) startRejoinLoop(peers []string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.stopRejoin = cancel
	go m.runRejoinLoop(ctx, peers)
}

// runRejoinLoop runs for the manager's lifetime. On each tick, while the node
// sees fewer than targetStrength() alive members, it re-attempts Join(peers).
// memberlist's Join is idempotent and the call is skipped once the node is at
// strength, so this is cheap when the cluster is healthy. To avoid log spam it
// only logs on the under-strength ↔ healthy transition, not per attempt.
func (m *Manager) runRejoinLoop(ctx context.Context, peers []string) {
	ticker := time.NewTicker(m.rejoinInterval())
	defer ticker.Stop()

	underStrength := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.list == nil {
				continue
			}
			under := m.list.NumMembers() < m.targetStrength()
			if under {
				if n, err := m.list.Join(peers); err != nil {
					slog.Debug("cluster re-join attempt failed", "contacted", n, "err", err)
				}
			}
			// Log only on state change so a permanently under-strength cluster
			// (e.g. a node intentionally scaled down) does not spam the log.
			if under != underStrength {
				if under {
					slog.Warn("[CLUSTER] below target strength — re-joining peers",
						"members", m.list.NumMembers(), "target", m.targetStrength())
				} else {
					slog.Info("[CLUSTER] re-converged to target strength",
						"members", m.list.NumMembers())
				}
				underStrength = under
			}
		}
	}
}

// Broadcast queues a GossipPayload for UDP gossip delivery to all members.
// A newer payload for the same (node, target) automatically invalidates the
// previous queued entry, so only the latest state is transmitted.
func (m *Manager) Broadcast(p GossipPayload) {
	b, err := newBroadcast(p)
	if err != nil {
		slog.Error("cluster broadcast marshal", "err", err)
		return
	}
	m.delegate.broadcasts.QueueBroadcast(b)
}

// BroadcastReliable sends a GossipPayload to every member via TCP.
// Use for critical state transitions where guaranteed delivery is needed.
func (m *Manager) BroadcastReliable(p GossipPayload) {
	data, err := json.Marshal(p)
	if err != nil {
		slog.Error("cluster broadcast reliable marshal", "err", err)
		return
	}
	local := m.list.LocalNode()
	for _, member := range m.list.Members() {
		if member.Name == local.Name {
			continue
		}
		if err := m.list.SendReliable(member, data); err != nil {
			slog.Warn("cluster reliable send failed", "to", member.Name, "err", err)
		}
	}
}

// OnStateReceived merges an incoming payload into peerStates using Lamport
// ordering (higher seq wins; equal seq uses lexicographic NodeName tie-break).
//
// After a successful merge, if this node is the consistent-hash primary for
// the target AND the new state is hard_down, it calls the registered
// PeerAlertHandler so the engine can dispatch an alert for targets that this
// node does not probe locally. This is the "primary-forwards-peer-alert" path
// that ensures exactly-once alerting works even when different nodes have
// different target configs.
func (m *Manager) OnStateReceived(p GossipPayload) {
	// soft_down is a transient suspect signal, not a Lamport-versioned state.
	// Never stored in peerStates. If we are a co-prober for this target we
	// trigger an immediate verification probe so detection latency stays low
	// even if the originating prober crashes before reaching hard_down.
	if p.State == "soft_down" {
		if p.NodeName != m.cfg.NodeName && m.IsLocalProber(p.TargetID) {
			m.mu.Lock()
			n := m.softDownNotifier
			m.mu.Unlock()
			if n != nil {
				go n.NotifyCoProberSoftDown(p.TargetID)
			}
		}
		return
	}

	m.mu.Lock()

	if m.peerStates[p.NodeName] == nil {
		m.peerStates[p.NodeName] = make(map[string]GossipPayload)
	}
	existing, ok := m.peerStates[p.NodeName][p.TargetID]
	accepted := !ok || p.Seq > existing.Seq ||
		(p.Seq == existing.Seq && p.NodeName > existing.NodeName)

	// A brand-new (node, target) combination grows the candidate set for that
	// target — Phase 13 schedules a recompute so probe loops can rebalance.
	// State value transitions on an existing entry do NOT trigger recompute
	// (they would defeat the debounce on busy clusters).
	isNewEntry := !ok && accepted

	if accepted {
		m.peerStates[p.NodeName][p.TargetID] = p
		slog.Debug("cluster state accepted",
			"from", p.NodeName, "target", p.TargetID,
			"state", p.State, "seq", p.Seq)
	}

	// Peer-alert path: fire only when all three conditions hold:
	//   1. This node is the primary responsible for alerting on this target.
	//   2. The payload carries a new hard_down with a higher seq than the last
	//      peer-alert we dispatched (prevents duplicates from UDP + TCP paths).
	//   3. Quorum is healthy (isolated nodes must not alert).
	shouldDispatch := accepted &&
		p.State == "hard_down" &&
		!m.isolated.Load() &&
		m.IsResponsible(p.TargetID) &&
		p.Seq > m.peerAlerted[p.TargetID]

	handler := m.peerAlertHandler
	if shouldDispatch {
		m.peerAlerted[p.TargetID] = p.Seq
	}

	m.mu.Unlock()

	if shouldDispatch && handler != nil && !handler.HasLocalProbe(p.TargetID) {
		slog.Debug("cluster: peer-alert dispatch (no local probe)",
			"target", p.TargetID, "from", p.NodeName, "seq", p.Seq)
		go handler.DispatchPeerAlert(p)
	}

	// Schedule recompute outside the lock — debounced, so a burst of new
	// gossip entries during anti-entropy only triggers one recompute pass.
	if isNewEntry {
		m.scheduleRecompute()
	}
}

// Members returns a snapshot of all currently known cluster members.
// Zone is parsed from each member's NodeMeta payload; missing or malformed
// metadata yields an empty Zone string.
//
// Returns an empty slice when the manager was constructed without a real
// memberlist (test path) — callers can safely range over the result.
func (m *Manager) Members() []MemberInfo {
	if m.list == nil {
		// Test path: fall back to the alive override if set, otherwise self.
		alive := m.aliveSet()
		out := make([]MemberInfo, 0, len(alive))
		for name := range alive {
			zone, region := "", ""
			if name == m.cfg.NodeName {
				zone = m.cfg.Zone
				region = m.cfg.Region
			} else if m.testZoneOverride != nil {
				zone = m.testZoneOverride[name]
			}
			if m.testRegionOverride != nil {
				if r, ok := m.testRegionOverride[name]; ok {
					region = r
				}
			}
			out = append(out, MemberInfo{
				Name:   name,
				Status: "alive",
				Self:   name == m.cfg.NodeName,
				Zone:   zone,
				Region: region,
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	local := m.list.LocalNode()
	members := m.list.Members()
	out := make([]MemberInfo, 0, len(members))
	for _, mem := range members {
		var zone, region, httpPort string
		if len(mem.Meta) > 0 {
			var meta nodeMeta
			if json.Unmarshal(mem.Meta, &meta) == nil {
				zone = meta.Zone
				region = meta.Region
				httpPort = meta.HTTPPort
			}
		}
		if mem.Name == local.Name {
			if zone == "" {
				zone = m.cfg.Zone
			}
			if region == "" {
				region = m.cfg.Region
			}
			if httpPort == "" {
				httpPort = m.cfg.HTTPPort
			}
		}
		out = append(out, MemberInfo{
			Name:     mem.Name,
			Addr:     mem.Addr.String(),
			Port:     mem.Port,
			Status:   nodeStateStr(mem.State),
			Self:     mem.Name == local.Name,
			Zone:     zone,
			Region:   region,
			HTTPPort: httpPort,
		})
	}
	// Sort deterministically by name. memberlist returns members in an
	// internal map-iteration order that changes on every gossip round; the
	// UI re-renders look like flicker because each poll arrives with a
	// different ordering. Sorting here is the single source of truth so
	// frontends don't have to re-sort.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Snapshot returns a full view of cluster state for the /cluster/state endpoint.
func (m *Manager) Snapshot() ClusterStateSnapshot {
	m.mu.RLock()
	// deep copy peerStates so the caller holds an immutable snapshot
	ps := make(map[string]map[string]GossipPayload, len(m.peerStates))
	for node, targets := range m.peerStates {
		tc := make(map[string]GossipPayload, len(targets))
		for tid, p := range targets {
			tc[tid] = p
		}
		ps[node] = tc
	}
	m.mu.RUnlock()

	return ClusterStateSnapshot{
		LocalNode:  m.cfg.NodeName,
		Members:    m.Members(),
		PeerStates: ps,
	}
}

// AllPeerStates returns a flat slice of every GossipPayload currently stored
// across all peers. Used by the engine to build a cluster-wide state view for
// root-cause detection without exposing the two-level peerStates map.
//
// The returned slice is a defensive copy — callers may modify it freely.
func (m *Manager) AllPeerStates() []GossipPayload {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []GossipPayload
	for _, targets := range m.peerStates {
		for _, p := range targets {
			out = append(out, p)
		}
	}
	return out
}

// NodeName returns the name this node is known by in the cluster.
func (m *Manager) NodeName() string { return m.cfg.NodeName }

// LocalAddr returns this node's advertised address as "host:port", suitable
// for inclusion in a `netwatch join --addr <addr>` command shown to operators.
// Returns an empty string when memberlist has not yet started.
func (m *Manager) LocalAddr() string {
	if m == nil || m.list == nil {
		return ""
	}
	local := m.list.LocalNode()
	if local == nil {
		return ""
	}
	return net.JoinHostPort(local.Addr.String(), fmt.Sprintf("%d", local.Port))
}

// PrimaryKey returns the base64-encoded primary AES key (the key used for
// outgoing encryption). Empty when the cluster runs without encryption.
//
// Suitable for inclusion in the operator-facing join command. Treat as a secret.
func (m *Manager) PrimaryKey() string {
	if m == nil || len(m.cfg.Keyring) == 0 {
		return ""
	}
	return m.cfg.Keyring[0]
}

// ── Hash ring — responsible-node selection ────────────────────────────────────

// updateRing rebuilds the sorted alive-member list from the current memberlist
// state. Called by eventDelegate on every Join/Leave/Update and once in New().
// Holds ringMu for the full function body so that the m.list nil check and the
// ring update are atomic with respect to New()'s list assignment.
func (m *Manager) updateRing() {
	m.ringMu.Lock()
	defer m.ringMu.Unlock()
	if m.list == nil {
		return
	}
	members := m.list.Members()
	names := make([]string, 0, len(members))
	for _, mem := range members {
		if mem.State == memberlist.StateAlive {
			names = append(names, mem.Name)
		}
	}
	sort.Strings(names)
	m.ring = names
}

// hashTarget returns a stable FNV-32a hash of targetID.
func hashTarget(targetID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(targetID))
	return h.Sum32()
}

// GetResponsibleNode returns the primary and secondary node names responsible
// for alerting on targetID.
//
// The ring is sorted lexicographically so every node computes the same result.
// If the ring has fewer than 2 members the secondary is returned as "".
func (m *Manager) GetResponsibleNode(targetID string) (primary, secondary string) {
	m.ringMu.RLock()
	ring := make([]string, len(m.ring))
	copy(ring, m.ring)
	m.ringMu.RUnlock()

	n := len(ring)
	if n == 0 {
		return "", ""
	}
	idx := int(hashTarget(targetID)) % n
	primary = ring[idx]
	if n < 2 {
		return primary, ""
	}
	secondary = ring[(idx+1)%n]
	return primary, secondary
}

// IsResponsible returns true when this node is the primary responsible node
// for targetID. Only the primary sends alerts. When the primary leaves, the
// ring is updated via NotifyLeave → updateRing(), and the next node becomes
// the new primary — natural failover without dual-sending.
func (m *Manager) IsResponsible(targetID string) bool {
	primary, _ := m.GetResponsibleNode(targetID)
	return m.cfg.NodeName == primary
}

// zoneOf returns the zone label declared by nodeName, or "" when no zone
// is set (or the node is unknown).
//
// Lookup is O(N) over current memberlist members; callers that need many
// lookups in the same operation should cache the result. Memberlist already
// distributes NodeMeta automatically so no extra gossip traffic is involved.
func (m *Manager) zoneOf(nodeName string) string {
	// Test override takes precedence so unit tests can drive zone-aware logic
	// without a running memberlist.
	if m.testZoneOverride != nil {
		if z, ok := m.testZoneOverride[nodeName]; ok {
			return z
		}
	}
	if m.list == nil {
		// Local-only fast path used by tests / standalone construction.
		if nodeName == m.cfg.NodeName {
			return m.cfg.Zone
		}
		return ""
	}
	// Fast path: querying ourselves — config is authoritative even before
	// the first NodeMeta cycle has completed.
	if nodeName == m.cfg.NodeName {
		return m.cfg.Zone
	}
	for _, mem := range m.list.Members() {
		if mem.Name != nodeName {
			continue
		}
		if len(mem.Meta) == 0 {
			return ""
		}
		var meta nodeMeta
		if err := json.Unmarshal(mem.Meta, &meta); err != nil {
			return ""
		}
		return meta.Zone
	}
	return ""
}

// ZoneOf is the exported view of zoneOf. Returns "" for unknown nodes or
// nodes that have not declared a zone.
func (m *Manager) ZoneOf(nodeName string) string {
	return m.zoneOf(nodeName)
}

// PeerStatesForTarget returns the most recent GossipPayload from every peer
// that has reported a state for targetID. Does NOT include this node's own
// state (the engine holds that in lastKnown).
func (m *Manager) PeerStatesForTarget(targetID string) []GossipPayload {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []GossipPayload
	for _, targets := range m.peerStates {
		if p, ok := targets[targetID]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Leave performs a graceful shutdown, announcing the departure to peers.
// Call this before process exit so other nodes update their membership tables.
func (m *Manager) Leave(timeout time.Duration) error {
	// Stop the quorum goroutine before shutting down memberlist so it does
	// not fire on the transitional member count during Leave().
	if m.stopQuorum != nil {
		m.stopQuorum()
	}
	// Stop the config-sync goroutine (P1.5).
	if m.stopConfigSync != nil {
		m.stopConfigSync()
	}
	// Stop the background re-join goroutine.
	if m.stopRejoin != nil {
		m.stopRejoin()
	}
	// Cancel any pending recompute so listener callbacks are not invoked
	// against an engine that is also shutting down.
	m.recomputeMu.Lock()
	if m.recomputeTimer != nil {
		m.recomputeTimer.Stop()
		m.recomputeTimer = nil
	}
	m.recomputeMu.Unlock()
	if m.list == nil {
		return nil
	}
	slog.Info("cluster leaving", "node", m.cfg.NodeName)
	if err := m.list.Leave(timeout); err != nil {
		return fmt.Errorf("cluster leave: %w", err)
	}
	return m.list.Shutdown()
}

// SetStateProvider registers an AntiEntropyProvider on the manager.
// Must be called before the first push-pull cycle occurs (i.e., in Engine.Init
// immediately after cluster.New returns). It is safe to call even when the
// manager was not created via New() (e.g. in tests with NewTestManager).
func (m *Manager) SetStateProvider(p AntiEntropyProvider) {
	m.mu.Lock()
	m.stateProvider = p
	m.mu.Unlock()
}

// SetPeerAlertHandler registers the engine callback used to dispatch alerts
// for targets this node does not probe locally. See PeerAlertHandler.
// Must be called in Engine.Init after cluster.New returns, before gossip
// messages can arrive.
func (m *Manager) SetPeerAlertHandler(h PeerAlertHandler) {
	m.mu.Lock()
	m.peerAlertHandler = h
	m.mu.Unlock()
}

// SetSoftDownNotifier registers the engine callback invoked when this node
// receives a soft_down suspect signal from a co-prober. Must be called in
// Engine.Init after cluster.New, before gossip messages can arrive.
func (m *Manager) SetSoftDownNotifier(n SoftDownNotifier) {
	m.mu.Lock()
	m.softDownNotifier = n
	m.mu.Unlock()
}

// SetInventoryRefreshHandler registers the engine callback invoked on each
// NotifyJoin event so the engine re-broadcasts its target states to
// late-joining peers. Must be called in Engine.Init after cluster.New.
func (m *Manager) SetInventoryRefreshHandler(h InventoryRefreshHandler) {
	m.mu.Lock()
	m.inventoryRefreshHandler = h
	m.mu.Unlock()
}

// ── Quorum ────────────────────────────────────────────────────────────────────

// AliveCount returns the number of cluster members currently in StateAlive.
// Returns 0 in test contexts where the memberlist has not been initialised
// (callers should treat 0 as "no cluster" rather than "quorum lost").
func (m *Manager) AliveCount() int {
	if m.list == nil {
		// Test path — derive from the optional aliveSet override; useful so
		// quorum checks in tests can simulate a populated cluster.
		return len(m.aliveSet())
	}
	count := 0
	for _, node := range m.list.Members() {
		if node.State == memberlist.StateAlive {
			count++
		}
	}
	return count
}

// checkQuorum returns true when the number of alive members meets the minimum
// required by the configured quorum formula:
//
//	needed = floor(ExpectedNodeCount * MinQuorumRatio) + 1
//
// When ExpectedNodeCount is 0 (not configured) quorum is always considered
// healthy — this preserves standalone and two-node setups.
func (m *Manager) checkQuorum() bool {
	if m.cfg.ExpectedNodeCount <= 0 {
		return true
	}
	ratio := m.cfg.MinQuorumRatio
	if ratio <= 0 {
		ratio = 0.5
	}
	needed := int(math.Floor(float64(m.cfg.ExpectedNodeCount)*ratio)) + 1
	return m.AliveCount() >= needed
}

// startQuorumLoop creates a background goroutine that monitors quorum every 5s.
// The goroutine exits when the returned context is cancelled (on Leave).
func (m *Manager) startQuorumLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	m.stopQuorum = cancel
	go m.runQuorumLoop(ctx)
}

// startConfigSyncLoop starts the P1.5 config-sync goroutine.
// No-op when config_sync is disabled.
func (m *Manager) startConfigSyncLoop() {
	if m.cfg.ConfigSync == nil || !m.cfg.ConfigSync.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.stopConfigSync = cancel
	go m.runConfigSyncLoop(ctx)
}

// runQuorumLoop is the quorum-monitoring goroutine.
// It logs transitions and flips the isolated flag accordingly.
func (m *Manager) runQuorumLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Seed initial state without logging — avoids false "quorum lost" on startup
	// when not all peers have joined yet.
	wasHealthy := m.checkQuorum()
	m.isolated.Store(!wasHealthy)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			healthy := m.checkQuorum()
			switch {
			case !healthy && wasHealthy:
				ratio := m.cfg.MinQuorumRatio
				if ratio <= 0 {
					ratio = 0.5
				}
				needed := int(math.Floor(float64(m.cfg.ExpectedNodeCount)*ratio)) + 1
				slog.Warn("[CLUSTER] quorum lost",
					"active", m.AliveCount(),
					"needed", needed,
					"expected_total", m.cfg.ExpectedNodeCount,
				)
				m.isolated.Store(true)
			case healthy && !wasHealthy:
				slog.Info("[CLUSTER] quorum recovered",
					"active", m.AliveCount(),
					"expected_total", m.cfg.ExpectedNodeCount,
				)
				m.isolated.Store(false)
				m.startAntiEntropy()
			}
			wasHealthy = healthy
		}
	}
}

// IsolatedMode returns true when this node has lost cluster quorum and should
// suppress alert sending. Phase 8 gates alarm dispatch on this flag.
func (m *Manager) IsolatedMode() bool {
	return m.isolated.Load()
}

// QuorumHealthy returns true when the cluster currently satisfies its quorum
// requirement. Exported alias for the internal checkQuorum() so callers outside
// this package (e.g. engine.FleetSnapshot) can read quorum state without
// accessing cluster internals.
func (m *Manager) QuorumHealthy() bool {
	return m.checkQuorum()
}

// ReplicationFactor returns the current effective prober count. With a fixed
// probe_replication_factor this is that number; with probe_replication_percent
// it is derived from the live cluster size (a representative value for display
// and metrics — per-target selection uses each target's own candidate count).
func (m *Manager) ReplicationFactor() int {
	return m.cfg.effectiveReplicationFactor(m.AliveCount())
}

// MinProbeConfirmations returns the configured min_probe_confirmations (default 0 = 1).
// A value > 1 means shouldAlert requires that many probers to agree on hard_down
// before dispatching a notification — useful to avoid false alerts from a single
// node with a flaky network path.
func (m *Manager) MinProbeConfirmations() int {
	return m.cfg.MinProbeConfirmations
}

// startAntiEntropy is called when quorum recovers.
// The actual state reconciliation happens automatically via memberlist's
// push-pull mechanism: the next push-pull cycle with join=true will call
// LocalState → FullState and MergeRemoteState → ApplyRemoteState on all
// peers, driven by the registered AntiEntropyProvider (Phase 9).
func (m *Manager) startAntiEntropy() {
	slog.Info("[CLUSTER] anti-entropy triggered — push-pull sync will reconcile state")
}

// ── helpers ───────────────────────────────────────────────────────────────────

// ── Hot-reload helpers ───────────────────────────────────────────────────────

// UpdateNodeMeta refreshes this node's zone/region labels at runtime and pushes
// the new NodeMeta to all peers via memberlist. Call after Engine.Reload when
// cluster.zone or cluster.region changed in the config — otherwise peers keep
// the stale labels they learned at startup, breaking zone-aware prober
// selection and geo-latency grouping.
//
// Returns nil when the cluster is not running (m.list == nil) so callers can
// invoke it unconditionally.
func (m *Manager) UpdateNodeMeta(zone, region string) error {
	if m.list == nil {
		return nil
	}
	m.mu.Lock()
	prevZone := m.cfg.Zone
	prevRegion := m.cfg.Region
	if prevZone == zone && prevRegion == region {
		m.mu.Unlock()
		return nil
	}
	m.cfg.Zone = zone
	m.cfg.Region = region
	m.mu.Unlock()

	// memberlist.UpdateNode forces a NodeMeta refresh — the delegate's
	// NodeMeta(limit) callback is invoked again and the new bytes are
	// broadcast to peers as a NodeMeta update.
	if err := m.list.UpdateNode(1 * time.Second); err != nil {
		slog.Warn("cluster: UpdateNodeMeta propagation failed", "err", err)
		return err
	}
	slog.Info("[CLUSTER] node meta refreshed",
		"zone_old", prevZone, "zone_new", zone,
		"region_old", prevRegion, "region_new", region)
	// Trigger a local recompute so this node's own zone/region picks update
	// immediately — peers will recompute on their own once they receive the
	// NodeMeta update (handled by NotifyUpdate → scheduleRecompute).
	m.scheduleRecompute()
	return nil
}

// OrphanedLocalTargets returns the local target IDs that have no eligible
// prober — typically because `probe_from` or `probe_from_regions` filtered
// the candidate set to empty, or every candidate is currently dead.
//
// Returns nil in standalone mode or when the engine has not registered a
// LocalTargetProvider. Operators consult this via the network_probe_target_orphaned
// metric and the periodic edge-triggered log message.
func (m *Manager) OrphanedLocalTargets() []string {
	m.mu.RLock()
	provider := m.localTargetProvider
	m.mu.RUnlock()
	if provider == nil {
		return nil
	}
	var orphans []string
	for _, id := range provider.LocalTargets() {
		if len(m.SelectProbers(id)) == 0 {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	return orphans
}

// ── Keyring rotation ─────────────────────────────────────────────────────────

// KeyringInfo describes the current state of the live AES keyring.
type KeyringInfo struct {
	// KeyCount is the total number of keys on the ring (including old ones not yet removed).
	KeyCount int `json:"key_count"`
	// PrimaryHex is the first 8 hex chars of the current encryption key (for display only).
	PrimaryHex string `json:"primary_key_prefix,omitempty"`
	// KeyPrefixes lists the first 8 hex chars of every key on the ring.
	KeyPrefixes []string `json:"key_prefixes,omitempty"`
}

// KeyringAddKey installs a new AES key on the gossip ring so that all nodes
// can decrypt messages encrypted with it. The key must be 16, 24, or 32 bytes.
// Call this on every node before promoting the key to primary with KeyringUseKey.
func (m *Manager) KeyringAddKey(key []byte) error {
	if m.keyring == nil {
		return fmt.Errorf("cluster: no keyring configured (cluster.keyring is empty)")
	}
	return m.keyring.AddKey(key)
}

// KeyringUseKey promotes key to primary — subsequent gossip messages are
// encrypted with it. All cluster nodes must already have the key installed
// (via KeyringAddKey) before this is called, otherwise they cannot decrypt.
func (m *Manager) KeyringUseKey(key []byte) error {
	if m.keyring == nil {
		return fmt.Errorf("cluster: no keyring configured")
	}
	return m.keyring.UseKey(key)
}

// KeyringRemoveKey drops key from the ring. Returns an error if the key is
// currently the primary — demote it first with KeyringUseKey(newPrimary).
func (m *Manager) KeyringRemoveKey(key []byte) error {
	if m.keyring == nil {
		return fmt.Errorf("cluster: no keyring configured")
	}
	return m.keyring.RemoveKey(key)
}

// KeyringInfo returns a display-safe summary of the live keyring state.
func (m *Manager) KeyringInfo() KeyringInfo {
	if m.keyring == nil {
		return KeyringInfo{}
	}
	keys := m.keyring.GetKeys()
	primary := m.keyring.GetPrimaryKey()
	info := KeyringInfo{KeyCount: len(keys)}
	if len(primary) > 0 {
		info.PrimaryHex = fmt.Sprintf("%x", primary)[:8]
	}
	for _, k := range keys {
		if len(k) > 0 {
			info.KeyPrefixes = append(info.KeyPrefixes, fmt.Sprintf("%x", k)[:8]+"...")
		}
	}
	return info
}

// nodeStateStr converts a memberlist node state enum into a human-readable
// string ("alive"/"suspect"/"dead"/"left") for snapshots and the API.
func nodeStateStr(s memberlist.NodeStateType) string {
	switch s {
	case memberlist.StateAlive:
		return "alive"
	case memberlist.StateSuspect:
		return "suspect"
	case memberlist.StateDead:
		return "dead"
	case memberlist.StateLeft:
		return "left"
	default:
		return "unknown"
	}
}
