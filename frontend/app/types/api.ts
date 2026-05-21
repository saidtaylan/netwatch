// Backend API response types — keep in sync with backend Go structs

// ── Core ────────────────────────────────────────────────────────────────────

export type TargetState = 'up' | 'hard_down' | 'soft_down' | 'soft_up' | 'unknown'
export type TargetType  = 'tcp' | 'http' | 'ping' | 'dns' | 'sql' | 'grpc' | 'synthetic'
export type Scope       = 'GLOBAL' | 'PARTIAL' | 'NODE_LOCAL' | 'STANDALONE'
export type Classification = 'REAL_OUTAGE' | 'NETWORK_PARTITION' | 'LOCAL_FAILURE' | 'AMBIGUOUS'
export type Severity    = 'critical' | 'warning' | 'info'   // B2 — backend not yet

// ── /health ─────────────────────────────────────────────────────────────────

export interface HealthResponse {
  status: 'ok'
}

// ── /version ────────────────────────────────────────────────────────────────

export interface VersionResponse {
  version:    string
  build_time: string
}

// ── /auth/whoami ─────────────────────────────────────────────────────────────

export interface WhoAmIResponse {
  role: 'admin' | 'anonymous'
}

// ── /status ─────────────────────────────────────────────────────────────────

export interface StatusEntry {
  name:       string
  target:     string
  type:       TargetType
  status:     TargetState
  seq:        number
  error_code: string
}

// ── /cluster/state ──────────────────────────────────────────────────────────

export interface ClusterMember {
  name:   string
  addr:   string
  status: string
  zone?:  string
  region?: string
}

export interface ClusterState {
  members:    ClusterMember[]
  peer_states: Record<string, Record<string, PeerTargetState>>
}

export interface PeerTargetState {
  state:      TargetState
  seq:        number
  error_code: string
  owner_node: string
  latency?:   number
}

// ── /fleet/status ────────────────────────────────────────────────────────────

// Actual backend response shape (fleet.go FleetSnapshot)
export interface FleetSnapshot {
  cluster?:   FleetClusterInfo       // nil in standalone mode
  summary:    TargetCounts           // rollup counts
  targets:    FleetTarget[]          // array (not Record)
  incidents?: FleetIncident[]        // active outages
}

export interface FleetClusterInfo {
  local_node:         string
  members:            string[]       // member names
  size:               number
  alive_count:        number
  quorum_healthy:     boolean
  isolated:           boolean
  replication_factor: number
}

export interface FleetIncident {
  target_id:    string
  target_name:  string
  scope:        Scope
  seq:          number
  error_code?:  string
  root_cause?:  string
}

export interface TargetCounts {
  total?:    number
  up:        number
  hard_down: number
  soft_down: number
  soft_up?:  number
  unknown:   number
}

export interface FleetTarget {
  id:              string           // target.key() — matches config id/name
  name:            string
  target:          string           // probe address
  type:            TargetType
  consensus_state: TargetState
  scope?:          Scope
  classification?: Classification
  confidence?:     number           // 0.0–1.0
  affected_apps?:  string[]
  owner_teams?:    string[]
  root_cause?:     string           // target id of root cause
  cascading_impact?: string[]       // transitive dependents
  down_since?:     string           // ISO timestamp of first hard_down
  by_node?:        Record<string, FleetNodeView>
  severity?:       Severity         // B2 — backend not yet
}

// Per-node view inside FleetTarget.by_node
export interface FleetNodeView {
  state:       TargetState
  seq:         number
  error_code?: string
}

// ── /topology ────────────────────────────────────────────────────────────────

export interface TopologySnapshot {
  targets: Record<string, TopologyNode>
}

export interface TopologyNode {
  id:                string
  name:              string
  depends_on:        string[]
  reverse_deps:      string[]
  cascading_impact:  string[]
  depth?:            number
}

// ── /slo ─────────────────────────────────────────────────────────────────────

export interface SLOSnapshot {
  targets: SLOTargetResult[]
}

export interface SLOTargetResult {
  id:                    string
  name?:                 string
  target_uptime:         number
  actual_uptime:         number
  window:                string
  error_budget_seconds:  number
  breached:              boolean
  incidents:             SLOIncident[]
}

export interface SLOIncident {
  started_at:  string
  ended_at?:   string
  duration_sec?: number
}

// ── /cluster/config ──────────────────────────────────────────────────────────

export interface ConfigSyncSnapshot {
  local_hash:   string
  local_size:   number
  loaded_at:    string
  peers:        Record<string, PeerConfigInfo>
  in_sync:      boolean
  drift_count:  number
}

export interface PeerConfigInfo {
  hash:      string
  size:      number
  loaded_at: string
}

// ── /cluster/probers ─────────────────────────────────────────────────────────

export interface ProberAssignmentSnapshot {
  targets: Record<string, TargetProbers>
}

export interface TargetProbers {
  target_id:   string
  probers:     string[]
  primary?:    string
  probe_from?: string[]
  candidates:  string[]
}

// ── /cluster/maintenance ─────────────────────────────────────────────────────

export interface MaintenanceWindow {
  id:         string
  target_id:  string
  reason:     string
  started_at: string
  ends_at:    string
  created_by: string
}

// ── /geo/latency/{targetID} ──────────────────────────────────────────────────

export interface GeoLatencySnapshot {
  target_id:  string
  anomaly:    boolean
  by_node:    GeoNodeLatency[]
}

export interface GeoNodeLatency {
  node:    string
  region?: string
  zone?:   string
  latency: number   // seconds
  anomaly: boolean
}

// ── /cluster/keyring/rotate ──────────────────────────────────────────────────

export interface KeyringStatus {
  key_count:      number
  primary_prefix: string
}

// ── SharedConfig (for PUT /cluster/config) ───────────────────────────────────

export interface SharedConfig {
  timeout?:               number
  max_retries?:           number
  retry_interval_sec?:    number
  ticker_interval_sec?:   number
  probe_interval_sec?:    number
  reload_interval_sec?:   number
  watchdog_threshold_sec?: number
  recovery_probes?:       number
  default_notify?:        string[]
  cluster?: {
    peers?:                   string[]
    expected_node_count?:     number
    min_quorum_ratio?:        number
    probe_replication_factor?: number
    min_probe_confirmations?: number
  }
}

// ── /cluster/config (PUT) result ─────────────────────────────────────────────

export interface ConfigPushResult {
  applied_locally: boolean
  broadcast_to:    string[]
  failed_nodes:    Record<string, string>
  fields_applied:  string[]
  pushed_at:       string
}

// ── Alerts (in-memory, B7 sonrası /alerts) ───────────────────────────────────

export interface AlertEntry {
  id:              string
  target_id:       string
  target_name:     string
  target_type:     TargetType
  status:          'unreachable' | 'reachable'
  scope:           Scope
  classification:  Classification
  confidence:      number
  seq:             number
  error_code?:     string
  affected_apps?:  string[]
  timestamp:       string
  // UI-only fields
  acked?: boolean
  muted?: boolean
}
