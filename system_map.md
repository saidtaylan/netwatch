# netwatch System Map

## Context Dosyaları Rehberi

| Görev Türü | Oku | Açıklama |
|------------|-----|----------|
| Proje yapısı, dosya haritası, mimari, tech stack | **system_map.md** (bu dosya) | Projenin statik anatomisi |
| Ne yapıldı, hangi değişiklikler yapıldı | **developments.md** | Kronolojik changelog |
| Sonraki aşamalar, görev listesi, kabul kriterleri | **sprint.md** | Aktif sprint + bekleyen planlar |
| Claude Code agent talimatları, build komutları, sabit kararlar | **CLAUDE.md** | LLM agent için birincil rehber |

> Eski kayıtlar henüz `docs/archive/` altında tutulmuyor. Tüm geçmiş developments.md'de.

---

## Project Overview

**netwatch**, ağdaki TCP/HTTP/ICMP/DNS/SQL hedeflerini özerk olarak izleyen, Prometheus metrics exporter olarak çalışan bir Go agent'ıdır. Başlangıçta tek-node bir exporter iken; gossip protokolü (memberlist) üzerinde quorum bazlı karar veren, exactly-once alarm garantisi sunan dağıtık bir cluster monitoring sistemine dönüştürülmektedir. SRE ekipleri tarafından infra ve uygulama bağımlılıklarını izlemek için kullanılır.

Sistemin iki bileşeni vardır:
- **backend**: Go agent — probe, gossip, alarm, metrik
- **frontend**: Nuxt 3 SPA — tüm endpoint'leri kapsayan admin UI

- **Proje türü:** Distributed monitoring system (backend: Go agent, frontend: Nuxt 3 SPA)
- **Module (backend):** `github.com/saidtaylan/netwatch`
- **Root:** `/Users/saidtaylan/Documents/network cluster/`
- **Backend:** `backend/` — Go 1.25.7
- **Frontend:** `frontend/` — Nuxt 3, Node 20+, pnpm

---

## Related Documentation

| Dosya | Açıklama |
|-------|----------|
| `CLAUDE.md` | LLM agent talimatları, build komutları, sabit mimari kararlar |
| `developments.md` | Kronolojik değişiklik günlüğü + planlanan sprint başlıkları |
| `sprint.md` | Aktif sprint planı, görev listeleri, kabul kriterleri, mimari kararlar |
| `GUIDE.md` / `GUIDE_EN.md` | Son kullanıcı rehberi — kurulum + cluster setup adım adım |
| `config.yaml` | Canlı örnek config — tüm alanlar açıklamalı |
| `instructions/agent-instructions.md` | Orijinal agent talimatları (referans) |
| `instructions/project-introduction.md` | Proje tanıtımı |
| `gemini_thoughts.md` | Bağımsız 3rd-party LLM (Gemini) test raporu (2026-05-16) — 8 edge-case senaryo BAŞARILI |

---

## Mevcut Dosya Yapısı (Sprint 0 sonrası — backend/frontend split)

### Kök Dizin (Paylaşılan)

```
network cluster/
  backend/             ← Go kodu (tüm ayrıntı aşağıda)
  frontend/            ← Nuxt 3 SPA (Sprint 1'den itibaren doluyor)
  developments.md      ← [backend]/[frontend] etiketli changelog
  sprint.md            ← Aktif sprint (backend + frontend sprint'leri ayrı başlıklarda)
  system_map.md        ← Bu dosya
  todo.md              ← Backend backlog (B1-B11)
  CLAUDE.md            ← Build komutları, mimari kararlar (her iki servis için)
  README.md            ← Proje tanıtımı, "iki servis nasıl çalıştırılır"
  frontend-plan.md     ← UI mimari planı (Sprint 0-10 sırası, sayfa/endpoint haritası)
```

### Backend Dosya Yapısı (`backend/`)

```
backend/
  internal/engine/
    engine.go          # Config (recovery_probes eklendi), Engine struct (maintMgr, pendingRecovery)
    loop.go            # startProbeLoop (stagger offset), processPending (2-phase), runCheck (soft-up)
    maintenance.go     # MaintenanceWindow, maintenanceManager (RAM + disk), GenerateWindowID  ← F3
    configpush.go      # SharedConfig (recovery_probes eklendi)
    join.go            # GenerateKeyringKey, LocalClusterAddr, ClusterPrimaryKey
    ...diğer mevcut dosyalar...

  internal/cluster/
    cluster.go         # maintenanceHandler field, NotifyMsg maintenance dispatch
    maintenance.go     # MaintenanceBroadcast, MaintenanceHandler interface, Broadcast*  ← F3
    ...diğer mevcut dosyalar...

  <state_file_dir>/
    state.json         # target up/down state (v2) — gitignored
    incidents.json     # SLO incident history — gitignored
    maintenance.json   # ad-hoc maintenance windows — gitignored

  Makefile, Dockerfile, go.mod, go.sum, config.example.yaml
  deploy/netwatch.service, helm/, notifications/
```

### Frontend Dosya Yapısı (`frontend/`) — Sprint 1-4 tamamlandı

```
frontend/
  nuxt.config.ts       # ssr:false, runtimeConfig (NUXT_PUBLIC_DEFAULT_BACKEND_URL)
  tailwind.config.ts   # status/scope renkler, dark mode
  vitest.config.ts     # @nuxt/test-utils env, 67 unit test
  tests/
    unit/utils/        # format.test.ts, classifyState.test.ts
    unit/stores/       # auth, nodes, alerts, ui
    unit/composables/  # useNodeConnection
    setup.ts           # $fetch + navigateTo global stubs

  app/
    app.vue            # NuxtLayout + NuxtPage
    layouts/
      default.vue      # Sidebar + TopBar + Toast
      auth.vue         # Centered card (setup)
    pages/
      index.vue        # Cluster overview (quorum, drift, down targets, members)
      setup.vue        # Multi-node URL + token form → connect
      login.vue        # Redirect → /setup (single-user app)
      targets/
        index.vue      # Target list: search, status/type filter, TargetRow table
        [id].vue       # Target detail: scope, by-node, deps, probers, geo
      topology.vue     # Root/dependent + full table; graph visualizasyon bekliyor
      slo.vue          # SLO per-target: uptime/budget/incidents; 503 = disabled
      apps.vue         # App → target grouping, down badge
      alerts.vue       # In-memory ring buffer + B5 Ack placeholder
      maintenance.vue  # Window CRUD (create/cancel), gossip-replicated
      geo.vue          # Per-target/per-node latency + anomaly
      silences.vue     # B1 placeholder "Coming Soon"
      audit.vue        # B7 placeholder "Coming Soon"
      config/
        index.vue      # Config drift view + sync now
        push.vue       # PUT /cluster/config form
        keyring.vue    # Keyring add/use/remove + rotation steps
      settings/
        index.vue      # Polling interval, theme, disconnect
        nodes.vue      # Backend node CRUD (test/use/remove)
    components/
      common/          # StatusBadge, SeverityBadge (B2 ready), ConnectionStatus,
                       # Toast, EmptyState, ConfirmDialog, Sidebar, TopBar
      cluster/         # NodeCard, QuorumIndicator, ConfigDriftCard
      targets/         # TargetRow, ByNodeBreakdown, ScopeClassificationCard,
                       # DependencyChip
    composables/
      useApi.ts        # Bearer inject, 401 → logout, failover retry
      useAuth.ts       # Single-token auth (self-hosted), checkToken → /auth/whoami
      useNodeConnection.ts  # Promise.any race, ensureActive, seedFromEnv
      usePolling.ts    # Visibility-aware interval fetcher
      useCluster.ts    # /cluster/state + /cluster/config polling
      useFleet.ts      # /fleet/status + state-change → alerts store
      useMaintenance.ts # GET/PUT/DELETE /cluster/maintenance
      useTopology.ts   # /topology polling
      useGeoLatency.ts # /geo/latency/{id} polling
      useSLO.ts        # /slo polling
    stores/
      auth.ts          # token, role — persisted
      nodes.ts         # configured[], active, health — persisted
      ui.ts            # pollingIntervalMs, sidebarCollapsed, toasts (partial persist)
      alerts.ts        # in-memory ring buffer cap=100
    middleware/
      auth.global.ts   # Token yoksa → /setup
      node-health.global.ts  # No nodes → /setup; null active → race
    types/api.ts       # Backend response TS tipleri (B1-B11 için hazır alanlar dahil)
    utils/
      format.ts        # fmtDurationSec, fmtPercent, fmtLatency, capitalize, fmtRelative
      classifyState.ts # stateStyle, isDown, SCOPE_STYLE, CLASS_STYLE
    plugins/
      pinia-persist.client.ts  # localStorage persistence
    assets/css/main.css        # Tailwind directives + scrollbar + page transitions
```

**Auth modeli:** Single admin token (config.yaml `admin.token`). Token → `/auth/whoami` ile verify. Self-hosted, SaaS değil. İleride LDAP eklenirse `WhoAmIResponse.role` genişletilir.

**Routing kuralı:** Tüm `NuxtLink`/`navigateTo` çağrıları **named route** kullanır (`{ name: 'targets-id', params: { id } }`). String path **YASAK** (refactor güvencesi). Detay → `CLAUDE.md` "Frontend Routing Kuralı". Mevcut kodda ~30 string-path violation var; S9 sprint'inde toplu temizlenecek.

**Persistent store:** `auth`, `nodes`, `ui` localStorage'da kalıcı (`pinia-plugin-persistedstate`). F5'te tekrar giriş gerekmesin. `alerts` in-memory (B7 sonrası `/alerts` endpoint'i geldiğinde değişir).

**Multi-node failover:** `stores/nodes.ts` birden fazla URL tutar. `useNodeConnection.selectActiveNode()` → `Promise.any($fetch /health)` ile en hızlı yanıt veren. Active node down → `markUnhealthy` + failover.

## Yol Haritası

### Backend Sprint — Tamamlandı (2026-05-20)

| ID | Özellik | Durum |
|----|---------|-------|
| F1 | Probe Interval Staggering | ✅ Tamamlandı |
| F2 | Cross-Node ROOT_CAUSE Fix (bug fix) | ✅ Tamamlandı |
| F3 | Maintenance Window (API-driven) | ✅ Tamamlandı |
| F4 | Soft-Up State (recovery_probes) | ✅ Tamamlandı |
| F5 | Kubernetes Service Discovery | ⏸ Sonraki backend sprint |
| F6 | Process-Level Auto Discovery | ❌ Reddedildi (APM scope) |

### Frontend Sprint — Aktif (2026-05-20+)

| Sprint | Kapsam | Durum |
|--------|--------|-------|
| S0 | Repo reorganizasyonu (backend/ split) | ✅ Tamamlandı |
| S1 | Nuxt 3 iskelet, layout, routing, backend endpoints | ✅ Tamamlandı |
| S2 | Auth (single-token) + multi-node connection + 67 unit test | ✅ Tamamlandı |
| S3 | Tüm sayfalar: targets, slo, maintenance, alerts, config, geo, placeholders | ✅ Tamamlandı |
| S4 | Target detail + topology | ✅ Tamamlandı |
| S5 | Polish: skeletons, error banner, a11y, polling back-off | ✅ Tamamlandı |
| S6 | systemd target + install.sh + root Makefile + README | ✅ Tamamlandı |
| S7 | Playwright e2e (17 geçen + 9 skip) | ⚠ Kısmen — S8'e taşındı |
| S8 | E2E reliability — pinia hydration race fix | ⏳ Bekliyor |
| S9 | Named routes refactor (string path → name) | ⏳ Bekliyor |
| S10 | CI gate integration | ⏳ Bekliyor |

### Backend Backlog (B-items) — UI sonrası

Detay için `todo.md`. Önerilen sıra: B2 (Severity) → B1 (Silence Rules) → B7 (Audit Log) → B3 (Latency Alerting) → B6 (gRPC).

**F5 (Kubernetes SD) sonraki sprint'te aktive olursa yeni yapı:**

```
internal/discovery/
  kubernetes/           # CLAUDE.md "iki dizin" kuralına bilinçli istisna
    watcher.go
    parser.go
    reconciler.go
    config.go
```

---

## Entry Points / URLs / Endpoints

### CLI Subcommands

```
netwatch [--config FILE]                          start the monitoring agent
netwatch init [--cluster] [--config-dir DIR] [--bind-port N] [--force]
                                                  generate config skeleton (cluster mode includes random AES-256 keyring + copy-paste join command)
netwatch join --keyring K --addr H:P [--config PATH] [--bind-port N] [--node-name N]
                                                  one-command join: writes minimal config with cluster.enabled=true and gossip seed
netwatch keyring generate                         print a fresh AES-256 base64 key (32 raw bytes) — for rotation or manual setup
netwatch validate [--config FILE]                 validate config without starting
netwatch leave [--port PORT]                      tell a running agent to gracefully leave the cluster
netwatch uninstall                                stop service, remove unit, optionally delete config
netwatch service install|remove   (Windows only)  register/unregister the Windows Service
```

### HTTP Endpoints

| Endpoint | Method | Açıklama |
|----------|--------|----------|
| `/metrics` | GET | Prometheus scrape — probe sonuçları; her çağrıda watchdog'u bildirir |
| `/health` | GET | Liveness check — her zaman `200 OK` |
| `/status` | GET | Tüm target'ların anlık JSON durumu (name, state, seq, error_code) |
| `/cluster/state` | GET | Üye listesi + peer target durumları; cluster kapalıysa 503 |
| `/cluster/leave` | POST | Graceful cluster leave + process exit; `?reason=TEXT` opsiyonel |
| `/cluster/probers` *(Phase 13)* | GET | Target başına seçilen prober subset, primary, candidate seti, probe_from constraint'i; cluster kapalıysa 503 |
| `/fleet/status` *(Phase 13)* | GET | Cluster-wide özet: members (zone'larla), quorum/isolated flag'leri, target counts (up/hard_down/unknown), down target ID listesi (cap=100); cluster kapalıysa 503 |
| `/slo` *(P1.4)* | GET | SLO snapshot: per-target uptime ratio, error budget, breach status, incident history; `slo.enabled: false` → 503 |
| `/cluster/config` *(P1.5)* | GET | Config-sync snapshot: this node's SHA-256 hash + each peer's hash + in-sync flag + drift count; cluster kapalıysa 503 |
| `/cluster/config` | PUT | Shared config fields dağıt (JSON/YAML body) → self apply + gossip TCP ile tüm peer'lara. `admin.token` ayarlıysa auth zorunlu. |
| `/cluster/config/sync` | POST | Bu node'un shared config'ini peer'lara dağıt. Body yok. `admin.token` ayarlıysa auth zorunlu. |
| `/geo/latency/{targetID}` *(P1.6)* | GET | Per-node latency view for a target: region labels, last probe latency, anomaly flag (any node >3× min) |

**Default port:** `10240` (config'den override edilebilir)

---

## Tech Stack

### Core

| Technology | Version | Purpose |
|-----------|---------|---------|
| Go | 1.25.7 | Runtime dil |
| `github.com/prometheus/client_golang` | 1.23.2 | Prometheus metrics (GaugeVec, Registry) |
| `github.com/hashicorp/memberlist` | 0.5.4 | UDP/TCP gossip cluster (Phase 6+) |
| `sigs.k8s.io/yaml` | 1.6.0 | config.yaml parsing |
| `golang.org/x/net` | 0.52.0 | ICMP ping (CAP_NET_RAW) |

### SQL Drivers

| Driver | Purpose |
|--------|---------|
| `github.com/go-sql-driver/mysql` | MySQL probe |
| `github.com/lib/pq` | PostgreSQL probe |
| `github.com/microsoft/go-mssqldb` | SQL Server probe |
| `github.com/sijms/go-ora/v2` | Oracle probe |

### Infra

| Technology | Purpose |
|-----------|---------|
| Docker (multi-stage) | Container image |
| Prometheus | Scraper (external) |
| systemd / Windows Service | Process supervisor |

### Mimari Diyagram

```
[config.yaml] ──→ [Engine.Init()]
                        │
           ┌────────────┼────────────────────┐
           ▼            ▼                    ▼
   [ProbeLoops]   [RetryLoop]         [ClusterMgr]
   (per-target    (soft→hard-down)    (memberlist)
    goroutines)        │                    │
           │           │              [Gossip UDP/TCP]
           ▼           ▼                    │
      [Checkers]  [StateTransition]    [peerStates]
   tcp/http/ping   markHardDown()           │
   dns/sql         markRecovered()          │
           │            │                   │
           └──────┬─────┘                   │
                  ▼                         │
            [state.json v2]          [/cluster/state]
            (persistence)                   │
                  │                         │
                  └──────┬──────────────────┘
                         ▼
                   [Alerters]
               script / mail / webhook
                         │
                         ▼
              [Prometheus /metrics]
```

---

## Data Layer

### state.json (v2)

Disk üzerinde kalıcı probe durumu. Restart sonrası anti-entropy ve rolling restart alarm storm önleme için kritik.

```json
{
  "version": 2,
  "targets": {
    "target-key": {
      "state": "up | hard_down",
      "seq": 3,
      "error_code": "dial tcp: connection refused",
      "owner_node": ""
    }
  }
}
```

- `state`: `up` veya `hard_down` (SOFT_DOWN sadece RAM'de, persist edilmez)
- `seq`: Lamport clock — her `markHardDown` / `markRecovered` çağrısında increment
- `error_code`: Son başarısız probe'un hata mesajı
- `owner_node`: Şu an boş; Phase 9 anti-entropy sync sırasında responsible node adı yazılacak
- **v1 format** (`map[string]bool`) → otomatik v2'ye migrate edilir
- Atomik yazma: önce `.tmp`, sonra `os.Rename`

### Config (config.yaml)

`sigs.k8s.io/yaml` ile parse edilir. `${VAR}` syntax ile `credentials_file`'dan env injection desteklenir. Hot-reload: `reload_interval_sec` saniyede bir SIGHUP olmadan yeniden okunur.

### State Machine

```
UNKNOWN ──→ SOFT_DOWN (pending map, RAM)
                │  probe fail, max_retries dolmadı
                ▼
           HARD_DOWN (lastKnown, disk)
                │
UP ←────────────┘  markRecovered() — Seq++
UP ──→ SOFT_DOWN   enqueue() — probe fail
```

---

## Authentication & Security

### Cluster Encryption (Phase 6+)

- AES-128/192/256 symmetric keyring via memberlist
- Config'de `cluster.keyring: [base64key1, base64key2]`
- İlk key şifreler, tüm key'ler çözer (zero-downtime key rotation)
- `cluster.enabled: false` → hiçbir network port açılmaz

### SMTP TLS

- `tls_mode: starttls | tls | none`
- `tls_insecure: true` → sertifika doğrulaması atlanır (test ortamı)
- `ca_cert: /path/to/ca.pem` → custom CA

---

## Project Structure

```
/Users/saidtaylan/Documents/network cluster/
├── cmd/
│   ├── linux/
│   │   ├── main.go          # Linux giriş noktası — signal handler, HTTP server
│   │   └── config.yaml      # linux-specific örnek config
│   └── windows/
│       └── main.go          # Windows Service entegrasyonu (svc.IsWindowsService)
├── internal/
│   ├── engine/              # Business logic — tüm probe, state, alert mantığı
│   │   ├── protocol.go      # Checker interface: Run, ValidateOptions, ParseAddr
│   │   ├── engine.go        # Config struct, Engine struct, Init, Reload, state persistence
│   │   ├── loop.go          # startProbeLoop, runRetryLoop, runCheck, state transitions
│   │   ├── notify.go        # Alerter interface, sendAlert, mergeNotifyChannels
│   │   ├── app.go           # App struct, buildAppTargetIndex, validateApps
│   │   ├── topology.go      # DependencyGraph, FindRootCause, CascadingImpact, TopologySnapshot
│   │   ├── fleet.go         # FleetSnapshot — per-target detail, scope, classification, confidence, apps, root cause, incidents
│   │   ├── scope.go         # DetailedScope, classifyScope — REAL_OUTAGE/NETWORK_PARTITION/LOCAL_FAILURE/AMBIGUOUS + confidence
│   │   ├── slo.go           # SLOConfig, sloManager, incidents.json, ComputeSLO, breach alerts, SLO Prometheus metrics
│   │   ├── webhook.go       # WebhookAlerter — generic + alertmanager format
│   │   ├── watchdog.go      # runWatchdog, NotifyScrape, network_probe_prometheus_connected
│   │   ├── mail.go          # SMTP alerter — starttls/tls/plain, multipart/alternative HTML
│   │   ├── http.go          # HTTP/HTTPS Checker
│   │   ├── tcp.go           # TCP Checker
│   │   ├── ping.go          # ICMP ping Checker (CAP_NET_RAW gerektirir)
│   │   ├── dns.go           # DNS Checker
│   │   └── sql.go           # SQL Checker — oracle/mysql/postgres/mssql
│   ├── appinfo.go           # var BinaryName = "netwatch" (ldflags ile override edilebilir)
│   └── cluster/             # Gossip cluster katmanı (Phase 6–9)
│       ├── cluster.go       # Config (Zone, Region, ProbeReplicationFactor dahil), GossipPayload (Latency eklendi), AntiEntropyProvider, Manager, hash ring, quorum, NodeMeta (zone+region), MemberInfo (zone+region)
│       ├── probers.go       # LocalTargetProvider (ProbeFromRegionsConstraint dahil), ProberAssignmentListener, CandidatesFor (region filter), SelectProbers, IsLocalProber, zoneAwarePick (3-tier)
│       ├── configsync.go    # P1.5: ConfigBroadcast, ConfigHashOf, SetLocalConfigInfo, handleConfigBroadcast, ConfigSyncSnapshot, runConfigSyncLoop, GaugeConfigDrift
│       ├── geolat.go        # P1.6: GeoLatencyEntry, GeoLatencySnapshot, GeoLatencyForTarget, detectLatencyAnomaly, UpdateGeoMetrics, regionOf, GaugeGeoLatency, GaugeGeoLatencyAnomaly
│       ├── views.go         # ProberAssignmentsSnapshot (/cluster/probers), FleetSummarySnapshot (/fleet/status), FleetDownTargetsCap
│       ├── testhelpers.go   # NewTestManager, SetIsolated, SetPeerState, SetTestAliveSet, SetTestZones, SetTestRegions
│       ├── cluster_test.go  # Hash ring, quorum, IsolatedMode, PeerStatesForTarget testleri
│       ├── antientropy_test.go    # LocalState/MergeRemoteState dispatch, mockProvider testleri
│       ├── configsync_test.go     # P1.5: ConfigHashOf, SetLocalConfigInfo, drift detection, ConfigSyncSnapshot
│       ├── geolat_test.go         # P1.6: anomaly detection, regionOf, GeoLatencyForTarget, probe_from_regions filter
│       ├── phase13_config_test.go  # Config validation, NodeMeta, zoneOf
│       ├── phase13_probers_test.go # CandidatesFor, hashCandidateOrder, zoneAwarePick, SelectProbers, IsLocalProber, ProbeFrom
│       ├── phase13_recompute_test.go # recomputeProberAssignments, Seed, scheduleRecompute debounce
│       └── phase13_views_test.go     # ProberAssignmentsSnapshot, FleetSummarySnapshot
├── notifications/           # Alert scriptleri (.sh / .ps1) buraya konur
├── config.yaml              # Örnek config — tüm alanlar açıklamalı
├── go.mod                   # Module: github.com/saidtaylan/netwatch, go 1.25.7
├── go.sum
├── Dockerfile               # Multi-stage build
├── CLAUDE.md                # LLM agent rehberi
├── system_map.md            # Bu dosya
├── developments.md          # Değişiklik günlüğü
├── sprint.md                # Bekleyen aşamalar
└── README.md
```

**Kısıtlama:** Yalnızca `internal/engine/` ve `internal/cluster/` dizinleri kullanılır. Yeni alt-paket oluşturulamaz.

---

## Key Components / Modules

### Engine (`internal/engine/`)

| Bileşen | Dosya | Görev |
|---------|-------|-------|
| `Config` struct | `engine.go` | Tüm config alanları + Validate + Load |
| `Engine` struct | `engine.go` | Koordinasyon: probeCancel, lastKnown, pending, clusterMgr |
| `Checker` interface | `protocol.go` | `Run(ctx, target, options) (bool, error)` |
| `Alerter` interface | `notify.go` | `Send(env map[string]string) error` |
| `startProbeLoop` | `loop.go` | Per-target ticker goroutine |
| `runRetryLoop` | `loop.go` | Soft-down pending queue processor |
| `markHardDown` | `loop.go` | SOFT_DOWN → HARD_DOWN, Seq++, broadcast |
| `markRecovered` | `loop.go` | ANY_DOWN → UP, Seq++, broadcast |
| `broadcastState` | `loop.go` | Cluster gossip broadcast (nil-safe) |
| `sendAlert` | `notify.go` | Kanal seçimi + env build + async send |
| `buildAppTargetIndex` | `app.go` | targetKey → []*App index |
| `runWatchdog` | `watchdog.go` | Prometheus scrape izleme |
| `WebhookAlerter` | `webhook.go` | HTTP POST — generic / alertmanager format |
| `mailAlerter` | `mail.go` | SMTP — multipart/alternative |
| `shouldAlert` | `engine.go` | Cluster alarm kapısı: nil→true, isolated→false, not-responsible→false |
| `computeScope` | `engine.go` | Peer states'e bakarak GLOBAL/NODE_LOCAL/PARTIAL/STANDALONE döner |
| `classifyScope` | `scope.go` | Zenginleştirilmiş scope: Classification + Confidence + per-node breakdowns; classifyScope().ScopeEnv() alert env'e eklenir |
| `DetailedScope` | `scope.go` | REAL_OUTAGE/NETWORK_PARTITION/LOCAL_FAILURE/AMBIGUOUS + DownNodes/UpNodes/OfflineNodes/PartitionGroups/Confidence |
| `FleetSnapshot` | `fleet.go` | /fleet/status için per-target detay: scope, classification, confidence, apps, root cause, cascading impact, incidents |
| `TopologySnapshot` | `topology.go` | /topology için dependency graph: depends_on, reverse deps, cascading impact |
| `sloManager` | `slo.go` | incidents.json lifecycle: RecordStart/End, PruneOldIncidents, ComputeSLO, breach flag |
| `ComputeSLO` | `slo.go` | Rolling-window uptime: downtime klamp + aktif incident sayma + SLOResult |
| `SLOSnapshot` | `slo.go` | /slo endpoint verisi; slo.enabled=false → nil |
| `runSLOChecker` | `slo.go` | 60sn goroutine: checkSLOBreaches + edge-triggered sendSLOBreachAlert |
| `RegisterSLOMetrics` | `slo.go` | slo.enabled=true iken SLO gauge'larını registry'e kaydeder |
| `FullState` | `engine.go` | AntiEntropyProvider: lastKnown haritasını JSON serialize eder |
| `ApplyRemoteState` | `engine.go` | AntiEntropyProvider: Lamport kuralıyla remote state merge eder |
| `SetSyncing` | `engine.go` | AntiEntropyProvider: syncing flag'i set/clear eder; runCheck/processPending guard'ı |

### Cluster (`internal/cluster/`)

| Bileşen | Açıklama |
|---------|----------|
| `Manager` | memberlist örneği + peerStates map + ring (sorted alive nodes) |
| `gossipDelegate` | NotifyMsg → OnStateReceived; GetBroadcasts; LocalState/MergeRemoteState |
| `eventDelegate` | NotifyJoin/Leave/Update → slog + peerStates temizleme + ring güncelleme |
| `broadcast` | Invalidates(): aynı (NodeName, TargetID) çiftinde yeni Seq eskisini geçersiz kılar |
| `OnStateReceived` | Lamport merge: Seq > existing \|\| (Seq == existing && NodeName > existing.NodeName) |
| `Snapshot()` | /cluster/state için deep-copy ClusterStateSnapshot |
| `updateRing()` | Canlı üyeleri sıralayarak ring'i yeniden kurar; her üyelik değişiminde çağrılır |
| `GetResponsibleNode()` | FNV-32a(targetID) % n ile primary + secondary döner |
| `IsResponsible()` | Bu node primary veya secondary mi? Alarm kapısı bu soruyu sorar |
| `PeerStatesForTarget()` | Verilen targetID için tüm peer'lardan gelen GossipPayload listesi |
| `IsolatedMode()` | Quorum kayıpsa true; Phase 8 alarm dispatch'ini bu flag ile kesiyor |
| `SetStateProvider()` | Engine'i AntiEntropyProvider olarak kaydeder; push-pull döngüsünü engine'e devreder |
| `AntiEntropyProvider` | Interface: `FullState()`, `ApplyRemoteState()`, `SetSyncing()` |
| `PeerAlertHandler` | Interface: `HasLocalProbe()`, `DispatchPeerAlert()` — primary lokal probe yapmıyorsa sigorta |
| `CandidatesFor()` *(Phase 13)* | Verilen targetID için peerStates + local config'den candidate node listesi; ProbeFrom constraint varsa kesişim alır |
| `SelectProbers()` *(Phase 13)* | Hash ring + 3-tier zone-aware picker ile prober subset (factor adet) |
| `IsLocalProber()` *(Phase 13)* | Bu node verilen target için probe etmekle yükümlü mü |
| `ProberAssignmentListener` *(Phase 13)* | Interface: `StartProbing()`, `StopProbing()` — Engine implement eder; cluster recompute sonrası diff'ten callback gelir |
| `LocalTargetProvider` *(Phase 13)* | Interface: `LocalTargets()` + `ProbeFromConstraint(targetID)` — Engine implement eder |
| `NodeMeta` *(Phase 13)* | Memberlist built-in: zone bilgisi tüm node'lara otomatik dağıtılır |
| `bootstrapInventoryBroadcast()` *(Phase 13)* | Engine'de: Init + Reload sonrası her local target için 1 presence broadcast |
| `recomputeProberAssignments()` *(Phase 13)* | Manager'da: local target başına `IsLocalProber` hesapla, önceki snapshot ile diff al, transition'larda Start/Stop dispatch |
| `scheduleRecompute()` *(Phase 13)* | 5sn debounce timer; NotifyJoin/Leave/Update + yeni peerStates entry'sinde tetiklenir; membership flapping'i kasırgaya çevirmez |
| `TriggerProberRecompute()` *(Phase 13)* | Engine.Reload bunu çağırır — local target listesi değişince debounce'u beklemeden sync recompute |
| `SeedProberAssignments()` *(Phase 13)* | Engine.Init bunu çağırır — probe loop'lar başlatıldıktan sonra cluster'a "bunlar zaten çalışıyor" deyip ilk reactive recompute'u sessizleştirir |

### Prometheus Metrics

| Metrik | Tip | Labels |
|--------|-----|--------|
| `network_probe_local_status` | GaugeVec | name, target, type, source_host, app_name |
| `network_probe_local_latency_seconds` | GaugeVec | name, target, type, source_host, app_name |
| `network_probe_prometheus_connected` | Gauge | (label'sız) |
| `network_probe_cluster_status` | GaugeVec | name, target, type, source_host, app_name |
| `network_prober_quorum_healthy` *(Phase 7)* | Gauge | (label'sız) |
| `network_prober_isolated` *(Phase 7)* | Gauge | (label'sız) |
| `network_prober_cluster_size` *(Phase 7)* | Gauge | (label'sız) |
| `network_probe_local_assigned` *(Phase 13)* | GaugeVec | name, target, type — 1: bu node probe ediyor |
| `network_probe_prober_count` *(Phase 13)* | GaugeVec | name, target, type — seçilen prober sayısı |
| `network_probe_inventory_peers` *(Phase 13)* | Gauge | (label'sız) — candidate set'te görünen peer sayısı |
| `network_probe_slo_uptime_ratio` *(P1.4)* | GaugeVec | target_id, window — gerçek uptime oranı (0.0–1.0) |
| `network_probe_slo_error_budget_seconds` *(P1.4)* | GaugeVec | target_id, window — kalan hata bütçesi saniye (negatif = ihlal) |
| `network_probe_slo_breached` *(P1.4)* | GaugeVec | target_id — 1=SLO ihlali aktif, 0=normal |
| `network_probe_config_drift` *(P1.5)* | Gauge | (label'sız) — 1=en az bir peer farklı config hash'e sahip, 0=tümü senkron |
| `network_probe_geo_latency_seconds` *(P1.6)* | GaugeVec | name, target, type, region — bölge bazlı son probe latency'si |
| `network_probe_geo_latency_anomaly` *(P1.6)* | GaugeVec | name, target, type — 1=herhangi bir node'un latency'si min'in 3×'inden fazla |

---

## Config Extensions (Tamamlanan Aşamalar Özeti)

### Phase 13 — Distributed Probe Ownership ✅

```yaml
cluster:
  zone: "istanbul"                  # opsiyonel; memberlist NodeMeta üzerinden dağıtılır
  probe_replication_factor: 3       # opsiyonel, default 3; ≤ candidate → hepsi probe eder

targets:
  - id: "db-restricted"
    type: "tcp"
    target: "10.0.0.5:5432"
    probe_from: ["node-fr", "node-tr"]  # opsiyonel; dolu ise sadece bu node'lar candidate olur
```

- 5sn debounce `recomputeProberAssignments` → `StartProbing`/`StopProbing` callback'leri
- 3-tier zone-aware picker: Tier-1 zone diversity, Tier-2 zone repeat, Tier-3 zone-less fallback
- Bootstrap broadcast: Init + Reload sonrası her local target için 1 `GossipPayload`

### P1.3 — Scope Intelligence ✅

Alert env değişkenleri enriched:
- `CLASSIFICATION`: REAL_OUTAGE / NETWORK_PARTITION / LOCAL_FAILURE / AMBIGUOUS
- `CONFIDENCE`: "0.00"–"1.00"
- `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES`: virgülle ayrılmış node adları

`/fleet/status` endpoint'i `classification` + `confidence` alanlarını per-target içerir.

### P1.4 — SLO Tracker ✅

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops"]
  targets:
    - id: "db-primary"
      target_uptime: 0.999   # 99.9%
      window: "30d"          # 30d | 7d | 24h
```

- `incidents.json`: state.json ile aynı dizinde; restart sonrası açık incident'lar yeniden açılır
- `/slo` endpoint: per-target uptime ratio, error budget, breach status, incident history
- 3 Prometheus metriği: `network_probe_slo_uptime_ratio`, `network_probe_slo_error_budget_seconds`, `network_probe_slo_breached`

### P1.5 — Gossip Config Sync ✅

```yaml
cluster:
  config_sync:
    enabled: true
    mode: "drift_detection"   # "drift_detection" (default, safe) | "auto_sync" (reserved)
    sync_interval_sec: 30     # default; min 5
    primary_node: ""          # sadece auto_sync modunda kullanılır
```

- Her node `config.yaml` ham baytlarının SHA-256[:16] parmak izini yayıyor
- `GET /cluster/config`: self hash + peer hash listesi + drift count
- `network_probe_config_drift` Prometheus metriği: 1=drift var, 0=tümü senkron
- `cluster.config_sync.enabled: false` (default) → tüm gossip ve metrik no-op

### P1.6 — Geo Latency View ✅

```yaml
cluster:
  region: "eu-west"   # node-level coğrafi etiket (zone'dan ayrı)

targets:
  - id: "api-gw"
    probe_from_regions: ["eu-west", "us-east"]   # sadece bu bölgeler probe eder
```

- Başarılı her probe'da `GossipPayload.Latency` doluyor (elapsed saniye)
- `GET /geo/latency/{targetID}`: per-node latency + region label + anomaly flag
- Anomaly: en az 2 non-zero değer + max > 3×min
- `network_probe_geo_latency_seconds` (labels: name, target, type, region)
- `network_probe_geo_latency_anomaly` (labels: name, target, type)
