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
