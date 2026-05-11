# netwatch — Yeni Özellik Roadmap'i

Bu dosya cluster mesh'in alarm dışında sunabileceği değerleri detaylandırır. Ürünün asıl farklılaşma noktaları burada — standalone mod sıradan, cluster mod magic.

**Pazarlama konumu:** "The first masterless, consensus-based network monitoring agent."

**Öncelik sırası:** P0 = ürün için hayati, P1 = güçlü farklılaşma, P2 = nice-to-have.

---

## 1. Dependency Graph + Root Cause Detection [P0]

### Neden hayati
Şu an alarm mesajı şunu söylüyor: "db-primary unreachable". Operatöre fayda yok — neden down? Bağlı servisler de etkileniyor mu? Bu bilgi yok.

Dependency graph + cluster consensus birleşince netwatch şunu söyleyebilir:
> "payment-service unreachable. Root cause: db-primary down (confirmed by 2/3 nodes). Cascading impact: 3 apps affected."

Bu Zabbix'te bile saatlerce config gerektiren bir şey. netwatch'ta YAML'da `depends_on:` tanımlamak yeter.

### Config genişlemesi

```yaml
targets:
  - id: "payment-service"
    type: "http"
    target: "https://payments.local/health"
    depends_on:
      - "db-primary"        # target id
      - "redis-cache"
      - "auth-service"

  - id: "db-primary"
    type: "tcp"
    target: "10.0.0.5:5432"
    depends_on: []          # kök bağımlılık

apps:
  - name: "checkout"
    uses: ["payment-service", "inventory-api"]
    critical_path: ["payment-service"]    # bu down olursa app down
```

### Implementation outline

**Yeni dosya: `internal/engine/topology.go`**

```go
type DependencyGraph struct {
    nodes map[string]*Target            // targetID → Target
    edges map[string][]string           // targetID → bağımlı olduğu targetID'ler
    reverse map[string][]string         // targetID → ona bağımlı olanlar
}

func buildDependencyGraph(cfg Config) *DependencyGraph
func (g *DependencyGraph) FindRootCause(failedID string, allStates map[string]PersistedState) []string
func (g *DependencyGraph) CascadingImpact(failedID string, allStates map[string]PersistedState) []string
```

**Algoritma:**
- Bir target down olduğunda → `depends_on` listesini yürü
- Her bağımlılık için cluster-wide state'e bak (peer states + local)
- En derinde olan down target'ı = root cause
- Reverse edge'lerle "etkilenen target'lar" listesi çıkar

### Alarm mesajı zenginleşmesi

`Alert` struct'ına ekle:
```go
RootCause       string   // örn: "db-primary"
CascadingImpact []string // örn: ["payment-service", "inventory-api", "checkout-app"]
DependencyDepth int      // bu target zincirin neresinde
```

Env değişkenleri (script + mail + webhook hepsi alır):
- `ROOT_CAUSE=db-primary`
- `CASCADING_IMPACT=payment-service,inventory-api`
- `DEPENDENCY_DEPTH=2`

Webhook payload'a (Alertmanager format):
```json
"annotations": {
  "root_cause": "db-primary",
  "cascading_impact": "payment-service,inventory-api,checkout-app",
  "summary": "payment-service unreachable. Root cause: db-primary down."
}
```

### Cluster vs Standalone
- **Standalone:** Sadece local state'e bakar, root cause hesaplar. Çalışır ama "confirmed by N nodes" diyemez.
- **Cluster:** Peer state'leri ile birleşir. "Root cause confirmed by 2/3 nodes" demek mümkün → `ROOT_CAUSE_CONFIRMATIONS=2/3` env'i eklenir.

### Yeni endpoint
- `GET /topology` — Dependency graph'ı JSON döner
- `GET /topology/impact/{targetID}` — Bu target down olursa neler etkilenir

### Validation
- Cyclic dependency yasak (build sırasında detect)
- Var olmayan target ID'sine `depends_on` referansı hata
- `depends_on` reload'da güncellenmeli

### Kabul Kriteri
1. `db-primary` down → `payment-service` alarmında `ROOT_CAUSE=db-primary`
2. `db-primary` UP, `payment-service` down → `ROOT_CAUSE=payment-service` (kendisi)
3. `GET /topology` graph'ı doğru gösterir
4. Cyclic config validation hatası verir

---

## 2. /fleet/status — Decentralized Fleet Aggregation [P0]

### Neden hayati
Bu özellik netwatch'ı "Prometheus exporter"dan "kendi başına monitoring agent"a çeviriyor. Küçük/orta ekipler Grafana kurmak istemiyor — `curl /fleet/status` ile her şeyi görsünler.

Master-less. Hiçbir node "merkez" değil. Hangi node'a sorarsan sor aynı cevap.

Bu **gerçekten** Zabbix'in yapamadığı bir şey — Zabbix proxy bile merkezi server'a bağımlı.

### Endpoint Tasarımı

`GET /fleet/status`
```json
{
  "cluster": {
    "size": 3,
    "healthy": true,
    "isolated": false,
    "quorum_ratio": 1.0,
    "members": ["node-1", "node-2", "node-3"]
  },
  "summary": {
    "total_targets": 47,
    "up": 42,
    "soft_down": 2,
    "hard_down": 3,
    "unknown": 0
  },
  "targets": {
    "db-primary": {
      "consensus_state": "hard_down",
      "scope": "GLOBAL",
      "last_seen_up": "2026-05-09T12:34:56Z",
      "down_duration_sec": 1247,
      "by_node": {
        "node-1": {"state": "hard_down", "seq": 5, "error": "connection refused"},
        "node-2": {"state": "hard_down", "seq": 5, "error": "connection refused"},
        "node-3": {"state": "hard_down", "seq": 5, "error": "connection refused"}
      },
      "responsible_node": "node-2",
      "alert_sent": true,
      "root_cause": "db-primary",
      "affected_apps": ["payment-service", "checkout"]
    },
    ...
  },
  "incidents": [
    {
      "target": "db-primary",
      "started_at": "2026-05-09T12:34:56Z",
      "scope": "GLOBAL",
      "duration_sec": 1247,
      "alert_dispatched_by": "node-2"
    }
  ]
}
```

`GET /fleet/status?format=text` — terminal-friendly tablo:
```
CLUSTER: 3 nodes, healthy, quorum 1.00
TARGETS: 47 total | 42 UP | 2 SOFT | 3 DOWN

DOWN:
  db-primary       GLOBAL    20m47s   confirmed by 3/3   → payment-service, checkout
  redis-cache      PARTIAL   5m12s    confirmed by 2/3   → session-store
  api-gateway      LOCAL     1m08s    1/3 (node-2)       → (lokal sorun)
```

### Implementation

**Yeni dosya: `internal/engine/fleet.go`**

```go
type FleetSnapshot struct {
    Cluster   ClusterInfo
    Summary   FleetSummary
    Targets   map[string]TargetFleetState
    Incidents []Incident
}

type TargetFleetState struct {
    ConsensusState   string                       // up | soft_down | hard_down
    Scope            string                       // GLOBAL | PARTIAL | NODE_LOCAL
    LastSeenUp       *time.Time
    DownDurationSec  int64
    ByNode           map[string]NodeTargetView
    ResponsibleNode  string
    AlertSent        bool
    RootCause        string                       // (1) ile entegre
    CascadingImpact  []string
    AffectedApps     []string
}

func (e *Engine) FleetSnapshot() FleetSnapshot {
    // Her target için:
    //   - local state (e.lastKnown)
    //   - tüm peer state (cluster.PeerStatesForTarget)
    //   - consensus hesapla (>50% aynı state → consensus)
    //   - scope (zaten var, computeScope)
    //   - dependency root cause
}
```

### Yeni metric
- `network_probe_fleet_consensus_state` — label'lı: up=1, hard_down=0, mismatch=-1
- `network_probe_fleet_consensus_disagreement` — label'lı: kaç node karşı çıkıyor

### Cluster vs Standalone
- **Standalone:** Sadece kendi state'i döner, `cluster.size=1`, scope hep `NODE_LOCAL`. Yine de yararlı — ama esas değer cluster'da.

### Kabul kriteri
1. 3 node cluster'da `curl localhost:10240/fleet/status` ve `curl localhost:10241/fleet/status` aynı sonucu döner
2. Bir node down olduğunda `cluster.healthy=false` ama diğer node'lar fleet view'i sunmaya devam eder
3. Target by-node breakdown'ı doğru gösterir
4. `?format=text` çalışır

---

## 3. Scope Intelligence Enhancement (Network Partition Detection) [P1]

### Neden değerli
Phase 8'de `SCOPE=GLOBAL/PARTIAL/NODE_LOCAL` zaten var. Ama alarm geldiğinde operatör hâlâ "ağ partition'ı mı, gerçek outage mı?" diye düşünüyor. Bu adımda sınıflandırmayı zenginleştir:

- `SCOPE=GLOBAL` + tüm node'lar bağlı + target tek başına down → **REAL_OUTAGE**
- `SCOPE=PARTIAL` + bir grup node target'ı UP, diğer grup DOWN → **NETWORK_PARTITION**
- `SCOPE=NODE_LOCAL` + sadece tek node DOWN, diğerleri UP → **LOCAL_FAILURE**
- `SCOPE=GLOBAL` + bazı node'lar offline → **AMBIGUOUS** (yetersiz veri)

### Implementation outline

**`internal/engine/scope.go`** (yeni veya `engine.go`'ya ekle)

```go
type DetailedScope struct {
    Scope            string  // GLOBAL | PARTIAL | NODE_LOCAL
    Classification   string  // REAL_OUTAGE | NETWORK_PARTITION | LOCAL_FAILURE | AMBIGUOUS
    DownNodes        []string
    UpNodes          []string
    OfflineNodes     []string
    PartitionGroups  [][]string  // sadece PARTIAL durumunda dolu
    Confidence       float64     // 0.0–1.0
}

func (e *Engine) classifyScope(targetID string) DetailedScope {
    peerStates := e.clusterMgr.PeerStatesForTarget(targetID)
    members := e.clusterMgr.Members()
    // ...
}
```

**Network partition detection mantığı:**
- Peer state'lere bak: hangi node'lar UP görüyor, hangileri DOWN
- Eğer DOWN gören node'lar coğrafi/ağ olarak bir kümede → muhtemelen partition
- (İleride: node'larda `region` label'ı eklenebilir, partition detection daha akıllı olur)

### Alarm mesajına yansıma

```
ROOT_CAUSE=db-primary
SCOPE=PARTIAL
CLASSIFICATION=NETWORK_PARTITION
DOWN_NODES=node-1,node-2
UP_NODES=node-3
CONFIDENCE=0.85
```

Mail HTML body'de uyarı kutusu:
> ⚠️ This appears to be a network partition. node-3 still sees the target as UP. The target itself may not be down.

### Kabul kriteri
1. Tüm node'lar down görüyor → `CLASSIFICATION=REAL_OUTAGE`
2. 2 node down, 1 UP görüyor → `NETWORK_PARTITION`
3. 1 node down, 2 UP → `LOCAL_FAILURE`
4. Confidence değeri tutarlı

---

## 4. SLO Tracker [P1]

### Neden değerli
"Bu hizmet bu ay kaç dakika down kaldı?" sorusunun cevabı için ekipler şu an Prometheus + manuel hesaplama yapıyor. netwatch zaten her state geçişini biliyor — sadece persist etsin, hesaplasın.

Bu özellik DBA/SRE'ler için satış noktası: aylık SLO raporu için ek tool gerekmez.

### Veri Modeli

**Yeni dosya: `internal/engine/slo.go`**

`incidents.json` (state.json yanında):
```json
{
  "version": 1,
  "incidents": [
    {
      "target_id": "db-primary",
      "started_at": "2026-05-01T10:23:00Z",
      "ended_at": "2026-05-01T10:45:00Z",
      "duration_sec": 1320,
      "scope": "GLOBAL",
      "alert_dispatched": true,
      "alert_node": "node-2",
      "error_code": "connection refused"
    }
  ]
}
```

State machine `markHardDown` ve `markRecovered` çağrılarına incident logger hook'u eklenir:

```go
func (e *Engine) recordIncidentStart(t Target, errCode string)
func (e *Engine) recordIncidentEnd(t Target)
```

`incidents.json` rotation: aylık otomatik archive (`incidents-2026-04.json` gibi).

### SLO Config

```yaml
slo:
  enabled: true
  retention_days: 90
  targets:
    - id: "db-primary"
      target_uptime: 0.999      # %99.9
      window: "30d"
    - id: "payment-service"
      target_uptime: 0.9995
      window: "7d"
```

### Endpoint

`GET /slo`
```json
{
  "window": "30d",
  "computed_at": "2026-05-10T08:00:00Z",
  "targets": {
    "db-primary": {
      "target_uptime": 0.999,
      "actual_uptime": 0.9987,
      "downtime_sec": 3420,
      "downtime_minutes": 57,
      "incident_count": 3,
      "longest_incident_sec": 1320,
      "slo_breached": true,
      "remaining_error_budget_sec": -120,
      "incidents": [
        {"started_at": "...", "duration_sec": 1320, "scope": "GLOBAL"}
      ]
    }
  }
}
```

`GET /slo?format=text`:
```
TARGET            UPTIME   TARGET   STATUS    BUDGET REMAINING
db-primary        99.87%   99.90%   BREACHED  -2m 0s
payment-service   99.95%   99.95%   OK         0s
api-gateway       99.99%   99.90%   OK         12m 30s
```

### Yeni metric
- `network_probe_slo_uptime_ratio` — label: target, window
- `network_probe_slo_error_budget_seconds` — label: target, window
- `network_probe_slo_breached` — label: target (1 = breached, 0 = healthy)

### Alarm tetikleyici
SLO breach olduğunda alarm gönder:
- Channel: `default_notify` veya `slo_notify`
- Status: `slo_breached`
- Env: `SLO_TARGET_UPTIME=0.999`, `SLO_ACTUAL_UPTIME=0.9987`

### Cluster vs Standalone
- **Standalone:** Sadece kendi gözlemleri (yarısını kaçırabilir).
- **Cluster:** Consensus state'inden hesapla (gerçek downtime, lokal sorun değil). Tek bir node SLO hesaplar (consistent hash → SLO için sorumlu node).

### Kabul kriteri
1. Manuel target down/up tetikle, `incidents.json`'a düzgün yazılsın
2. `/slo` endpoint doğru uptime hesaplar
3. SLO breach olursa alarm tetiklenir
4. 30 günlük rolling window doğru çalışır

---

## 5. Gossip Config Sync [P1]

### Neden değerli
Şu an her node bağımsız config tutuyor. Birinde target eklenir, diğerlerinde unutulursa **drift** olur. Gossip zaten her şeyi paylaşıyor — config hash'i de paylaşsın.

Bu **opt-in** bir özellik olmalı (güvenlik için). İki mod:

**Mod 1: Drift Detection (varsayılan)**
- Her node config hash'ini broadcast eder
- Diğer node'lar mismatch görürse uyarı log'lar + metric set eder
- Config DEĞİŞMEZ — sadece uyarı

**Mod 2: Auto Sync (opt-in, dikkatli)**
- Bir node "primary config source" olarak işaretlenir
- Diğer node'lar config farklıysa primary'den çekip uygular
- Reload tetiklenir

### Config genişlemesi

```yaml
cluster:
  config_sync:
    enabled: true
    mode: "drift_detection"   # drift_detection | auto_sync
    primary_node: ""          # auto_sync için zorunlu
    sync_interval_sec: 30
```

### Implementation outline

**`internal/cluster/configsync.go`** (yeni)

```go
type ConfigBroadcast struct {
    NodeName   string
    ConfigHash string         // sha256(config.yaml)
    ConfigSize int
    LoadedAt   time.Time
}

func (m *Manager) BroadcastConfig(hash string, loadedAt time.Time)
func (m *Manager) ConfigDriftDetected() []ConfigDrift
```

`gossipDelegate.NotifyMsg` config broadcast'ı tanır → `peerConfigs` map'ini günceller.

**Engine entegrasyonu:**
- `LoadConfig` sonrası → hash hesapla → broadcast
- Periyodik: `m.ConfigDriftDetected()` çalıştır → mismatch varsa log + metric

### Yeni endpoint
- `GET /cluster/config` — kendi hash + tüm peer hash'leri
```json
{
  "self": {"node": "node-1", "hash": "abc123...", "loaded_at": "..."},
  "peers": [
    {"node": "node-2", "hash": "abc123...", "in_sync": true},
    {"node": "node-3", "hash": "xyz789...", "in_sync": false}
  ],
  "drift_count": 1
}
```

- `POST /cluster/config/sync` (auto_sync mode) — manuel trigger

### Yeni metric
- `network_probe_config_drift` — drift tespit edildiğinde 1, sync'teyken 0
- `network_probe_config_hash` — info metric (label: hash)

### Auto-sync güvenlik
- Sadece keyring'le authenticated mesajlar kabul edilir (memberlist zaten yapıyor)
- Primary config en az 3 node'dan onay almadan dağıtılmaz (yeni özellik)
- Backup yapılır (`config.yaml.bak`)

### Kabul kriteri
1. 3 node aynı config → `drift_count=0`
2. Bir node'da target ekle → diğer 2 node loglarında "config drift detected"
3. `/cluster/config` mismatch'leri gösterir
4. (auto_sync) Drift sonrası 30sn içinde otomatik düzelir

---

## 6. Active Probe Delegation [P2]

### Neden değerli
Catchpoint, Datadog Synthetic, Site24x7'nin sattığı şey: "Frankfurt'tan, Tokyo'dan, São Paulo'dan ölç". Çok pahalı SaaS.

netwatch cluster'ında: her node bir lokasyon. Aynı target'a 3 lokasyondan probe yap, latency'leri karşılaştır, anomali tespit et. Self-hosted, gossip-native, multi-region synthetic monitoring.

### Config

```yaml
cluster:
  node_name: "frankfurt-1"
  region: "eu-central"        # YENİ — node label'ı

targets:
  - id: "checkout-public"
    type: "http"
    target: "https://checkout.example.com/health"
    probe_from:               # YENİ
      - "frankfurt-1"
      - "tokyo-1"
      - "sao-paulo-1"
    # veya region bazlı:
    # probe_from_regions: ["eu-central", "asia-pacific", "south-america"]
```

### Davranış
- `probe_from` boşsa → her node kendi probe'unu yapar (eski davranış)
- `probe_from` doluysa → sadece listedeki node'lar probe yapar
- Sonuçlar gossip ile paylaşılır
- `/fleet/status`'ta her bölgenin latency'si ayrı görünür

### Implementation outline

**`internal/engine/loop.go` değişikliği:**
```go
func (e *Engine) shouldProbe(t Target) bool {
    if len(t.ProbeFrom) == 0 {
        return true
    }
    for _, n := range t.ProbeFrom {
        if n == e.hostname {
            return true
        }
    }
    return false
}
```

`startProbeLoop` her target için bunu kontrol eder, `false` ise sadece state listener olur (gossip'ten state alır).

### Yeni metric
- `network_probe_local_latency_seconds` zaten var → cluster'da artık her node'un latency'si ayrı görünür
- `network_probe_geo_latency_p50` — label: target, region (consensus latency hesapla)
- `network_probe_geo_latency_anomaly` — bölgeler arası varyans yüksekse 1

### Yeni endpoint
- `GET /geo/latency/{targetID}` — bölgesel latency breakdown
```json
{
  "target": "checkout-public",
  "by_region": {
    "eu-central": {"latency_p50_ms": 45, "node": "frankfurt-1"},
    "asia-pacific": {"latency_p50_ms": 220, "node": "tokyo-1"},
    "south-america": {"latency_p50_ms": 180, "node": "sao-paulo-1"}
  },
  "anomaly": false
}
```

### Anomaly detection
- Bölgeler arası latency varyansı %200'ü aşarsa alarm
- Status: `geo_anomaly`
- Env: `ANOMALY_REGION=tokyo-1`, `ANOMALY_RATIO=2.5`

### Kabul kriteri
1. `probe_from: [node-1]` → sadece node-1 probe eder, diğerleri gossip'ten state alır
2. 3 bölgeden farklı latency'ler `/geo/latency`'de görünür
3. Bir bölgede latency 5x artarsa anomaly alarm

---

# Implementasyon Sırası Önerisi

```
P0 (Hayati — bu olmadan ürün konumlandırılamaz):
  1. Dependency Graph + Root Cause      [3-4 gün]
  2. /fleet/status                       [2-3 gün]

P1 (Güçlü farklılaşma — pazarlamayı yapan özellikler):
  3. Scope Intelligence Enhancement     [1-2 gün]
  4. SLO Tracker                         [3-4 gün]
  5. Gossip Config Sync                  [2-3 gün]

P2 (Nice-to-have — premium hissi veren):
  6. Active Probe Delegation             [3-5 gün]
```

# Aşama Bağımlılıkları

```
Phase 12 (Integration Tests) → P0.1 (Dependency Graph)
                            ↓
                            P0.2 (/fleet/status) ← P0.1 dependency tree'yi kullanır
                            ↓
                            P1.3 (Scope Intelligence) ← P0.2 fleet view'i kullanır
                            ↓
                            P1.4 (SLO) ← P0.2 incident verisini kullanır
                            ↓
                            P1.5 (Config Sync) ← bağımsız
                            ↓
                            P2.6 (Probe Delegation) ← P0.2 bölgesel view kullanır
```

# Ortak Pre-requisite'ler

Bu özelliklere başlamadan önce:

1. **Phase 12 (Integration Tests)** tamamlanmalı — yeni özellikler eski davranışı bozmamalı
2. `Manager.PeerStatesForTarget()` zaten var — kullanılabilir
3. `Manager.Members()` zaten var — fleet listesi için
4. `internal/engine/topology.go` ve `internal/engine/fleet.go` yeni dosyalar olarak eklenecek (CLAUDE.md'deki "iki dizin" kuralına uygun)

# Pazarlama Stratejisi (özet)

Her özellik landing page'de bir bullet:

- ✅ **Consensus-based monitoring** — Single-node false alarms eliminated
- ✅ **Network partition detection** — Know if it's the network or the service
- ✅ **Dependency root cause** — Get the cause, not just the symptom
- ✅ **Master-less fleet view** — No Prometheus, no Grafana required
- ✅ **Built-in SLO tracking** — Monthly reports without separate tooling
- ✅ **Multi-region synthetic** — Self-hosted Catchpoint alternative
- ✅ **Config drift detection** — GitOps-friendly cluster sanity

Bu liste hiçbir lightweight monitoring agent'ında yok. Zabbix'te kısmen var ama saatlerce config gerekir. Datadog'da var ama $$$.

netwatch'ın pazarlama tek cümlesi:
> **"The masterless monitoring agent that knows what it doesn't know."**

(Yani: "ağ partition'ı mı, gerçek outage mı, lokal sorun mu" sorusunu otomatik cevaplayan ilk agent.)
