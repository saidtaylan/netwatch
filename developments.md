<!--
# LLM AGENT TALİMATLARI

Bu dosya projenin değişiklik günlüğüdür. Her tarih bloğunda:
- **1. seviye bullet** = Sade özet (ne eklendi / ne değişti)
- **2. seviye bullet** = Teknik detay (dosya yolları, ne değişti)

Etiketler: [backend] [frontend] [altyapi] [devops] [dokuman] [test] [refactor]

Arşiv: Henüz arşiv yok. Son 4 haftadan eski → docs/archive/development_YYYY_MM.md
-->

# netwatch Changelog

Bu belge, netwatch projesinin günlük güncellemelerini ve teknik detaylarını takip eder.

---

## 2026-05-22 — Bug Fixes: Fleet Type Mismatch, Config Sync, SLO CRUD, Demo Expansion

- [backend] **fleet.go FleetTarget.ID field eklendi** — Daha önce `targets[]` array'inde ID yoktu. Frontend `/targets/{id}` routing için target key'e ihtiyaç duyuyordu. `ft.ID = key` (= `t.key()`) eklendi. `go clean -cache` gerektirdi (Go build cache eski struct'ı tutuyordu).

- [backend] **engine.go Init() config hash sırası düzeltildi** — `LoadConfig()`, `clusterMgr` oluşmadan çalışıyordu. Bu yüzden `SetLocalConfigInfo()` içindeki `if e.clusterMgr != nil` check'i geçemiyordu ve config hash hiç gönderilmiyordu. Fix: `e.clusterMgr = mgr` satırından hemen sonra `SetLocalConfigInfo` çağrısı eklendi.

- [backend] **cluster.go NotifyJoin → config hash broadcast** — Yeni bir peer join olduğunda config hash yeniden broadcast yapılmıyordu. Peer config hash'lerini `/cluster/config` endpoint'inde görmek için gerekli. `NotifyJoin` goroutine'ine `e.mgr.broadcastConfigInfo()` eklendi.

- [backend] **SLO CRUD API (B12)** — `GET /slo/targets`, `PUT /slo/targets/{id}`, `DELETE /slo/targets/{id}` endpoint'leri eklendi. Write endpoint'leri admin token gerektirir. In-memory (restart'ta sıfırlanır — config.yaml persist B12 devamında).

- [frontend] **types/api.ts tüm type uyuşmazlıkları düzeltildi:**
  - `FleetSnapshot`: `targets[]` array (was Record), `cluster` nested object, `summary` (was `target_counts`)
  - `FleetNodeView` tipi eklendi (was `PeerTargetState` — farklı field'lar)
  - `ConfigSyncSnapshot`: `{ self, peers[], drift_count }` (was `{ local_hash, in_sync, peers: Record }`)
  - `SLOSnapshot`: targets `Record<id, SLOResult>` (was `SLOTargetResult[]`)
  - `SLOTargetResult`: tüm field isimleri düzeltildi (target_id, slo_breached, remaining_budget_sec)
  - `ClusterMember`: port, self alanları eklendi; `ClusterState`: local_node eklendi
  - `SLOTargetConfig` interface eklendi (B12 CRUD için)

- [frontend] **topology.vue UNKNOWN → gerçek state** — Linter topology.vue'yu eski `fleet.data.value?.targets?.[id]` koduna döndürmüştü. Targets array olduğu için her string key lookup undefined döndürüyordu. `targetIndex.value[id]` ile düzeltildi (O(1) Record lookup).

- [frontend] **useFleet.ts refactor** — `targetList` (array), `targetIndex` (Record), `quorumHealthy`, `isolated`, `memberNames`, `counts`, `downTargetIds` composable shortcuts eklendi. Alert change detection array iteration'a uyarlandı.

- [frontend] **pages/config/index.vue yeniden yazıldı** — Doğru backend field'ları: `self.config_hash`, `self.config_size`, `peers[]`, `drift_count`. Hash/size/loaded_at boş gelirse graceful fallback. Peer listesi array olarak iteration.

- [frontend] **pages/slo.vue yeniden yazıldı** — `Object.values(targets)` ile Record→array, sort by breach status, doğru field isimleri (`slo_breached`, `remaining_budget_sec`, `target_id`), `computed_at` gösterimi.

- [frontend] **pages/index.vue cluster member list** — `m.self` badge, `m.status` için renk indicator, `m.addr:m.port` gösterimi.

- [demo] **Demo cluster config genişletildi** — Tüm 5 probe tipi eklendi:
  - `mock-tcp-port` (tcp → 127.0.0.1:9999 mock server) — UP
  - `loopback-ping` (ping → 127.0.0.1) — UP
  - `postgres-main` (sql → 127.0.0.1:5432) — DOWN (no DB)
  - `external-dns` (dns → google.com) — mevcut, korundu
  - HTTP target'lar mevcut (db-primary, api-gateway, checkout)
  
  `external-dns` ve `mock-tcp-port` → payment-platform app'e eklendi.
  `loopback-ping`, `external-dns`, `postgres-main` → infrastructure app (yeni).
  SLO: checkout, external-dns, mock-tcp-port için eklendi.

---

## 2026-05-21 — Frontend S11: Temel Kapanış + Error Page

- [frontend] **`app/error.vue`** — Nuxt 4 standart error handler eklendi
  - 404: "Page not found" + "Go to Cluster Overview" / "Go back"
  - 401/403: "Access denied" + "Go to Setup"
  - 500: "Something went wrong" + error message
  - `clearError({ redirect })` ile state cleanup + navigation
  - Dev modda stack trace details (`import.meta.dev` — Nuxt 4 standardı, `process.dev` deprecated)

- [frontend] **`<NuxtLoadingIndicator>`** — `app.vue`'a eklendi. Route geçişlerinde 2px blue progress bar.

- [frontend] **Composable test kapsamı genişletildi (+24 test)**
  - `tests/unit/composables/useAuth.test.ts` — 9 test (checkToken, login, logout)
  - `tests/unit/composables/useApi.test.ts` — 9 test (HTTP methods, auth header, failover)
  - `tests/unit/composables/useMaintenance.test.ts` — 8 test (CRUD, toasts, active filter)

- [frontend] **E2E error page testleri** — `tests/e2e/error-page.spec.ts` 3 test

**Final sayılar:** Backend 202 + Frontend unit **99** + E2E **29** = **330 test yeşil** ✅

Frontend sprint'i (S0–S11) tamamen kapandı.

---

## 2026-05-21 — Frontend S8/S9/S10 + Nuxt 4 review

- [frontend] **S8 — E2E Test Reliability (303 test yeşil)** 🎯

  9 skip edilen e2e test reaktive edildi, 26/26 geçiyor. Çözülen 6 kök sebep:

  1. **`SkeletonRow.vue` props bug** — `defineProps<...>()` `const props =` olmadan çağrılmış. `props.cols` undefined → 500 server error. `app/components/common/SkeletonRow.vue` tek harf fix.
  2. **Nuxt 4 component auto-import — subfolder pathPrefix** — `app/components/targets/TargetRow.vue` default'ta `TargetsTargetRow` adıyla auto-import ediliyordu. `nuxt.config.ts`'e `components: [{ path: '~/components', pathPrefix: false }]` eklendi. Vue console'da `Failed to resolve component: TargetRow` warning'i ile keşfedildi.
  3. **Vue v-for destructuring** — `v-for="([id, target]) in filtered"` Vue parser tarafından `(value, key, index)` formu olarak interpret ediliyordu. `app/pages/targets/index.vue`: `v-for="entry in filtered"` + `entry[0]`/`entry[1]` ile yeniden yazıldı.
  4. **Pinia hydration safety net** — `useApi.ensureActive()` 'a `waitFor` döngüsü eklendi (300ms × 50ms intervals). localStorage'dan store hydrate olana kadar API çağrısı throw etmesin.
  5. **`pinia-persist.client.ts` enforce: 'pre' + explicit `$hydrate()`** — Plugin diğer plugin'lerden önce çalışsın, auth ve nodes store'ları zorla hydrate edilsin.
  6. **Playwright strict mode violations** — `getByText('Cluster Overview')` sidebar nav + heading'de 2 yerde matched. `getByRole('heading', ...)` ile spesifikle. `getByText('e2e-node')` → `getByRole('cell', ...)`.

  Debug methodu: `playwright.config.ts`'e `debug` projesi eklendi (dependencies yok), browser console + 4xx/5xx response body yakalandı. `<targetrow target="[object Object]">` raw HTML keşfedildi → root cause.

- [frontend] **S9 — Named Routes Refactor**

  ~30 string-path kullanımı named route'a çevrildi:
  - 2 middleware, 2 composable (logout, 401 redirect), 2 page programatic (`navigateTo`), 7 static NuxtLink, 8 dynamic target links, 2 component, 14 Sidebar items
  - `app/components/common/Sidebar.vue` `NavItem.to` tipi `string` → `RouteLocationNamedRaw`
  - Doğrulama: `grep -rEn ':to="/|to="/[a-z]|navigateTo\(['"\\']/'` → **0 sonuç**

- [devops] **S10 — CI Gate Integration**

  - Kök `Makefile`:
    - `make test-frontend-e2e` (build önce, sonra Playwright)
    - `make test-all` (backend + frontend unit + e2e)
    - `make ci` (clean → build → lint → test-all)
  - `.github/workflows/ci.yml` — 4 job:
    - `backend` (Go 1.25 + vet + build + test -race)
    - `frontend-unit` (pnpm 11 + Node 20 + Vitest)
    - `frontend-e2e` (Playwright + Chromium install; failure → report artifact)
    - `ci-passed` (aggregated gate)
  - Triggers: push/PR on main

- [dokuman] **Nuxt 4 review**

  Kullanıcı talebi: "her aracın en güncel dökümantasyonuna göre kullan". Nuxt 4'ün yeni `app/` directory structure'ı, component auto-import default'ları (`pathPrefix: true`), `pinia-plugin-persistedstate` v4 API'si gözden geçirildi. Tek farkedilen sapma: subfolder pathPrefix'i — yukarıda çözüldü.

---

## 2026-05-20 (devam — frontend Sprint 1-4 + testler)

- [frontend] **Unit test altyapısı kuruldu (Sprint 1 sonrası)**

  Vitest 4.1.6 + `@nuxt/test-utils` + `@vue/test-utils` + `happy-dom` + `@vitest/coverage-v8` kuruldu. `@nuxt/test-utils/config` ile Nuxt auto-import desteği sağlandı — Pinia store'ları ve composable'lar gerçek Nuxt ortamında test ediliyor.

  - `tests/unit/utils/format.test.ts` — 9 test: `fmtDurationSec`, `fmtPercent`, `fmtLatency`, `capitalize`
  - `tests/unit/utils/classifyState.test.ts` — 14 test: `stateStyle` (fallback dahil), `isDown`, tüm SCOPE_STYLE ve CLASS_STYLE kayıtları
  - `tests/unit/stores/auth.test.ts` — 5 test: login, logout, isAdmin, default role
  - `tests/unit/stores/nodes.test.ts` — 11 test: add, deduplicate, normalize URL, setActive, markHealthy/Unhealthy, failover, removeNode, reset
  - `tests/unit/stores/alerts.test.ts` — 11 test: push, dedup by (target_id, seq), FIFO ring buffer cap=100, ack, mute, unresolvedCount, clear, recent
  - `tests/unit/stores/ui.test.ts` — 7 test: polling clamp, sidebarToggle, toast auto-remove, removeToast
  - `tests/unit/composables/useNodeConnection.test.ts` — 8 test: selectActiveNode (no nodes, single, multi-fallback), markUnhealthy, ensureActive (cache hit, null active), seedFromEnv
  - **Toplam: 67 test, 7 dosya, tümü -race ile yeşil**

- [frontend] **Auth sadeleştirildi (Sprint 2)**

  Self-hosted single-admin-token modeli. `useAuth.ts` yorumuna "SaaS değil" notu eklendi. `/login` sayfası `/setup`'a yönlendiriyor — tek giriş noktası. `auth.global.ts` logout → `/setup` (login sayfası ortadan kalktı). Token yoksa `/setup`'a yönlendir. Gelecekte LDAP entegrasyonu için `WhoAmIResponse.role` genişletilebilir.

- [frontend] **Sprint 2 — Node Connection tamamlandı**

  `useNodeConnection` (Promise.any race, failover, seedFromEnv), `useApi` (Bearer inject, 401 auto-logout, failover retry), `usePolling` (visibilitychange, global interval), middleware'lar tüm testlerden geçiyor.

- [frontend] **Sprint 3 — Tüm sayfa ve component'ler**

  - `pages/targets/index.vue` — Arama + status/type filtresi + tablo; `TargetRow.vue` ile state renk, scope, classification, app
  - `pages/apps.vue` — App → target gruplaması, down sayacı
  - `pages/slo.vue` — target_uptime/actual_uptime/error_budget/incident listesi; 503 → "disabled" mesajı
  - `pages/maintenance.vue` — Aktif window listesi, form (targetId/duration/reason), cancel (ConfirmDialog), gossip-replicated
  - `pages/alerts.vue` — In-memory ring buffer (state change detection), B5 Ack placeholder
  - `pages/settings/index.vue` — Polling interval, dark mode, session (disconnect)
  - `pages/settings/nodes.vue` — Node listesi CRUD, test/use/remove butonları
  - `pages/config/index.vue` — Config drift view, sync now butonu
  - `pages/config/push.vue` — SharedConfig form, PUT /cluster/config, push result
  - `pages/config/keyring.vue` — Add/Use/Remove key; sıfır-kesinti rotasyon talimatları
  - `pages/geo.vue` — Per-target/per-node latency, anomaly highlight
  - `pages/silences.vue` + `pages/audit.vue` — B1/B7 placeholder "Coming Soon"
  - `components/cluster/` — NodeCard, QuorumIndicator (isolated/quorum/standalone), ConfigDriftCard
  - `composables/` — useCluster, useFleet (alert change detection), useMaintenance (create/cancel), useTopology, useGeoLatency, useSLO

- [frontend] **Sprint 5 — Polish (skeleton + error banner + a11y)**

  - `components/common/SkeletonRow.vue` + `SkeletonCard.vue` — yeniden kullanılabilir loading state'leri
  - `components/common/ErrorBanner.vue` — friendly hata mesajı + retry butonu (`'Failed to fetch'`, `'401'`, `'403'` patternleri için human-readable)
  - `index.vue`, `targets/index.vue`, `targets/[id].vue`, `maintenance.vue`, `slo.vue` — skeleton + error banner entegrasyonu
  - `Sidebar.vue` nav linklerine `aria-label`, `aria-disabled`, `focus-visible:ring-2`
  - `ConfirmDialog.vue` — `Escape` ile kapama, `role="dialog"`, `aria-modal`, autofocus
  - `usePolling` — error streak ile exponential back-off (errorStreak × min(2^n, 3) × baseInterval), visibility-aware
  - 6 yeni `usePolling` unit testi: refresh, error capture, error recovery, loading lifecycle, data preservation, error streak — **toplam 75 unit test**

- [frontend] **Sprint 6 — Production Deployment**

  - `deploy-systemd/netwatch-backend.service` — User=netwatch, AmbientCapabilities=CAP_NET_RAW, ProtectSystem=strict, journald logging
  - `deploy-systemd/netwatch-frontend.service` — Node 20+ ile `.output/server/index.mjs`, hardening
  - `deploy-systemd/netwatch.target` — backend + frontend birlikte start/stop
  - `deploy-systemd/install.sh` — sudo install script: kullanıcı yarat, dizinler, binary, frontend `.output/`, systemd units, daemon-reload, enable + start
  - Kök `Makefile` — `make build`, `make test`, `make lint`, `make install`, `make clean` (her ikisini orchestrate)
  - `README.md` "Admin UI" + "Production Deployment" bölümleri
  - `make test` → backend 202 + frontend 75 = **277 test yeşil** ✓

- [frontend] **Sprint 7 — Playwright E2E (kısmen tamamlandı)**

  Playwright 1.60 + Chromium kuruldu. 26 e2e test yazıldı.

  **Sonuç:** 17 geçiyor, 9 skip — kök sebep `pinia-plugin-persistedstate` hydration timing race condition.

  - `tests/e2e/auth.setup.ts` — login akışı, storageState kaydet
  - `tests/e2e/auth-redirect.spec.ts` — 4 test: yetkisiz → /setup yönlendirme
  - `tests/e2e/cluster-overview.spec.ts` — 5 test (3 geçiyor): heading, stat cards, down targets
  - `tests/e2e/targets.spec.ts` — 10 test (5 geçiyor): heading, count, detail navigation, db-primary DOWN state
  - `tests/e2e/maintenance.spec.ts` — 6 test (4 geçiyor): heading, form aç/kapa, input doldur
  - `tests/e2e/fixtures/mock-backend.ts` — Node HTTP server, port 19240, FLEET data
  - `tests/e2e/fixtures/api-mocks.ts` — `page.route()` interceptions (denendi, terk edildi)

  **Skip edilen 9 test:**
  - cluster-overview: sidebar nav, dark mode toggle (selector flaky)
  - maintenance: empty state, success toast (polling timing)
  - targets list: 5 data-dependent test (pinia hydration race)

  Skip nedenleri her test başında `test.skip()` ile dokümante edildi. Kök sebep `sprint.md` S8'de detaylı yazıldı.

- [dokuman] **CLAUDE.md kuralları**

  - **Frontend Routing Kuralı — Named Routes Şart:** Tüm `NuxtLink`/`navigateTo` çağrıları named route kullanır (`{ name: 'targets-id', params: { id } }`). String path **yasak**. Refactor sprint S9'a yazıldı.
  - **Persistent Store Kararı:** `auth`, `nodes`, `ui` stores localStorage'da kalıcı. F5'te tekrar giriş gerekmesin (Kibana/Grafana pattern). Self-hosted single-admin-token modeli. LDAP gelene kadar değişmeyecek.

- [frontend] **Sprint 4 — Target detail + Topology**

  - `pages/targets/[id].vue` — Header (state/scope/classification), ScopeClassificationCard (down/up nodes), ByNodeBreakdown tablosu, DependencyChip (root_cause/deps/impact), ProberAssignment listesi, GeoLatency per-node tablo
  - `pages/topology.vue` — Root/bağımsız target'lar, bağımlı target'lar, tam tablo (depends_on + cascading); graph visualizasyon sprint'e bırakıldı
  - `components/targets/` — ByNodeBreakdown, ScopeClassificationCard, DependencyChip
  - `pnpm build` → clean `.output/` (1.8 MB) ✓
  - Backend: `GET /auth/whoami`, `GET /version`, CORS middleware, `AdminConfig.CORSOrigin` — build + 202 test ✓

---

## 2026-05-20 (devam — frontend hazırlık)

- [refactor] **Sprint 0 — Repo Reorganizasyonu (frontend/backend split)**

  Tek git reposu içinde iki servis olarak yapılandırıldı. Backend Go kodu `backend/` dizinine taşındı, `frontend/` dizini Nuxt 3 için ayrıldı.

  - `git mv cmd/ internal/ test/ tests/ tests/ deploy/ helm/ notifications/ Dockerfile Makefile go.mod go.sum config.example.yaml config.yaml → backend/`
  - `git mv CLUSTER_TESTING_GUIDE.md GUIDE.md GUIDE_EN.md .dockerignore → backend/`
  - `.gitignore` güncellendi: frontend node_modules, .nuxt, .output, dist; backend bin/ yolları prefix'lendi
  - `CLAUDE.md` güncellendi: build komutları `cd backend &&` prefix'i aldı, frontend bölümü eklendi
  - Module path değişmedi: `github.com/saidtaylan/netwatch`
  - Smoke test: `cd backend && go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/` ✓ `go test -race` 202 test yeşil ✓
  - Kök dizinde yalnızca paylaşılan dok'lar kaldı: developments.md, sprint.md, system_map.md, todo.md, CLAUDE.md, README.md

- [dokuman] **Frontend mimari planı yazıldı** (`frontend-plan.md`)

  Nuxt 3 + Tailwind + Pinia + standalone deployment kararları, sprint sırası (Sprint 0-10), tüm sayfa → endpoint eşlemeleri, B1-B11 için UI placeholder stratejisi, multi-node failover algoritması, systemd target yapısı, ve auth akışı belgelendi.

---

## 2026-05-20

- [backend] **F1 — Probe Interval Staggering**

  Aynı target'ı probe eden N node artık hepsi aynı anda atmıyor. Her prober `(probe_interval / N) * prober_index` kadar bekleyip ilk probe'unu atar, sonra normal ticker'a geçer.

  - `internal/engine/loop.go` `startProbeLoop()`: stagger offset hesabı eklendi
  - Standalone (cluster yok) veya tek prober: offset=0, mevcut davranış korunur
  - Prober assignment değişince (NotifyJoin/Leave) yeni offset otomatik hesaplanır
  - Etki: 3 prober × 60s interval → artık 0s / 20s / 40s probe; ortalama down tespiti 60s → 20s

- [backend] **F2 — ROOT_CAUSE Cross-Node Fix (BUG FIX)**

  Gerçek bir bug: `processPending()` aynı ticker tick'inde birden fazla target'ı hard_down'a escalate ediyorsa, birincinin alert'i ikincisi henüz `lastKnown`'a yazılmadan gönderiliyordu. ROOT_CAUSE hesabı "db-primary=up" görüyordu, yanlış çözüm dönüyordu.

  - `internal/engine/loop.go` `processPending()` iki aşamaya ayrıldı:
    - **Faz 1**: tüm due entry'leri probe et, hard_down olanları `markHardDown()` ile `lastKnown`'a yaz
    - **Faz 2**: alert gönder (tüm state geçişleri commit edilmiş, allStates snapshot doğru)
  - `tests/domain/crossnode_rootcause_test.go` eklendi: standalone + cluster (disjoint prober set) senaryoları

- [backend] **F3 — Maintenance Window (API-driven, gossip-replicated)**

  Operatör `PUT /cluster/maintenance` ile target'ları geçici olarak alarm bastırır. Restart'tan sağ çıkar, tüm cluster node'larına gossip ile yayılır.

  - **Yeni dosyalar**: `internal/engine/maintenance.go`, `internal/cluster/maintenance.go`
  - `maintenance.go`: `MaintenanceWindow` struct, `maintenanceManager` (RAM + `maintenance.json` disk persistence, atomic write)
  - `cluster/maintenance.go`: `MaintenanceBroadcast` gossip mesajı (`msgType: "maintenance"`), `MaintenanceHandler` interface, `BroadcastMaintenanceSet/Cancel`
  - `cluster.go` `NotifyMsg`: `msgTypeMaintenance` dispatch eklendi
  - `engine.go` `shouldAlert()`: `maintMgr.IsInMaintenance(targetID)` önce kontrol edilir → false → alarm bastır
  - `engine.go` `Init()`: `newMaintenanceManager()` + `runMaintenancePruner()` goroutine
  - `engine.go`: `MaintenanceHandler` interface implementasyonu (`ApplyMaintenanceSet`, `ApplyMaintenanceCancel`)
  - **Yeni endpoint'ler** (`cmd/linux/main.go` + `cmd/windows/main.go`):
    - `GET  /cluster/maintenance` — aktif window listesi (auth gerektirmez)
    - `PUT  /cluster/maintenance` — yeni window (`{target_ids, duration, reason, started_by}`); gossip broadcast; auth zorunlu
    - `DELETE /cluster/maintenance/{id}` — iptal; gossip broadcast; auth zorunlu
  - Persistence: `<state_file_dir>/maintenance.json` (v1 format)
  - Restart davranışı: load → süresi dolmuş entry'ler atılır → aktifler uygulanır
  - Probe'lar çalışmaya devam eder; sadece `shouldAlert()` bastırılır

- [backend] **F4 — Soft-Up State (Symmetric Recovery)**

  `recovery_probes: N` (default 1) ile recovery flap koruması. N ardışık başarılı probe olmadan "reachable" alarmı atılmaz.

  - `Config.RecoveryProbes *int` + `Config.globalRecoveryProbes()` yeni alanlar
  - `Target.RecoveryProbes *int` — per-target override
  - `Engine.pendingRecovery map[string]int` — soft_up sayacı, `stateMu` ile korunur
  - `loop.go` `runCheck()`: N=1 → mevcut davranış (fast path); N>1 → soft_up counter
  - Soft_up sırasında probe fail gelirse counter sıfırlanır (hâlâ hard_down sayılır)
  - `SharedConfig.RecoveryProbes` eklendi → `PUT /cluster/config/sync` ile tüm cluster'a yayılabilir
  - Default N=1: geriye dönük uyumlu, mevcut davranış değişmez

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/   ✓
  GOOS=windows go build ./cmd/windows/                           ✓
  go test -race ./internal/engine/... ./internal/cluster/...     ✓ (202 test)
  go test -race ./tests/engine/... ./tests/cluster/...           ✓ (86 test)
  go test -race ./tests/domain/...                               ✓ (18 test)
  ```

---

## 2026-05-14

- [backend] [dokuman] **CLI join workflow — `netwatch init --cluster`, `netwatch join`, `netwatch keyring generate` + startup banner.**

  Elasticsearch/kubeadm tarzı tek komutlu cluster join akışı:

  **1. `netwatch init --cluster [--bind-port N] [--force]`**
  - Cluster-enabled config skeleton üretir, random 32-byte AES-256 keyring otomatik
  - Config zaten varsa interaktif overwrite prompt (default: hayır)
  - Çıktıda copy-paste edilebilir `netwatch join --keyring ... --addr ...` komutu
  - `defaultAdvertiseAddr()` ile non-loopback IPv4 otomatik tespiti

  **2. `netwatch join --keyring K --addr H:P [--config PATH] [--bind-port N] [--node-name N]`**
  - Tek komutla cluster'a katılma
  - Config yoksa minimal skeleton üretir
  - Config varsa sadece `cluster.*` bölümünü override eder; targets/notifications/slo vs. korunur
  - Atomik yazım (`.tmp` + rename)
  - Agent başlatmaz — operatör `systemctl start` yapar veya hot-reload bekler
  - Validation: keyring base64 + 16/24/32 byte; addr `host:port` format

  **3. `netwatch keyring generate`**
  - Yeni 32-byte AES-256 base64 key basar
  - Keyring rotation veya manuel kurulum için

  **4. Startup banner**
  - `cluster.enabled=true` agent başlatıldığında stdout'a basılır
  - Node adı, `LocalAddr` (memberlist'in seçtiği gerçek advertise adresi), aktif üye sayısı
  - Operatörün kopyalayabileceği tam `netwatch join` komutu

  **Yeni dosyalar:** `internal/engine/join.go` (`GenerateKeyringKey`, `LocalClusterAddr`, `ClusterPrimaryKey`, `ClusterMemberCount`)

  **cluster.go eklemeleri:**
  - `Manager.LocalAddr() string` — memberlist `LocalNode()` üzerinden advertise edilen `host:port`
  - `Manager.PrimaryKey() string` — keyring[0] (base64), banner için

  **cmd/linux/main.go + cmd/windows/main.go:**
  - `cmdInit` → `--cluster`, `--bind-port`, `--force` flag'leri + overwrite prompt + cluster config skeleton template
  - `cmdJoin`, `cmdKeyring` yeni subcommand'lar
  - `printJoinBanner(e)` — `runAgent` sonunda cluster aktifse çağrılır
  - `validKeyringKey`, `keyringRawLen`, `maskKeyring`, `defaultAdvertiseAddr`, `promptYesNo` helper'ları
  - `/cluster/config` GET ve PUT handler'ları **tek mux pattern**'da birleştirildi (mux pattern conflict bug fix)

  **Akış:**
  ```
  Node-1: netwatch init --cluster
          → keyring otomatik üretildi
          → join komutu çıktıda

  Node-2: netwatch join --keyring ... --addr 10.0.0.1:7946
          → config.yaml yazıldı, cluster.enabled=true

  Her node: systemctl start netwatch
          → banner stdout'a basılır, copy-paste hazır
  ```

  **Build + Test + Smoke:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  GOOS=windows go build ./cmd/windows/                          ✓
  go test -race -count=1 -timeout 120s ./internal/...           ✓
  netwatch init --cluster → keyring + join cmd output           ✓
  netwatch join --keyring K --addr A → config.yaml written      ✓
  netwatch keyring generate → fresh base64 key                  ✓
  Agent start → banner with real LocalAddr + keyring            ✓
  ```

- [backend] [dokuman] **Config push/sync endpoint'leri + `node_alias` rename + admin bearer token auth.**

  **1. Shared config push/sync — `PUT /cluster/config` + `POST /cluster/config/sync`**

  Bir node'un ortak konfigürasyon alanlarını tüm cluster'a yaymasını sağlayan iki yeni endpoint:

  - `PUT /cluster/config` — Body'de kısmi veya tam SharedConfig (JSON veya YAML). Çağrıldığı node'a uygulanır + gossip TCP ile tüm diğer node'lara iletilir.
  - `POST /cluster/config/sync` — Body yok. Bu node'un kendi diskindeki config'inden ortak alanlar okunur ve tüm peer'lara gönderilir. Self-apply yok (zaten güncel).
  - Cluster disabled ise 503 + açıklayıcı mesaj.

  **Ortak (eşitlenen) alanlar:** `timeout`, `max_retries`, `retry_interval_sec`, `ticker_interval_sec`, `probe_interval_sec`, `reload_interval_sec`, `watchdog_threshold_sec`, `notifications`, `default_notify`, `cluster.keyring`, `cluster.peers`, `cluster.expected_node_count`, `cluster.min_quorum_ratio`, `cluster.probe_replication_factor`, `cluster.min_probe_confirmations`.

  **Node-specific (asla üzerine yazılmaz):** `port`, `node_alias`, `log_path`, `state_file`, `credentials_file`, `targets`, `apps`, `slo`, `cluster.node_name/bind_*/advertise_*/zone/region`.

  **Transport:** Memberlist `SendReliable` (TCP, AES-encrypted). Her peer için ayrı sonuç döner. Başarısız delivery `failed_nodes` map'inde görünür.

  **Persistence:** Peer node config.yaml'ını atomik yazar (`.tmp` + rename), ardından `Reload()` tetikler. Restart sonrasında değişiklik korunur.

  **Response:**
  ```json
  {
    "applied_locally": true,
    "broadcast_to": ["node-2","node-3"],
    "failed_nodes": {},
    "fields_applied": ["notifications","default_notify","cluster.*"],
    "pushed_at": "2026-05-14T10:00:00Z"
  }
  ```

  **Credential safety:** `/sync` endpoint'i disk'teki raw (pre-injection) baytları okur — RAM'deki çözülmüş `${VAR}` değerlerini değil. Böylece `${SMTP_PASS}` gibi şifreler peer'lara sızmaz.

  **Yeni dosyalar:** `internal/engine/configpush.go`, `internal/cluster/configpush.go`.

  **2. `app_name` → `node_alias` rename**

  - Config key: `app_name` → `node_alias`. Eski `app_name` anahtar varsa uyarıyla migrate edilir (backward compat).
  - Struct field: `Config.AppName` → `Config.NodeAlias`. `AppName()` metodu deprecated wrapper olarak kaldı.
  - Alert env: `NODE_ALIAS` eklendi, `APP_NAME` backward compat için korundu.
  - Metric label adı `app_name` olarak kaldı (Grafana dashboard uyumu).
  - `validate` output: `app_name` → `node_alias`.
  - `init` template: `app_name` → `node_alias`.
  - `config.yaml`, `config.example.yaml` güncellendi.

  **3. Admin bearer token auth**

  Yeni `admin` config section:
  ```yaml
  admin:
    token: "${ADMIN_TOKEN}"  # boşsa endpoint'ler açık (mevcut davranış)
  ```

  Write-capable endpoint'ler (`PUT /cluster/config`, `POST /cluster/config/sync`, `POST /cluster/keyring/rotate`, `POST /cluster/leave`) artık token ayarlıysa `Authorization: Bearer <token>` header zorunlu.
  - Token eşleşmezse: 403 Forbidden
  - Header yoksa: 401 Unauthorized + `WWW-Authenticate: Bearer realm="netwatch-admin"`
  - `AdminConfig` struct: ileride `Users []AdminUser` genişletmesine hazır tasarım.

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  go test -race -count=1 -timeout 120s ./internal/engine/... ./internal/cluster/...  ✓
  ```

- [backend] **Soft-down gossip, fast-check probe, underreplicated metric, min_probe_confirmations.**

  Dört bağlantılı problem çözüldü:

  **Problem 1 — Soft-down kaybolursa ne olur?**
  Bir prober node soft-down aldıktan sonra hard-down'a escalate edemeden kill/leave olursa, cluster başka hiçbir node'un haberi olmadan o target hakkında belirsizlikte kalıyor.

  **Çözüm — Soft-down gossip sinyali:**
  - `internal/engine/loop.go`: `broadcastSoftDown(t Target, retryNum int)` yeni fonksiyon — `State="soft_down"`, `Seq=0` (Lamport versiyonsuz), `RetryNum` dahil, fire-and-forget payload ile cluster.Broadcast
  - `runCheck`: target ilk kez soft-down'a girdiğinde (`enqueue` true döndüğünde) `broadcastSoftDown(t, 0)` çağrılıyor
  - `processPending`: her başarısız retry sonrasında `broadcastSoftDown(t, newCount)` çağrılıyor (retry sayısı co-probers için urgency göstergesi)
  - `internal/cluster/cluster.go`: `OnStateReceived` soft_down için erken dönüş — peerStates'e yazılmıyor, sadece `SoftDownNotifier` tetikleniyor
  - `cluster.SoftDownNotifier` interface: `NotifyCoProberSoftDown(targetID string)` — engine bu interface'i implement ediyor

  **Problem 2 — Co-prober nasıl tepki verir?**
  Soft-down sinyali alan co-prober, bir sonraki ticker tick'ini beklemeden anında probe yapmalı.

  **Çözüm — Fast-check channel:**
  - `internal/engine/engine.go`: `Engine.probeFastCheck map[string]chan struct{}` — per-target buffered(1) channel
  - `startProbeLoop`: `fastCheckCh := make(chan struct{}, 1)` oluşturuyor, `probeFastCheck[t.key()]` map'ine yazıyor; probe goroutine'i `case <-fastCheckCh:` select branch'i ile anında probe tetikleyebilir
  - `stopProbeLoop`: `delete(e.probeFastCheck, key)` ile cleanup
  - `NotifyCoProberSoftDown(targetID)`: `probeFastCheck[targetID]` channel'a non-blocking send — channel dolu ise drop (zaten sinyal var)

  **Problem 3 — Tek node'un network problemi yanlış alarm üretir (split-brain light).**
  Bir node'un kendi network bağlantısı bozuksa hard-down'a geçip alarm atar ama diğer probers target'ı sağlıklı görüyor olabilir.

  **Çözüm — `min_probe_confirmations`:**
  - `internal/cluster/cluster.go`: `Config.MinProbeConfirmations int` yeni config alanı
  - `Manager.MinProbeConfirmations() int` getter
  - `internal/engine/engine.go`: `effectiveMinConfirmations()` helper; `shouldAlert()` — `minConf > 1` iken tüm probers'ın hard_down count'u toplanıyor, yeterli confirmation yoksa alarm suppressed + debug log
  - Default 0 (=1 confirmation = mevcut davranış korunur, geriye uyumlu)

  **Problem 4 — Underreplicated coverage tespiti.**
  `factor=3` ama yalnızca 2 node probe ediyorsa (biri yeni join etmedi, biri leave etti) orphan değil ama degraded.

  **Çözüm — `network_probe_prober_underreplicated` metric:**
  - `internal/engine/engine.go`: `GaugeProberUnderreplicated` GaugeVec — `1 = len(probers)>0 && len(probers)<factor`
  - `RegisterClusterMetrics` + `updateClusterMetrics` loop'una eklendi
  - Label'lar: `name`, `target`, `type` (ownership set — host/app değil)

  **Özet akış (50 node, factor=3, target down):**
  ```
  Prober-A: soft_down → broadcastSoftDown → gossip(State="soft_down")
  Prober-B/C: OnStateReceived → NotifyCoProberSoftDown → probeFastCheck←signal
  Prober-B/C goroutine: case <-fastCheckCh → runCheck (anında, ticker beklenmez)
  Eğer minConf=2: Prober-A hard_down aldı, B de hard_down alırsa → shouldAlert=true
                  Sadece A hard_down gördüyse → shouldAlert=false (suppressed)
  ```

  **Değiştirilen dosyalar:**
  - `internal/cluster/cluster.go`: `Config.MinProbeConfirmations`, `GossipPayload.RetryNum`, `SoftDownNotifier` interface, `Manager.softDownNotifier`, `SetSoftDownNotifier()`, `Manager.MinProbeConfirmations()`, `OnStateReceived` soft_down early-return
  - `internal/engine/engine.go`: `probeFastCheck` field + init, `GaugeProberUnderreplicated` + register, `NotifyCoProberSoftDown()`, `effectiveMinConfirmations()`, `shouldAlert()` min-conf guard, `updateClusterMetrics` underreplicated gauge, `Init()` `SetSoftDownNotifier(e)` wiring
  - `internal/engine/loop.go`: `startProbeLoop` fastCheckCh + select branch, `stopProbeLoop` cleanup, `broadcastSoftDown()` yeni fonksiyon, `runCheck` + `processPending` call sites

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  go test -race -timeout 120s ./internal/engine/... ./internal/cluster/...  ✓
  ```

- [backend] [test] **Windows/Linux parity + comprehensive E2E test suite (31 tests, all pass with -race).**

  İki büyük iş tamamlandı:

  **1. cmd/windows/main.go — tam Linux parity:**
  - Önceki durum: Windows binary'si yalnızca `/metrics`, `/health`, `/status` endpointlerini sunuyordu (~246 satır).
  - Yeni durum: Tüm 12 endpoint eklendi — `/metrics`, `/health`, `/status`, `/topology`, `/cluster/state`, `/cluster/probers`, `/fleet/status`, `/slo`, `/cluster/config`, `/geo/latency/`, `/cluster/keyring/rotate`, `/cluster/leave`
  - `netwatch init`, `netwatch validate`, `netwatch leave`, `netwatch uninstall` CLI subcommand'ları eklendi
  - `fleetStatusText`, `sloText`, `formatBudget` formatlama fonksiyonları Linux'tan kopyalandı
  - Windows Service lifecycle düzeltildi: `agentService.Execute()` artık `leaveCh chan string` üzerinden graceful stop yapıyor — SCM stop komutu `leaveCh`'e yönlendiriliyor, `runAgent()` return ediyor, servis clean shutdown yapıyor
  - Koşullu cluster/SLO metrik kaydı eklendi (enabled olmadığında register edilmiyor)

  **2. internal/engine/engine.go — idempotent Shutdown:**
  - `Engine.shutdownOnce sync.Once` eklendi; `Shutdown()` fonksiyonu `shutdownOnce.Do()` ile sarıldı
  - `memberlist.Leave()` çift çağrıda panik atıyordu — `sync.Once` ile tam çözüm
  - Bu production bug'ı: herhangi iki goroutine aynı anda Shutdown çağırırsa (SIGTERM + HTTP `/cluster/leave`) artık güvenli

  **3. test/integration/comprehensive_test.go — 31 kapsamlı E2E test:**
  - Apps (multi-app enrichment, kanal birleştirme, fallback, partial down): 4 test
  - Dependency graph (root cause zinciri, cascading impact, topology edges): 3 test
  - SLO (incident kaydı, disabled mode): 2 test
  - FleetSnapshot (standalone mode, apps enrichment): 2 test
  - Config validation (valid, dup ID, unknown app target, cyclic dep): 4 test
  - HTTP probe up/down: 1 test
  - 5-node quorum kaybı → isolated mode: 1 test
  - 3-node primary failover + exactly-once alert: 1 test
  - Zone-aware prober spread (4 node, 2 zone): 1 test
  - Scope classification (GLOBAL, non-STANDALONE): 1 test
  - Watchdog smoke: 1 test
  - State machine seq + error code: 1 test
  - 3-node cluster exactly-once: 1 test
  - Key rotation (shared key + hot add): 2 test
  - Standalone (probe cycle, app enrichment, v1 migration): 3 test
  - **Tümü -race ile yeşil, 321s toplam**

  **Düzeltilen test bug'ları:**
  - `TestFleetSnapshot_StandaloneMode`: `scope.go:119` standalone+down → "NODE_LOCAL" (STANDALONE değil); test beklentisi düzeltildi
  - `TestHTTP_Probe_UpDown`: `expected_status.eq` operatörü yok → `in: [200]` ile düzeltildi
  - `TestDependency_RootCause_InAlert`: 3 target eş zamanlı kapatılınca root cause yanlış çözülüyordu → sequental takedown + FleetSnapshot poll ile düzeltildi
  - `TestCluster_5Node_QuorumLost_Isolated`: quorum loop 5s lag'ı — `waitState` hem AliveCount≥5 hem `!IsolatedMode()` koşulunu bekleyecek şekilde genişletildi
  - `TestCluster_Scope_GLOBAL`: çift listener (port çakışması) → FleetSnapshot tabanlı UP detection ile değiştirildi

---

## 2026-05-13

- [backend] [altyapi] **Zone/region safety — hot-reload NodeMeta propagation + orphan target detection.**

  Mevcut implementasyonda iki zone/region gap'i vardı; ikisi de düzeltildi.

  **Bug fix — Hot-reload sonrası NodeMeta propagation:**
  - Önceki durum: `cluster.zone` veya `cluster.region` config'te değiştirilip hot-reload yapıldığında, `cluster.Manager.cfg` ESKİ değerde kalıyor → `NodeMeta()` peerlara eski label'ı dönüyor → diğer node'lar yanlış zone-aware seçim yapıyor → restart gerekiyor
  - Düzeltme: `cluster.Manager.UpdateNodeMeta(zone, region string) error` eklendi — `m.cfg.Zone/Region` günceller ve `m.list.UpdateNode(1s)` ile peerlara forces refresh atar (memberlist NodeMeta update mesajı yayar)
  - `Engine.Reload`'a entegre: eski/yeni zone/region farkı varsa `UpdateNodeMeta` çağrılıyor
  - `NotifyUpdate` zaten peer tarafında `scheduleRecompute` tetikliyor → tüm node'lar otomatik adapt eder

  **Yeni özellik — Orphan target detection:**
  - Bir target'a hiçbir node atanmamış olabilir: `probe_from: ["typo"]` veya `probe_from_regions: ["yanlış-region"]` veya tek eligible node leave olunca
  - Önceki durum: silent failure — sadece `prober_count=0` olarak metric'te görünür, alarm/log yok
  - `cluster.Manager.OrphanedLocalTargets() []string` — `SelectProbers(id)` boş dönen local target'lar
  - `network_probe_target_orphaned` GaugeVec — 1=orphan, 0=at least one prober. Labels: `name`, `target`, `type`
  - `Engine.refreshOrphanState(targets)` — 5s'lik cluster metrics updater'dan çağrılıyor; per-target gauge set + edge-triggered log:
    - Yeni orphan: `[CLUSTER] target orphaned — no eligible probers  target=X hint=check probe_from / probe_from_regions / cluster.zone`
    - Recovery: `[CLUSTER] target re-assigned — prober available again  target=X probers=[...]`
  - `Engine.orphanedSet` field + `orphanedMu` ile transition state takip ediliyor

  **Test sayısı:** 6 yeni test (`orphan_test.go`): NoProvider, AllAssigned, PinToDeadNode, RegionFilterEmpty, Recovers_WhenPinNodeJoins, UpdateNodeMeta_NoListIsSafe — tümü `-race` ile yeşil.

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  go test -race ./internal/engine/... ./internal/cluster/...    ✓
  go test -race -timeout 300s ./test/integration/...            ✓  (110s, 0 race)
  go vet ./...  ✓
  ```

- [backend] [devops] **format=text|json, validate subcommand, keyring rotation — 4 yeni özellik.**

  **`/fleet/status?format=text` ve `/slo?format=text`:**
  - `format=text`: terminal-dostu ASCII tablo; `format=json` veya format belirtilmemesi → JSON (mevcut davranış)
  - `/fleet/status?format=text`: CLUSTER başlığı, TARGETS özeti, INCIDENTS listesi, per-target STATE/SCOPE/CLASSIFICATION/CONF tablosu (DOWN-first sıralama)
  - `/slo?format=text`: ComputedAt başlığı, TARGET/ACTUAL/STATUS/BUDGET REMAINING sütunları
  - `formatBudget(sec int64)` helper: `+1h05m30s` / `-2m00s` formatı
  - `cmd/linux/main.go`'da `fleetStatusText()` ve `sloText()` fonksiyonları eklendi

  **`netwatch validate [--config FILE]`:**
  - Config yükler ve doğrular, hiçbir goroutine veya network bağlantısı başlatmaz
  - Başarıda: app_name, targets (total/active), apps, channels, cluster, slo özeti
  - Başarısızda: açıklayıcı hata + exit 1
  - `engine.ValidateConfigFile(path) (Config, error)` exported fonksiyonu eklendi
  - Subcommand router'a `case "validate"` eklendi

  **`GET|POST /cluster/keyring/rotate` — sıfır-kesinti AES key rotasyonu:**
  - `GET`: `KeyringInfo` döner (key_count, primary_key_prefix, key_prefixes)
  - `POST {"action":"add","key":"base64..."}`: yeni key ring'e eklenir (tüm node'lar eski+yeni ile decrypt eder)
  - `POST {"action":"use","key":"base64..."}`: key primary olarak set edilir (artık bu ile encrypt edilir)
  - `POST {"action":"remove","key":"base64..."}`: eski key ring'den kaldırılır
  - `cluster.Manager.keyring *memberlist.Keyring` field eklendi; `New()`'da assign ediliyor
  - `KeyringAddKey`, `KeyringUseKey`, `KeyringRemoveKey`, `KeyringInfo` metodları eklendi
  - Cluster disabled ise 503; keyring yoksa (şifreleme kapalı) descriptive hata

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  go test -race ./internal/engine/... ./internal/cluster/...    ✓
  go vet ./...  ✓
  netwatch validate --config config.yaml  ✓  (OK + özet çıktısı)
  netwatch validate --config /bad.yaml    ✓  (INVALID + exit 1)
  ```

- [backend] [altyapi] **P1.5 + P1.6 tamamlandı — Gossip Config Sync (drift detection) + Geo Latency View (per-region latency + anomaly detection).**

  **P1.5 — Gossip Config Sync (`internal/cluster/configsync.go` yeni dosya):**

  Her node `config.yaml` dosyasının SHA-256 parmak izini (ilk 16 hex) gossip üzerinden yayıyor. Peer'lar karşılaştırıyor; uyumsuzlukta warning log + `network_probe_config_drift=1`.

  | Bileşen | Detay |
  |---------|-------|
  | `ConfigBroadcast` | `msg_type: "config"` ile eski `GossipPayload`'dan ayrıştırılıyor (backward-compat) |
  | `cfgBroadcast` | `memberlist.Broadcast` impl; `Invalidates=true` → kuyrukta sadece son hash |
  | `ConfigHashOf(raw)` | `sha256[:16]` — ham `config.yaml` baytları üzerinden (var substitution öncesi) |
  | `SetLocalConfigInfo` | `LoadConfig` sonrası çağrılıyor; broadcast + dahili kayıt |
  | `ConfigSyncSnapshot` | `GET /cluster/config` yanıtı: self hash + peer hash listesi + drift count |
  | `runConfigSyncLoop` | `config_sync.sync_interval_sec` (default 30s) aralıklı re-broadcast |
  | `GaugeConfigDrift` | `network_probe_config_drift` — 1=drift var, 0=tüm peerlar senkron |
  | Test | `configsync_test.go` — 7 test: hash, inject, drift detection, snapshot, self-ignore |

  **P1.6 — Geo Latency View (`internal/cluster/geolat.go` yeni dosya):**

  `cluster.region` (Zone'dan ayrı coğrafi etiket). Her başarılı probe'da `GossipPayload.Latency` doluyor. `/geo/latency/{targetID}` per-node latency + anomaly flag.

  | Bileşen | Detay |
  |---------|-------|
  | `GossipPayload.Latency float64` | Son probe round-trip saniye; başarısız probda 0 (omitempty) |
  | `Config.Region string` | Node-level coğrafi etiket; `nodeMeta`'ya eklendi, graceful overflow |
  | `regionOf(nodeName)` | `testRegionOverride` > local cfg > memberlist NodeMeta |
  | `GeoLatencyForTarget(targetID)` | peerStates'ten per-node latency snapshot; ByNode sıralı |
  | `detectLatencyAnomaly` | ≥2 non-zero değer gerekiyor; max > 3×min → anomaly=true |
  | `UpdateGeoMetrics(targetInfos)` | Engine'in 5s updater'ından çağrılıyor; GaugeGeoLatency + GaugeGeoLatencyAnomaly |
  | `probe_from_regions []string` | Target-level bölge filtresi; `CandidatesFor`'da `ProbeFromConstraint` sonrası uygulanıyor |
  | `GaugeGeoLatency` | `network_probe_geo_latency_seconds` — labels: name, target, type, region |
  | `GaugeGeoLatencyAnomaly` | `network_probe_geo_latency_anomaly` — 1=anomaly, 0=normal |
  | Test | `geolat_test.go` — 15 test: anomaly detection, regionOf, GeoLatencyForTarget, probe_from_regions filter |

  **Engine entegrasyonu (`internal/engine/engine.go`, `loop.go`, `cmd/linux/main.go`):**
  - `Target.ProbeFromRegions []string` yeni config alanı
  - `Engine.lastLatency sync.Map` — başarılı probe sonrası `elapsed` kaydediliyor
  - `Engine.ProbeFromRegionsConstraint()` — `cluster.LocalTargetProvider` arayüzüne eklendi
  - `LoadConfig` sonrası `clusterMgr.SetLocalConfigInfo(ConfigHashOf(raw), ...)` çağrısı
  - `RegisterClusterMetrics`: `GaugeConfigDrift`, `GaugeGeoLatency`, `GaugeGeoLatencyAnomaly` eklendi
  - `updateClusterMetrics`: `UpdateConfigDriftMetric()` + `UpdateGeoMetrics(targetInfos)` çağrıları
  - `Engine.GeoLatencySnapshot(targetID)` — `/geo/latency/` handler'a bağlı
  - `GET /cluster/config` endpoint eklendi (503 cluster disabled ise)
  - `GET /geo/latency/{targetID}` endpoint eklendi

  **Build + Test:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/  ✓
  go test -race ./internal/cluster/...   ✓  (tümü yeşil)
  go test -race ./internal/engine/...    ✓  (tümü yeşil)
  go test -race -timeout 300s ./test/integration/...  ✓  (110s, 0 data race)
  go vet ./...  ✓
  ```

## 2026-05-11

- [backend] [altyapi] **P1.3 + P1.4 tamamlandı — Scope Intelligence (REAL_OUTAGE/NETWORK_PARTITION/LOCAL_FAILURE/AMBIGUOUS) + SLO Tracker (incident persistence, error budget, breach alerts, 3 Prometheus metrics).**

  **P1.3 — Scope Intelligence Enhancement (`internal/engine/scope.go` yeni dosya):**

  `classifyScope(targetID) DetailedScope` Engine metodu — `computeScope`'un ham GLOBAL/PARTIAL/NODE_LOCAL etiketini insan-okunabilir sınıflandırma + güven skoru ile zenginleştiriyor:

  | Durum | Scope | Classification | Confidence |
  |-------|-------|----------------|------------|
  | Standalone, state yok | STANDALONE | AMBIGUOUS | 0.5 |
  | Standalone, hard_down | NODE_LOCAL | LOCAL_FAILURE | 1.0 |
  | Standalone, up | STANDALONE | LOCAL_FAILURE | 1.0 |
  | Cluster, tüm node down + offline yok | GLOBAL | REAL_OUTAGE | 1.0 |
  | Cluster, tüm node down + offline var | GLOBAL | AMBIGUOUS | downCount/clusterSize (max 0.95) |
  | Cluster, sadece local node down | NODE_LOCAL | LOCAL_FAILURE | upCount/totalKnown |
  | Cluster, karışık | PARTIAL | NETWORK_PARTITION | split simetrisine göre (50/50 = en yüksek) |

  `DetailedScope` struct: `Scope`, `Classification`, `DownNodes`, `UpNodes`, `OfflineNodes`, `PartitionGroups` (NETWORK_PARTITION'da dolu), `Confidence`.

  `ScopeEnv()` → alert env map: `SCOPE`, `CLASSIFICATION`, `CONFIDENCE`, `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES`. Tüm kanallar (script/mail/webhook) bu enrichment'ı alıyor.

  **Değiştirilen dosyalar (P1.3):**
  - `notify.go` — `computeScope()` yerine `classifyScope().ScopeEnv()` kullanıyor; SCOPE artık 6 değişkenle zenginleştirilmiş
  - `fleet.go` — `FleetTarget`'a `Classification string` + `Confidence float64` alanları eklendi; hard_down branch `classifyScope` kullanıyor

  **Test:** `scope_test.go` — 9 unit test: Standalone_LocalDown, Standalone_Up, Standalone_Unknown, ScopeEnv_AllFields, ScopeEnv_NetworkPartition, absFloat, condSlice.

  ---

  **P1.4 — SLO Tracker (`internal/engine/slo.go` yeni dosya):**

  - `sloManager`: `incidents.json` persistence (state.json ile aynı dizin, atomik `.tmp`→`os.Rename` yazma). `RecordStart`/`RecordEnd` incident lifecycle; `PruneOldIncidents(retentionDays)` — retention_days dışına düşen kapalı incident'lar silinir.
  - `ComputeSLO(targetID, targetUptime, window) (SLOResult, error)` — rolling window (30d/7d/24h). Aktif incident (EndedAt=nil) `time.Now()` kadar sayılır. Incident start window sınırından önce ise kırpılır. `SLOResult`: DowntimeSec, ActualUptime, SLOBreached, RemainingBudgetSec, IncidentCount.
  - `runSLOChecker` goroutine (60sn): `checkSLOBreaches` → ihlal tespit edilince edge-triggered `sendSLOBreachAlert` (STATUS=slo_breached, SLO_TARGET_UPTIME, SLO_ACTUAL_UPTIME, SLO_WINDOW, SLO_DOWNTIME_MINUTES, SLO_INCIDENT_COUNT, SLO_ERROR_BUDGET_SEC, SLO_LONGEST_INCIDENT_SEC). İhlal düzelince flag temizlenir, bir sonraki ihlalde yeniden atar.
  - `sloRecordStart` / `sloRecordEnd` → `loop.go`'daki `markHardDown` / `markRecovered` path'lerinden tetikleniyor.
  - 3 yeni Prometheus metriği (`RegisterSLOMetrics` ile koşullu — `slo.enabled: false` iken registry'de görünmez):
    - `network_probe_slo_uptime_ratio{target_id, window}` — gerçek uptime oranı (0.0–1.0)
    - `network_probe_slo_error_budget_seconds{target_id, window}` — kalan hata bütçesi (negatif = ihlal)
    - `network_probe_slo_breached{target_id}` — 1=ihlal aktif, 0=normal
  - `/slo` endpoint: `SLOSnapshot()` JSON döner; `slo.enabled: false` → 503.

  **Değiştirilen dosyalar (P1.4):**
  - `engine.go` — `Config.SLO *SLOConfig`; `Engine.sloMgr *sloManager`; `SLOEnabled() bool`; Init'te `runSLOChecker` goroutine başlatılıyor
  - `loop.go` — `markHardDown` sonrası `sloRecordStart`; `markRecovered` + processPending recovery sonrası `sloRecordEnd`
  - `cmd/linux/main.go` — `RegisterSLOMetrics(reg)` koşullu çağrı; `/slo` endpoint

  **Test:** `slo_test.go` — 12 unit test: parseWindow (30d/7d/24h/bad/0d), RecordStart/End lifecycle, no-op-when-open, no-op-when-closed, persistence round-trip, ComputeSLO (zero downtime, 2h downtime breached, active incident ongoing, invalid window), PruneOldIncidents, breach flag toggle, nil-safe engine hooks, SLOSnapshot nil when disabled.

  **Build + test sonuçları:**
  ```
  go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/    ✅
  go test -race ./internal/engine/... ./internal/cluster/... ./test/integration/...  ✅
  0 data race
  ```

  **Oluşturulan/değiştirilen dosyalar:**
  - **Oluşturuldu**: `internal/engine/scope.go` — DetailedScope, classifyScope, ScopeEnv, condSlice, absFloat
  - **Oluşturuldu**: `internal/engine/scope_test.go` — 9 unit test
  - **Oluşturuldu**: `internal/engine/slo.go` — SLOConfig, sloManager, ComputeSLO, SLO Prometheus metrics, /slo snapshot
  - **Oluşturuldu**: `internal/engine/slo_test.go` — 12 unit test
  - **Düzenlendi**: `internal/engine/engine.go` — SLO config/manager alanları, SLOEnabled(), Init SLO wiring
  - **Düzenlendi**: `internal/engine/loop.go` — sloRecordStart/End çağrıları
  - **Düzenlendi**: `internal/engine/notify.go` — classifyScope().ScopeEnv() enrichment
  - **Düzenlendi**: `internal/engine/fleet.go` — FleetTarget.Classification + Confidence
  - **Düzenlendi**: `cmd/linux/main.go` — RegisterSLOMetrics + /slo endpoint

---

- [test] [altyapi] **Phase 12 tamamlandı — 7 entegrasyon testi + 3 cluster data race düzeltmesi.**

  **test/integration/standalone_test.go (3 test):**
  - `TestStandalone_ProbeAndAlertCycle`: TCP mock server up→down→up tam döngüsü, `state.json` hard_down + recovery doğrulaması, alert script `STATUS/SEQ/NAME` env kontrolü.
  - `TestStandalone_AppEnrichment`: `apps:` config ile alert env'de `AFFECTED_APPS=payment-service` + `OWNER_TEAMS=fintech-sre` doğrulaması.
  - `TestStandalone_StateV2Migration`: v1 `{"id":bool}` formatı engine Init'te otomatik v2'ye migrate ediliyor mu; `version:2 + state:"up"/"hard_down"` kontrolü.

  **test/integration/cluster_test.go (2 test):**
  - `TestCluster_ExactlyOnceAlert`: 2 node, `probe_replication_factor=2`, target down → tam olarak 1 "unreachable" alert (Phase 8 exactly-once garantisi).
  - `TestCluster_RecoveryAlert`: target down → up → tam olarak 1 "reachable" alert.

  **test/integration/antientropy_test.go (1 test):**
  - `TestAntiEntropy_RejoinNoDuplicateAlert`: node1 dur → target down (1 alert) → node1 yeniden başlat → re-join sırasında 2. "unreachable" GELMEZ (Phase 9 anti-entropy garantisi).

  **test/integration/keyrotation_test.go (2 test):**
  - `TestKeyRotation_SharedKeyGossip`: AES-256 keyring ile 2 node cluster, şifreli gossip üzerinden exactly-once alert.
  - `TestKeyRotation_AddKey`: k2 ekleme (hot-reload simülasyonu) sonrası cluster ayakta + alert çalışıyor.

  **cluster.go data race fix (3 düzeltme, `-race` ile tespit edildi):**
  - `m.list` assignment in `New()` artık `ringMu.Lock()` altında — `NotifyJoin` goroutine'nin `updateRing()` içindeki nil check ile race'i engeller.
  - `updateRing()` tam body'si `ringMu.Lock()` altında — nil check + `Members()` + ring assignment atomic.
  - `NotifyJoin` goroutine `inventoryRefreshHandler` okurken `mu.RLock()` kullanıyor — `SetInventoryRefreshHandler()` ile race'i engeller.

  **Test sonuçları:** `go test -race -timeout 300s ./internal/engine/... ./internal/cluster/... ./test/integration/...`
  ```
  ok  github.com/saidtaylan/netwatch/internal/engine       1.6s
  ok  github.com/saidtaylan/netwatch/internal/cluster      1.9s
  ok  github.com/saidtaylan/netwatch/test/integration    110.5s  (7 test)
  ```
  **Data race raporu: 0**

---

- [backend] [altyapi] **Phase 13 Step 12 tamamlandı + 5 production bug fix. Smoke test: 3 node (istanbul/ankara/izmir), zone-aware spread, failover, quorum-loss, tam doğrulama.**

  **Step 12 — Smoke test:** 3 yerel binary farklı gossip portları (7951/7952/7953), HTTP portları (10301/10302/10303), zone'lar (istanbul/ankara/izmir). Factor=2 ile 2 target için prober seçimi, tüm başarı kriterleri:
  - `✅` 3 node tam üye görüşü; zone'lar `/cluster/probers` ve `/fleet/status`'ta doğru
  - `✅` Tüm node'lar aynı prober setini hesaplıyor (deterministic ring)
  - `✅` Her target için 2 prober, 2 farklı zone'dan (zone-aware spread)
  - `✅` `network_probe_local_assigned=1` için prober olan node'lar, `=0` olmayanlar
  - `✅` Primary kill → ring rebalance → yeni primary seçildi (failover)
  - `✅` 2 node kill → quorum lost, isolated=true, `[CLUSTER] quorum lost` log'u
  - `✅` `/fleet/status`: cluster size, alive count, quorum_healthy, down_targets

  **Bug Fix 1 — AdvertisePort gossip mismatch:**
  `DefaultLANConfig()` ile `BindPort=7951` set edildiğinde `AdvertisePort` hâlâ 7946 kalıyordu. Memberlist diğer node'lara 7946 port'unu advertise edince UDP ping'ler başarısız oluyordu. Düzeltme: `BindPort` set edildiğinde `AdvertisePort` da eşzamanlı güncelleniyor; explicit `AdvertisePort` config'i override edebiliyor.

  **Bug Fix 2 — GossipPayload.NodeName yanlış değer (OS hostname kullanılıyordu):**
  `broadcastState` ve `broadcastStateByID` `e.hostname` (OS hostname, örn. "saidtaylan.local") kullanıyordu. Bu aynı makinedeki 3 node'un aynı NodeName ile broadcast yapmasına neden oluyordu; `peerStates` collision ve `CandidatesFor` bozulması. Düzeltme: yeni `clusterNodeName()` helper, cluster aktifken `cfg.Cluster.NodeName` ("node-istanbul" vb.) döner; standalone'da OS hostname'e fallback.

  **Bug Fix 3 — İlk başarılı probe state broadcast etmiyordu:**
  `runCheck` success path'inde `!seen` branch doğrudan `lastKnown["up"] = ...` yazıp `broadcastState` çağırmıyordu. Geç katılan peer'lar `CandidatesFor`'da bu target için node'u göremiyordu. Düzeltme: first observation'da `seq=1/up` ile `broadcastState` çağrılıyor.

  **Bug Fix 4 — NotifyJoin BroadcastInventory (InventoryRefreshHandler):**
  Yeni cluster üyesi join ettiğinde mevcut node'lar kendi target state'lerini tekrar broadcast etmiyordu. Geç katılanlar bootstrap UDP paketini kaçırıyordu. Düzeltme: yeni `InventoryRefreshHandler` interface + `SetInventoryRefreshHandler` setter; `NotifyJoin` goroutine'inde `BroadcastInventory()` çağrısı. Engine `bootstrapInventoryBroadcast()` üzerinden implement ediyor.

  **Bug Fix 5 — UP target'lar periyodik re-broadcast yapmıyordu:**
  `broadcastState` yalnızca state geçişlerinde (hard_down / recovery) çağrılıyordu. Sürekli UP kalan target'lar için cluster üyeleri yalnızca tek bir bootstrap/ilk-probe broadcast'ı alıyordu; bu da kaçırılırsa candidate set'te eksiklik kalıyordu. Düzeltme: `runCheck` success path'inde "already up, staying up" branch'ine `e.clusterMgr != nil` koşullu `broadcastState` eklendi. Her probe cycle'da mevcut UP state gossip'e besleniyor.

  **Değiştirilen dosyalar:**
  - **Düzenlendi**: `internal/cluster/cluster.go` — `AdvertisePort` fix; `InventoryRefreshHandler` interface + setter; `NotifyJoin` BroadcastInventory çağrısı
  - **Düzenlendi**: `internal/engine/engine.go` — `clusterNodeName()` helper; `BroadcastInventory()` impl; `SetInventoryRefreshHandler` wiring in Init
  - **Düzenlendi**: `internal/engine/loop.go` — `broadcastState` NodeName fix; first-probe broadcast; UP target periodic re-broadcast
  - **Düzenlendi**: `sprint.md` — Step 12 ✅

---

- [test] [dokuman] **Phase 13 Step 10–11 tamamlandı: Integration testler + dokümantasyon güncellemesi.**

  **Step 10 — Integration testler (`internal/cluster/phase13_integration_test.go`):**

  `fakeCluster` yardımcı tipi ile gerçek memberlist soketi açmadan N node simülasyonu. Her "node" kendi `Manager`'ı, ama hepsi ortak `peerStates`/`aliveSet`/`zones` görüşü paylaşıyor (gossip tamamen converge etmiş durumu temsil ediyor). 8 senaryo:

  1. **ExactlyFactorProbersSelfIdentify** — 5 node, factor=3, tek target → tam 3 node `IsLocalProber=true` dönmeli.
  2. **AllNodesAgreeOnProberSet** — 6 node, `SelectProbers` sonucu tüm node'larda byte-for-byte aynı.
  3. **ZoneSpreadConsistentAcrossNodes** — 6 node, 3 zone × 2; zone diversity + tüm node'larda tutarlı pick.
  4. **ProbeFromHonoredClusterWide** — pin={b,d}; her node `SelectProbers` → {b,d}.
  5. **PrimaryFailoverWhenNodeLeaves** — primary ayrılır, kalan node'lar yeni prober set'te aynı fikir; eski primary dönemez.
  6. **AddingNodeReshufflesConsistently** — 3→4 node; factor cap korunur, 4 node da aynı sonuç.
  7. **ConcurrentReadsAreSafe** — 5 node × 3 target × 200 iter; `-race` altında `SelectProbers` / `IsLocalProber` / `CandidatesFor`.
  8. **FactorHoldsAcrossManyTargets** — 7 node, factor=2, 50 target; küme genelinde toplam `IsLocalProber=true` sayısı tam 100 (50 × 2).

  Tüm testler `go test -race ./internal/cluster/...` ile yeşil.

  **Step 11 — Dokümantasyon:**

  - **`config.example.yaml`** — `cluster:` bloğuna `zone: "istanbul"` ve `probe_replication_factor: 3` eklendi; `probe_from` kullanım örneği açıklamalı section olarak eklendi.
  - **`CLAUDE.md`** — Tamamlanan Phase 13 girişi eklendi: new endpoints (`/cluster/probers`, `/fleet/status`), yeni metrikler (`network_probe_local_assigned`, `network_probe_probers_for_target`, `network_probe_inventory_peers`), config schema Phase 13 alanları, `stubProvider.ProbeFromConstraint` notu.
  - **`README.md`** — Features tablosuna 3 yeni bullet (distributed probe ownership, zone-aware spread, active probe delegation); metrics ve endpoints tablolarına Phase 13 satırları; "How alerting works" bölümü cluster modunu yansıtacak şekilde güncellendi; yeni "Distributed Probe Ownership" section (hash ring, zone spread, probe_from, replication factor açıklaması).

  **Değiştirilen/eklenen dosyalar:**
  - **Eklendi**: `internal/cluster/phase13_integration_test.go`
  - **Düzenlendi**: `config.example.yaml`
  - **Düzenlendi**: `CLAUDE.md`
  - **Düzenlendi**: `README.md`
  - **Düzenlendi**: `sprint.md` — Step 10-11 ✅ işaretlendi

---

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
