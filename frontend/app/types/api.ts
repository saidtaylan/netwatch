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

// ── Auth (B28) ──────────────────────────────────────────────────────────────

export interface AuthStatusResponse {
  setup_completed: boolean
  user_count: number
}

export interface AuthSetupRequest {
  setup_token: string
  username: string
  password: string
  display_name?: string
  node_urls?: string[]
}

export interface AuthLoginResponse {
  token: string
  user: UserPublic
  cluster_nodes?: string[]
}

export interface UserPublic {
  id: string
  username: string
  role: 'admin' | 'operator' | 'viewer'
  display_name?: string
  created_at: string
  created_by?: string
  last_login_at?: string
  disabled?: boolean
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
  name:    string
  addr:    string
  port?:   number
  status:  string
  self?:   boolean
  zone?:   string
  region?: string
}

export interface ClusterState {
  local_node:  string
  members:     ClusterMember[]
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
  latency?:    number  // last measured round-trip in seconds; 0/absent = not measured
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
// Actual backend: slo.go SLOSnapshot → targets is Record<id, SLOResult>

export interface SLOSnapshot {
  computed_at: string
  targets:     Record<string, SLOTargetResult>  // keyed by target_id
}

export interface SLOTargetResult {
  target_id:            string
  target_uptime:        number
  actual_uptime:        number
  window:               string
  window_duration_sec:  number
  downtime_sec:         number
  downtime_minutes:     number
  incident_count:       number
  longest_incident_sec?: number
  slo_breached:         boolean
  remaining_budget_sec: number  // negative = breached
  incidents?:           SLOIncident[]
}

export interface SLOIncident {
  started_at:  string
  ended_at?:   string | null
  duration_sec?: number
}

// ── /slo/targets (B12 — CRUD API, planned) ───────────────────────────────────

export interface SLOTargetConfig {
  id:            string   // must match a target id in config.yaml
  target_uptime: number   // 0.0–1.0
  window:        string   // "24h" | "7d" | "30d"
}

// ── /cluster/config ──────────────────────────────────────────────────────────
// Actual backend: cluster/configsync.go ConfigSyncSnapshot

export interface ConfigSyncSnapshot {
  self:        ConfigNodeInfo       // this node's config info (ConfigBroadcast)
  peers:       PeerConfigInfo[]     // peer nodes' config info (with in_sync flag)
  drift_count: number               // peers with a KNOWN different hash; excludes peers with no hash yet
}

export interface ConfigNodeInfo {
  msg_type?:    string
  node_name:    string
  config_hash:  string               // first 16 hex chars of SHA-256
  config_size:  number
  loaded_at:    string               // ISO timestamp
}

export interface PeerConfigInfo {
  node_name:   string
  config_hash: string                // empty when peer hasn't broadcast yet
  in_sync:     boolean               // backend-authoritative: true when same OR not-yet-known
  loaded_at:   string
}

// Computed helpers (derived in component, not from API)
// in_sync = drift_count === 0
// local_hash = self.config_hash

// ── /cluster/probers ─────────────────────────────────────────────────────────

export interface ProberAssignmentSnapshot {
  local_node?:         string
  replication_factor?: number
  // The backend keys per-target assignments under `assignments` (not `targets`).
  assignments: Record<string, TargetProbers>
}

export interface TargetProbers {
  target_id:   string
  probers:     string[]
  primary?:    string
  probe_from?: string[]
  candidates?: string[]
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
  target_id:   string
  computed_at: string                 // ISO timestamp
  anomaly:     boolean                // true when ANY node > 3× min non-zero
  by_node:     GeoNodeLatency[]
}

export interface GeoNodeLatency {
  node_name:        string
  region?:          string
  latency_seconds:  number   // 0 means not yet measured / not applicable
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
    probe_replication_percent?: number
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
