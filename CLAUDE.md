# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## Proje Özeti

**Uygulama adı:** `netwatch` (`github.com/saidtaylan/netwatch`)
**Konum:** `/Users/saidtaylan/Documents/network cluster/`
**Go versiyonu:** 1.25.7
**Amaç:** Tek-node Prometheus exporter olarak başlayan ağ izleme ajanını, gossip protokolü ile haberleşen, quorum bazlı karar veren dağıtık bir cluster monitoring sistemine dönüştürmek.

---

## Repo Yapısı

Tek git repo, iki servis:

```
network cluster/
  backend/        ← Go kodu (github.com/saidtaylan/netwatch)
  frontend/       ← Nuxt 3 SPA
  deploy-systemd/ ← systemd .service + .target + install.sh
  Makefile        ← Kök Makefile (her iki servisi orchestrate eder)
  developments.md, system_map.md, sprint.md, todo.md, CLAUDE.md   ← Paylaşılan
```

---

## Kök Makefile Komutları

```bash
make build          # backend (linux-amd64) + frontend (.output/) birlikte build
make test           # backend go test -race + frontend pnpm test
make lint           # go vet
make clean          # backend bin/ + frontend .output/.nuxt temizle
make install        # sudo: systemd servisleri kur (Linux)
```

---

## Build Komutları — Backend

```bash
cd backend   # tüm Go komutları buradan çalışır

# Sadece macOS/Linux'ta derle (Windows cmd'i platform kısıtlı)
go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/

# Makefile üzerinden (tercih edilen):
make build-linux          # backend/bin/netwatch-linux-amd64 üretir
make test                 # go test -race
make vet                  # go vet
make all                  # build-linux + test + vet

# Binary üret (geçici test için)
go build -o /tmp/netwatch-test ./cmd/linux/

# Çalıştır
./netwatch-test -config config.yaml

# Test + race detector
go test -race ./internal/engine/... ./internal/cluster/...

# Vet
go vet ./internal/engine/ ./internal/cluster/ ./cmd/linux/
```

**Önemli:** `go build ./...` macOS'ta başarısız olur çünkü `cmd/windows/` yalnızca Windows'ta derlenen `golang.org/x/sys/windows/svc` paketini import eder. Her zaman `./internal/engine/ ./internal/cluster/ ./cmd/linux/` hedefleri kullan.

---

## Build Komutları — Frontend

```bash
cd frontend   # tüm Node komutları buradan çalışır

pnpm dev              # Geliştirme sunucusu (localhost:3000)
pnpm build            # .output/ üretir
pnpm preview          # Production build önizleme
pnpm lint             # ESLint
pnpm test             # Vitest unit tests
```

---

## Paket Yapısı — Backend (`backend/`)

```
backend/
internal/engine/          ← business logic paketi
  protocol.go             # Checker interface (Run, ValidateOptions, ParseAddr)
  engine.go               # Config, Engine struct, state persistence, hot-reload, metrikler
  loop.go                 # Per-target probe goroutine, retry loop, state machine + broadcastState()
  notify.go               # Alerter interface, kanal yönlendirme, sendAlert
  app.go                  # App struct, AppTargetIndex, buildAppTargetIndex, validateApps
  topology.go             # DependencyGraph, buildDependencyGraph, FindRootCause, CascadingImpact, TopologySnapshot
  fleet.go                # FleetSnapshot — rich /fleet/status: per-target detail, scope, apps, root cause, incidents
  scope.go                # DetailedScope, classifyScope — REAL_OUTAGE / NETWORK_PARTITION / LOCAL_FAILURE / AMBIGUOUS
  slo.go                  # SLOConfig, sloManager, incidents.json, ComputeSLO, runSLOChecker, /slo endpoint data
  webhook.go              # WebhookAlerter (generic + alertmanager format)
  watchdog.go             # Prometheus scrape watchdog goroutine + NotifyScrape()
  mail.go                 # SMTP alerter (multipart/alternative, HTML body)
  http.go                 # HTTP/HTTPS Checker
  tcp.go                  # TCP Checker
  ping.go                 # ICMP ping Checker (CAP_NET_RAW gerektirir)
  dns.go                  # DNS Checker
  sql.go                  # SQL Checker (oracle/mysql/postgres/mssql)

internal/cluster/         ← gossip cluster paketi (Phase 6–9)
  cluster.go              # Config, GossipPayload, AntiEntropyProvider, Manager, hash ring, quorum
  testhelpers.go          # NewTestManager, SetIsolated, SetPeerState

cmd/linux/main.go         # Signal handler, HTTP server, /metrics /health /status /cluster/state
cmd/windows/main.go       # Windows Service entegrasyonu

notifications/            # Alert scriptleri buraya konur (.sh veya .ps1)
config.yaml               # Canlı config (sample — içinde açıklamalar var)
```

**KRİTİK KARAR (backend):** Backend yalnızca **iki dizin** üzerine kurulu: `internal/engine/` + `internal/cluster/`. Hiçbir başka alt-paket oluşturulMAYACAK.

---

## Paket Yapısı — Frontend (`frontend/`)

```
frontend/
  nuxt.config.ts           # ssr:false (SPA), runtimeConfig (NUXT_PUBLIC_BACKEND_URL)
  pages/                   # File-based routing
    index.vue              # Cluster overview (landing)
    setup.vue              # İlk giriş: backend URL + admin token
    login.vue              # Token re-entry
    targets/index.vue      # Target list
    targets/[id].vue       # Target detail (by-node, scope, deps, geo)
    topology.vue           # Dependency graph
    slo.vue                # SLO dashboard
    apps.vue               # Apps & teams
    alerts.vue             # Alert feed (in-memory, B7 öncesi)
    maintenance.vue        # Maintenance CRUD
    silences.vue           # B1 placeholder
    config/index.vue       # Cluster config view + drift
    config/push.vue        # PUT /cluster/config form
    config/keyring.vue     # Keyring rotate
    geo.vue                # Per-region latency
    settings/index.vue     # UI prefs
    settings/nodes.vue     # Backend node listesi
  layouts/
    default.vue            # Sidebar + topbar
    auth.vue               # Setup/login minimal layout
  components/              # common/ cluster/ targets/ topology/ slo/ maintenance/ config/
  composables/             # useApi, useAuth, useNodeConnection, usePolling, useTargets, useCluster, …
  stores/                  # auth.ts, nodes.ts (persisted), ui.ts, alerts.ts
  middleware/
    auth.global.ts         # Token yoksa /setup'a yönlendir
    node-health.global.ts  # Failover tetikler
  types/api.ts             # Backend response TS tipleri (FleetSnapshot, ClusterState, …)
  utils/                   # format.ts, classifyState.ts, matchers.ts
```

**Multi-node failover:** `stores/nodes.ts` birden fazla backend URL tutar. `useNodeConnection.ts` başlangıçta `Promise.any` ile en hızlı cevap vereni seçer; aktif node 401/network error verirse otomatik fallback.

**Auth akışı:** Token yoksa `auth.global.ts` → `/setup`. Token `POST` ile check edilir (`GET /auth/whoami`). Başarılıysa Pinia+localStorage'a yazılır. Her API çağrısına `Authorization: Bearer` eklenir. 401 → otomatik logout.

---

## 🚨 Frontend Routing Kuralı — Named Routes Şart

**KURAL:** Tüm `NuxtLink`, `navigateTo`, ve `router.push` çağrılarında **named route** kullanılır. String path **YASAK**.

### Neden?

Path değişirse (örn. `/targets/[id]` → `/services/[id]`), string referansları her dosyada tek tek aramak gerekir. Named route ile sadece dosya adı değişir, name'i kullanan yerler değişmez. Refactor güvencesi.

### Nuxt 4 file-based routing → otomatik isim üretimi

| Dosya | Auto-generated `name` |
|---|---|
| `app/pages/index.vue` | `index` |
| `app/pages/setup.vue` | `setup` |
| `app/pages/targets/index.vue` | `targets` |
| `app/pages/targets/[id].vue` | `targets-id` |
| `app/pages/config/index.vue` | `config` |
| `app/pages/config/push.vue` | `config-push` |
| `app/pages/config/keyring.vue` | `config-keyring` |
| `app/pages/settings/index.vue` | `settings` |
| `app/pages/settings/nodes.vue` | `settings-nodes` |

### ✅ Doğru kullanım

```vue
<!-- NuxtLink with route object -->
<NuxtLink :to="{ name: 'targets' }">Targets</NuxtLink>
<NuxtLink :to="{ name: 'targets-id', params: { id: targetId } }">Detail</NuxtLink>
<NuxtLink :to="{ name: 'config-push' }">Push Config</NuxtLink>

<!-- navigateTo with route object -->
navigateTo({ name: 'setup' })
navigateTo({ name: 'targets-id', params: { id: 'db-primary' } })
```

### ❌ Yasak

```vue
<!-- String path — YASAK -->
<NuxtLink to="/targets">Targets</NuxtLink>
<NuxtLink :to="`/targets/${encodeURIComponent(id)}`">Detail</NuxtLink>
navigateTo('/setup')
navigateTo(`/targets/${id}`)
```

### Tek istisna: External URL'ler

```vue
<a :href="externalUrl">External</a>     <!-- external URL'ler için OK -->
```

### Test kuralları

Playwright e2e testlerinde de path yerine name veya `route().name` kullanılabilir, ama URL assertion'ları `expect(page).toHaveURL(...)` path olabilir (browser'ın gerçek URL'sini test ediyoruz).

---

## Persistent Store Kararı

Pinia + `pinia-plugin-persistedstate` ile **3 store kalıcı:**

| Store | İçerik | Neden persist? |
|---|---|---|
| `auth` | token + role | F5'te tekrar giriş yapmasın (Kibana/Grafana benzeri) |
| `nodes` | backend URL listesi + aktif node + health | Kullanıcı her seferinde URL girmesin |
| `ui` | `pollingIntervalMs`, `sidebarCollapsed`, theme (`color-mode`) | Kullanıcı tercihleri kalıcı |

**Persist edilmeyen:** `alerts` (in-memory ring buffer, sayfa kapanınca silinmesi mantıklı — B7 backend hazır olunca `/alerts` endpoint'ine geçecek).

**Alternatifler değerlendirildi:**
- Cookie + backend session: Şu an gereksiz karmaşıklık. LDAP geldiğinde değerlendirilir.
- IndexedDB: localStorage yeterli; veri küçük.

**Karar sabittir** — değişmeyecek.

---

## HTTP Endpointleri

| Endpoint | Açıklama |
|---|---|
| `GET /metrics` | Prometheus — her çağrıda `e.NotifyScrape()` tetiklenir (watchdog için) |
| `GET /health` | Liveness check — her zaman `200 OK` döner |
| `GET /status` | Tüm target'ların JSON durumu: name, status, seq, error_code |
| `GET /cluster/state` | Cluster üyeleri + peer target durumları (raw); cluster kapalıysa 503 |
| `GET /cluster/probers` | **Phase 13:** Her target için seçilen prober subset + primary + candidate seti + `probe_from` constraint'i + zone'larla üye listesi |
| `GET /fleet/status` | Rich engine-level fleet view: per-target consensus state, scope, **classification** (REAL_OUTAGE/NETWORK_PARTITION/LOCAL_FAILURE/AMBIGUOUS), **confidence**, by-node breakdown, affected apps, root cause, cascading impact, active incidents. Standalone modda da çalışır (cluster=nil). |
| `GET /topology` | Target dependency graph (depends_on ilişkileri): her target için direct deps, reverse deps, transitive cascading impact. |
| `GET /slo` | SLO metrics: per-target uptime ratio, error budget, incident history, breach status. `slo.enabled: false` ise 503. |
| `GET /cluster/config` | **P1.5:** Config-sync snapshot — this node's hash + peer hashes + drift count; cluster kapalıysa 503. |
| `PUT /cluster/config` | Kısmi SharedConfig (JSON/YAML body) al, kendine uygula + tüm peer'lara gossip TCP ile dağıt. Auth gerektirir (`admin.token` ayarlıysa). |
| `POST /cluster/config/sync` | Body yok. Bu node'un diskindeki shared field'larını peer'lara dağıt. Auth gerektirir. |
| `GET /geo/latency/{targetID}` | **P1.6:** Per-node latency view: region labels, last probe latency, anomaly flag (max > 3× min). |
| `GET /fleet/status?format=text` | Terminal-friendly ASCII tablo: cluster başlığı, summary, incidents, per-target state/scope/classification. |
| `GET /slo?format=text` | Terminal-friendly ASCII tablo: per-target TARGET%, ACTUAL%, STATUS, BUDGET REMAINING. |
| `GET /cluster/keyring/rotate` | Keyring durumu: key sayısı + primary prefix. |
| `POST /cluster/keyring/rotate` | Sıfır-kesinti AES key rotasyonu: `{"action":"add|use|remove","key":"base64..."}`. |
| `POST /cluster/leave` | Graceful cluster leave + process exit |
| `GET /cluster/maintenance` | Aktif maintenance window listesi |
| `PUT /cluster/maintenance` | Yeni maintenance window; gossip ile tüm node'lara yayılır; auth gerekir |
| `DELETE /cluster/maintenance/{id}` | Maintenance window iptal; auth gerekir |

---

## Metrikler

Eski isimler (`netwatch_probe_*`) kaldırıldı, direkt yeni isimlere geçildi:

| Metrik | Açıklama |
|---|---|
| `network_probe_local_status` | Bu node'da son probe sonucu: 1=UP, 0=DOWN |
| `network_probe_local_latency_seconds` | Son probe round-trip süresi (saniye) |
| `network_probe_prometheus_connected` | 1=scraping normal, 0=watchdog threshold aşıldı |
| `network_probe_cluster_status` *(cluster)* | Konsensüs sonucu — tüm node'lar up görüyorsa 1, herhangi node down ise 0 |
| `network_prober_quorum_healthy` *(cluster)* | 1=quorum tamam, 0=quorum kaybı |
| `network_prober_isolated` *(cluster)* | 1=izole mod (alarm suppressed), 0=normal |
| `network_prober_cluster_size` *(cluster)* | Bu node'un gördüğü alive üye sayısı |
| `network_probe_local_assigned` *(Phase 13)* | 1=bu node target'ı probe ediyor, 0=etmiyor (cluster atadı başkasına) |
| `network_probe_prober_count` *(Phase 13)* | Bu target için cluster'da seçilen toplam prober sayısı |
| `network_probe_inventory_peers` *(Phase 13)* | Gossip ile keşfedilen peer sayısı |
| `network_probe_target_orphaned` *(zone-safety)* | 1=cluster'da hiçbir node target'ı probe etmiyor; 0=en az bir prober var. `probe_from`/`probe_from_regions`/`cluster.zone` yanlışsa tetiklenir. Edge-triggered log eşlik eder. Labels: `name`, `target`, `type` |
| `network_probe_slo_uptime_ratio` *(SLO)* | Gerçek uptime oranı window içinde (0.0–1.0). Labels: `target_id`, `window` |
| `network_probe_slo_error_budget_seconds` *(SLO)* | Kalan error budget saniye cinsinden (negatif = ihlal). Labels: `target_id`, `window` |
| `network_probe_slo_breached` *(SLO)* | 1=SLO ihlali aktif, 0=budget dahilinde. Label: `target_id` |
| `network_probe_config_drift` *(P1.5)* | 1=en az bir peer farklı config hash'e sahip, 0=tümü senkron. Label'sız. |
| `network_probe_geo_latency_seconds` *(P1.6)* | Per-region son probe latency. Labels: `name`, `target`, `type`, `region`. |
| `network_probe_geo_latency_anomaly` *(P1.6)* | 1=herhangi bir node latency'si min'in 3×'inden fazla. Labels: `name`, `target`, `type`. |

Label'lar (`local_status`, `local_latency_seconds`, `cluster_status`): `name`, `target`, `type`, `source_host`, `app_name`
Label'lar (`local_assigned`, `prober_count`): `name`, `target`, `type` (ownership = host/app'tan bağımsız)
Diğerleri label'sız.

`network_probe_prometheus_connected` `watchdog_threshold_sec: 0` (varsayılan) olduğunda daima 1 kalır.
Cluster metrikleri yalnızca `cluster.enabled=true` iken `RegisterClusterMetrics` aracılığıyla kaydedilir — disabled durumda registry'de görünmez.
SLO metrikleri yalnızca `slo.enabled=true` iken `RegisterSLOMetrics` aracılığıyla kaydedilir.

---

## Config Schema (Önemli Alanlar)

```yaml
port: "10240"
node_alias: "my-agent"         # eski "app_name" backward compat ile okunur, deprecated
state_file: "state.json"
log_path: "prober.log"        # boş bırakılırsa stdout
timeout: 5
max_retries: 2
retry_interval_sec: 30
ticker_interval_sec: 5
probe_interval_sec: 60        # per-target interval_sec ile override edilebilir
reload_interval_sec: 30       # 0 = hot-reload kapalı
recovery_probes: 1            # hard_down → recovery için gereken ardışık başarı (default 1, SharedConfig'e eklendi)
watchdog_threshold_sec: 120   # scrape bu kadar sn gelmezse [WATCHDOG] log + metrik=0; 0 = devre dışı
credentials_file: "credentials.env"  # ${VAR} injection için

# Admin auth — write-capable endpoint'ler için opsiyonel bearer token
admin:
  token: "${ADMIN_TOKEN}"  # boşsa endpoint'ler açık (default)

notifications:
  kanal-adi:
    type: "script"            # script | mail | webhook
    parameters: { KEY: "val" }

  # Webhook örneği:
  # my-hook:
  #   type: "webhook"
  #   parameters:
  #     url: "https://alertmanager.company.local/api/v2/alerts"
  #     format: "alertmanager"     # generic (varsayılan) | alertmanager
  #     timeout_sec: "10"
  #     tls_insecure: "false"
  #     header_Authorization: "Bearer token"   # header_<İsim> → custom header
  #     username: "user"           # HTTP Basic Auth (opsiyonel)
  #     password: "pass"

default_notify: ["kanal-adi"]

targets:
  - id: "stable-id"           # opsiyonel; App.Uses buna referans verir
    type: "tcp"               # tcp | http | ping | dns | sql
    target: "host:port"
    name: "display-name"
    notify: ["kanal-adi"]     # opsiyonel; yoksa default_notify
    options: {}               # tip'e özgü, json.RawMessage olarak saklanır
    depends_on:               # opsiyonel; root-cause detection için bağımlılık listesi
      - "other-target-id"     # cyclic refs ve bilinmeyen ID'ler config yükleme hatası verir

apps:                         # opsiyonel; yoksa eski davranış korunur
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["stable-id"]       # target id veya name
    notifications: ["dba"]    # target.notify ile UNION alınır

cluster:
  enabled: false                  # true → cluster aktif
  node_name: "node-1"             # zorunlu; cluster içinde benzersiz olmalı
  bind_addr: "0.0.0.0"
  bind_port: 7946                 # varsayılan; TCP+UDP gossip portu
  advertise_addr: "192.168.1.100" # NAT/container arkasındaysa ayarla
  peers:
    - "192.168.1.101:7946"        # seed node'lar; best-effort, hepsi down olsa sorun değil
  keyring:                        # base64(AES-128/192/256 key); ilki şifreler, tümü çözer
    - "base64encodedkey32byteslong=="
  expected_node_count: 3          # Phase 7 quorum hesabı için
  min_quorum_ratio: 0.5           # Phase 7: varsayılan basit çoğunluk
  zone: "istanbul"                # Phase 13: opsiyonel; node-level label, hostname'den türetilmez
  probe_replication_factor: 3     # Phase 13: per-target prober cap; varsayılan 3
  min_probe_confirmations: 2      # opsiyonel; N prober hard_down görmeden alarm atılmaz; varsayılan 0 (=1)
```

**Phase 13 — Distributed Probe Ownership:**

**Probe Interval Staggering:** N prober'lı bir target'ta offset = `(probe_interval / N) * prober_index`. Burst yok; mean detection latency ≈ probe_interval/N. Standalone'da offset=0.

Cluster modda her node her target'ı probe etmez. `probe_replication_factor` (default 3) kadar node seçilir:
- Candidate set = peerStates'ten gelen + local LocalTargetProvider'dan (alive olanlar)
- Hash ring (FNV-32a) ile deterministic sıralama
- 3-tier zone-aware picker: (1) farklı zone'lardan birer, (2) zone-tagged repeat, (3) zone-less son tercih
- Operatör hiçbir şey yazmaz → tam otomatik; isterse `target.probe_from: [n1,n2]` ile manuel pin (Active Probe Delegation)

Target başına opsiyonel `probe_from`:

```yaml
targets:
  - id: "iran-vpn"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "istanbul-1"]   # sadece bu iki node probe eder
```

> **Kontrat:** Aynı target'ı taşıyan tüm node'lar aynı `probe_from` listesini deklare etmelidir; aksi takdirde candidate set node'lar arasında farklılaşır ve exactly-once garantisi bozulur.

**SLO Tracker:**

```yaml
slo:
  enabled: true
  retention_days: 90        # incidents.json'dan bu kadar gün öncesi silinir
  slo_notify: ["ops"]       # breach alertleri için; boşsa default_notify kullanılır
  targets:
    - id: "db-primary"
      target_uptime: 0.999  # 99.9%
      window: "30d"         # 30d veya 7d veya 24h
    - id: "api-gateway"
      target_uptime: 0.9995
      window: "7d"
```

- `incidents.json` dosyası `state_file` ile aynı dizinde oluşturulur
- Açık incident'lar (EndedAt=nil) restart sonrası otomatik yeniden açılır
- Breach alert edge-triggered'dır: bir SLO döneminde tek bir alert atılır
- Breach düzeldiğinde `breachAlerted` flag temizlenir (bir sonraki ihlalde yeniden alert gider)

**KRİTİK:** `Target.Options` asla düz alanlara dönüştürülmez. `json.RawMessage` olarak saklanır; her Checker kendi parse fonksiyonunu çağırır. Bu HTTP `expected_status` operatörleri, DNS `expected_ips`, SQL `query` gibi zengin opsiyonları korur.

---

## State Machine

```
UNKNOWN → SOFT_DOWN (pending map, RAM) → HARD_DOWN (lastKnown, disk)
                                       ↑
                     probe başarısız, max_retries dolmadı
HARD_DOWN / SOFT_DOWN → UP: markRecovered() çağrılır, Seq++
UP → SOFT_DOWN: enqueue() çağrılır
SOFT_DOWN → HARD_DOWN: markHardDown() çağrılır, Seq++
```

**state.json v2 formatı** (Phase 3 sonrası):
```json
{
  "version": 2,
  "targets": {
    "core-db": {
      "state": "hard_down",
      "seq": 3,
      "error_code": "dial tcp: connection refused",
      "owner_node": ""
    }
  }
}
```

- **v1 format** (plain `map[string]bool`) otomatik migrate edilir ve v2 olarak yeniden yazılır.
- `Seq` her state geçişinde (hard_down veya recovery) `stateMu` altında increment edilir.
- `OwnerNode` şu an boş (standalone mod); cluster katmanında sorumlu node adı yazılacak.
- `state.json` atomik yazılır: önce `.tmp` dosyasına, sonra `os.Rename`.

---

## Notification Kanalı Seçimi

Bir target down olduğunda kanal seçimi şu öncelik sırasına göre yapılır:

1. `union(target.Notify, app.Notifications for her app referencing t)` — deduplicated
2. Hiçbiri yoksa → `cfg.DefaultNotify`
3. Hiçbiri kalmadıysa → sessiz (info log)

Alert env değişkenleri (script, mail ve webhook'un tümü alır):

| Değişken | Açıklama | Her zaman mı? |
|---|---|---|
| `NAME` | target name | ✓ |
| `TARGET` | host:port veya URL | ✓ |
| `HOST`, `PORT` | parse edilmiş adres bileşenleri | ✓ |
| `APP_NAME` | agent'ın `node_alias` değeri (eski ad, korundu) | ✓ |
| `NODE_ALIAS` | agent'ın `node_alias` değeri | ✓ |
| `NODE_NAME` | `os.Hostname()` çıktısı | ✓ |
| `STATUS` | `unreachable` veya `reachable` | ✓ |
| `TYPE` | tcp / http / ping / dns / sql | ✓ |
| `SEQ` | Lamport seq — her state geçişinde artar | ✓ |
| `ERROR_CODE` | son probe hata metni; recovery'de boş | ✓ |
| `AFFECTED_APPS` | virgülle ayrılmış app isimleri | apps varsa |
| `OWNER_TEAMS` | virgülle ayrılmış takım isimleri | apps varsa |
| `ROOT_CAUSE` | root cause target ID; zincir varsa en derin down bağımlılık | depends_on varsa + unreachable |
| `CASCADING_IMPACT` | bu target down kalırsa etkilenecek target ID'leri (virgülle) | depends_on varsa + unreachable |
| `DEPENDENCY_DEPTH` | root cause'dan bu target'a hop mesafesi (0=root) | depends_on varsa + unreachable |
| `SCOPE` | GLOBAL \| PARTIAL \| NODE_LOCAL \| STANDALONE | ✓ |
| `CLASSIFICATION` | REAL_OUTAGE \| NETWORK_PARTITION \| LOCAL_FAILURE \| AMBIGUOUS | ✓ |
| `CONFIDENCE` | 0.00–1.00 ondalık (ne kadar kesin olduğu) | ✓ |
| `DOWN_NODES` | hard_down gören node adları (virgülle) | cluster modda + down |
| `UP_NODES` | up gören node adları (virgülle) | cluster modda + down |
| `OFFLINE_NODES` | cevap vermeyen alive node'lar | cluster modda, varsa |

**SLO breach alertlerinde** ek env değişkenleri (`STATUS=slo_breached` ile gönderilir):

| Değişken | Açıklama |
|---|---|
| `SLO_TARGET_UPTIME` | hedef uptime (örn. "0.9990") |
| `SLO_ACTUAL_UPTIME` | gerçekleşen uptime (örn. "0.9981") |
| `SLO_WINDOW` | ölçüm penceresi (örn. "30d") |
| `SLO_DOWNTIME_MINUTES` | toplam downtime dakika cinsinden |
| `SLO_INCIDENT_COUNT` | penceredeki toplam olay sayısı |
| `SLO_ERROR_BUDGET_SEC` | kalan hata bütçesi saniye (negatif=ihlal) |
| `SLO_LONGEST_INCIDENT_SEC` | en uzun olay süresi saniye |

---

## App → Target Indirection

`app.go` dosyasında tanımlı. `App` struct:
```go
type App struct {
    Name          string   `json:"name"`
    OwnerTeam     string   `json:"owner_team,omitempty"`
    Uses          []string `json:"uses"`          // target id veya name
    Notifications []string `json:"notifications,omitempty"`
}
```

`buildAppTargetIndex(cfg)` → `map[targetKey][]*App` index'i üretir.
Engine `e.appIndex` field'ında tutar, `e.mu` ile korunur.
`sendAlert()` her target için bu index'e bakar.

---

## Tamamlanan Aşamalar

### ✅ Phase 0 — Sabit Kararlar
- Metrik isimleri direkt değiştirildi (alias yok)
- Modül path korundu: `github.com/saidtaylan/netwatch`
- Yapı: `internal/engine/` + `internal/cluster/` (ileride)

### ✅ Phase 1 — Metrik Yeniden Adlandırma
- `netwatch_probe_up` → `network_probe_local_status`
- `netwatch_probe_duration_seconds` → `network_probe_local_latency_seconds`
- `engine.go` içinde `GaugeUp` ve `GaugeDuration` değişkenleri güncellendi

### ✅ Phase 2 — App→Target Indirection
- `Target.ID string` (opsiyonel) eklendi; `key()` metodu ID varsa onu, yoksa Name döner
- `Config.Apps []App` eklendi
- `engine/app.go` dosyası oluşturuldu: `App`, `AppTargetIndex`, `buildAppTargetIndex`, `validateApps`
- `Engine.appIndex` field'ı eklendi
- `sendAlert()` `AFFECTED_APPS` + `OWNER_TEAMS` env değişkenlerini inject ediyor
- `mergeNotifyChannels()` + `buildAppContext()` helper'ları `notify.go`'ya eklendi
- Smoke test: `AFFECTED_APPS=payment-gateway,inventory-api`, `OWNER_TEAMS=fintech-sre,logistic-dev` ✓

### ✅ Phase 3 — Lamport-aware State Machine
- `PersistedState` struct: `State`, `Seq`, `ErrorCode`, `OwnerNode`
- `stateFileV2` envelope ile v2 state.json formatı
- `lastKnown map[string]bool` → `map[string]PersistedState`
- `loadPersistedState()` v1→v2 otomatik migration (+ anında yeniden yazar)
- `markHardDown(pkey, t, errCode)` — Seq++, ErrorCode saklar
- `markRecovered(pkey, t)` — Seq++, ErrorCode temizler
- `PendingEntry.LastErrorCode` eklendi
- `StatusSnapshot` artık `Seq` ve `ErrorCode` içeriyor → `/status` endpoint'inde görünür
- Smoke test: `/status` → `seq: 1`, `error_code: "..."` ✓

### ✅ Phase 4 — Webhook Notification Kanalı
- `internal/engine/webhook.go` yeni dosya: `WebhookAlerter`
- `format: generic` — tek JSON objesi (name, target, host, port, seq, error_code, affected_apps, owner_teams, fired_at)
- `format: alertmanager` — Prometheus Alertmanager v2 `/api/v2/alerts` uyumlu array; recovery'de `endsAt=now` + `alertName="ProbeUp"`
- Custom headers: `header_<İsim>` parametresi
- HTTP Basic Auth: `username` / `password` parametresi
- TLS skip: `tls_insecure: "true"` parametresi
- `notify.go` → `newAlertChannel()` webhook case'i eklendi
- **Env enrichment:** Tüm kanallar (script, mail, webhook) artık `SEQ`, `ERROR_CODE`, `NODE_NAME` alıyor
- `mail.go` → `multipart/alternative` format; HTML part'ta affected apps `<table>` içeriyor
- Smoke test: generic + alertmanager POST, `affected_apps` + `owner_teams` + `seq` dahil ✓

### ✅ Phase 5 — Watchdog
- `internal/engine/watchdog.go` yeni dosya: `runWatchdog(ctx)` goroutine
- `Config.WatchdogThresholdSec *int` alanı eklendi (`watchdog_threshold_sec: 0` = devre dışı)
- `Engine.lastScrapeNano atomic.Int64` — `NotifyScrape()` metodu ile `/metrics` handler'dan güncellenir
- `cmd/linux/main.go` → `/metrics` handler'ı `e.NotifyScrape()` çağrısıyla sarmalandı
- `network_probe_prometheus_connected` gauge eklendi (1=OK, 0=threshold aşıldı)
- Goroutine threshold/3 aralıklarla kontrol eder (min 5s); ilk scrape gelene kadar sessiz kalır
- **Önemli:** Probu veya alertleri etkilemez — yalnızca "Prometheus körleşti" uyarısı verir
- Smoke test: 8s threshold, 12s gap → `[WATCHDOG]`, scrape resume → `[PROMETHEUS]` ✓

### ✅ Phase 6 — Cluster Layer
- `internal/cluster/cluster.go` yeni paket: `Config`, `GossipPayload`, `Manager`
- `memberlist v0.5.4` bağımlılığı eklendi; `gossipDelegate`, `eventDelegate`, `broadcast`
- `Broadcast` (UDP) + `BroadcastReliable` (TCP); `OnStateReceived` Lamport merge; `Snapshot` → `/cluster/state`
- `engine.go`: `Engine.clusterMgr`; `Init()` cluster başlatır; `Shutdown()` `Leave` çağırır

### ✅ Phase 7 — Quorum + Isolated Mode
- `cluster.go`: `checkQuorum()`, `runQuorumLoop()` (5sn), `IsolatedMode()`, `startAntiEntropy()` placeholder
- `engine.go`: `GaugeQuorumHealthy`, `GaugeIsolated`, `GaugeClusterSize`; `runClusterMetricsUpdater` (5sn)

### ✅ Phase 8 — Consistent Hashing + Exactly-Once Alerting
- `cluster.go`: `updateRing()`, `hashTarget()` (FNV-32a), `GetResponsibleNode()`, `IsResponsible()`, `PeerStatesForTarget()`
- `engine.go`: `shouldAlert()`, `computeScope()`, `GaugeClusterStatus` (`network_probe_cluster_status`)
- `loop.go`: `runCheck` + `processPending` → `shouldAlert()` ile alarm kapısına bağlandı
- `notify.go`: `SCOPE` env değişkeni eklendi
- Test: `cluster_test.go`, `phase8_test.go`, `testhelpers.go`
- **Bug fix (post-demo):** `IsResponsible()` artık sadece PRIMARY'yi kontrol ediyor (önceden primary+secondary → çift alarm). Secondary kavramı kaldırıldı; primary öldüğünde `NotifyLeave → updateRing()` ile bir sonraki node otomatik primary oluyor.
- **Bug fix (post-demo):** `scriptAlerter.Send()` artık config'deki `script` parametresini kullanıyor; yoksa `alertScriptsDir()+channelName` fallback'i geçerli.

### ✅ Phase 9 — Anti-Entropy
- `cluster.go`: `AntiEntropyProvider` interface; `Manager.stateProvider`; `SetStateProvider()`; `LocalState(join)` + `MergeRemoteState(join)` join-aware
- `engine.go`: `Engine.syncing atomic.Bool`; `FullState()`, `ApplyRemoteState()`, `SetSyncing()` (AntiEntropyProvider impl); `Init()` → `SetStateProvider(e)`
- `loop.go`: `runCheck` + `processPending` → `syncing.Load()` early-return guard
- Test: `antientropy_test.go`, `phase9_test.go`

### ✅ Primary-Forwards-Peer-Alert (post-Phase-9 bug fix)

**Sorun:** Farklı node'ların farklı target listelerine sahip olduğu senaryoda (veya hash ring'in primary'si bir target'ı config'inde bulundurmuyorsa) alarm hiç atılmıyordu. Non-primary node suppressed, primary ise lokal probe olmadığından `processPending` çağrısına hiç girmiyordu.

**Düzeltme:**

- `cluster.go`:
  - `GossipPayload`'a `TargetName string` ve `TargetType string` eklendi — probing node bunları dolduruyor, primary düzgün alert mesajı oluşturabiliyor
  - `PeerAlertHandler` interface: `HasLocalProbe(targetID string) bool` + `DispatchPeerAlert(payload GossipPayload)`
  - `Manager.peerAlertHandler PeerAlertHandler` + `Manager.peerAlerted map[string]uint64` (duplicate suppression)
  - `OnStateReceived`: accept sonrası eğer primary AND hard_down AND quorum sağlıklı AND seq > peerAlerted → `go handler.DispatchPeerAlert(p)` (sadece `!HasLocalProbe` ise)
  - `SetPeerAlertHandler(h)` metodu eklendi

- `engine.go`:
  - `Engine.localProbeIDs map[string]bool` — Init ve hot-reload'da doldurulur
  - `HasLocalProbe(targetID)` — `cluster.PeerAlertHandler` impl
  - `DispatchPeerAlert(p)` — gossip payload'dan env map inşa eder, `default_notify` + apps enrichment, `e.channels` üzerinden alert gönderir; `syncing` ise suppress
  - `Init()` → `SetPeerAlertHandler(e)`
  - `ApplyRemoteState` için `broadcastStateByID` helper eklendi (sadece ID bilinirken kullanılır)

- `loop.go`:
  - `broadcastState(t Target, ps PersistedState)` — `TargetName` + `TargetType` payload'a eklendi
  - `broadcastStateByID(targetID, ps)` — sadece ID biliniyorsa config'den lookup yapar, yoksa minimal payload gönderir

**Sonuç:** Primary node, kendi config'inde olmayan bir target için peer gossip'i alırsa artık `DispatchPeerAlert` ile alarm atıyor. `NODE_NAME` env var'ı gerçekte detect eden node'u gösteriyor. Apps enrichment best-effort: primary'nin kendi apps index'inde target varsa doldurulur.

### ✅ Phase 13 — Distributed Probe Ownership

**Hedef:** Cluster'daki her node her target'ı probe etmez. Hash ring + zone-aware spread ile `probe_replication_factor` adet (default 3) node seçilir, geri kalanlar gossip dinler. Hedef servisler üzerinde gereksiz yük yok, erişim izni olmayan node'lar pin'lenebilir.

**Yeni config alanları:**
- `cluster.zone: "istanbul"` — opsiyonel node label (hostname'den türetilmez)
- `cluster.probe_replication_factor: 3` — target başına max prober (varsayılan 3)
- `target.probe_from: ["n1","n2"]` — manuel pin (Active Probe Delegation, todo.md F6)

**Yeni dosyalar (`internal/cluster/`):**
- `probers.go` — `LocalTargetProvider`, `ProberAssignmentListener`, `CandidatesFor`, `SelectProbers`, `IsLocalProber`, `zoneAwarePick` (3-tier), `recomputeProberAssignments`, `scheduleRecompute` (5sn debounce), `SeedProberAssignments`, `TriggerProberRecompute`
- `views.go` — `ProberAssignmentsSnapshot` (/cluster/probers), `FleetSummarySnapshot` (/fleet/status)

**Engine entegrasyonu:**
- `Engine.LocalTargets()` + `Engine.ProbeFromConstraint()` (LocalTargetProvider impl)
- `Engine.StartProbing()` + `Engine.StopProbing()` (ProberAssignmentListener impl)
- `Engine.bootstrapInventoryBroadcast()` — Init + Reload sonrası presence broadcast (chicken-and-egg çözer)
- `startProbeLoop` koşullu: `clusterMgr != nil && !IsLocalProber → erken return`
- Anti-entropy sync guard'ı: `syncing=true` iken Start/Stop/bootstrap ertelenir; `SetSyncing(false)` recompute tetikler

**Yeni metrikler:** `network_probe_local_assigned`, `network_probe_prober_count`, `network_probe_inventory_peers`

**Yeni endpoint'ler:** `/cluster/probers` (per-target), `/fleet/status` (özet — zone'lar dahil; per-target detay yok)

**Memberlist gossip mesajı sayısı değişmedi** — zone bilgisi `NodeMeta` (built-in), candidate set mevcut state broadcast'larından türetiliyor.

**Davranış değişikliği:** 5 node'lu cluster + factor=3 → bir target için tam 3 probe goroutine çalışır, geri kalan 2 node sessiz dinleyici. Operatör hiçbir şey değiştirmedi.

**Test sayısı:** 54 (config/probers/recompute/views/integration toplamı), tümü `-race` ile yeşil.

---

## Bekleyen Aşamalar

### ✅ Phase 10 — Lifecycle Komutları (TAMAMLANDI)

CLI subcommand routing eklendi: `netwatch init`, `netwatch validate`, `netwatch leave`, `netwatch uninstall`, Windows için `service install/remove`. `/cluster/leave` HTTP endpoint'i eklendi. Detay için `developments.md` 2026-05-07.

**2026-05-14 — CLI cluster join workflow eklendi:**

- `netwatch init --cluster [--bind-port N] [--force]` — cluster-enabled config + random AES-256 keyring + copy-paste join command output
- `netwatch join --keyring K --addr H:P [--config PATH] [--bind-port N] [--node-name N]` — tek komutla cluster'a katılma; mevcut config'in `cluster.*` bölümünü override eder, diğer alanları korur
- `netwatch keyring generate` — yeni AES-256 base64 key basar
- Startup banner: cluster aktifse agent başladığında stdout'a node bilgileri + `netwatch join` komutu basar
- Yardımcı: `engine.GenerateKeyringKey()`, `Engine.LocalClusterAddr()`, `Engine.ClusterPrimaryKey()`, `Manager.LocalAddr()`, `Manager.PrimaryKey()`

---

### ✅ Phase 11 — Deployment Artifacts (TAMAMLANDI)

**Çıktılar:**
- `Makefile` — `build-linux`, `build-windows`, `test`, `test-integration`, `lint`, `vet`, `all`, `install`, `uninstall`, `docker-build`, `docker-push`, `clean`; `BINARY_NAME` override edilebilir
- `Dockerfile` — `ARG BINARY_NAME` ile parametrik; `EXPOSE 7946`; ldflags BinaryName injection
- `deploy/netwatch.service` — statik referans systemd unit; `AmbientCapabilities=CAP_NET_RAW`; journal logging
- `config.example.yaml` — tüm probe tipleri + tüm kanal tipleri + cluster alanları açıklamalı
- `helm/netwatch/` — DaemonSet chart; `hostNetwork: true`; `NET_RAW`; headless service (gossip DNS discovery); Downward API (NODE_NAME, HOST_IP); keyring Secret

---

### ✅ Phase 12 — Integration Tests (TAMAMLANDI)

**7 in-process end-to-end test, gerçek net.Listener + gerçek memberlist:**

- `test/integration/standalone_test.go`:
  - `TestStandalone_ProbeAndAlertCycle` — up→down→recovery tam döngüsü, state.json + alert doğrulaması
  - `TestStandalone_AppEnrichment` — `AFFECTED_APPS`/`OWNER_TEAMS` env var kontrolü
  - `TestStandalone_StateV2Migration` — v1 boolean format otomatik v2 migrate
- `test/integration/cluster_test.go`:
  - `TestCluster_ExactlyOnceAlert` — 2 node, probe_replication_factor=2, tam 1 unreachable alert
  - `TestCluster_RecoveryAlert` — down→recovery, tam 1 reachable alert
- `test/integration/antientropy_test.go`:
  - `TestAntiEntropy_RejoinNoDuplicateAlert` — re-join sonrası 2. alarm gelmez
- `test/integration/keyrotation_test.go`:
  - `TestKeyRotation_SharedKeyGossip` — AES-256 şifreli gossip, exactly-once
  - `TestKeyRotation_AddKey` — hot-reload ile k2 ekleme, cluster ayakta kalır

**cluster.go race fix:** `ringMu` → `m.list` + ring atomik; `inventoryRefreshHandler` `mu.RLock()` altında okunur.

**CI gate:** `go test -race -timeout 300s ./internal/engine/... ./internal/cluster/... ./test/integration/...` → 0 data race, 3 paket yeşil.

---

### ✅ Phase 13 — Distributed Probe Ownership (TAMAMLANDI)

Cluster artık her node her target'ı probe etmiyor; hash + zone-aware spread ile `probe_replication_factor` adet (default 3) prober seçiyor. todo.md F6 (Active Probe Delegation, `target.probe_from`) ve todo.md F2 (minimal `/fleet/status`) entegre. Detay için `developments.md` 2026-05-11.

---

### ✅ todo.md P0.1 + P0.2 — Dependency Graph + Root Cause + Rich /fleet/status (TAMAMLANDI)

**Dosyalar:** `internal/engine/topology.go`, `topology_test.go`, `fleet.go`, `fleet_test.go`

P0.1: `depends_on` config alanı, `DependencyGraph` (cycle detection, BFS/DFS), `FindRootCause`, `CascadingImpact`, `DependencyDepth`, `TopologySnapshot`, `GET /topology`, `ROOT_CAUSE`/`CASCADING_IMPACT`/`DEPENDENCY_DEPTH` alert env. `AllPeerStates()`/`QuorumHealthy()`/`ReplicationFactor()` cluster.Manager'a eklendi.

P0.2: Engine-level `FleetSnapshot` — `by_node`, `consensus_state`, `scope`, `affected_apps`, `root_cause`, `incidents[]`. Standalone + cluster her ikisinde çalışır.

---

### ✅ todo.md P1.3 + P1.4 — Scope Intelligence + SLO Tracker (TAMAMLANDI)

**Dosyalar:** `internal/engine/scope.go`, `scope_test.go`, `slo.go`, `slo_test.go`

**P1.3 — Scope Intelligence Enhancement:**
- `DetailedScope` struct: Scope, Classification, DownNodes, UpNodes, OfflineNodes, PartitionGroups, Confidence
- `classifyScope(targetID)` — REAL_OUTAGE (tüm node'lar down, offline yok) / NETWORK_PARTITION (mixed votes) / LOCAL_FAILURE (sadece bu node down) / AMBIGUOUS (yetersiz veri)
- `ScopeEnv()` — `SCOPE`, `CLASSIFICATION`, `CONFIDENCE`, `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES` alert env'ini doldurur
- `fleet.go` `FleetTarget`'a `classification` + `confidence` alanları eklendi
- `notify.go` `computeScope` → `classifyScope` ile değiştirildi

**P1.4 — SLO Tracker:**
- `SLOConfig`, `SLOTarget` config alanları
- `sloManager`: `incidents.json` persistence, `RecordStart`/`RecordEnd`, `PruneOldIncidents`, `ComputeSLO`
- `loop.go` `markHardDown` → `sloRecordStart`, `markRecovered` → `sloRecordEnd` hook'ları
- `runSLOChecker` goroutine (saatlik): breach detection, edge-triggered alert, retention pruning
- `network_probe_slo_uptime_ratio`, `network_probe_slo_error_budget_seconds`, `network_probe_slo_breached` Prometheus metrikleri
- `GET /slo` endpoint; `slo.enabled: false` ise 503

**Tests:** `scope_test.go` 9 test, `slo_test.go` 12 test — tümü `-race` ile yeşil.

---

### ✅ todo.md P1.5 — Gossip Config Sync (TAMAMLANDI)

**Dosyalar:** `internal/cluster/configsync.go`, `configsync_test.go`

- `ConfigBroadcast` gossip mesajı: `msg_type: "config"` ile eski payload'dan ayrıştırılıyor (backward-compat)
- `ConfigHashOf(raw)`: SHA-256[:16] hex — ham config baytları üzerinden
- `cfgBroadcast` → `memberlist.Broadcast` impl; `Invalidates=true` → kuyrukta sadece son hash
- `SetLocalConfigInfo(hash, size, loadedAt)`: `LoadConfig` sonrası çağrılıyor; broadcast + kayıt
- `ConfigSyncSnapshot` → `GET /cluster/config` endpoint
- `GaugeConfigDrift` (`network_probe_config_drift`): 1=drift var, 0=senkron
- Engine entegrasyonu: `RegisterClusterMetrics`'e eklendi; `updateClusterMetrics`'te `UpdateConfigDriftMetric()` çağrısı

---

### ✅ todo.md P1.6 — Geo Latency View (TAMAMLANDI)

**Dosyalar:** `internal/cluster/geolat.go`, `geolat_test.go`

- `Config.Region string` — node-level coğrafi etiket; `nodeMeta`'ya eklendi (graceful overflow: önce Region düşer)
- `GossipPayload.Latency float64` — başarılı probda `elapsed` değeri; `runCheck` → `lastLatency.Store`
- `Target.ProbeFromRegions []string` — bölge bazlı candidate filtresi; `CandidatesFor`'da `ProbeFromConstraint` sonrası uygulanıyor
- `Engine.ProbeFromRegionsConstraint()` — `LocalTargetProvider` arayüzüne eklendi
- `GeoLatencyForTarget(targetID)`: peerStates'ten per-node latency snapshot; `ByNode` sıralı
- `detectLatencyAnomaly`: ≥2 non-zero + max > 3×min → true
- `UpdateGeoMetrics(targetInfos)`: engine'in 5s updater'ından çağrılıyor
- `GET /geo/latency/{targetID}` endpoint
- `GaugeGeoLatency` + `GaugeGeoLatencyAnomaly` → `RegisterClusterMetrics`'e eklendi

**Tests:** `configsync_test.go` 7 test, `geolat_test.go` 15 test — tümü `-race` ile yeşil.

---

## Aşama Bağımlılık Grafiği

```
✅0 → ✅1 → ✅2 → ✅3 → ✅4 → ✅5
                            │
                            ▼
                           ✅6 → ✅7 → ✅8 → ✅9
                                                  │
                                                  ▼
                                              ✅10 → ✅11 → ✅12 → ✅13
```

Her aşama bittikten sonra **kullanıcı onayı** alınır. Sonraki aşamaya geçilmeden önce smoke test yapılır.

---

## Her Aşama Sonu Doğrulama

1. `go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/` — derleme hataları yok
2. `go test -race ./internal/engine/... ./internal/cluster/...` — yeşil
3. `go vet ./internal/engine/ ./internal/cluster/ ./cmd/linux/` — temiz
4. **Manuel smoke:** Mevcut `config.yaml` ile binary'yi çalıştır → `curl localhost:10240/metrics`, `curl /health`, `curl /status`. Bir target'ı intentional olarak down et → log'da state geçişlerini doğrula.
5. **Cluster aşamaları (Phase 6+):** 3 lokal binary instance, farklı port ve `node_name`. `curl /cluster/state` her node'da 3 üye göstermeli.

---

## Başlangıçta Belirlenen Yapısal Kararlar

Proje planlanırken tespit edilen 4 kritik yapısal sorun ve çözümleri:

### 1. Config schema — `Target.Options` korundu
**Sorun:** Düz alanlar (`expected_status int`, `resolve string`) kullanılsaydı HTTP body assertion, DNS `expected_ips`, SQL `query` gibi zengin opsiyonlar kaybolacaktı.
**Karar:** `Target.Options json.RawMessage` olarak tutulur. Her Checker kendi `parseOptions(raw)` fonksiyonunu çağırır. Bu pattern mevcut kodda zaten vardı — korundu.

### 2. `state.json` korundu
**Sorun:** Cluster modunda bazı tasarımlarda state persistence kaldırılıyor. Ancak restart sonrası node kendi son durumunu bilmezse rolling restart sırasında alert storm oluşur.
**Karar:** `state.json` cluster modunda da korunur. Re-join sonrası anti-entropy bu state ile başlar ve gereksiz alarm üretmez.

### 3. Lamport clock `(OwnerNode, Seq)` tuple
**Sorun:** Sadece `Seq uint64` per-target yetmez — iki farklı node aynı anda aynı target için `seq=5` üretirse gossip'te hangisinin kazanacağı belirsiz.
**Karar:** Causal ordering için `(OwnerNode, Seq)` tuple. Karşılaştırma: önce Seq, eşitse NodeName lex sıralaması. Phase 8'de consistent hash primary sequence bump eder; secondary sadece primary öldüğünde devralır ve `Seq = max(known) + 1` ile başlar.

### 4. Target-level `notify` korundu
**Sorun:** Notification sadece `apps[].notifications` içinde olsaydı herhangi bir uygulamaya bağlı olmayan target'lar (core-router-ping, dc-gateway, DNS health) için sahte app tanımlamak gerekirdi.
**Karar:** Target'ta `notify: []` alanı korunur (mevcut davranış). App referansı varsa app kanalları + target kanalları **birleştirilir** (set union, dedupe). Hiçbiri yoksa `default_notify` devreye girer.

---

## Önemli Sabit Kararlar

1. **Paket yapısı:** Yalnızca `internal/engine/` + `internal/cluster/`. Alt-paket split yok.
2. **Metrik rename:** Direkt geçiş, alias yok. Eski Grafana sorguları varsa elle güncellenir.
3. **Modül path:** `github.com/saidtaylan/netwatch` korunur.
4. **`Target.Options`:** Asla düz alanlara dönüştürülmez — `json.RawMessage` olarak kalır.
5. **`state.json`:** Cluster modunda da korunur; restart sonrası anti-entropy bu state ile başlar.
6. **Lamport clock:** `(OwnerNode, Seq)` tuple. Seq eşitse OwnerNode lex sıralaması. Cluster olmadığında OwnerNode boş string.
7. **Her aşama sonrası kullanıcı onayı** alınır, sonraki aşamaya geçilmez.
8. **Bu CLAUDE.md birincil referanstır.** Orijinal plan dosyası `/Users/saidtaylan/.claude/plans/readme-md-dosyas-nda-g-rd-n-mevcut-smooth-pizza.md` adresinde olup iki-dizin kararı ve diğer güncel kararlar buradaki belgeden okunmalıdır.

---

## Webhook Payload Formatları

### generic (varsayılan)
```json
{
  "name": "db-probe",
  "target": "127.0.0.1:1",
  "host": "127.0.0.1",
  "port": "1",
  "app_name": "webhook-test",
  "node_name": "saidtaylan.local",
  "status": "unreachable",
  "type": "tcp",
  "seq": 1,
  "error_code": "dial tcp 127.0.0.1:1: connect: connection refused",
  "affected_apps": "payment-service",
  "owner_teams": "fintech-sre",
  "fired_at": "2026-05-06T07:39:38Z"
}
```

### alertmanager (Alertmanager v2 uyumlu)
```json
[{
  "labels": {
    "alertname": "ProbeDown",
    "app_name": "webhook-test",
    "name": "db-probe",
    "source_host": "saidtaylan.local",
    "target": "127.0.0.1:1",
    "type": "tcp"
  },
  "annotations": {
    "affected_apps": "payment-service",
    "error_code": "dial tcp 127.0.0.1:1: connect: connection refused",
    "owner_teams": "fintech-sre",
    "seq": "1",
    "summary": "Target db-probe is unreachable"
  },
  "startsAt": "2026-05-06T07:39:38Z"
}]
```
Recovery'de: `alertname = "ProbeUp"`, `endsAt = now`.

---

## Watchdog Davranışı

```
watchdog_threshold_sec: 0   → devre dışı (varsayılan)
watchdog_threshold_sec: 120 → 120s scrape gelmezse tetiklenir
```

- Problar ve alertler **etkilenmez** — özerk çalışmaya devam eder
- `network_probe_prometheus_connected` metriği: başlangıçta 1
- Threshold aşılırsa: `[WATCHDOG] Prometheus scrape not detected` + metrik=0
- Scrape gelince: `[PROMETHEUS] Prometheus scraping resumed` + metrik=1
- İlk scrape gelmeden uyarı üretmez (restart grace period)

---

## Smoke Test Config'leri

### Phase 2-3: Apps + Script + State
```bash
# /tmp/netwatch-apps-test.yaml yazıktan sonra:
go build -o /tmp/netwatch-test ./cmd/linux/
rm -f /tmp/netwatch-apps-state.json
/tmp/netwatch-test -config /tmp/netwatch-apps-test.yaml
# Beklenen: channels="[ops dba]" apps=2
# state.json: {"version":2,"targets":{"core-db":{"state":"hard_down","seq":1,"error_code":"..."}}}
# /status: seq ve error_code dolu
```

### Phase 4: Webhook
```bash
# Mock receiver başlat:
python3 -c "
from http.server import HTTPServer, BaseHTTPRequestHandler; import json
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n=int(self.headers.get('Content-Length',0)); body=self.rfile.read(n)
        print(json.dumps(json.loads(body),indent=2),flush=True)
        self.send_response(200); self.end_headers()
    def log_message(self,*a): pass
HTTPServer(('127.0.0.1',9191),H).serve_forever()" &

# /tmp/netwatch-webhook-test.yaml config ile çalıştır (port 10242, mock receiver 9191)
# Beklenen: generic + alertmanager POST, affected_apps + seq dahil
```

### Phase 5: Watchdog
```bash
# /tmp/netwatch-watchdog-test.yaml: watchdog_threshold_sec: 8
# Scrape yap → dur → 12s bekle → scrape yap
# Beklenen: "[WATCHDOG] ... last_scrape_ago=11s" → "[PROMETHEUS] ... resumed"
```

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->