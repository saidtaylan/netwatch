# netwatch Sprint — Bekleyen Aşamalar

Bu dosya aktif geliştirme planını içerir. Tamamlananlar → **developments.md**. Mimari kararlar → **CLAUDE.md**.

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

## [ Phase 12 ] — Integration Tests

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

---

## [ Phase 13 ] — Distributed Probe Ownership (Probe Sorumluluğu Dağıtımı)

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
✅ Step 3: CandidatesFor (peerStates derivation) + LocalTargetProvider interface  (devam ediyor)
✅ Step 4: SelectProbers + zone-aware picker (3-tier) + unit testler          (devam ediyor)
   Step 5: Bootstrap broadcast (Init + Reload sonrası N target × 1 state broadcast)
   Step 6: ProberAssignmentListener interface + engine StartProbing/StopProbing entegrasyonu
   Step 7: Reactive recompute + 5sn debounce (NotifyJoin/Leave/Update)
   Step 8: /cluster/probers endpoint + metrikler
   Step 9: Phase 9 anti-entropy ile uyum doğrulama (syncing guard)
   Step 10: Integration test (3-tier zone senaryoları + 5-6 node)
   Step 11: README + CLAUDE.md güncelleme
   Step 12: Smoke test (3 yerel binary, target down/recover, zone karışık)
```

Her adım sonrası `go build` + `go test -race` yeşil olmalı.

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
