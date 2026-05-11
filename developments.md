<!--
# LLM AGENT TALİMATLARI

Bu dosya projenin değişiklik günlüğüdür. Her tarih bloğunda:
- **1. seviye bullet** = Sade özet (ne eklendi / ne değişti)
- **2. seviye bullet** = Teknik detay (dosya yolları, ne değişti)

Etiketler: [backend] [altyapi] [devops] [dokuman] [test]

Arşiv: Henüz arşiv yok. Son 4 haftadan eski → docs/archive/development_YYYY_MM.md
-->

# netwatch Changelog

Bu belge, netwatch projesinin günlük güncellemelerini ve teknik detaylarını takip eder.

---

## 2026-05-11

- [backend] [altyapi] **Phase 13 Step 8–9 tamamlandı: `/cluster/probers` + `/fleet/status` endpoint'leri, Phase 13 metrikleri, anti-entropy sync guard'ları. todo.md F2'nin (fleet/status) özet versiyonu entegre edildi.**

  **Step 8 — Observability:** Üç yeni public yüzey, bir kaç metric, MemberInfo'ya zone alanı.

  - `GET /cluster/probers` — **per-target prober assignment view**. Her target için: seçilen probers (factor adet), primary (probers[0]), candidates (factor cap'inden önce), bu node prober mı (`i_probe`), aktif `probe_from` constraint'i (varsa). Members listesi zone'larla birlikte ekleniyor. Operatör "neden bu node X'i probe etmiyor?" sorusunu tek `curl` ile cevaplayabiliyor.
  - `GET /fleet/status` — **cluster-wide özet view** (todo.md F2 minimal). Per-target detay YOK (kullanıcı açıkça istemedi). İçerik:
    - `Cluster`: size, alive count, quorum_healthy, isolated, expected_node_count, min_quorum_ratio, replication_factor
    - `Members`: zone'lar dahil tüm üye listesi
    - `Targets`: cluster-wide aggregate counts — Total (unique target ID'lerinin sayısı), Up (tüm reporting node'lar agree), HardDown (herhangi node down rapor ediyorsa — exactly-once alerting modeliyle uyumlu), Unknown (bootstrap state'i veya consensus yok)
    - `DownTargets`: down olan target ID'lerinin listesi (cap=100, count exact ama liste bounded)
  - `MemberInfo` — `Zone` alanı eklendi (memberlist NodeMeta'dan parse edilir; eksikse boş)
  - Yeni metrikler (`RegisterClusterMetrics` ile koşullu kayıt):
    - `network_probe_local_assigned{name,target,type}` — bu node probe ediyor mu (1/0)
    - `network_probe_prober_count{name,target,type}` — seçilen prober sayısı
    - `network_probe_inventory_peers` — gossip ile keşfedilen peer sayısı (cluster size'a yaklaşır)
  - Metrikler `updateClusterMetrics` içinde her 5sn'de bir güncelleniyor (mevcut updater goroutine'i içinde)

  **Step 9 — Anti-entropy sync guard'ları:** Phase 9'da `runCheck` ve `processPending` zaten `syncing.Load()` ile guard'lıydı. Phase 13'ün eklediği yollar da artık aynı guard'a tabi:

  - `Engine.StartProbing(targetID)` — syncing true ise erken return + debug log
  - `Engine.StopProbing(targetID)` — syncing true ise erken return (mevcut probe loop'lar dokunulmuyor)
  - `Engine.bootstrapInventoryBroadcast()` — syncing true ise erken return (FullState merge ile race önleniyor; seq=0 placeholder'larla otoriter seq'leri ezme riski yok)
  - `Engine.SetSyncing(false)` — true→false transition'ında **goroutine ile** `clusterMgr.TriggerProberRecompute()` çağırıyor; sync sırasında ertelenen Start/Stop callback'leri sync biter bitmez catch-up yapıyor (max 5sn debounce beklemek yerine)

  Sonuç: Anti-entropy join sırasında ne ekstra alarm gidiyor, ne probe loop kasırgası, ne de yanlış seq=0 broadcast'i.

  **Test:** 8 yeni cluster testi (`phase13_views_test.go`): ProberSnapshot içerik/primary/i_probe/constraint exposure; FleetSummary state counting, DownTargets cap, local provider inclusion, cluster info config reflection. 4 yeni engine testi (`phase13_test.go` ekleri): bootstrap-deferred-while-syncing, StartProbing-deferred-while-syncing, StopProbing-deferred-while-syncing, SetSyncing-flag-transition. Plus `Members()` ve `AliveCount()` test path'inde nil-safe edildi (m.list=nil durumunda aliveSet override'a düşüyor).

  **Smoke test ipucu:**
  ```
  curl localhost:10240/fleet/status | jq '.targets, .cluster, .members[].zone'
  curl localhost:10240/cluster/probers | jq '.assignments["my-target"]'
  curl localhost:10240/metrics | grep network_probe_local_assigned
  ```

  - **Oluşturuldu**: `internal/cluster/views.go` — `ProberAssignment`, `ProberSnapshot`, `FleetClusterInfo`, `FleetTargetCounts`, `FleetSummary` tipleri; `ProberAssignmentsSnapshot()`, `FleetSummarySnapshot()` metodları; `FleetDownTargetsCap=100`
  - **Değiştirildi**: `internal/cluster/cluster.go` — `MemberInfo.Zone` alanı; `Members()` NodeMeta'dan zone parse + nil-safe; `AliveCount()` nil-safe
  - **Değiştirildi**: `internal/engine/engine.go` — 3 yeni cluster gauge (`GaugeLocalAssigned`, `GaugeProberCount`, `GaugeInventoryPeers`); `RegisterClusterMetrics` bunları register ediyor; `updateClusterMetrics` Phase 13 ownership gauge'larını günceller; `StartProbing`/`StopProbing` sync guard'ları; `bootstrapInventoryBroadcast` sync guard; `SetSyncing` true→false transition'ında recompute tetiklemesi
  - **Değiştirildi**: `cmd/linux/main.go` — `/cluster/probers` ve `/fleet/status` endpoint kayıtları
  - **Oluşturuldu**: `internal/cluster/phase13_views_test.go` — 8 unit test
  - **Değiştirildi**: `internal/engine/phase13_test.go` — 4 yeni sync guard testi

---

- [backend] [altyapi] **Phase 13 Step 6–7 tamamlandı: Dinamik probe loop atama + 5sn debounce'lu reactive recompute. Davranış değişikliği aktif: artık hash + zone seçimi gerçekten probe loop'larını başlatıp durduruyor.**

  **Step 6 — `ProberAssignmentListener` interface + engine entegrasyonu:** Cluster paketine yeni `ProberAssignmentListener` interface'i eklendi (`StartProbing(targetID)` + `StopProbing(targetID)`). Engine bu interface'i implement ediyor: cluster'dan gelen callback'lere lookup yapıp ilgili target'ın probe goroutine'ini başlatıyor veya iptal ediyor. `Manager.recomputeProberAssignments` her local target için `IsLocalProber` hesaplayıp önceki `proberAssignments` snapshot'ı ile diff alıyor; sadece transition'lar için callback fırlatıyor (idempotent recompute = sessiz). Stop'lar start'lardan önce çağrılıyor → swap senaryolarında overlap minimize edilmiş.

  `startProbeLoop` artık `clusterMgr != nil && !IsLocalProber(...)` durumunda erken return ediyor. Standalone moda dokunulmadı: clusterMgr nil → her zaman probe.

  `Init` flow'u yeniden düzenlendi: cluster setup → `SetLocalTargetProvider` + `SetProberAssignmentListener` + `bootstrapInventoryBroadcast` → her aktif target için `IsLocalProber` hesaplayıp `SeedProberAssignments` ile cluster'a "bunlar zaten çalışıyor" diye seed ediliyor. Sonra `startProbeLoop` çağrıları yapılıyor (filtered by IsLocalProber içeriden). Bu sayede ilk reactive recompute (5s sonra peer broadcast'ları gelince) gereksiz Start/Stop callback'leri üretmiyor.

  `Reload` sonuna `TriggerProberRecompute` eklendi — local target listesi değiştiğinde sync recompute, debounce'u beklemeden.

  **Step 7 — Reactive recompute + debounce:** `Manager.scheduleRecompute` 5sn'lik debounce timer kullanıyor. `time.AfterFunc` ile arm/reset. Hook'lar:
  - `eventDelegate.NotifyJoin` — yeni üye eklendi, candidate set değişebilir
  - `eventDelegate.NotifyLeave` — üye ayrıldı, prober devralma gerekebilir
  - `eventDelegate.NotifyUpdate` — NodeMeta (zone) değişmiş olabilir, zone-aware picker farklı sonuç verebilir
  - `OnStateReceived` — sadece **yeni** `(node, targetID)` entry'sinde (state value transition değil); aksi takdirde busy cluster'da timer hiç fire olmazdı

  `Leave` çağrısında timer durduruluyor (goroutine leak önlemi).

  **Debounce mantığı:** 50 schedule call'u tek bir recompute'a düşüyor (test ile doğrulandı). Membership flapping veya anti-entropy burst'lerinde sürekli start/stop kasırgası olmuyor.

  **Test sonuçları:** 9 yeni cluster testi (`phase13_recompute_test.go`):
  - `Recompute`: idempotent silence, prober transition, target removed, stop-before-start ordering, nil listener safety, nil provider safety
  - `Seed`: redundant-start suppression, defensive copy
  - `ScheduleRecompute`: burst collapse (50→1)

  Tüm cluster + engine testleri `-race` ile yeşil.

  **Davranış değişikliği yansıması:** Artık 5+ node'lu cluster'da bir target için sadece factor (default 3) adet probe goroutine çalışıyor. Tek başına config'inde target'ı olan ama hash/zone tarafından seçilmemiş node'lar sessiz dinleyici durumunda. Operatör hiçbir şey değiştirmedi — Phase 13'ün vaadi tutuldu.

  - **Değiştirildi**: `internal/cluster/cluster.go` — `Manager` struct'ına `assignmentListener`, `proberAssignments`, `recomputeMu`, `recomputeTimer` field'ları; `eventDelegate` NotifyJoin/Leave/Update sonuna `scheduleRecompute`; `OnStateReceived` içine `isNewEntry` track + lock sonrası `scheduleRecompute`; `Leave` içinde timer durdurma
  - **Değiştirildi**: `internal/cluster/probers.go` — `ProberAssignmentListener` interface; `SetProberAssignmentListener`; `recomputeProberAssignments` (diff-based callback dispatch); `TriggerProberRecompute` (sync API); `SeedProberAssignments` (init için); `scheduleRecompute` (debounce); `recomputeDebounce = 5s` sabit
  - **Değiştirildi**: `internal/engine/engine.go` — `Engine.StartProbing(targetID)` + `StopProbing(targetID)` metodları (cluster.ProberAssignmentListener); `Init` cluster setup içine `SetProberAssignmentListener` + probe loop başlatmadan önce `SeedProberAssignments`
  - **Değiştirildi**: `internal/engine/loop.go` — `startProbeLoop` Phase 13 guard (cluster + !IsLocalProber → erken return); `Reload` sonuna `TriggerProberRecompute`
  - **Oluşturuldu**: `internal/cluster/phase13_recompute_test.go` — 9 unit test + `recordingListener` test helper + `drain()` utility

---

- [backend] [altyapi] **Phase 13 Step 5 tamamlandı: Bootstrap broadcast + todo.md F6 (Active Probe Delegation) entegre edildi.**

  **Bootstrap broadcast:** Engine artık `cluster.LocalTargetProvider` interface'ini implement ediyor. `Init()` cluster setup'tan sonra `SetLocalTargetProvider(e)` + `bootstrapInventoryBroadcast()` çağırıyor. Her aktif local target için bir `GossipPayload` yayınlanıyor — `state.json`'da kayıt varsa onu, yoksa `state="unknown"`, `seq=0` ile presence announcement. computeScope ve peer-alert path'i "unknown" state'i ignore ettiği için bu mesajlar benign; ilk gerçek probe seq>=1 ile Lamport üzerinden eziyor. `Reload()` sonrası da çağrılıyor → yeni target'lar cluster'a duyuruluyor.

  Bu Phase 13'ün chicken-and-egg problemini çözüyor: yeni başlayan veya hiçbir target için prober olarak seçilmeyen bir node bile peer'lar tarafından candidate set'te görülüyor.

  **F6 — Active Probe Delegation (todo.md'den entegre):** `Target.ProbeFrom []string` alanı eklendi. Boş bırakılırsa (varsayılan) cluster otomatik karar veriyor; doldurulursa yalnızca listedeki node'lar candidate set'e giriyor. Bu Phase 13'ün otomatik seçimini "varsayılan otomatik, isteğe bağlı manuel" yapıyor.

  Mekanizma: `cluster.LocalTargetProvider.ProbeFromConstraint(targetID)` interface metodu eklendi. `Manager.CandidatesFor` peerStates'ten + local config'ten derlediği listeyi pin set ile kesişime sokuyor. Dead pinned node'lar `aliveSet` filtresinden geçemez → otomatik dışlanır. Zone-aware picker pin override'a tabi (pin > zone diversity).

  Kullanım örneği: `probe_from: ["node-fr", "node-tr"]` → bir target sadece bu iki node'dan probe edilir; diğer cluster üyeleri (zone ne olursa olsun) candidate olmaz.

  **Operatör sorumluluğu:** Aynı target'ı taşıyan tüm node'lar aynı `probe_from` listesini deklare etmeli; aksi takdirde candidate set hesabı node'lar arasında farklılaşır ve exactly-once garantisi bozulur. Bu kısıt sprint.md ve config.example.yaml'de belgelenecek (Step 11).

  - **Değiştirildi**: `internal/engine/engine.go` — `Target.ProbeFrom` alanı; `Engine.LocalTargets()` ve `Engine.ProbeFromConstraint()` metodları (cluster.LocalTargetProvider); `Engine.bootstrapInventoryBroadcast()` helper; `Init()` cluster setup içine `SetLocalTargetProvider` + bootstrap çağrısı eklendi
  - **Değiştirildi**: `internal/engine/loop.go` — `Reload()` sonuna `bootstrapInventoryBroadcast()` çağrısı (yeni target'lar cluster'a duyurulur)
  - **Değiştirildi**: `internal/cluster/probers.go` — `LocalTargetProvider.ProbeFromConstraint()` metodu interface'e eklendi; `CandidatesFor` constraint kesişim filtresi
  - **Değiştirildi**: `internal/cluster/phase13_probers_test.go` — `stubProvider` yeni metodu ekledi; 5 yeni test: constraint filter, empty constraint, unknown pinned node, dead pinned node, zone override
  - **Oluşturuldu**: `internal/engine/phase13_test.go` — 8 unit test: LocalTargets (full/empty), ProbeFromConstraint (unknown/unset/copy/ID-key), bootstrapInventoryBroadcast (standalone no-op, disabled-target skip)

---

- [backend] [altyapi] **Phase 13 Step 3–4 tamamlandı: Candidate set derivation + 3-tier zone-aware prober selection.**

  Step 3: `LocalTargetProvider` interface — engine cluster paketine import dependency yaratmadan kendi local target inventory'sini deklare ediyor. `Manager.SetLocalTargetProvider(p)` ile bağlanır. `Manager.CandidatesFor(targetID)` aşağıdaki birleşimden lex-sıralı listeyi döner:
  1. Mevcut `peerStates[node][targetID]` kaydı olan **alive** node'lar (gossip'ten türetildi, ek mesaj yok)
  2. Local node — eğer `LocalTargetProvider.LocalTargets()` targetID'yi içeriyorsa (bootstrap chicken-and-egg çözümü; ilk state broadcast atılmadan da görünür)

  Dead/left node'lar `memberlist.StateAlive` filtresiyle dışarıda bırakılıyor.

  Step 4: `Manager.SelectProbers(targetID)` — `effectiveReplicationFactor()` adet prober döner. Candidate sayısı factor'dan azsa hepsi seçilir (legacy davranış); fazlaysa `hashCandidateOrder` (FNV-32a) ile deterministic sıralama yapılır ve `zoneAwarePick` üç katmanlı seçimle prober subset'i belirler:
  - **Tier 1 (zone diversity):** her unique zone'dan birer node (failure domain redundancy)
  - **Tier 2 (zone repeat):** Tier-1 bittiyse zone-tagged repeat — zone-less'ten önce
  - **Tier 3 (zone-less fallback):** sadece zone-tagged tükendiğinde

  `Manager.IsLocalProber(targetID)` — Engine'in Step 6'da probe loop'u başlatıp başlatmayacağına karar verirken kullanacağı kapı.

  Test override mekanizması: `testAliveOverride` ve `testZoneOverride` field'ları + `SetTestAliveSet`/`SetTestZones` exported test helper'ları — unit testler memberlist instance'ı kurmadan multi-node davranışı simüle edebiliyor.

  - **Oluşturuldu**: `internal/cluster/probers.go` — `LocalTargetProvider` interface, `SetLocalTargetProvider`, `aliveSet`, `CandidatesFor`, `hashCandidateOrder`, `zoneAwarePick` (3-tier), `SelectProbers`, `IsLocalProber`
  - **Değiştirildi**: `internal/cluster/cluster.go` — `Manager.localTargetProvider` field; `Manager.testAliveOverride` + `testZoneOverride` test field'ları; `zoneOf()` test override path eklendi
  - **Değiştirildi**: `internal/cluster/testhelpers.go` — `SetTestAliveSet(names...)`, `SetTestZones(map)` helper'ları
  - **Oluşturuldu**: `internal/cluster/phase13_probers_test.go` — 25 unit test: candidate derivation (peer-only, local-only, dedupe, dead filter, sorted), hash order (deterministic, full rotation, empty), zone picker (full diversity, repeat-beats-zoneless, zoneless-skipped, zoneless-fallback, all-zoneless, factor-overflow, zero-factor, empty), SelectProbers (all-when-below, 100x deterministic, factor-respected, zone integration), IsLocalProber (true/false/unknown). Tüm 5 node'da self-identification toplam **2** (factor=2) — yani exactly-once invariant doğrulandı.

  Davranış değiştirici hâlâ yok: bu fonksiyonlar henüz `runCheck`/`startProbeLoop` tarafından çağrılmıyor. Engine entegrasyonu Step 6'da yapılacak.

---

- [backend] [altyapi] **Phase 13 Step 1–2 tamamlandı: Zone + ProbeReplicationFactor config alanları + memberlist NodeMeta üzerinden zone propagation.**

  Step 1: `cluster.Config` artık iki yeni opsiyonel alan taşıyor — `Zone` (operatörün el yazısı; hostname'den türetme yok) ve `ProbeReplicationFactor` (default 3, `effectiveReplicationFactor()` helper'ı tek noktada tanımlar). Validation negatif factor'ü reddediyor, zero "default kullan" anlamına geliyor, zone serbest. Cluster disabled olduğunda tüm bu kontroller atlanıyor (eski davranış birebir korunuyor).

  Step 2: `gossipDelegate.NodeMeta` artık `{Node, Zone}` JSON döndürüyor. Memberlist bu metadata'yı kendi içinde tüm üyelere otomatik dağıtıyor — ek gossip mesaj tipi yok, ek bandwidth yok. Overflow korumalı: Zone limit'i aşarsa sadece `{Node}` döner, o da fitmezse nil. `Manager.zoneOf(nodeName)` — memberlist `Node.Meta` üzerinden zone okur; local node için cfg'den fast path (ilk NodeMeta cycle tamamlanmadan da doğru cevap).

  Tüm bunlar **davranış değiştirici değil**: operatör config yazmadığı sürece cluster eski şekilde çalışmaya devam eder.

  - **Değiştirildi**: `internal/cluster/cluster.go` — `Config.Zone`, `Config.ProbeReplicationFactor` alanları + `effectiveReplicationFactor()` helper; `Validate()` negatif factor reddi; `nodeMeta` struct (`Node`, `Zone`); `gossipDelegate.NodeMeta` zone'u dahil + overflow fallback; `Manager.zoneOf()` + `ZoneOf()` exported alias
  - **Oluşturuldu**: `internal/cluster/phase13_config_test.go` — 14 unit test: default factor, override, negative reject, empty-zone omission, NodeMeta limit-overflow fallback, zoneOf self/unknown/exported senaryoları

---

- [dokuman] **Phase 13 planlandı: Distributed Probe Ownership (probe sorumluluğu dağıtımı).**

  Mevcut sorun: cluster'daki her node config'indeki tüm target'ları probe ediyor. 100 node'lu bir cluster aynı target için 100 probe/dakika atıyor — hedef servisler üzerinde gereksiz yük + erişim izni olmayan node'ların başarısız probe gürültüsü.

  Çözüm: bir target için cluster içinde N candidate (config'inde o target'ı olan node'lar) varsa, bunlardan replication factor kadarı (varsayılan 3) hash ring üzerinden seçilir. Geri kalanlar sadece gossip dinler. Operatör hiçbir şey yazmaz — cluster otomatik karar verir.

  Zone önceliği üç katmanlı: (1) farklı zone'lardan birer node, (2) zone-tagged repeat, (3) zone-less son tercih. Zone bilgisi memberlist `NodeMeta` üzerinden taşınır (yeni gossip mesajı yok).

  Bootstrap chicken-and-egg sorunu mevcut `GossipPayload` ile çözülür: startup ve `LoadConfig` sonrası her local target için tek bir state broadcast atılır — candidate set bu broadcast'lardan türetilir, ek mesaj tipi yok, memberlist trafiği eski seviyede kalır.

  Phase 9 peer-alert mekanizması korunur (sigorta olarak — zone constraint nedeniyle primary'nin prober olmadığı edge case'ler için).

  - **Düzenlendi**: `sprint.md` — Phase 13 görev listesi: 12 adımlık implementasyon sırası, 16 test senaryosu (3-tier zone permütasyonları), 8 kabul kriteri, 8 risk-önlem, 4 açık soru
  - **Düzenlendi**: `todo.md` — 6 yeni özellik roadmap'i (dependency graph + root cause, /fleet/status, scope intelligence, SLO tracker, gossip config sync, active probe delegation); pazarlama konumlanması "The masterless monitoring agent that knows what it doesn't know"
  - **Düzenlendi**: `system_map.md` — Phase 13 bekleyen değişiklikler işaretleri (Config alanları, yeni metrikler, yeni endpoint, yeni dosya `probers.go`)

---

## 2026-05-07

- [devops] **Phase 11 tamamlandı: Deployment artifact'ları oluşturuldu — Makefile, Dockerfile, systemd unit, config.example.yaml, Helm chart.**

  Uygulamayı production ortamına taşımak için gereken tüm dosyalar bu aşamada eklendi.

  **Makefile** — `build-linux`, `build-windows`, `test`, `test-integration`, `lint`, `vet`, `all`, `install`, `uninstall`, `docker-build`, `docker-push`, `clean` hedefleri. `BINARY_NAME` değişkeni override edilebilir (varsayılan: `netwatch`). ldflags üzerinden `BinaryName` otomatik set edilir.

  **Dockerfile** — `ARG BINARY_NAME` ile parametrik hale getirildi (eski sabit `/network-prober` yolu kaldırıldı). `EXPOSE 7946` (gossip TCP+UDP) eklendi. `notifications/` dizini `/notifications` olarak kopyalanır, volume mount için hazır. Distroless `nonroot` base image korundu.

  **`deploy/netwatch.service`** — Repo içinde statik referans systemd unit dosyası. `AmbientCapabilities=CAP_NET_RAW`, `Restart=on-failure`, `SyslogIdentifier` ile systemd journal yönlendirmesi.

  **`config.example.yaml`** — Tüm config alanlarının açıklamalı referans dosyası. Standalone + cluster modunu tek dosyada kapsar; TCP, HTTP (operatörler dahil), ping, DNS, SQL (postgres/mysql/oracle/mssql), webhook (generic + alertmanager), mail, script kanal örnekleri ve apps indirection örnekleri mevcut.

  **`helm/netwatch/`** — Kubernetes DaemonSet chart'ı:
  - `Chart.yaml` — chart metadata
  - `values.yaml` — hostNetwork: true, NET_RAW capability, gossip port, keyring secret, Downward API NODE_NAME/HOST_IP
  - `templates/daemonset.yaml` — rolling update, liveness/readiness probe, security context
  - `templates/service-headless.yaml` — ClusterIP (Prometheus scrape) + Headless (gossip DNS peer discovery)
  - `templates/configmap.yaml` — config.yaml ConfigMap
  - `templates/secret.yaml` — keyring Secret
  - `templates/_helpers.tpl` — standart Helm label helper'ları

  - **Oluşturuldu**: `Makefile`
  - **Güncellendi**: `Dockerfile` — `ARG BINARY_NAME`, `EXPOSE 7946`, ldflags BinaryName injection, eski hardcoded binary yolu kaldırıldı
  - **Oluşturuldu**: `deploy/netwatch.service`
  - **Oluşturuldu**: `config.example.yaml`
  - **Oluşturuldu**: `helm/netwatch/Chart.yaml`
  - **Oluşturuldu**: `helm/netwatch/values.yaml`
  - **Oluşturuldu**: `helm/netwatch/templates/_helpers.tpl`
  - **Oluşturuldu**: `helm/netwatch/templates/daemonset.yaml`
  - **Oluşturuldu**: `helm/netwatch/templates/service-headless.yaml`
  - **Oluşturuldu**: `helm/netwatch/templates/configmap.yaml`
  - **Oluşturuldu**: `helm/netwatch/templates/secret.yaml`

---

- [backend] **Phase 10 tamamlandı: Uygulama ismi merkezi değişkene taşındı, CLI lifecycle komutları eklendi.**

  Daha önce "netwatch" ismi `const serviceName = "netwatch"`, log mesajları ve ICMP payload gibi farklı yerlere dağılmış yazılıydı. Şimdi `internal/engine/appinfo.go` dosyasındaki tek bir `var BinaryName = "netwatch"` satırı tüm projenin referans noktası. İsmi değiştirmek için ya bu satırı düzenlemek ya da `go build -ldflags "-X .../engine.BinaryName=myagent"` komutunu çalıştırmak yeterli — tüm log mesajları, servis adı, unit dosyası ve ICMP paketi otomatik olarak yeni ismi kullanıyor.

  Buna ek olarak `netwatch init`, `netwatch leave` ve `netwatch uninstall` CLI komutları eklendi. `init` komutu çalışmaya hazır bir `config.yaml`, `credentials.env` ve `netwatch.service` systemd unit dosyası üretiyor; sistemde systemd yoksa unit dosyasını config dizinine yazıp kullanıcıya hint veriyor. `leave` komutu çalışan bir agent'a `/cluster/leave` endpoint'i üzerinden graceful shutdown sinyali gönderiyor. `uninstall` ise sırayla: leave isteği → `systemctl stop/disable` → unit dosyası silme → config dizini silme (onay ile) adımlarını yapıyor.

  Windows tarafında `service install` ve `service remove` komutları CLI'a bağlandı; artık `netwatch service install --config C:\...` ile Windows Service olarak kayıt edilebiliyor.

  - **Oluşturuldu**: `internal/engine/appinfo.go` — `var BinaryName = "netwatch"` (ldflags ile override edilebilir); tüm hardcoded referansların tek kaynağı
  - **Değiştirildi**: `internal/engine/ping.go` — ICMP echo payload `"netwatch-ping"` → `BinaryName+"-ping"`
  - **Değiştirildi**: `internal/engine/mail.go` — MIME boundary `const` → `var`, `BinaryName` kullanıyor
  - **Değiştirildi**: `cmd/linux/main.go` — subcommand routing (`init`, `leave`, `uninstall`); `/cluster/leave` HTTP endpoint; `engine.BinaryName` log mesajı; `leaveCh` ile unified shutdown goroutine; `configSkeleton()` + `systemdUnit()` + `credsSkeleton` template/constant; systemd dizini yoksa graceful fallback
  - **Değiştirildi**: `cmd/windows/main.go` — `const serviceName` → `engine.BinaryName`; `DisplayName` → `engine.BinaryName+" Monitoring Agent"`; `type netwatchService` → `agentService`; `cmdService install/remove` + `cmdLeave` subcommandları; `installService` + `removeService` helper'ları

---

- [backend] [altyapi] **Phase 9 tamamlandı: Cluster'a yeniden katılan bir node artık daha önce gönderilmiş alarmları tekrar tetiklemiyor.**

  Bir node restart edilip cluster'a geri döndüğünde iki senaryo söz konusudur: (1) yokken bir target down olmuş ve diğer node'lar alarm göndermiş, (2) kendisi down görürken diğerleri up görmüştür. Her iki durumda da "fazladan alarm" üretmemek gerekir. Bu aşamada memberlist'in push-pull mekanizması anti-entropy için kullanıldı.

  Çözüm üç katmanda çalışıyor: **`AntiEntropyProvider` arayüzü** cluster paketinin engine paketini import etmeden geri çağırmasını sağlar. **`LocalState(join=true)`** çağrıldığında bu node kendi `lastKnown` haritasını JSON olarak karşı tarafa gönderir. **`MergeRemoteState(join=true)`** ise diğer node'dan gelen tam durumu alır; bu süre boyunca `syncing=true` set ederek `runCheck` ve `processPending` fonksiyonlarının erken çıkmasını sağlar — böylece merge tamamlanana kadar yeni alarm üretilmez. Merge sonrası Lamport kuralı uygulanır: remote Seq > local Seq ise remote kabul edilir (alarm gönderilmez, cluster zaten gönderdi), eşitse OwnerNode lex sıralaması karar verir, local daha yüksekse local korunur ve broadcast ile karşı tarafa bildirilir.

  - **Değiştirildi**: `internal/cluster/cluster.go` — `AntiEntropyProvider` arayüzü eklendi; `Manager.stateProvider` alanı ve `SetStateProvider()` metodu eklendi; `gossipDelegate.LocalState(join bool)` join=true'da provider'ın `FullState()`'ini, join=false'da peerStates'i döner; `gossipDelegate.MergeRemoteState(join bool)` join=true'da `SetSyncing(true)` → `ApplyRemoteState()` → `SetSyncing(false)` sırasıyla provider'ı çağırır; `startAntiEntropy()` placeholderı güncellendi
  - **Değiştirildi**: `internal/engine/engine.go` — `Engine.syncing atomic.Bool` field'ı eklendi; `FullState()`, `ApplyRemoteState()`, `SetSyncing()` metodları eklendi (cluster.AntiEntropyProvider implementasyonu); `Init()` içinde `e.clusterMgr.SetStateProvider(e)` bağlantısı kuruldu
  - **Değiştirildi**: `internal/engine/loop.go` — `runCheck()` ve `processPending()` başına `if e.syncing.Load() { return }` syncing guard'ı eklendi
  - **Oluşturuldu**: `internal/engine/phase9_test.go` — `FullState`, `ApplyRemoteState` (newer/older/new/lamport/malformed/empty senaryoları), `SetSyncing` flag testi, `RunCheck_SkipsWhenSyncing` testi
  - **Oluşturuldu**: `internal/cluster/antientropy_test.go` — `LocalState` (join=true/false, no-provider fallback), `MergeRemoteState` (provider dispatch, periodic path, empty buf, no-provider fallback) testleri

---

- [backend] [altyapi] **Phase 8 tamamlandı: Aynı target'ı izleyen birden fazla node artık sadece bir tanesi alarm gönderiyor.**

  Cluster modunun en kritik sorusu şuydu: 3 node aynı sunucuyu izliyorsa ve o sunucu çökerse, 3 alarm mı gönderilmeli, yoksa 1 mi? Cevap elbette 1. Bu aşamada "hangi node alarm gönderir?" sorusu, ağ konuşmasına gerek kalmadan tüm node'ların bağımsız olarak aynı cevaba ulaşabileceği şekilde çözüldü: **consistent hashing**.

  Her node, canlı üyelerin isimleri sıralandığında FNV-32a hash algoritmasıyla aynı sonucu hesaplar. Yani "db-probe target'ından node-1 mi sorumlu, node-2 mi?" sorusunun cevabı hiçbir koordinasyon gerektirmeden her node'da aynı çıkar. Primary + secondary seçimi (primary düşerse secondary devralır), `IsolatedMode()` kontrolü (quorum yoksa alarm yok) ve `shouldAlert()` merkezi kapısı birlikte exactly-once garantisini oluşturuyor.

  Alert mesajına `SCOPE` env değişkeni eklendi: Tüm node'lar aynı şeyi görüyorsa `GLOBAL`, sadece bu node'un bağlantısı kopuksa `NODE_LOCAL`, karma durumda `PARTIAL`. Bu bilgi operatöre "ağ problemi mi yoksa uygulama problemi mi?" konusunda anlık ipucu veriyor.

  Son olarak `network_probe_cluster_status` metriği eklendi: tüm node'lar up görüyorsa 1, herhangi biri down görüyorsa 0. `network_probe_local_status`'tan farkı budur — local, bu node'un bakış açısını; cluster, tüm cluster'ın konsensüsünü temsil eder.

  - **Değiştirildi**: `internal/cluster/cluster.go` — `updateRing()` (sorted alive members), `hashTarget()` (FNV-32a), `GetResponsibleNode()`, `IsResponsible()`, `PeerStatesForTarget()` metodları eklendi; `ringMu sync.RWMutex` + `ring []string` field'ları eklendi; `eventDelegate` NotifyJoin/Leave/Update her üyelik değişiminde ring'i güncelliyor
  - **Oluşturuldu**: `internal/cluster/testhelpers.go` — `NewTestManager()`, `SetIsolated()`, `SetPeerState()` — engine paketinin testlerinin cluster bileşenlerini gerçek memberlist instance'ı olmadan kullanabilmesi için
  - **Değiştirildi**: `internal/engine/engine.go` — `computeScope()` (GLOBAL/NODE_LOCAL/PARTIAL/STANDALONE hesabı), `shouldAlert()` (cluster modunda alarm kapısı), `GaugeClusterStatus` (`network_probe_cluster_status` GaugeVec), `updateClusterMetrics()` consensus durumunu her 5s güncelliyor
  - **Değiştirildi**: `internal/engine/loop.go` — `runCheck()` ve `processPending()` içindeki tüm `sendAlert` çağrıları `shouldAlert()` koşuluna bağlandı
  - **Değiştirildi**: `internal/engine/notify.go` — `sendAlert()` içinde `SCOPE` env değişkeni eklendi
  - **Oluşturuldu**: `internal/engine/phase8_test.go` — `shouldAlert` ve `computeScope` için kapsamlı unit testler (standalone, isolated, not-responsible, responsible, GLOBAL/NODE_LOCAL/PARTIAL/no-peer-data senaryoları)
  - **Oluşturuldu**: `internal/cluster/cluster_test.go` — hash ring (deterministic, empty, single-node, distribution), IsResponsible, quorum formula, IsolatedMode, PeerStatesForTarget unit testleri

---

- [backend] [altyapi] **Phase 7 tamamlandı: Cluster artık çoğunluk kaybını algılıyor ve node'u "izole mod"a alıyor.**

  Bir cluster kurulduğunda, aynı hedefleri izleyen birden fazla node birbirini dinler. Bu aşamada her node 5 saniyede bir "kaç node hayatta?" diye soruyor. Konfigürasyon dosyasında `expected_node_count: 3` ve `min_quorum_ratio: 0.5` varsa, 2 node yeterliyken sadece 1 kaldığında node kendini "izole" olarak işaretliyor. Bir sonraki aşamada (Phase 8) izole moddaki node alarm göndermeyecek — çünkü "tüm network bozuk" değil, "bu node'un kendi bağlantısı bozuk" olabilir.

  Üç yeni Prometheus metriği eklendi: `network_prober_quorum_healthy` (çoğunluk var mı?), `network_prober_isolated` (bu node izole mi?) ve `network_prober_cluster_size` (kaç canlı üye görünüyor?). Bu metrikler `cluster.enabled: false` olduğunda hiç register edilmiyor — sıfır overhead.

  - **Değiştirildi**: `internal/cluster/cluster.go` — quorum hesaplama (`checkQuorum`), 5 saniyelik arka plan döngüsü (`runQuorumLoop`), `IsolatedMode()` getter ve Phase 9 için `startAntiEntropy()` placeholder'ı eklendi; graceful shutdown için `Leave()` quorum goroutine'ini iptal ediyor
  - **Değiştirildi**: `internal/engine/engine.go` — 3 yeni cluster gauge (`network_prober_quorum_healthy`, `network_prober_isolated`, `network_prober_cluster_size`), `RegisterClusterMetrics()` fonksiyonu ve `runClusterMetricsUpdater` goroutine'i eklendi
  - **Değiştirildi**: `cmd/linux/main.go` — cluster aktifse cluster metriklerini registry'e kaydeden koşullu çağrı eklendi

---

## 2026-05-06

- [dokuman] **CLAUDE.md bölümlere ayrılarak system_map.md, developments.md ve sprint.md dosyaları oluşturuldu.**
  - **Oluşturuldu**: `system_map.md` — Projenin statik anatomisi: dosya ağacı, mimari diyagram, tech stack, endpoint'ler, Prometheus metrikleri
  - **Oluşturuldu**: `developments.md` — Kronolojik değişiklik günlüğü (bu dosya)
  - **Oluşturuldu**: `sprint.md` — Bekleyen Phase 7–12 tam görev listeleri + kabul kriterleri + bağımlılık grafiği
  - **Değiştirildi**: `CLAUDE.md` — Bekleyen aşamalara tam Hedef/Görevler/Kabul detayları eklendi; Aşama Bağımlılık Grafiği, Her Aşama Sonu Doğrulama ve Başlangıçta Belirlenen Yapısal Kararlar bölümleri eklendi

---

- [backend] [altyapi] **Phase 6 tamamlandı: memberlist tabanlı gossip cluster katmanı eklendi.**
  - **Oluşturuldu**: `internal/cluster/cluster.go` — `Config`, `GossipPayload`, `Manager`, `gossipDelegate`, `eventDelegate`, `broadcast`
  - **Değiştirildi**: `internal/engine/engine.go` — `Config.Cluster cluster.Config` eklendi; `Engine.clusterMgr *cluster.Manager` eklendi; `Init()` cluster başlatır; `Shutdown()` `clusterMgr.Leave(5s)` çağırır; `ClusterManager()` getter eklendi
  - **Değiştirildi**: `internal/engine/loop.go` — `broadcastState(targetID, ps)` helper eklendi; `markHardDown` + `markRecovered` sonrası broadcast çağrısı eklendi
  - **Değiştirildi**: `cmd/linux/main.go` — `/cluster/state` endpoint eklendi; cluster disabled → 503 + JSON error
  - **Değiştirildi**: `go.mod` — `github.com/hashicorp/memberlist v0.5.4` bağımlılığı eklendi

---

- [backend] **Phase 5 tamamlandı: Prometheus scrape watchdog eklendi.**
  - **Oluşturuldu**: `internal/engine/watchdog.go` — `runWatchdog(ctx)` goroutine; threshold/3 aralıklarla kontrol (min 5s); ilk scrape gelene kadar sessiz
  - **Değiştirildi**: `internal/engine/engine.go` — `Config.WatchdogThresholdSec *int` eklendi; `Engine.lastScrapeNano atomic.Int64` eklendi; `GaugePrometheusConnected` gauge eklendi; `Init()` watchdog goroutine'i başlatır
  - **Değiştirildi**: `cmd/linux/main.go` — `/metrics` handler `e.NotifyScrape()` çağrısıyla sarmalandı

---

- [backend] **Phase 4 tamamlandı: Webhook notification kanalı + mail HTML + env enrichment.**
  - **Oluşturuldu**: `internal/engine/webhook.go` — `WebhookAlerter`; `generic` ve `alertmanager` formatları; custom headers (`header_<İsim>`); HTTP Basic Auth; TLS skip
  - **Değiştirildi**: `internal/engine/notify.go` — `newAlertChannel()` `webhook` case'i eklendi; `sendAlert()` artık `NODE_NAME`, `SEQ`, `ERROR_CODE` env değişkenlerini inject ediyor
  - **Değiştirildi**: `internal/engine/mail.go` — `multipart/alternative` format; `buildHTMLBody()` — renk kodlu status, affected apps `<table>`; `lastEnv` field eklendi

---

- [backend] **Phase 3 tamamlandı: Lamport-aware state machine ve state.json v2.**
  - **Değiştirildi**: `internal/engine/engine.go` — `PersistedState{State, Seq, ErrorCode, OwnerNode}` struct; `stateFileV2` envelope; `lastKnown map[string]bool` → `map[string]PersistedState`; `loadPersistedState()` v1→v2 otomatik migration + anında yeniden yazar; `StatusSnapshot` artık `Seq` ve `ErrorCode` içeriyor
  - **Değiştirildi**: `internal/engine/loop.go` — `markHardDown(pkey, t, errCode)` Seq++, ErrorCode saklar; `markRecovered(pkey, t)` Seq++ + ErrorCode temizler; `PendingEntry.LastErrorCode` eklendi; `runCheck()` `PersistedState` ile çalışacak şekilde güncellendi

---

- [backend] **Phase 2 tamamlandı: App→Target indirection + notification kanal birleştirme.**
  - **Oluşturuldu**: `internal/engine/app.go` — `App{Name, OwnerTeam, Uses, Notifications}`, `AppTargetIndex`, `buildAppTargetIndex()`, `validateApps()`
  - **Değiştirildi**: `internal/engine/engine.go` — `Target.ID string` opsiyonel alan; `Config.Apps []App`; `Engine.appIndex` field'ı
  - **Değiştirildi**: `internal/engine/notify.go` — `mergeNotifyChannels()` + `buildAppContext()` helper'ları; `sendAlert()` `AFFECTED_APPS` + `OWNER_TEAMS` inject ediyor

---

- [backend] **Phase 1 tamamlandı: Prometheus metrik isimleri yeniden adlandırıldı.**
  - **Değiştirildi**: `internal/engine/engine.go` — `netwatch_probe_up` → `network_probe_local_status`; `netwatch_probe_duration_seconds` → `network_probe_local_latency_seconds`

---

## 2026-05-05 (ve öncesi)

- [altyapi] **Phase 0: Proje baseline — sabit kararlar belirlendi.**
  - Modül path `github.com/saidtaylan/netwatch` olarak korundu
  - Paket yapısı: yalnızca `internal/engine/` + `internal/cluster/` — alt-paket split yok
  - Metrik isimleri direkt yeni isimlere geçiş (alias yok)
  - `Target.Options json.RawMessage` olarak korundu
  - `state.json` cluster modunda da korunacak şekilde kararlaştırıldı
  - Lamport clock `(OwnerNode, Seq)` tuple olarak belirlendi

---

- [altyapi] **Proje başlangıç durumu: tek-node Prometheus exporter.**
  - **Mevcut**: `internal/engine/` — protocol.go, engine.go, loop.go, notify.go, mail.go, http.go, tcp.go, ping.go, dns.go, sql.go
  - **Mevcut**: `cmd/linux/main.go`, `cmd/windows/main.go`
  - **Mevcut**: `config.yaml`, `Dockerfile`, `go.mod`
  - Tek-node probe, state.json v1 (`map[string]bool`), script + SMTP alerting
