# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## Proje Özeti

**Uygulama adı:** `netwatch` (`github.com/saidtaylan/netwatch`)
**Konum:** `/Users/saidtaylan/Documents/network cluster/`
**Go versiyonu:** 1.25.7
**Amaç:** Tek-node Prometheus exporter olarak başlayan ağ izleme ajanını, gossip protokolü ile haberleşen, quorum bazlı karar veren dağıtık bir cluster monitoring sistemine dönüştürmek.

---

## Build Komutları

```bash
# Sadece macOS/Linux'ta derle (Windows cmd'i platform kısıtlı)
go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/

# Makefile üzerinden (tercih edilen):
make build-linux          # bin/netwatch-linux-amd64 üretir
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

## Paket Yapısı

```
internal/engine/          ← business logic paketi
  protocol.go             # Checker interface (Run, ValidateOptions, ParseAddr)
  engine.go               # Config, Engine struct, state persistence, hot-reload, metrikler
  loop.go                 # Per-target probe goroutine, retry loop, state machine + broadcastState()
  notify.go               # Alerter interface, kanal yönlendirme, sendAlert
  app.go                  # App struct, AppTargetIndex, buildAppTargetIndex, validateApps
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

**KRİTİK KARAR:** Proje yalnızca **iki dizin** üzerine kurulu: `internal/engine/` + `internal/cluster/`. Hiçbir başka alt-paket oluşturulMAYACAK.

---

## HTTP Endpointleri

| Endpoint | Açıklama |
|---|---|
| `GET /metrics` | Prometheus — her çağrıda `e.NotifyScrape()` tetiklenir (watchdog için) |
| `GET /health` | Liveness check — her zaman `200 OK` döner |
| `GET /status` | Tüm target'ların JSON durumu: name, status, seq, error_code |
| `GET /cluster/state` | Cluster üyeleri + peer target durumları; cluster kapalıysa 503 |

---

## Metrikler

Eski isimler (`netwatch_probe_*`) kaldırıldı, direkt yeni isimlere geçildi:

| Metrik | Açıklama |
|---|---|
| `network_probe_local_status` | Bu node'da son probe sonucu: 1=UP, 0=DOWN |
| `network_probe_local_latency_seconds` | Son probe round-trip süresi (saniye) |
| `network_probe_prometheus_connected` | 1=scraping normal, 0=watchdog threshold aşıldı |

Label'lar (`local_status`, `local_latency_seconds`): `name`, `target`, `type`, `source_host`, `app_name`

`network_probe_prometheus_connected` label'sız tek gauge'dır; `watchdog_threshold_sec: 0` (varsayılan) olduğunda daima 1 kalır.

Cluster gelince `network_probe_cluster_status` (konsensüs değeri) eklenecek — mevcut metrikler dokunulmayacak.

---

## Config Schema (Önemli Alanlar)

```yaml
port: "10240"
app_name: "my-agent"
state_file: "state.json"
log_path: "prober.log"        # boş bırakılırsa stdout
timeout: 5
max_retries: 2
retry_interval_sec: 30
ticker_interval_sec: 5
probe_interval_sec: 60        # per-target interval_sec ile override edilebilir
reload_interval_sec: 30       # 0 = hot-reload kapalı
watchdog_threshold_sec: 120   # scrape bu kadar sn gelmezse [WATCHDOG] log + metrik=0; 0 = devre dışı
credentials_file: "credentials.env"  # ${VAR} injection için

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
```

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
| `APP_NAME` | agent'ın app_name config değeri | ✓ |
| `NODE_NAME` | `os.Hostname()` çıktısı | ✓ |
| `STATUS` | `unreachable` veya `reachable` | ✓ |
| `TYPE` | tcp / http / ping / dns / sql | ✓ |
| `SEQ` | Lamport seq — her state geçişinde artar | ✓ |
| `ERROR_CODE` | son probe hata metni; recovery'de boş | ✓ |
| `AFFECTED_APPS` | virgülle ayrılmış app isimleri | apps varsa |
| `OWNER_TEAMS` | virgülle ayrılmış takım isimleri | apps varsa |

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

---

## Bekleyen Aşamalar

### Phase 10 — Lifecycle Komutları

**Hedef:** systemd/Windows Service entegrasyonu için CLI komutları.

**Görevler:**
1. `netwatch init [--config-dir DIR]` — `config.yaml` + `credentials.env` iskeleti yaz, Linux'ta systemd unit dosyası oluştur, Windows'ta servis kayıt komutu hint'i ver.
2. `netwatch leave [--reason TEXT]` — `/cluster/leave` HTTP endpoint'ine POST → çalışan agent graceful leave yapar.
3. `netwatch uninstall` — leave + servisi kaldır + dosyaları sil (onay sorar).
4. `netwatch service install/remove` (Windows) — mevcut `installService` fonksiyonu zaten var, CLI'a bağla.

**Kabul kriteri:** `init` sıfırdan çalışan setup üretir; `leave` cluster'ı bilgilendirir; `uninstall` her şeyi temizler.

---

### ✅ Phase 11 — Deployment Artifacts (TAMAMLANDI)

**Çıktılar:**
- `Makefile` — `build-linux`, `build-windows`, `test`, `test-integration`, `lint`, `vet`, `all`, `install`, `uninstall`, `docker-build`, `docker-push`, `clean`; `BINARY_NAME` override edilebilir
- `Dockerfile` — `ARG BINARY_NAME` ile parametrik; `EXPOSE 7946`; ldflags BinaryName injection
- `deploy/netwatch.service` — statik referans systemd unit; `AmbientCapabilities=CAP_NET_RAW`; journal logging
- `config.example.yaml` — tüm probe tipleri + tüm kanal tipleri + cluster alanları açıklamalı
- `helm/netwatch/` — DaemonSet chart; `hostNetwork: true`; `NET_RAW`; headless service (gossip DNS discovery); Downward API (NODE_NAME, HOST_IP); keyring Secret

---

### Phase 12 — Integration Tests

**Hedef:** Regresyon güvencesi.

**Görevler:**
1. `test/integration/standalone_test.go` — config ile başlat, mock TCP server'ı kapat, `state.json`'a `hard_down` yazılmış mı, alert script çağrılmış mı.
2. `test/integration/cluster_test.go` — 3 node lokal başlat, target'ı kontrol et, scope hesabı, exactly-once alarm.
3. `test/integration/antientropy_test.go` — Phase 9 senaryosu (re-join alarm storm yok).
4. `test/integration/keyrotation_test.go` — keyring rotation sıfır kesinti.
5. CI gate: `go test ./... -race -timeout 120s`.

**Kabul kriteri:** Tüm testler `-race` ile geçer.

---

## Aşama Bağımlılık Grafiği

```
✅0 → ✅1 → ✅2 → ✅3 → ✅4 → ✅5
                            │
                            ▼
                           ✅6 → ✅7 → ✅8 → ✅9
                                                  │
                                                  ▼
                                              ✅10 → ✅11 → [ 12 ]
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
