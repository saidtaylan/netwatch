# netwatch Sprint — Bekleyen Aşamalar

Bu dosya aktif geliştirme planını içerir. Tamamlananlar → **developments.md**. Mimari kararlar → **CLAUDE.md**.

---

## ✅ Sprint Tamamlandı — Production Quality Features (F1-F4)

Uygulama tarihi: 2026-05-20. F5 (K8s SD) → sonraki sprint.

### ✅ F1 — Probe Interval Staggering

**Hedef:** Aynı target'ı probe eden N node, hepsi aynı anda probe atmasın. Probe yükü interval içinde eşit dağıtılsın.

**Mevcut sorun:**
```
3 prober, probe_interval=60s:
T+0s:  prober-A → probe
T+0s:  prober-B → probe   ← AYNI saniyede
T+0s:  prober-C → probe   ← AYNI saniyede
T+60s: hepsi yine aynı anda
```

**Hedeflenen davranış:**
```
3 prober, probe_interval=60s:
T+0s:  prober-A → probe   (offset = 0)
T+20s: prober-B → probe   (offset = 60/3 * 1)
T+40s: prober-C → probe   (offset = 60/3 * 2)
T+60s: prober-A → probe   ← döngü başa
```

**Faydalar:**
- Target servise düz yük (burst yok)
- Mean detection latency: probe_interval/N (örn. 60s yerine 20s)
- Network bandwidth düz dağılır

**İmplementasyon:**

Dosya: `internal/engine/loop.go` — `startProbeLoop()` içinde:

```go
func (e *Engine) startProbeLoop(t Target) {
    if e.clusterMgr != nil && !e.clusterMgr.IsLocalProber(t.key()) {
        return
    }

    // ... mevcut probeFastCheck ve probeCancel kurulumu ...

    // ── YENİ: stagger offset hesabı ──
    var offset time.Duration
    if e.clusterMgr != nil {
        probers := e.clusterMgr.SelectProbers(t.key())  // sorted
        myName := e.clusterNodeName()
        for i, p := range probers {
            if p == myName {
                // offset = (probe_interval / N) * i
                e.mu.RLock()
                interval := e.cfg.globalProbeInterval()
                e.mu.RUnlock()
                if t.IntervalSec != nil {
                    interval = *t.IntervalSec
                }
                offset = time.Duration(i) * (time.Duration(interval)*time.Second / time.Duration(len(probers)))
                break
            }
        }
    }

    go func() {
        // İlk probe öncesi stagger sleep
        if offset > 0 {
            slog.Debug("probe loop staggered", "target", t.key(), "offset", offset)
            select {
            case <-ctx.Done():
                return
            case <-time.After(offset):
            }
        }

        // Mevcut: immediate probe + ticker loop (değişmedi)
        e.runCheck(ctx, t)
        // ... ticker döngüsü ...
    }()
}
```

**Edge case'ler:**

1. Standalone mod (cluster=nil): offset = 0, mevcut davranış korunur
2. Tek prober (N=1): offset = 0
3. Prober assignment değişimi: mevcut `StopProbing` + `StartProbing` recompute mekanizması yeni offset'i otomatik hesaplar
4. Çok kısa interval (1s) + çok prober (10): 1s/10 = 100ms offset — kabul edilebilir

**Test (white-box, `internal/engine/loop_test.go` veya yeni `stagger_test.go`):**

```go
func TestStartProbeLoop_Stagger_ComputesCorrectOffset(t *testing.T) {
    // Mock cluster manager that returns SelectProbers = [n1, n2, n3]
    // Set local node = n2 (index 1)
    // probe_interval = 60s
    // Expected offset = 20s
    // Verify: probe loop sleeps 20s before first probe
}

func TestStartProbeLoop_Stagger_StandaloneZeroOffset(t *testing.T) {
    // clusterMgr = nil
    // Expected: probe runs immediately (offset=0)
}

func TestStartProbeLoop_Stagger_SingleProberZeroOffset(t *testing.T) {
    // SelectProbers returns single node (self)
    // Expected: offset = 0
}
```

**E2E test (tests/domain/stagger_test.go):**

3 node cluster, probe_replication_factor=3, probe_interval=6s. Bir target'ı probe ederler. Tüm probe'ların timestamp'ini topla (mock TCP server probe count'u). 30 saniye sonra prober başına ~5 probe görünmeli ve probe'lar arası boşluk ~2s olmalı (6/3=2).

**Tahmini efor:** 1.5 saat (kod + test)

**Kabul kriterleri:**
- [x] Standalone mod (cluster yok): mevcut davranış (offset yok)
- [x] 3 prober cluster: ilk probe'lar 0, 20, 40 saniyede (probe_interval=60s için)
- [x] Prober set değişince yeni offset uygulanır
- [x] race detector temiz
- [x] Mevcut tüm testler hâlâ geçer

---

### ✅ F2 — ROOT_CAUSE Cross-Node Fix (Bug Fix)

**Hedef:** Bir target'ın bağımlılığı başka bir node'da probe ediliyor olabilir. ROOT_CAUSE hesabı sadece local state'e değil, peer gossip state'ine de bakmalı.

**Mevcut bug:**

20 node, factor=3. Aynı target listesi (config sync). Ama her target'ı sadece 3 node probe eder.

```
target `db-primary` probers: nodes[1, 5, 9]
target `api-gateway` probers: nodes[2, 6, 10]    ← farklı set
api-gateway depends_on: ["db-primary"]
```

api-gateway hard_down olduğunda node-2 alarm gönderir. node-2 `e.lastKnown["db-primary"]`'a bakar → **YOK** (node-2 db-primary probe etmiyor). ROOT_CAUSE boş çıkar.

**Düzeltme:**

Dosya: `internal/engine/notify.go` veya `topology.go` — `rootCauseEnv()` veya FindRootCause çağırma yeri.

```go
// rootCauseEnv: dependency state'i hesaplarken cluster gossip'i de kullan
func (e *Engine) rootCauseEnv(t Target, status string, localStates map[string]PersistedState) map[string]string {
    if e.topoGraph == nil || !e.topoGraph.HasDependencies() {
        return nil
    }
    if status != "unreachable" {
        return nil
    }

    // ── YENİ: gossip-aware merged state ──
    mergedStates := make(map[string]PersistedState, len(localStates))
    for id, ps := range localStates {
        mergedStates[id] = ps
    }
    if e.clusterMgr != nil {
        // Bu target'ın tüm transitif bağımlılıkları için peer state lookup
        for _, depID := range e.topoGraph.AllDependencyIDs(t.key()) {
            if _, ok := mergedStates[depID]; ok {
                continue // local'de var, peer'a bakmaya gerek yok
            }
            peers := e.clusterMgr.PeerStatesForTarget(depID)
            if len(peers) == 0 {
                continue
            }
            // En yüksek seq'li (en taze) peer state'i seç
            best := peers[0]
            for _, p := range peers[1:] {
                if p.Seq > best.Seq || (p.Seq == best.Seq && p.NodeName > best.NodeName) {
                    best = p
                }
            }
            mergedStates[depID] = PersistedState{
                State:     best.State,
                Seq:       best.Seq,
                ErrorCode: best.ErrorCode,
            }
        }
    }

    rootCause := e.topoGraph.FindRootCause(t.key(), mergedStates)
    // ... env üret ...
}
```

**Yeni eklenecek helper (`topology.go`):**

```go
// AllDependencyIDs returns all transitive dependencies of targetID (BFS).
func (g *DependencyGraph) AllDependencyIDs(targetID string) []string {
    visited := make(map[string]bool)
    var out []string
    queue := []string{targetID}
    for len(queue) > 0 {
        cur := queue[0]
        queue = queue[1:]
        for _, dep := range g.dependsOn[cur] {
            if visited[dep] {
                continue
            }
            visited[dep] = true
            out = append(out, dep)
            queue = append(queue, dep)
        }
    }
    return out
}
```

**Test:**

E2E (yeni `tests/domain/crossnode_rootcause_test.go`):
1. 6 node cluster, factor=2
2. target-a depends_on target-b
3. probe_from constraint kullanarak target-a'yı node-1, node-2'ye; target-b'yi node-5, node-6'ya pinle (disjoint set)
4. Mock server'ları kapat: hem target-a hem target-b down
5. Alarm yakala
6. Alarm env'inde ROOT_CAUSE=target-b olmalı

**Tahmini efor:** 2 saat (kod + test + edge case'ler)

**Kabul kriterleri:**
- [x] Disjoint prober set'leri arasında ROOT_CAUSE doğru çözülür
- [x] Local state varsa onu tercih eder (peer state'e ihtiyaç yok)
- [x] Peer state Lamport seq'iyle çakışma çözer (en yüksek seq kazanır, eşitse NodeName lex)
- [x] HasDependencies()=false olan target'lar için no-op (perf)
- [x] gemini_thoughts.md'deki Case 3 (zincirleme çöküş) artık 20-node disjoint set'lerde de çalışır

---

### ✅ F3 — Maintenance Window (API-Driven)

**Hedef:** Operatör elle config dosyası düzenlemeden, REST API ile belirli target'ları belirli süre için "alarm üretmesi durdurulmuş" duruma getirebilsin. Restart-survivable, gossip-replicated.

**API tasarımı:**

```
PUT /cluster/maintenance        Authorization: Bearer <admin_token>
Content-Type: application/json
Body: {
  "target_ids": ["db-primary", "api-gateway"],
  "duration": "2h",                          # Go duration: 30m, 1h30m, 24h
  "reason": "DB migration v2.3",
  "started_by": "ops-team"                   # opsiyonel
}

Response 200:
{
  "applied_locally": true,
  "broadcast_to": ["node-2", ..., "node-20"],
  "expires_at": "2026-05-16T18:30:00+03:00",
  "id": "mw-2026-05-16T16:30:00Z-abc1234"    # iptal için
}
```

```
DELETE /cluster/maintenance/{id}    Authorization: Bearer <admin_token>

Response 200: { "cancelled": true }
```

```
GET /cluster/maintenance

Response:
[
  {
    "id": "mw-...",
    "target_ids": ["db-primary"],
    "started_at": "...",
    "expires_at": "...",
    "reason": "...",
    "started_by": "..."
  }
]
```

**Persistence:**

Yeni dosya: `<state_file_dir>/maintenance.json`

```json
{
  "version": 1,
  "windows": [
    {
      "id": "mw-2026-05-16T16:30:00Z-abc1234",
      "target_ids": ["db-primary", "api-gateway"],
      "started_at": "2026-05-16T16:30:00Z",
      "expires_at": "2026-05-16T18:30:00Z",
      "reason": "DB migration v2.3",
      "started_by": "ops-team"
    }
  ]
}
```

**Gossip mesaj tipi:** `msgType: "maintenance"`

```go
type MaintenanceBroadcast struct {
    MsgType    string    `json:"msg_type"`   // "maintenance"
    Action     string    `json:"action"`     // "set" | "cancel"
    Window     *MaintenanceWindow `json:"window,omitempty"`  // set için
    CancelID   string    `json:"cancel_id,omitempty"`        // cancel için
    OriginNode string    `json:"origin_node"`
    Timestamp  time.Time `json:"timestamp"`
}
```

**Engine entegrasyonu:**

Dosya: `internal/engine/maintenance.go` (yeni)

```go
type MaintenanceWindow struct {
    ID         string    `json:"id"`
    TargetIDs  []string  `json:"target_ids"`
    StartedAt  time.Time `json:"started_at"`
    ExpiresAt  time.Time `json:"expires_at"`
    Reason     string    `json:"reason,omitempty"`
    StartedBy  string    `json:"started_by,omitempty"`
}

type maintenanceManager struct {
    mu        sync.RWMutex
    windows   map[string]MaintenanceWindow      // id → window
    byTarget  map[string][]string               // target_id → window IDs
    path      string                            // disk persistence path
}

func newMaintenanceManager(path string) *maintenanceManager
func (m *maintenanceManager) IsInMaintenance(targetID string) bool
func (m *maintenanceManager) Set(window MaintenanceWindow) error  // disk + memory
func (m *maintenanceManager) Cancel(id string) error
func (m *maintenanceManager) List() []MaintenanceWindow
func (m *maintenanceManager) load()  // startup
func (m *maintenanceManager) save()  // atomic write
func (m *maintenanceManager) pruneExpired()  // periodic
```

**shouldAlert() değişikliği:**

```go
func (e *Engine) shouldAlert(targetID string) bool {
    // ── YENİ: maintenance check (önce gelir) ──
    if e.maintMgr != nil && e.maintMgr.IsInMaintenance(targetID) {
        slog.Debug("alert suppressed: target in maintenance", "target", targetID)
        return false
    }
    // ... mevcut isolation, IsResponsible, min_probe_confirmations checks ...
}
```

**Probe loop değişikliği yok:** Probe'lar çalışmaya devam eder. Sadece alert bastırılır. Bu, maintenance bitince state'in doğru olmasını sağlar (eğer hâlâ down ise alarm gider).

**Cron tabanlı (config'de tekrarlayan) maintenance — Faz 2:**

Bu sprint'te değil. Şimdilik sadece **ad-hoc API-driven** maintenance. Cron eklenirse `MaintenanceWindow.Recurring: cron string` opsiyonel alan olur.

**Timezone:**

Yeni Config alanı (SharedConfig'e eklenir, sync olur):
```yaml
timezone: "Europe/Istanbul"   # opsiyonel, default = system local
```

Maintenance API'de `expires_at` her zaman UTC olarak hesaplanır ve döner. UI/CLI gösterimi local timezone'a çevrilir. Ad-hoc maintenance için zone önemsiz (sadece duration); cron eklenince önem kazanır.

**Yeni metrik:**

```
network_probe_in_maintenance{name="db-primary", target="db:5432", type="tcp"} 1
network_probe_maintenance_active_count gauge   # toplam aktif window sayısı
```

**Yeni endpoint'ler (`cmd/linux/main.go` + `cmd/windows/main.go`):**

| Endpoint | Method | Auth | Açıklama |
|---|---|---|---|
| `/cluster/maintenance` | GET | optional | Aktif maintenance'ları listele |
| `/cluster/maintenance` | PUT | required | Yeni maintenance ekle (gossip ile yayar) |
| `/cluster/maintenance/{id}` | DELETE | required | İptal et (gossip ile yayar) |

**Yeni CLI komutu (opsiyonel ama operatör için kolaylık):**

```bash
$ netwatch maintenance set --target db-primary --duration 2h --reason "DB migration"
$ netwatch maintenance list
$ netwatch maintenance cancel mw-2026-...
```

Bu CLI komutları curl wrapper'ı — admin token'ı env'den okur.

**Test stratejisi:**

Unit (tests/engine/maintenance_test.go):
- IsInMaintenance(targetID) → expired window'lar atılır
- Set() → disk'e yazar, memory'i günceller
- Cancel() → memory + disk
- load() restart sonrası state restore

Integration (tests/domain/maintenance_test.go):
- 3 node cluster, target down
- PUT /cluster/maintenance ile maintenance girer
- Hard-down geçişine rağmen alarm gelmez
- Cancel sonrası bir sonraki probe'da (hâlâ down) alarm gelir
- Restart node-1 → maintenance persist eder

Gossip propagation test:
- node-1'e PUT, 3 saniye sonra node-5'in maintenance map'inde de var mı?

**Tahmini efor:** 6-8 saat (kod + test + dokümantasyon)

**Kabul kriterleri:**
- [x] PUT /cluster/maintenance → alarm bastırılır
- [x] Gossip ile tüm cluster'a propagate olur (<3s)
- [x] Restart sonrası maintenance.json'dan restore
- [x] Süresi dolan window otomatik kaldırılır
- [x] DELETE /cluster/maintenance/{id} → anında etkili
- [x] Probe loop çalışmaya devam eder (metric'ler güncellenir)
- [x] `in_maintenance: true` flag /status'ta görünür

---

### ✅ F4 — Soft-Up State (Symmetric Recovery)

**Hedef:** Recovery alarmları flap'leri filtrelesin. Tek başarılı probe yerine N consecutive başarılı probe sonrası "reachable" alarmı atılsın.

**Mevcut state machine:**
```
UP → SOFT_DOWN → HARD_DOWN          (asimetrik, sadece down tarafında flap koruma)
HARD_DOWN → UP                       (tek probe yeterli)
```

**Yeni state machine:**
```
UP → SOFT_DOWN → HARD_DOWN
HARD_DOWN → SOFT_UP → UP             (yeni: recovery flap koruma)
SOFT_UP + probe fail → HARD_DOWN     (back-off)
```

**Yeni config alanı:**
```yaml
recovery_probes: 2            # default 1 (backward compat: mevcut davranış)
# veya target-specific:
targets:
  - id: "flap-prone"
    recovery_probes: 3
```

SharedConfig'e eklenir, sync olur.

**Engine değişikliği:**

`internal/engine/loop.go` — `runCheck()` içinde recovery path:

```go
if ok {
    if !seen {
        // ... mevcut first-observation logic
    } else if inPending || !prevUp {
        // ── YENİ: HARD_DOWN/SOFT_DOWN → recovery
        // markRecovered() → markRecoveringOrUp() ile değiştir
        recoveryThreshold := e.effectiveRecoveryProbes(t)
        if recoveryThreshold > 1 {
            // soft_up state'inde bir entry oluştur
            done := e.markSoftUpOrUp(pkey, t, recoveryThreshold)
            if done {
                e.sloRecordEnd(t)
                slog.Info("target fully recovered", ...)
                if e.shouldAlert(t.key()) {
                    e.sendAlert(t, "reachable")
                }
            }
        } else {
            // Backward compat: tek probe yeterli (mevcut davranış)
            if e.markRecovered(pkey, t) {
                e.sloRecordEnd(t)
                if e.shouldAlert(t.key()) {
                    e.sendAlert(t, "reachable")
                }
            }
        }
    }
}
```

**Yeni state machine sub-component:**

```go
// PendingRecovery: state machine soft_up tracking
type pendingRecovery struct {
    Target       Target
    SuccessCount int       // ardışık başarı sayısı
    LastSuccess  time.Time
}

// markSoftUpOrUp:
//   - İlk başarılı probe → soft_up (pending recovery'e ekle)
//   - N-inci ardışık başarı → up (markRecovered → return true)
//   - Probe fail → pending recovery'den sil (back-off to hard_down)
```

**state.json değişikliği:**

Yeni state değeri: `"soft_up"` (transient, sadece bilgi amaçlı persist edilir).

```json
{
  "version": 2,
  "targets": {
    "db": { "state": "soft_up", "seq": 5, ... }
  }
}
```

**Yeni metrik:**

`network_probe_local_status` extension — Şu an 0/1. Soft_up için ara değer? Hayır, metric semantic'ini bozar. **Çözüm:** `local_status` UP=1 DOWN=0 olarak kalır. SOFT_UP için ayrı:

```
network_probe_local_state{name=...,target=...,type=...,state="up|soft_up|soft_down|hard_down"} 1
```

State Vec metric: hangi state aktifse 1, diğerleri 0.

**Test stratejisi:**

Unit:
- recovery_probes=2, hard_down state'inde 1 başarı → soft_up, alarm yok
- 2 ardışık başarı → up, recovery alarmı 1 kez
- Soft_up state'inde 1 fail → hard_down, alarm yok (zaten gönderilmişti)
- recovery_probes=1 (default) → mevcut davranış korunur (backward compat)

Integration:
- 3 node cluster, recovery_probes=2
- Target down → hard_down alarmı
- Target restart, 1 successful probe → no alarm
- 2. successful probe → recovery alarmı

**Tahmini efor:** 4-5 saat

**Kabul kriterleri:**
- [x] recovery_probes=1 default'unda mevcut davranış aynen korunur
- [x] recovery_probes=2 ile flap koruma çalışır (1 başarı yeterli değil)
- [x] state.json soft_up değerini doğru persist eder
- [x] Restart sonrası soft_up state'ten resume edebilir
- [x] Yeni `network_probe_local_state` metrik state Vec olarak çalışır
- [x] SLO incident kaydı: soft_up gelince incident kapanmaz; up gelince kapanır

---

### F5 — Kubernetes Service Discovery (DETAY — şimdi atlanıyor, sonra için)

**Hedef:** Kubernetes ortamında çalışan netwatch, k8s API'sini izleyerek Service/Pod/Ingress'leri otomatik target olarak ekler. Operatör manuel target listesi yönetmek zorunda kalmaz.

**Önerilen Config:**
```yaml
discovery:
  kubernetes:
    enabled: true
    incluster: true                       # pod içindeyse true (ServiceAccount kullanır)
    kubeconfig: ""                        # cluster dışıysa kubeconfig path
    namespaces: ["production", "staging"] # boş = tümü
    label_selector: "netwatch.io/monitor=true"
    refresh_interval_sec: 30
    resources: ["service", "pod", "ingress"]  # hangi kaynak tipleri taransın
```

**Annotation şeması (operatör Service/Pod üzerine koyar):**

```yaml
# Kubernetes Service örneği
apiVersion: v1
kind: Service
metadata:
  name: payment-api
  annotations:
    netwatch.io/monitor: "true"           # bunu izle
    netwatch.io/type: "http"              # probe tipi
    netwatch.io/path: "/health"           # HTTP path
    netwatch.io/expected-status: "200"
    netwatch.io/expected-body: "ok"
    netwatch.io/app: "payment-gateway"    # AFFECTED_APPS bağlamı
    netwatch.io/owner-team: "fintech-sre"
    netwatch.io/depends-on: "postgres-primary"
    netwatch.io/probe-interval: "30"
```

**Mimari:**

Yeni paket: `internal/discovery/kubernetes/`

> **NOT:** CLAUDE.md'nin "yalnızca iki dizin" kuralını bilinçli ihlal ediyoruz. K8s entegrasyonu mantıken kendi dünyası, engine'in bir parçası değil. CLAUDE.md güncellenecek: "k8s eklenirse internal/discovery/kubernetes/ kabul edilir, başka alt-paket eklenemez."

```
internal/discovery/kubernetes/
  watcher.go        # Service/Pod/Ingress informer'lar
  parser.go         # Annotation → engine.Target dönüşümü
  reconciler.go     # discovered set ↔ engine.Targets diff
  config.go         # KubernetesConfig struct
```

**Dependency:**
- `k8s.io/client-go` — ~80MB transitive
- `k8s.io/api`
- `k8s.io/apimachinery`

Binary boyutu artar: ~25-35MB. Bu yüzden:

**Build tag stratejisi:**
- Default build: k8s olmadan (engine compile time'da hızlı, binary küçük)
- `make build-k8s` veya `go build -tags k8s` ile özel build

`internal/discovery/kubernetes/watcher.go` üst tarafında:
```go
//go:build k8s
```

Engine içinde `discovery.kubernetes.enabled: true` ama k8s tag yok ise startup hatası: "K8s discovery requires `netwatch-k8s` build. Use plain netwatch with static targets."

**ServiceAccount RBAC (helm chart'a eklenecek):**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: netwatch-discovery
rules:
- apiGroups: [""]
  resources: ["services", "pods", "endpoints"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["networking.k8s.io"]
  resources: ["ingresses"]
  verbs: ["get", "list", "watch"]
```

**Discovered target lifecycle:**

1. Informer event (Service added) → parser → engine.Target
2. Engine'in target listesi: `cfg.Targets ∪ discovered`
3. Discovered target → mevcut hash ring atamasına dahil edilir
4. Service silindi → discovered set'ten çıkar → probe loop stop
5. Hot-reload mantığı: 5-10 saniyede bir reconcile

**Risk noktaları:**

| Risk | Çözüm |
|---|---|
| Service IP'leri ya da Pod IP'leri? | Service IP'leri (uzun ömürlü). Pod IP'leri opsiyonel (`netwatch.io/probe-pods: "true"`) |
| Pod create/delete burst — sürekli recompute? | `scheduleRecompute` zaten 5s debounce. Burst güvenli. |
| Manuel target + discovered çakışması (aynı ID)? | Discovered target ID prefix: `k8s-` (örn. `k8s-payment-api.production`) |
| Annotation parsing hataları? | Per-target try/catch + warning log; geçersiz target skip |
| K8s API rate limit? | client-go zaten exponential backoff yapar |
| ServiceAccount yoksa? | `discovery.kubernetes.incluster: true` ama SA bulunamaz → fatal startup error |

**Test stratejisi:**

- `k8s.io/client-go/kubernetes/fake` ile mock client
- Service ekle → discovered target oluşmalı
- Service annotation güncelle → target güncellensin
- Service sil → target kaybolsun
- Helm chart için integration test (kind cluster)

**Tahmini efor:** 3-4 gün (kod + test + helm chart + CI)

**Bu sprint'te yapılmıyor.** F1-F4 bitince ayrı bir sprint olarak ele alınır.

---

### F6 — Process-Level Auto Discovery (REDDEDİLDİ — eski APM scope'u)

Kullanıcı önerisi: Subnet tarama → SSH/agent → Java bytecode injection → outbound socket izleme → 24h sonra target öner.

**Karar:** Bu netwatch'ın kapsamı dışındadır. Datadog/New Relic/OpenTelemetry alanı.

**Gelecekte düşünülebilecek alternatif:** Passive network discovery (nmap-style) ile açık port tespiti, common service signature'larıyla target önerisi. Bu bile **opsiyonel ayrı paket** olur, ana ürüne dahil edilmez.

**Bu sprint'te yapılmıyor. Tartışma sonrası ayrıca değerlendirilir.**

---

### Sprint Sıralama ve Bağımlılık

```
F1 (stagger) → bağımsız, hemen yapılabilir
F2 (cross-node depends_on) → bağımsız, F1'den sonra
F3 (maintenance) → bağımsız, F4'le birleştirilebilir  
F4 (soft_up) → bağımsız, F3'le birleştirilebilir
F5 (k8s SD) → ayrı sprint, sonra
F6 (process discovery) → tartışılacak, şimdilik out of scope
```

**Önerilen iş akışı:**
1. F1 → smoke test → kullanıcı onayı → commit + push
2. F2 → ROOT_CAUSE fix → 20-node E2E ile doğrula → kullanıcı onayı → commit
3. F3 → maintenance feature full + tests → kullanıcı onayı → commit
4. F4 → soft_up state machine → kullanıcı onayı → commit
5. (yeni sprint) F5 → k8s SD

**Her aşama sonu:**
- `go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/`
- `go test -race -count=1 ./internal/... ./tests/...`
- Manual smoke test
- Docs güncelle (GUIDE.md, GUIDE_EN.md, CLAUDE.md, system_map.md, developments.md)

---

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

**Kural:** Her aşama bitmeden sonrakine geçilmez. Aşama bitişinde kullanıcı smoke test yapar ve onay verir.

---

## Her Aşama Sonu Doğrulama Kontrol Listesi

Aşağıdaki adımlar her aşama sonunda uygulanır:

- [ ] `go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/` — derleme hatası yok
- [ ] `go test -race ./internal/engine/... ./internal/cluster/...` — tüm testler yeşil
- [ ] `go vet ./internal/engine/ ./internal/cluster/ ./cmd/linux/` — temiz
- [ ] **Smoke:** `config.yaml` ile binary çalıştır → `curl localhost:10240/metrics` + `/health` + `/status`
- [ ] **Smoke:** Bir target'ı intentional down et → log'da state geçişlerini gözlemle
- [ ] **Cluster (Phase 6+):** 3 lokal instance, farklı port ve node_name → `curl /cluster/state` 3 üye göstermeli

---

## ✅ Phase 7 — Quorum + Isolated Mode (TAMAMLANDI)

**Hedef:** Cluster çoğunluğu yoksa alarm üretme.

**Görevler:**

1. `cluster.go` içine `checkQuorum() bool` metodu ekle:
   - Formül: `activeMembers >= floor(expectedNodeCount * minQuorumRatio) + 1`
   - `expectedNodeCount` ve `minQuorumRatio` → `cluster.Config`'den okunur
   - `activeMembers` → `list.Members()` içinden `StateAlive` olanların sayısı

2. `cluster.Manager`'a background quorum goroutine ekle:
   - 5 saniyede bir `checkQuorum()` çağır
   - Quorum kazanımdan kayba geçişte: `slog.Warn("[CLUSTER] quorum lost", "active", n, "needed", k)`
   - Kayıptan kazanıma geçişte: `slog.Info("[CLUSTER] quorum recovered", "active", n)`

3. `Manager.IsolatedMode() bool` getter — dışarıdan okunabilir; `engine.go`'dan Phase 8'de alarm kararına bağlanacak

4. Quorum recover olduğunda `startAntiEntropy()` çağrısını placeholder olarak ekle (Phase 9'da implemente edilecek; şimdilik sadece log)

5. Üç yeni Prometheus gauge `engine.go` içinde kayıt edilecek:
   - `network_prober_quorum_healthy` — 1: quorum var, 0: yok
   - `network_prober_isolated` — 1: izole mod, 0: normal
   - `network_prober_cluster_size` — aktif üye sayısı (float64)

6. `engine.go` → `Engine.clusterMgr != nil` koşuluyla her 5 saniyede `updateClusterMetrics()` çağır

**Kabul kriteri:**
- 3 node cluster → 2 node öldür → kalan node log'da `[CLUSTER] quorum lost` + `network_prober_isolated=1`
- 2 node geri aç → `[CLUSTER] quorum recovered` + `network_prober_quorum_healthy=1`
- `cluster.enabled: false` → 3 yeni metrik yok, kod yolu açılmaz

---

## ✅ Phase 8 — Consistent Hashing + Exactly-Once Alerting (TAMAMLANDI)

**Hedef:** Aynı target'ı izleyen N node, alarm sadece 1 kez gönderir.

**Görevler:**

1. `cluster.HashRing` struct ekle:
   - Üye listesi üzerinde `FNV-32a(targetID) % len(members)` ile mod-N hashing
   - `members []string` sıralı tutulur (lex); üye listesi her `NotifyJoin/Leave` sonrası güncellenir
   - `Primary(targetID) string` ve `Secondary(targetID) string` metodları

2. `getResponsibleNode(targetID string) (primary, secondary string)`:
   - Hash ring'den primary + secondary hesapla
   - Primary `StateAlive` değilse secondary'e düş; ikisi de ölüyse boş string dön

3. `isResponsible(targetID string) bool`:
   - `m.cfg.NodeName == primary || m.cfg.NodeName == secondary`

4. **Probe akışı değişikliği** (`engine.go` / `loop.go`):
   - Her node her target için probe çalıştırmaya devam eder (lokal teşhis)
   - `runCheck()` / `processPending()` içinde alarm gönderme koşuluna ekle:
     ```
     if clusterMgr != nil && !clusterMgr.IsolatedMode() && !clusterMgr.isResponsible(t.key()) {
         // alarm gönderme — başka node sorumlu
     }
     ```

5. `OnStateReceived()` içinde scope hesabı:
   - `peerStates` üzerinde targetID için tüm node state'lerine bak
   - Tüm node'lar down → `GLOBAL`
   - Sadece bu node down, diğerleri up → `NODE_LOCAL`
   - Karışık → `PARTIAL`
   - Scope bilgisi alarm env'ine `SCOPE` key'iyle eklenir

6. Yeni metrik: `network_probe_cluster_status` (GaugeVec, aynı label'lar):
   - Tüm node'lar up → 1, herhangi biri down → 0
   - `OnStateReceived()` her çağrıldığında güncellenir

**Kabul kriteri:**
- 3 node aynı target izliyor, target down → **tek alarm** gönderilir, `SCOPE=GLOBAL`
- 1 node'un ağını kes → o node down görür, diğerleri up → alarm sadece sorumlu node'dan, `SCOPE=NODE_LOCAL`
- `/metrics`'te `network_probe_cluster_status` 0 veya 1 görünür
- `isolated=true` iken hiçbir alarm gönderilmez

---

## ✅ Phase 9 — Anti-Entropy (TAMAMLANDI)

**Hedef:** Yeniden katılan node alarm storm yapmasın.

**Görevler:**

1. `gossipDelegate.LocalState(join bool) []byte`:
   - `join=true` → `engine.lastKnown` tam state'i serialize et (v2 JSON)
   - `join=false` → memberlist zaten periyodik push-pull yapıyor, bu kol için mevcut `peerStates` serialize et

2. `gossipDelegate.MergeRemoteState(buf []byte, join bool)`:
   - `join=true` olduğunda tam sync moduna gir
   - Her `(targetID, Seq)` çifti için karar:
     - Remote seq > lokal → lokal state'i güncelle, **alarm gönderme** (cluster zaten kararını verdi)
     - Lokal seq > remote → sadece broadcast et, alarm gönderme (sorumluluk Phase 8'in)
     - Remote cluster down biliyor + lokal zaten hard_down + alarm gönderildi → sessiz kal

3. `Engine.syncing atomic.Bool` field'ı ekle:
   - `MergeRemoteState(join=true)` başında `syncing.Store(true)`, bitişinde `false`
   - `runCheck()` ve `processPending()` başına: `if e.syncing.Load() { return }` — sync süresince yeni alarm üretme

4. Sync tamamlandıktan sonra `syncing=false`; normal operasyona geç

**Kabul kriteri:**
- 3 node, 1 node'u durdur → target down → 2 node alarm gönderir
- Durdurulan node'u geri başlat → re-join sırasında `syncing=true`
- Re-join tamamlanınca **3. alarm gönderilmez**; lokal `state.json` ve `lastKnown` cluster state'iyle uyuşur

---

## ✅ Phase 10 — Lifecycle Komutları (TAMAMLANDI)

**Hedef:** systemd/Windows Service entegrasyonu için CLI komutları.

**Görevler:**

1. `cmd/linux/main.go` veya yeni `cmd/netwatch/main.go`'ya subcommand routing ekle:
   - `netwatch init [--config-dir DIR]`:
     - `config.yaml` + `credentials.env` iskeleti üret
     - Linux: `/etc/systemd/system/netwatch.service` yaz
     - Windows: `sc create` hint'i yaz
   - `netwatch leave [--reason TEXT]`:
     - Çalışan agent'ın `/cluster/leave` endpoint'ine POST at
   - `netwatch uninstall`:
     - `leave` çağır → servis sil → dosyaları sil (onay sorar)
   - `netwatch service install/remove` (Windows):
     - `cmd/windows/main.go`'daki `installService` fonksiyonuna CLI'dan ulaş

2. `/cluster/leave` HTTP endpoint'ini `cmd/linux/main.go`'ya ekle:
   - Handler: `e.ClusterManager().Leave(5s)` + `os.Exit(0)`

**Kabul kriteri:**
- `netwatch init` → config dosyaları + systemd unit oluşur
- `netwatch leave` → çalışan agent'a POST → `[CLUSTER] leaving` logu
- `netwatch uninstall` → onay sonrası temizlenir

---

## ✅ Phase 11 — Deployment Artifacts (TAMAMLANDI)

**Hedef:** Operasyonel hazırlık.

**Görevler:**

1. `netwatch.service` (systemd unit):
   ```ini
   [Service]
   AmbientCapabilities=CAP_NET_RAW
   ExecStart=/usr/local/bin/netwatch --config /etc/netwatch/config.yaml
   Restart=on-failure
   ```

2. `Makefile` hedefleri:
   - `build-linux` — CGO_ENABLED=0 GOOS=linux
   - `build-windows` — GOOS=windows
   - `test` — `go test -race ./internal/...`
   - `test-integration` — `go test -race -tags integration ./test/integration/...`
   - `lint` — `golangci-lint run`
   - `all` — build-linux + test + lint

3. `Dockerfile` güncellemesi:
   - `notifications/` dizini için VOLUME tanımla
   - EXPOSE `10240/tcp` ve `7946/tcp` + `7946/udp` belgelenmeli
   - Multi-stage: builder → `scratch` veya `distroless`

4. `helm/` chart (DaemonSet):
   - `hostNetwork: true`
   - `securityContext.capabilities.add: [NET_RAW]`
   - Peer discovery için headless Service
   - `configMap` + `secret` (keyring)

5. `config.example.yaml` — tam dökümante, standalone + cluster örneği bir arada

**Kabul kriteri:**
- Helm ile 3-node DaemonSet → `kubectl exec` → `/cluster/state` 3 üye gösterir
- `make all` hatasız tamamlanır

---

## ✅ Phase 12 — Integration Tests (TAMAMLANDI)

**Hedef:** Regresyon güvencesi.

**Görevler:**

1. `test/integration/standalone_test.go`:
   - Config ile binary başlat (test helper)
   - Mock TCP server kapat → `state.json`'a `hard_down` yazıldığını doğrula
   - Alert script çağrıldığını doğrula (`AFFECTED_APPS` env dahil)

2. `test/integration/cluster_test.go`:
   - 3 node lokal başlat (farklı port + node_name)
   - Target down et → tek alarm (exactly-once)
   - Scope hesabı: GLOBAL vs NODE_LOCAL doğrula

3. `test/integration/antientropy_test.go`:
   - Phase 9 senaryosu: node dur → target down → node geri aç → 3. alarm gelmiyor

4. `test/integration/keyrotation_test.go`:
   - Keyring'e yeni key ekle → rolling restart → sıfır gossip kesintisi

5. CI gate: `go test ./... -race -timeout 120s`

**Kabul kriteri:**
- Tüm testler `go test -race -timeout 120s ./...` ile geçer
- Hiçbir data race raporu

**Sonuç:** 7 test, 3 paket, hepsi `-race` ile yeşil.
`go test -race -timeout 300s ./internal/engine/... ./internal/cluster/... ./test/integration/...`

```
ok  github.com/saidtaylan/netwatch/internal/engine       1.6s
ok  github.com/saidtaylan/netwatch/internal/cluster      1.9s
ok  github.com/saidtaylan/netwatch/test/integration    110.5s
```

cluster.go race fix: `ringMu` protects `m.list` assignment + `inventoryRefreshHandler` read under `mu.RLock`.

---

## ✅ Phase 13 — Distributed Probe Ownership (TAMAMLANDI) — Distributed Probe Ownership (Probe Sorumluluğu Dağıtımı)

**Hedef:** Cluster'daki her node'un her target'ı probe etmesi yerine, her target için consistent hashing ile seçilen N node'luk bir alt küme probe etsin. Geri kalan node'lar sadece gossip dinler. Operatör konfigürasyon yazmaz — cluster otomatik karar verir.

### Motivasyon

**Mevcut sorun:**
- 100 node'lu cluster'da aynı target → 100 probe/dakika (target üzerinde gereksiz yük; HTTP probe'larında özellikle DDoS riski)
- Erişim izni olmayan node'lar bile probe atıyor (her seferinde fail eden gürültü)

**Çözüm:**
- Bir target'ı "tanıyan" (config'inde olan) node'lar **candidate set** oluşturur
- Hash ring + replication factor ile **prober subset** seçilir
- Yalnızca bu subset probe eder; diğer node'lar gossip ile state alır
- Subset'ten bir node düşerse hash ring otomatik yenisini sokar (Phase 8 mekanizması zaten var, genişletilecek)

### Tasarım Kararları

| Karar | Değer | Gerekçe |
|-------|-------|---------|
| Default replication factor | **3** | Tipik cluster boyutuna uygun, 3 lokasyonlu redundancy yeterli |
| Config alanı | `cluster.probe_replication_factor` | Operatör gerekirse override eder |
| Candidate ≤ factor durumu | Tüm candidate'lar probe eder | Gereksiz seçim mantığı yok |
| Zone alanı | `cluster.zone` (opsiyonel string) | Operatör yazarsa kullanılır, yoksa kural devre dışı |
| Hostname türetimi | **Yok** | Implicit magic istemiyoruz; explicit > implicit |
| Geriye uyum | 3 node'lu cluster aynı davranışta kalır | factor=3 default + N≤3 hepsi probe → değişiklik yok |

### Görevler

#### 1. Config genişlemesi

**`internal/cluster/cluster.go`:**
```go
type Config struct {
    // ... mevcut alanlar
    Zone                   string `json:"zone,omitempty"`
    ProbeReplicationFactor int    `json:"probe_replication_factor,omitempty"`  // 0 = default 3
}

func (c Config) effectiveReplicationFactor() int {
    if c.ProbeReplicationFactor > 0 {
        return c.ProbeReplicationFactor
    }
    return 3
}
```

`config.example.yaml` güncellemesi:
```yaml
cluster:
  node_name: "node-01"
  zone: "istanbul"                   # opsiyonel
  probe_replication_factor: 3        # opsiyonel, default 3
```

#### 2. Candidate Set Derivation — Mevcut State Broadcast'larından

**Karar:** Ayrı bir `InventoryPayload` mesaj tipi **eklemiyoruz**. Memberlist'i overload etmemek için mevcut `GossipPayload` infrastructure'ı kullanılır.

**Candidate set türetimi:**
```
candidates(targetID) = { N : peerStates[N][targetID] exists }  ∪  { localNode if targetID in localConfig }
```

Yani: bir node'un bir target'a state broadcast etmiş olması = "ben bu target'ı tanıyorum" demektir. Candidate set bu listeden çıkar.

**Bootstrap problemi ve çözümü:**

Yeni başlayan veya reload edilen node henüz probe yapmadıysa peer'lar onun target listesini bilemez → o node candidate set'e girmez → hiç prober seçilmez (chicken-and-egg).

**Çözüm:** Startup ve `LoadConfig` (reload) sonrası, her local target için **tek bir** state broadcast at:
- `state.json`'da target için kayıt varsa → o state ile broadcast (örn. `up`, `hard_down`)
- Kayıt yoksa → `state="unknown"` ile broadcast

Bu N target × 1 broadcast = çok düşük yük. Tipik 50 target × 50 byte ≈ 2.5KB toplam (UDP MTU altında, tek seferlik).

**Engine değişikliği:**
```go
// engine.go — Init() ve Reload() sonunda
func (e *Engine) broadcastInitialInventory() {
    if e.clusterMgr == nil { return }
    e.mu.RLock()
    targets := append([]Target(nil), e.cfg.Targets...)
    e.mu.RUnlock()

    e.stateMu.RLock()
    defer e.stateMu.RUnlock()
    for _, t := range targets {
        if !t.active() { continue }
        ps, ok := e.lastKnown[t.key()]
        if !ok {
            ps = PersistedState{State: "unknown"}
        }
        e.broadcastState(t, ps)   // mevcut helper, TargetName/Type doluyor
    }
}
```

Anti-entropy (Phase 9) zaten 30sn'de bir tüm state'i push-pull ile sync ediyor → reload kaçırılırsa bile maks 30sn lag. Ek mesaj yok, ek bandwidth yok.

**Cluster Manager değişikliği:**
```go
// cluster.go — peerStates derivation helper
func (m *Manager) CandidatesFor(targetID string) []string {
    m.mu.RLock()
    defer m.mu.RUnlock()

    seen := map[string]bool{}
    var out []string

    // Mevcut peer state'lerinden çıkar
    for nodeName, targets := range m.peerStates {
        if _, ok := targets[targetID]; ok && !seen[nodeName] {
            seen[nodeName] = true
            out = append(out, nodeName)
        }
    }
    // Lokal config'i ekle (bootstrap durumunda peerStates'te kendimiz yokuz)
    if m.localKnowsTarget(targetID) && !seen[m.cfg.NodeName] {
        out = append(out, m.cfg.NodeName)
    }

    sort.Strings(out)   // determinism
    return out
}

// Engine, hangi target'ları lokal olarak bildiğini cluster'a deklare eder
func (m *Manager) SetLocalTargetProvider(p LocalTargetProvider)
```

**Zone bilgisi memberlist NodeMeta üzerinden:**

Memberlist `Delegate.NodeMeta(limit int) []byte` her node için küçük metadata yayınlar — otomatik distribution, ek mesaj yok.

```go
// gossipDelegate.NodeMeta()
type nodeMeta struct {
    Zone string `json:"zone,omitempty"`
}
func (d *gossipDelegate) NodeMeta(limit int) []byte {
    b, _ := json.Marshal(nodeMeta{Zone: d.mgr.cfg.Zone})
    if len(b) > limit { return nil }
    return b
}

// Manager.zoneOf
func (m *Manager) zoneOf(nodeName string) string {
    for _, n := range m.list.Members() {
        if n.Name == nodeName {
            var meta nodeMeta
            if err := json.Unmarshal(n.Meta, &meta); err == nil {
                return meta.Zone
            }
        }
    }
    return ""
}
```

NodeMeta limit (memberlist default `MetaMaxSize=512`) zone gibi tek alan için fazlasıyla yeterli.

#### 3. Prober Selection Algoritması

**`internal/cluster/probers.go`** (yeni dosya):

```go
// SelectProbers returns the deterministic prober set for a given target.
// Returned slice is at most replicationFactor long. Empty if target unknown.
func (m *Manager) SelectProbers(targetID string) []string {
    candidates := m.candidatesFor(targetID)         // peerInventories'ten
    factor := m.cfg.effectiveReplicationFactor()

    if len(candidates) == 0 {
        return nil
    }
    if len(candidates) <= factor {
        return candidates                            // hepsi probe eder
    }

    // Hash ring üzerinde walk: targetID hash'inden başla
    sorted := sortedByHash(candidates, targetID)

    if m.zonesAvailable() {
        return zoneAwarePick(sorted, factor, m.zoneOf)
    }
    return sorted[:factor]
}

// IsLocalProber returns true if this node should probe targetID.
func (m *Manager) IsLocalProber(targetID string) bool {
    selected := m.SelectProbers(targetID)
    for _, n := range selected {
        if n == m.cfg.NodeName {
            return true
        }
    }
    return false
}
```

**Zone-aware picker (öncelik kuralı):**

Zone'lu node'lar **her zaman** zone'suzlardan önce. Zone'suzlar son tercih (ancak zone'lu kalmadığında).

Üç katmanlı seçim:

1. **Tier-1: Zone diversity** — farklı zone'lardan birer node (en yüksek öncelik)
2. **Tier-2: Zone repeat** — Tier-1 tükendiyse aynı zone'dan ikinci node (zone'suza tercih edilir)
3. **Tier-3: Zone-less fallback** — sadece Tier-1+Tier-2 yetmediğinde

```go
// zoneAwarePick: tüm zone-tagged candidate'lar zone-less'lerden önce seçilir.
// Önce her zone'dan birer (diversity), sonra zone-tagged repeat, en son zone-less.
func zoneAwarePick(sortedByHash []string, factor int, zoneOf func(string) string) []string {
    var withZone, withoutZone []string
    for _, n := range sortedByHash {
        if zoneOf(n) != "" {
            withZone = append(withZone, n)
        } else {
            withoutZone = append(withoutZone, n)
        }
    }

    picked := make([]string, 0, factor)
    seenZones := map[string]bool{}

    // Tier 1: her zone'dan bir tane (diversity)
    var leftoverWithZone []string
    for _, n := range withZone {
        if len(picked) == factor { break }
        z := zoneOf(n)
        if !seenZones[z] {
            picked = append(picked, n)
            seenZones[z] = true
        } else {
            leftoverWithZone = append(leftoverWithZone, n)
        }
    }

    // Tier 2: zone repeat (zone-less'ten önce gelir)
    for _, n := range leftoverWithZone {
        if len(picked) == factor { break }
        picked = append(picked, n)
    }

    // Tier 3: zone'suz fallback (son tercih)
    for _, n := range withoutZone {
        if len(picked) == factor { break }
        picked = append(picked, n)
    }

    return picked
}
```

**Davranış örnekleri (factor=3):**

| Senaryo | Candidate'lar | Sonuç |
|---------|---------------|-------|
| 3 farklı zone (A,B,C × 1'er node) | A1, B1, C1 | A1, B1, C1 (her zone'dan bir) |
| 2 zone (A × 2, B × 1) | A1, A2, B1 | A1, B1, A2 (Tier-1: A,B → Tier-2: A2) |
| 1 zone (A × 5) + zone'suz × 5 | A1..A5, X1..X5 | A1, A2, A3 (zone-less'e hiç düşmez) |
| Zone tagged 2 + zone'suz 5 | A1, B1, X1..X5 | A1, B1, X1 (Tier-1+2 yetersiz, Tier-3'e iner) |
| Hiçbiri zone'lu (legacy mode) | X1..X5 | X1, X2, X3 (saf hash sırası) |

**Determinism:** Aynı `sortedByHash` + zone fonksiyonu → her zaman aynı çıktı. Hash fonksiyonu `FNV-32a` (Phase 8 ile aynı).

#### 4. Engine Probe Loop Entegrasyonu

**`internal/engine/loop.go`:**

`startProbeLoop` çağrısı koşula bağlanır:

```go
func (e *Engine) shouldProbeLocally(t Target) bool {
    if e.clusterMgr == nil {
        return true                                  // standalone
    }
    return e.clusterMgr.IsLocalProber(t.key())
}
```

**`Init()` ve `Reload()`:**
- Aktif target'lar için `shouldProbeLocally` çek
- `true` ise `startProbeLoop`, `false` ise `stopProbeLoop`
- Hot-reload sonrası inventory broadcast tetikle

**Membership değişikliği reaktivitesi:**
- `eventDelegate.NotifyJoin/Leave/Update` → `m.recomputeProberAssignments()` çağır
- Recompute: tüm bilinen target'lar için `IsLocalProber` yeniden hesaplanır, değişen target'lar için engine'e callback at
- Yeni interface: `cluster.ProberAssignmentListener`
  ```go
  type ProberAssignmentListener interface {
      StartProbing(targetID string)
      StopProbing(targetID string)
  }
  ```
- Engine bu interface'i implement eder; `SetProberAssignmentListener(e)` ile bağlanır.

> **Önemli:** Membership flapping ile sürekli start/stop olmaması için **debounce** uygula — `recomputeProberAssignments` 5 saniyelik tampon (`time.AfterFunc`) ile çağrılır.

#### 5. Peer-Alert Mekanizması Sadeleştirmesi

Phase 9 sonrası eklenen `PeerAlertHandler` mantığı **korunur** ama tetiklenme şartı zaten doğru:
- Primary node hard_down state aldı + `HasLocalProbe()=false` → DispatchPeerAlert

Yeni model'de primary genelde prober subset içinde olduğu için bu yol nadir tetiklenir. Yine de **tutuyoruz** — operatör hash collision veya zone constraint nedeniyle primary'nin prober olmadığı edge case'ler için sigorta.

`HasLocalProbe(targetID)` metodu artık **yalnızca aktif probe loop varsa** true döner (gerçek "şu an probe ediyorum" sorusunu cevaplar).

#### 6. Yeni Endpoint ve Metrikler

**`GET /cluster/probers`:**
```json
{
  "self": "node-01",
  "replication_factor": 3,
  "assignments": {
    "db-primary": {
      "probers": ["node-02", "node-05", "node-07"],
      "i_probe": false,
      "primary": "node-02",
      "zones": {"node-02": "istanbul", "node-05": "ankara", "node-07": "izmir"}
    }
  }
}
```

**Yeni metrikler:**
- `network_probe_local_assigned{target,type}` — 1: bu node probe ediyor, 0: değil
- `network_probe_prober_count{target,type}` — gerçek prober sayısı (replication factor'dan az olabilir)
- `network_probe_inventory_peers` — bilinen peer inventory sayısı (cluster size'a yaklaşmalı)

#### 7. Standalone Mod Davranışı

`cluster.enabled: false` durumunda **hiçbir değişiklik yok**:
- `clusterMgr == nil`
- `shouldProbeLocally` her zaman `true` döner
- Yeni endpoint 503 döner (mevcut `/cluster/state` davranışı gibi)
- Yeni metrikler kayıt edilmez

#### 8. Validation

`config.LoadCluster()`:
- `probe_replication_factor < 0` → hata
- `probe_replication_factor > 0` ve `expected_node_count` ile uyumsuzluk → uyarı log (örn. factor=10 ama expected=3)

`Manager.Start()`:
- Inventory broadcast'tan önce sürekli sıralı target ID listesi hazırlanır (deterministic hash için)

### Test Senaryoları (`phase13_test.go`)

1. **Replication factor uygulama:** 5 node, factor=3 → her target için tam 3 prober seçilir
2. **Determinism:** Aynı candidate + target → 100 iterasyon, her seferinde aynı prober set
3. **Zone diversity (full):** 6 node, 3 zone × 2 node, factor=3 → 3 farklı zone'dan birer node
4. **Zone repeat tercihi:** 6 node, 2 zone × 3 node, factor=3 → Tier-1: her zone'dan bir (2 node), Tier-2: zone repeat (3. node)
5. **Zone-less son tercih:** 5 node zone-tagged + 5 node zone-less, factor=3 → tamamen zone-tagged (zone-less'e hiç düşülmez)
6. **Zone-less fallback (mecbur kaldığında):** 2 zone-tagged + 5 zone-less, factor=3 → 2 zone-tagged + 1 zone-less (Tier-3 devreye girer)
7. **Legacy mode (zone yok):** 5 zone-less node, factor=3 → saf hash sırası, davranış değişmez
8. **Bootstrap broadcast:** Yeni node startup → her local target için 1 state broadcast → 1sn içinde diğer node'lar candidate set'e ekler
9. **Reload broadcast:** Hot-reload ile yeni target → broadcast tetikleyici çalışır, peer'lar görür
10. **Membership değişimi:** Prober node leave → 5sn debounce sonrası ring güncellenir, yeni node atanır, alarm storm yok
11. **NodeMeta zone propagation:** Node zone'u değişip restart edildi → memberlist `Update` event → 5sn içinde tüm peer'lar yeni zone'u görür
12. **Backward compat:** 3 node, factor=3 (default), zone yok → mevcut davranış (hepsi probe eder)
13. **Standalone:** `cluster.enabled=false` → tüm target'lar lokal probe edilir, yeni endpoint 503
14. **Edge: target hiçbir node'da yok** → CandidatesFor boş slice, SelectProbers boş, log uyarı
15. **Edge: tüm prober'lar offline** → kalan node'lar candidate set'i hâlâ peerStates'te tutar, recompute olur, yeni prober seçilir
16. **Anti-entropy bootstrap:** Cluster zaten 3 prober seçmişken yeni node join → anti-entropy push-pull → yeni node peerStates ve NodeMeta'yı alır, kendisini candidate olarak görür ama prober değilse probe başlatmaz

### Kabul Kriteri

- ✅ 5 node'lu cluster, factor=3, tek target → `/cluster/probers` endpoint'inde 3 prober gösterir
- ✅ 3 prober dışındaki node'lar Prometheus metriklerinde `network_probe_local_assigned=0` döner
- ✅ Aktif prober node'lardan biri Ctrl+C → 5sn içinde başka node devralır, alarm storm yok
- ✅ Zone tanımlı 6 node (3 farklı zone × 2'şer node) + factor=3 → 3 farklı zone'dan birer node
- ✅ Zone karma (5 zone-tagged + 5 zone-less) + factor=3 → tamamen zone-tagged seçilir, zone-less hiç seçilmez
- ✅ Zone tanımsız 5 node + factor=3 → ilk 3 hash sırasından seçilir, log temiz
- ✅ Standalone mod (`cluster.enabled=false`) → hiçbir davranış değişikliği, mevcut testler yeşil
- ✅ **Memberlist trafiği eski seviyede:** Yeni gossip mesaj tipi yok, sadece startup/reload başına N adet `GossipPayload` (state broadcast'ı zaten var)
- ✅ Hot-reload → 30sn içinde tüm cluster yeni candidate set'i alır (anti-entropy push-pull), prober atamaları güncellenir
- ✅ NodeMeta üzerinden zone propagation çalışır (`memberlist.Update` event ile)
- ✅ `go test -race -timeout 60s ./internal/cluster/... ./internal/engine/...` yeşil

### Riskler ve Karşı Önlemler

| Risk | Önlem |
|------|-------|
| Membership flapping → sürekli start/stop probe loop | 5sn debounce timer (recomputeProberAssignments) |
| Bootstrap chicken-and-egg (yeni node hiç probe yapmadığı için candidate set'te görünmez) | Startup ve reload'da her local target için tek bir state broadcast (mevcut altyapı, ek mesaj yok) |
| Yarım sync sırasında yanlış prober seçimi | `syncing=true` iken assignment değiştirme — Phase 9 guard'ı genişletilir |
| Zone yanlış konfigürasyon (typo) → hatalı diversity | `/cluster/probers` endpoint zone'ları gösterir, operatör doğrulayabilir |
| Hash collision → bir node prober subset'inde 2 kez | `sortedByHash` unique node listesi alır; impossible by construction |
| Geri dönüş için: tüm node'lar tekrar probe etsin | `probe_replication_factor: 999` ile factor>candidate olur, hepsi seçilir |
| NodeMeta size aşımı | Memberlist default `MetaMaxSize=512`; sadece zone (kısa string) → güvenli |
| State broadcast'larının candidate set türetimi için yeterli sıklıkta gelmemesi | Anti-entropy (Phase 9) push-pull zaten 30sn'de tüm state'i sync eder; bootstrap broadcast ek garanti |

### Açık Sorular

1. ~~Inventory boyut sınırı~~ → **Çözüldü:** Ayrı inventory mesajı yok; mevcut state broadcast'ları yeterli.
2. **State broadcast frequency yeterli mi?** → Probe başına 1 broadcast + anti-entropy 30sn → cluster'ın yeni target'ı öğrenmesi maks 30sn.
3. **NodeMeta değişikliği tüm cluster'a ulaşma süresi?** → Memberlist gossip → tipik <5sn convergence; testte ölç.
4. **`recomputeProberAssignments` debounce penceresi 5sn mi 10sn mi?** → 5sn'de başla, smoke test'te flapping görülürse artırılır.

### Implementasyon Sırası

```
✅ Step 1: Config alanları (Zone, ProbeReplicationFactor) + validation       (commit fd33b10)
✅ Step 2: NodeMeta üzerinden zone propagation (gossipDelegate.NodeMeta)     (commit fd33b10)
✅ Step 3: CandidatesFor (peerStates derivation) + LocalTargetProvider interface  (commit cabd6bb)
✅ Step 4: SelectProbers + zone-aware picker (3-tier) + unit testler          (commit cabd6bb)
✅ Step 5: Bootstrap broadcast + Active Probe Delegation (todo.md F6 entegre)  (commit 5826737)
✅ Step 6: ProberAssignmentListener interface + engine StartProbing/StopProbing entegrasyonu
✅ Step 7: Reactive recompute + 5sn debounce (NotifyJoin/Leave/Update)   (commit ba027b7)
✅ Step 8: /cluster/probers + /fleet/status endpoint'leri + Phase 13 metrikleri (todo.md F2 minimal entegre)
✅ Step 9: Anti-entropy syncing guard — StartProbing/StopProbing/bootstrap defer + SetSyncing(false) recompute trigger
✅ Step 10: Integration test — fakeCluster, 8 senaryo (factor invariant, zone spread, ProbeFrom pin, failover, churn, concurrency)
✅ Step 11: Dokümantasyon — config.example.yaml Phase 13 alanları, CLAUDE.md + README.md güncelleme
✅ Step 12: Smoke test — 3 yerel binary (istanbul/ankara/izmir zones), failover, quorum-loss doğrulandı
```

Her adım sonrası `go build` + `go test -race` yeşil olmalı.

---

## ✅ P1.3 — Scope Intelligence Enhancement (TAMAMLANDI)

**Hedef:** Ham GLOBAL/PARTIAL/NODE_LOCAL scope etiketlerini insan-okunabilir sınıflandırma (REAL_OUTAGE / NETWORK_PARTITION / LOCAL_FAILURE / AMBIGUOUS) ve [0.0–1.0] güven skoru ile zenginleştir.

**Yeni dosya:** `internal/engine/scope.go`

**Ana değişiklikler:**

- `DetailedScope` struct: `Scope`, `Classification`, `DownNodes`, `UpNodes`, `OfflineNodes`, `PartitionGroups`, `Confidence`
- `classifyScope(targetID) DetailedScope` — Engine metodu; standalone + cluster modları ayrı path'te
- `ScopeEnv()` → `SCOPE`, `CLASSIFICATION`, `CONFIDENCE`, `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES` env map'i; tüm alert kanalları alır
- `notify.go` — `computeScope()` yerine `classifyScope().ScopeEnv()` kullanıyor
- `fleet.go` — `FleetTarget`'a `Classification` + `Confidence` alanları eklendi

**Sınıflandırma kuralları:**

| Durum | Classification | Confidence |
|-------|----------------|------------|
| Tüm node'lar down, offline yok | REAL_OUTAGE | 1.0 |
| Tüm node'lar down, bazı offline | AMBIGUOUS | downCount/clusterSize (max 0.95) |
| Sadece local node down | LOCAL_FAILURE | upCount/totalKnown |
| Bazı down, bazı up | NETWORK_PARTITION | split simetrisine göre |
| Veri yetersiz | AMBIGUOUS | 0.4–0.5 |

**Test sonuçları:** `scope_test.go` — 9 unit test; `go test -race` yeşil.

---

## ✅ P1.4 — SLO Tracker (TAMAMLANDI)

**Hedef:** Rolling-window SLO hesabı, incident persistence, error budget tracking, breach alerting.

**Yeni dosya:** `internal/engine/slo.go`

**Özellikler:**

- `incidents.json` — state.json ile aynı dizinde; atomik yazma; `retention_days` (default 90) göre prune
- `sloManager.ComputeSLO(targetID, targetUptime, window)` — 30d/7d/24h; aktif (açık) incident'lar ongoing sayılır
- `runSLOChecker` goroutine (60sn): breach edge-triggered alert — `STATUS=slo_breached`, SLO_* env değişkenleri
- `sloRecordStart`/`sloRecordEnd` → `markHardDown`/`markRecovered` path'lerine bağlı
- 3 Prometheus metriği (sadece `slo.enabled: true` iken register):
  - `network_probe_slo_uptime_ratio{target_id, window}`
  - `network_probe_slo_error_budget_seconds{target_id, window}`
  - `network_probe_slo_breached{target_id}`
- `/slo` endpoint — JSON SLOSnapshot; disabled → 503

**Config örneği:**
```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops"]
  targets:
    - id: "db-primary"
      target_uptime: 0.999
      window: "30d"
```

**Test sonuçları:** `slo_test.go` — 12 unit test; `go test -race` yeşil.

---

## ✅ P1.5 — Gossip Config Sync (TAMAMLANDI)

**Hedef:** Cluster içindeki node'ların config drift'ini gossip üzerinden tespit etmesi.

**Tamamlanan işler:**
- `internal/cluster/configsync.go` yeni dosya: `ConfigBroadcast`, `cfgBroadcast`, `ConfigHashOf`, `SetLocalConfigInfo`, `handleConfigBroadcast`, `ConfigDriftDetected`, `ConfigSyncSnapshot`, `runConfigSyncLoop`
- `GaugeConfigDrift` (`network_probe_config_drift`) Prometheus metriği
- `NotifyMsg`'de `msg_type` peek ile backward-compat mesaj ayrıştırma
- `GET /cluster/config` endpoint (503 cluster disabled ise)
- `Engine.LoadConfig` sonrası `SetLocalConfigInfo(ConfigHashOf(raw), ...)` çağrısı
- `configsync_test.go` — 7 test, tümü yeşil

---

## ✅ P1.6 — Geo Latency + Region-Based Probe Filter (TAMAMLANDI)

**Hedef:** Coğrafi bölge bazlı latency görünümü ve anomaly tespiti; probe_from_regions kısıtlaması.

**Tamamlanan işler:**
- `internal/cluster/geolat.go` yeni dosya: `GeoLatencyEntry`, `GeoLatencySnapshot`, `GeoLatencyForTarget`, `detectLatencyAnomaly`, `UpdateGeoMetrics`, `regionOf`/`RegionOf`
- `GossipPayload.Latency float64` — başarılı probda `elapsed` değeri taşınıyor
- `Config.Region string` ve `nodeMeta.Region` — node-level coğrafi etiket
- `Target.ProbeFromRegions []string` — bölge bazlı candidate filtresi
- `Engine.ProbeFromRegionsConstraint()` — `LocalTargetProvider` arayüzüne eklendi
- `Engine.lastLatency sync.Map` — per-target son latency değeri
- `GaugeGeoLatency` + `GaugeGeoLatencyAnomaly` Prometheus metrikleri
- `GET /geo/latency/{targetID}` endpoint
- `geolat_test.go` — 15 test, tümü yeşil

---

---

## ✅ CLI Join Workflow + Startup Banner (TAMAMLANDI — 2026-05-14)

**Hedef:** Operatörün cluster'a node eklemesi için tek komutluk akış. kubeadm/elasticsearch tarzı.

**Tamamlanan görevler:**

1. `netwatch init --cluster` — cluster-enabled config skeleton + random AES-256 keyring + copy-paste join komutu çıktısı
2. `netwatch join --keyring K --addr H:P [--bind-port N] [--node-name N] [--config PATH]` — config yoksa skeleton, varsa cluster.* override, atomik yazım
3. `netwatch keyring generate` — base64 AES-256 key basar
4. Startup banner: `cluster.enabled=true` agent başladıktan sonra stdout'a node adı + LocalAddr + keyring ile join komutunu basar
5. `internal/engine/join.go` — `GenerateKeyringKey()`, `LocalClusterAddr()`, `ClusterPrimaryKey()`, `ClusterMemberCount()`
6. `cluster.Manager.LocalAddr()`, `Manager.PrimaryKey()` — memberlist LocalNode() üzerinden gerçek advertise adresi
7. cmd/linux + cmd/windows: yeni subcommand'lar + helper'lar (`promptYesNo`, `validKeyringKey`, `maskKeyring`, `defaultAdvertiseAddr`)
8. `/cluster/config` GET (drift snapshot) ve PUT (config push) handler'ları tek mux pattern'a birleştirildi (mux conflict bug fix)
9. Init overwrite prompt: config varsa "Overwrite? [y/N]" default hayır, `--force` ile bypass

**Build + Test:**
```
go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
GOOS=windows go build ./cmd/windows/                          ✓
go test -race -count=1 -timeout 120s ./internal/...           ✓
```

---

## ✅ Config Push/Sync + node_alias + Admin Auth (TAMAMLANDI — 2026-05-14)

**Hedef:** Bir node'dan ortak konfigürasyonu tüm cluster'a dağıtabilme.

**Tamamlanan görevler:**

1. `PUT /cluster/config` endpoint'i — kısmi SharedConfig body (JSON veya YAML), kendine uygula + gossip TCP ile tüm peer'lara dağıt. `applied_locally`, `broadcast_to`, `failed_nodes`, `fields_applied` response.
2. `POST /cluster/config/sync` endpoint'i — bu node'un diskindeki shared field'larını peer'lara dağıt (credential-safe: pre-injection bytes kullanır).
3. `internal/engine/configpush.go` — `SharedConfig`, `SharedClusterConfig` struct'ları; `ExtractSharedConfig()`, `ApplySharedConfigJSON()`, `AppliedFields()`.
4. `internal/cluster/configpush.go` — `ConfigPushPayload`, `ConfigPushHandler` interface, `BroadcastConfigPush()`, `handleConfigPush()`, `SetConfigPushHandler()`.
5. `cluster.go` `NotifyMsg`'da `msg_type: "config_push"` dispatch eklendi.
6. `engine.go` `Init()`'de `SetConfigPushHandler(e)` wiring.
7. `app_name` → `node_alias` rename: `Config.NodeAlias`, backward compat migration, `AppName()` deprecated wrapper, `NODE_ALIAS` env var eklendi, `APP_NAME` korundu.
8. `AdminConfig` struct + `admin.token` bearer auth: `checkAdminAuth()` helper, write-capable endpoint'lerde (`PUT /cluster/config`, `POST /cluster/config/sync`, `POST /cluster/keyring/rotate`, `POST /cluster/leave`) auth guard. Extensible tasarım (ileride `Users []AdminUser`).
9. `cmd/linux/main.go` + `cmd/windows/main.go` — tüm endpoint'ler + `checkAdminAuth` + `parseSharedConfigBody` (JSON/YAML content-type dispatch).
10. `config.example.yaml`, `config.yaml`, `GUIDE.md`, `GUIDE_EN.md`, `README.md`, `CLAUDE.md`, `developments.md`, `system_map.md` güncellendi.

**Ortak (eşitlenen) alanlar:**
```
timeout, max_retries, retry_interval_sec, ticker_interval_sec, probe_interval_sec,
reload_interval_sec, watchdog_threshold_sec, notifications, default_notify,
cluster.keyring, cluster.peers, cluster.expected_node_count, cluster.min_quorum_ratio,
cluster.probe_replication_factor, cluster.min_probe_confirmations
```

**Node-specific (asla üzerine yazılmaz):**
```
port, node_alias, log_path, state_file, credentials_file, targets, apps, slo,
cluster.node_name/bind_*/advertise_*/zone/region/config_sync.*
```

**Build + Test:**
```
go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
go test -race -count=1 -timeout 120s ./internal/engine/... ./internal/cluster/...  ✓
```

---

## Sabit Kısıtlamalar (değiştirilemez)

Bu kısıtlamalar sprint planlamasında daima geçerlidir:

| Kısıtlama | Detay |
|-----------|-------|
| Paket yapısı | Yalnızca `internal/engine/` + `internal/cluster/`. Yeni alt-paket yok. |
| `Target.Options` | `json.RawMessage` olarak kalır. Düz alanlara dönüştürülemez. |
| `state.json` | Cluster modunda da korunur. Anti-entropy bu state ile başlar. |
| Lamport clock | `(OwnerNode, Seq)` tuple. Eşit Seq → NodeName lex sıralaması. |
| Metrik isimleri | `network_probe_*` — eski isimler kalıcı olarak kaldırıldı. |
| Her aşama sonunda onay | Kullanıcı smoke test yapar, onay vermeden sonraki aşamaya geçilmez. |
