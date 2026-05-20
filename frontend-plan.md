# netwatch UI — Mimari ve İnşa Planı

> **Durum:** Plan. Henüz hiçbir kod yazılmadı. Bu dosya, inşa başlatıldığında adım adım takip edilecek referans.
> **Tarih:** 2026-05-20
> **Karar verici:** saidimtaylan@gmail.com — verdiği yanıtlardan türetildi (sohbet geçmişi 2026-05-20)

---

## 1. Karar Özeti

| Konu | Karar | Gerekçe |
|---|---|---|
| **Framework** | Nuxt 3 (Vue 3 + Vite) | Kullanıcı tercihi; SSR opsiyonel, çoğunlukla SPA modda çalışacak |
| **Styling** | Tailwind CSS + `@nuxtjs/tailwindcss` modülü | Hızlı UI iterasyonu, küçük bundle |
| **State** | Pinia (Nuxt resmi store) | Composables-friendly, TypeScript first-class |
| **API client** | `$fetch` (Nuxt built-in, ofetch tabanlı) + thin wrapper | Ekstra bağımlılık yok, SSR-safe |
| **Auth** | Kibana benzeri ilk-setup → admin bearer token → localStorage. İleride user register sayfaları (backend gerektirir) | Şu an `admin.token` mevcut, sıfırdan auth servisi inşa etmeyeceğiz |
| **Node bağlantı modeli** | Bir veya birden fazla node URL'si girilir; UI **healthcheck race** yapar (en hızlı cevaplayan kazanır), seçileni session boyunca tutar, fail olursa otomatik failover | Kullanıcının tercihi (`/cluster/state` zaten cluster üyelerini döner — UI o listeyi de cache'leyebilir) |
| **Deploy modeli** | **Standalone Nuxt servisi** (front ve back **iki ayrı servis**). Aynı `systemd` ile birlikte başlatılabilir. Farklı subdomain destekli (CORS) | Kullanıcı tercihi; embedded yapsaydık SSR/route customization kaybedilirdi |
| **Repo yapısı** | Tek git reposu içinde `backend/` (mevcut Go kodu) + `frontend/` (Nuxt). Paylaşılan: `developments.md`, `system_map.md`, `sprint.md`, `todo.md`, `CLAUDE.md` | Kullanıcı tercihi |
| **TypeScript** | Strict mode | Backend zaten JSON ile dönüyor; tip güvenliği büyük kazanım |
| **Test** | Vitest (unit) + Playwright (e2e, opsiyonel sonraki sprint) | Nuxt resmi destekli |

---

## 2. Best-Practice Notu — Systemd ile Ortak Başlatma

Kullanıcı "aynı systemd ile başlatılabilir mi?" diye sordu. **En sağlıklı yaklaşım:**

```
netwatch-backend.service     ← mevcut, /usr/local/bin/netwatch (port 10240)
netwatch-frontend.service    ← yeni, node /opt/netwatch-ui/.output/server/index.mjs (port 3000)
netwatch.target              ← her ikisini Wants= ile bağlar
```

Tek `systemctl start netwatch.target` her ikisini başlatır, `systemctl stop netwatch.target` ikisini de durdurur. Backend down olduğunda frontend ayrı kalır (UI "backend unreachable" göstermeli, kendi süreci ölmemeli) — bu yüzden **tek servis altında değil, iki ayrı servis + target wrapper**.

Geliştirme ortamında: `make dev-frontend` (Nuxt dev server) + `make dev-backend` (Go binary). Production'da systemd target.

---

## 3. Repo Reorganizasyonu (UI başlamadan önce yapılacak ilk iş)

### 3.1 Hedef Yapı

```
network cluster/
├─ backend/                  ← MEVCUT Go kodu buraya taşınacak
│  ├─ cmd/
│  │  ├─ linux/main.go
│  │  └─ windows/main.go
│  ├─ internal/
│  │  ├─ engine/
│  │  └─ cluster/
│  ├─ test/
│  ├─ tests/
│  ├─ deploy/
│  ├─ helm/
│  ├─ demo/
│  ├─ notifications/
│  ├─ Dockerfile
│  ├─ Makefile
│  ├─ go.mod
│  ├─ go.sum
│  ├─ config.example.yaml
│  └─ config.yaml
│
├─ frontend/                 ← YENİ
│  ├─ app.vue
│  ├─ nuxt.config.ts
│  ├─ tailwind.config.ts
│  ├─ package.json
│  ├─ tsconfig.json
│  ├─ pages/
│  ├─ components/
│  ├─ composables/
│  ├─ stores/
│  ├─ layouts/
│  ├─ middleware/
│  ├─ plugins/
│  ├─ assets/
│  ├─ public/
│  ├─ types/
│  └─ utils/
│
├─ deploy-systemd/           ← İKİSİNİ DE KAPSAYAN deploy artifacts (target dosyası buraya)
│  ├─ netwatch.target
│  ├─ netwatch-backend.service   ← backend/deploy/netwatch.service'den taşınır
│  └─ netwatch-frontend.service
│
├─ developments.md           ← Paylaşılan changelog (backend + frontend girdileri tek dosyada, tarih-bazlı)
├─ system_map.md             ← Hem backend hem frontend mimarisi
├─ sprint.md                 ← Aktif sprint (UI sprint'i şuradan açılacak)
├─ todo.md                   ← Backlog (B1-B11)
├─ CLAUDE.md                 ← Hem backend hem frontend kuralları
├─ README.md                 ← Top-level: "iki servis, nasıl çalıştırılır"
└─ .gitignore                ← node_modules, .nuxt, .output, dist, bin/, *.exe eklenmiş
```

### 3.2 Refactor Adımları (sırayla)

1. **Backend taşıma**
   - `cmd/`, `internal/`, `test/`, `tests/`, `deploy/`, `helm/`, `demo/`, `notifications/`, `Dockerfile`, `Makefile`, `go.mod`, `go.sum`, `config.example.yaml`, `config.yaml`, `credentials.env` → `backend/` altına `git mv`
   - Module path **değişmez** (`github.com/saidtaylan/netwatch`) — import path'leri etkilenmez çünkü `go.mod` taşındı, paket path'leri korundu
   - `Makefile` içindeki yollar relative olduğu için (`./internal/...`) düzeltme gerekmez; `cd backend && make build-linux` çalışır
   - `bin/`, `netwatch`, `linux` binary çıktıları silinir (`.gitignore` zaten içeriyor)
   - `Dockerfile`: `WORKDIR /app` → backend kodu COPY edildiğinden zaten doğru çalışır; path'leri kontrol et

2. **CLAUDE.md güncellemesi**
   - Build komutları `cd backend && go build ...` olur
   - Paket yapısı bölümünde `backend/internal/engine/`, `backend/internal/cluster/` denir
   - Frontend bölümü eklenir (alt başlık)

3. **Top-level README.md**
   - "Bu repo iki servis içerir: backend (Go) ve frontend (Nuxt)" girişi
   - Her birinin nasıl çalıştırılacağı kısa kısa, detay alt-README'lerde

4. **`.gitignore` ekleme**
   ```
   # Frontend
   frontend/node_modules/
   frontend/.nuxt/
   frontend/.output/
   frontend/dist/
   frontend/.env
   frontend/*.log
   ```

5. **Smoke test**
   - `cd backend && go build ./internal/engine/ ./internal/cluster/ ./cmd/linux/` — geçer
   - `cd backend && go test -race ./internal/engine/... ./internal/cluster/...` — yeşil
   - Binary'yi `cd backend && ./netwatch -config config.yaml` ile çalıştır → `/metrics` döner

6. **Commit** — "refactor: backend dizinine taşı, frontend için iskelet hazırla"

> Bu adım UI implementasyonu **başlamadan önce** ayrı bir PR/commit olarak yapılır. Karışıklığı önler.

---

## 4. Frontend Mimarisi

### 4.1 Klasör Yapısı (Nuxt 3 konvansiyonu)

```
frontend/
├─ nuxt.config.ts            # ssr: false (SPA mode), runtimeConfig (default backend URL env'den)
├─ app.vue                   # NuxtLayout + NuxtPage
├─ tailwind.config.ts
├─ tsconfig.json
├─ package.json
│
├─ pages/                    # File-based routing
│  ├─ index.vue              # Cluster overview (landing)
│  ├─ setup.vue              # İlk giriş: backend URL + admin token formu
│  ├─ login.vue              # Token gir (registered user gelecekte)
│  ├─ targets/
│  │  ├─ index.vue           # Target list
│  │  └─ [id].vue            # Target detail
│  ├─ topology.vue           # Dependency graph
│  ├─ slo.vue                # SLO dashboard
│  ├─ apps.vue               # Apps & affected services
│  ├─ alerts.vue             # Alert history (B7 öncesi: in-memory recent feed)
│  ├─ maintenance.vue        # Maintenance windows CRUD
│  ├─ silences.vue           # B1 — placeholder route (backend yokken disabled)
│  ├─ config/
│  │  ├─ index.vue           # Cluster config view + drift
│  │  ├─ push.vue            # PUT /cluster/config form
│  │  └─ keyring.vue         # Keyring rotate
│  ├─ geo.vue                # Per-region latency view
│  └─ settings/
│     ├─ index.vue           # Local UI prefs (theme, polling interval)
│     └─ nodes.vue           # Backend node list management
│
├─ layouts/
│  ├─ default.vue            # Sidebar + topbar
│  └─ auth.vue               # Setup/login için minimal layout
│
├─ components/
│  ├─ common/
│  │  ├─ AppShell.vue
│  │  ├─ Sidebar.vue
│  │  ├─ TopBar.vue
│  │  ├─ ConnectionStatus.vue   # Üst bara: hangi node'a bağlı + healthy/failover göstergesi
│  │  ├─ ErrorBanner.vue
│  │  ├─ DataTable.vue          # Generic sortable/filterable table
│  │  ├─ StatusBadge.vue        # UP/DOWN/SOFT_DOWN/SOFT_UP renkli
│  │  ├─ SeverityBadge.vue      # B2 için hazır (critical/warning/info)
│  │  ├─ ConfirmDialog.vue
│  │  ├─ Toast.vue
│  │  └─ EmptyState.vue
│  ├─ cluster/
│  │  ├─ NodeCard.vue
│  │  ├─ QuorumIndicator.vue
│  │  └─ ConfigDriftCard.vue
│  ├─ targets/
│  │  ├─ TargetRow.vue
│  │  ├─ TargetDetailHeader.vue
│  │  ├─ ByNodeBreakdown.vue
│  │  ├─ ScopeClassificationCard.vue
│  │  ├─ DependencyChip.vue
│  │  └─ ProbeAssignmentList.vue
│  ├─ topology/
│  │  └─ GraphCanvas.vue        # vue-flow veya d3 — TBD; ilk MVP'de basit ağaç gösterimi
│  ├─ slo/
│  │  ├─ SLOCard.vue
│  │  └─ IncidentTable.vue
│  ├─ maintenance/
│  │  ├─ MaintenanceForm.vue
│  │  └─ MaintenanceList.vue
│  └─ config/
│     ├─ SharedConfigForm.vue
│     └─ KeyringPanel.vue
│
├─ composables/
│  ├─ useApi.ts                 # $fetch wrapper, token injection, error handling
│  ├─ useAuth.ts                # Token yönetimi, localStorage, logout
│  ├─ useNodeConnection.ts      # Multi-node race + failover (detay §4.4)
│  ├─ usePolling.ts             # Generic poll-on-interval composable, visibilitychange aware
│  ├─ useTargets.ts             # /fleet/status reactive
│  ├─ useCluster.ts             # /cluster/state, /cluster/config reactive
│  ├─ useTopology.ts            # /topology reactive
│  ├─ useSLO.ts                 # /slo reactive
│  ├─ useMaintenance.ts         # /cluster/maintenance CRUD
│  ├─ useGeoLatency.ts          # /geo/latency/{id} reactive
│  └─ useToast.ts
│
├─ stores/                       # Pinia
│  ├─ auth.ts                   # token, currentUser (gelecek)
│  ├─ nodes.ts                  # backend node URL listesi + aktif node + health durumu
│  ├─ ui.ts                     # theme, sidebar collapsed, polling interval
│  └─ alerts.ts                 # in-memory recent alert feed (B7'ye kadar buradan beslenir)
│
├─ middleware/
│  ├─ auth.global.ts            # Token yoksa /setup veya /login'e yönlendir
│  └─ node-health.global.ts     # Aktif node'a healthcheck, başarısızsa failover tetikle
│
├─ plugins/
│  ├─ api.ts                    # $api global'i inject et
│  └─ error-handler.ts          # Global hata yakalayıcı
│
├─ utils/
│  ├─ format.ts                 # duration, bytes, percent formatters
│  ├─ classifyState.ts          # state → color/icon eşlemesi
│  └─ matchers.ts               # B1 silence matcher önizleme (label selector parser, frontend-only)
│
├─ types/
│  ├─ api.ts                    # Backend response tipleri (FleetSnapshot, ClusterState, TopologySnapshot, …)
│  └─ ui.ts                     # UI-specific tipler
│
└─ assets/
   └─ css/main.css              # Tailwind directives
```

### 4.2 Sayfalar — Her sayfa için endpoint eşlemesi

> Backend'in **mevcut** endpoint'leri zaten zengin. Frontend yalnızca consume edecek. B1-B11 için **route stub'ları** açıyoruz ki gelecekte backend hazır olduğunda sadece composable + component eklemek yeter.

| Route | Yaptığı iş | Kullandığı API'ler | Yazma yetkisi | Notlar |
|---|---|---|---|---|
| `/setup` | İlk giriş: backend URL + admin token gir, healthcheck yap, başarılıysa localStorage'a yaz | `GET /health`, `GET /cluster/state` | — | Token yoksa global middleware buraya atar |
| `/login` | Token re-entry (logout sonrası) | aynı | — | Gelecekte user/pass formuna evrilir |
| `/` | **Cluster overview** | `GET /cluster/state`, `GET /cluster/config`, `GET /fleet/status` (summary) | — | Quorum, isolated, cluster size, config drift, target sayısı, son alarmlar |
| `/targets` | Target list | `GET /fleet/status` | — | Filtre: status, scope, classification, app, search |
| `/targets/[id]` | Target detail | `GET /fleet/status`, `GET /topology`, `GET /geo/latency/{id}`, `GET /cluster/probers` | — | by-node breakdown, classification, confidence, dependencies, prober set, geo latency |
| `/topology` | Dependency graph | `GET /topology` | — | İlk MVP: tablo + transitive impact list; v2'de görsel graph |
| `/apps` | Apps & teams | `/fleet/status` içinden derive (apps map) | — | App → target hangi durumda |
| `/slo` | SLO dashboard | `GET /slo` | — | Per-target uptime/budget/incidents |
| `/alerts` | Alert feed | yok (in-memory) | — | Backend B7 hazır olunca `GET /alerts` ile değişir |
| `/maintenance` | Maintenance CRUD | `GET /cluster/maintenance`, `PUT /cluster/maintenance`, `DELETE /cluster/maintenance/{id}` | ✅ | Target ID + duration + reason formu |
| `/silences` | B1 — placeholder | — (disabled badge) | (gelecek) | Route var, "yakında" göster |
| `/config` | Config view + drift | `GET /cluster/config` | — | Drift varsa peer hash listesi |
| `/config/push` | Shared config push | `PUT /cluster/config` | ✅ | Form: timeout, max_retries, probe_interval_sec, notifications JSON, vs. |
| `/config/keyring` | Keyring rotate | `GET /cluster/keyring/rotate`, `POST /cluster/keyring/rotate` | ✅ | Add/Use/Remove key |
| `/geo` | Per-region latency | `GET /geo/latency/{id}` (each target) | — | Tablo + anomaly highlight |
| `/settings/nodes` | Backend node listesi yönetimi | — (localStorage) | ✅ (local) | Yeni node ekle/sil, health durumu |
| `/settings` | Tema, polling interval | — (localStorage) | ✅ (local) | UI prefs |
| `/cluster/leave` action | Confirmable button (cluster sayfasında) | `POST /cluster/leave` | ✅ | Sadece "advanced" mode'da görünür |

### 4.3 Auth Akışı

1. İlk açılış → `useAuth().token` yoksa `auth.global.ts` middleware `/setup`'a yönlendirir
2. `/setup`'ta:
   - Backend URL (örn. `http://localhost:10240`)
   - Admin token
   - "Connect" butonu → `GET /health` (token zorunlu değil, sadece reachability) **+** token ile herhangi bir write-protected endpoint'e probe (örn. `GET /cluster/config` — admin token gerekiyorsa; gerekmiyorsa `OPTIONS` veya UI-only check)
   - **Daha temiz yaklaşım:** Backend'e `GET /auth/whoami` endpoint'i ekle (yeni, hafif) — token doğruysa 200, yoksa 401. UI bunu kullanır
3. Başarılı → token + node URL Pinia'ya + localStorage'a yazılır → `/` (cluster overview)
4. Logout → token sil → `/login`
5. Her API çağrısında: `Authorization: Bearer <token>` header'ı `useApi.ts` tarafından eklenir
6. 401 alındığında → otomatik logout + toast: "Session expired, please login"

**Gelecek (B7+):**
- `POST /auth/register` (yeni backend feature gelecek)
- `POST /auth/login` (username/password → token döner)
- Role-based access (admin/observer/operator) — UI buton görünürlüğünü role'a göre ayarlar

### 4.4 Multi-Node Connection + Failover

Kullanıcının isteği: kullanıcı birden fazla node URL'si girebilir, UI en hızlı cevap vereni seçer; aktif node down olursa otomatik failover.

**`useNodeConnection.ts` algoritması:**

```typescript
// stores/nodes.ts
state: {
  configured: string[]        // ["http://a:10240", "http://b:10240"]
  active: string | null
  health: Record<string, "healthy" | "unhealthy" | "unknown">
  lastSwitchAt: number
}

// composables/useNodeConnection.ts
async function selectActiveNode(): Promise<string> {
  const promises = configured.map(url =>
    $fetch(`${url}/health`, { timeout: 2000 })
      .then(() => ({ url, ok: true }))
      .catch(() => ({ url, ok: false }))
  )
  // İlk başarılı yanıt kazanır (Promise.race + filter)
  const winner = await Promise.any(
    promises.map(p => p.then(r => r.ok ? r.url : Promise.reject()))
  )
  store.setActive(winner)
  return winner
}

// Her API çağrısı öncesi: active yoksa selectActiveNode() çağrılır
// API çağrısı sırasında network error → store.markUnhealthy(active) + selectActiveNode() + retry (1 kez)
```

**`/cluster/state` discovery:**
- Aktif node'a bağlandıktan sonra `GET /cluster/state` ile cluster üyelerinin advertise_addr'ini öğren
- Kullanıcı isterse "Add discovered nodes" butonu ile listeye eklenir
- Otomatik eklemeyiz — kullanıcı bilinçli olmalı (subdomain/port farklı olabilir)

**Sayfa yenileme:**
- Pinia + localStorage persistence (`pinia-plugin-persistedstate`) → aktif node kalıcı
- Sayfa yenilenince ilk `useApi` çağrısı önce health check yapar, başarısızsa failover
- Kullanıcı tercihini doğrudan kabul ettim: "her sayfa yenilenince bu olacağı için seçim işleminden feragat etmen gerekiyorsa et" → **gerek yok**, cache'lenmiş aktif node ilk denenir, hızlı

### 4.5 State Management Stratejisi

| Veri | Nerede | Yenileme | Persistence |
|---|---|---|---|
| Auth token | Pinia `auth` + localStorage | manual | ✅ |
| Backend node listesi | Pinia `nodes` + localStorage | manual | ✅ |
| Cluster state | Pinia `cluster` cache, composable `useCluster` | polling 5s | ❌ |
| Fleet status | Pinia `fleet` cache, composable `useFleet` | polling 5s | ❌ |
| Topology | Pinia `topology` cache | polling 30s (değişmez) | ❌ |
| SLO | Pinia `slo` | polling 60s | ❌ |
| Geo latency | Per-target on-demand | polling 10s (detail sayfası açıkken) | ❌ |
| Maintenance | Pinia `maintenance` | polling 15s + write sonrası invalidate | ❌ |
| UI prefs | Pinia `ui` + localStorage | — | ✅ |

**Polling stratejisi:**
- `usePolling(fetcher, intervalMs)` — sayfa visible iken poll, hidden olunca durur (`visibilitychange`)
- Polling interval kullanıcı `/settings`'ten değiştirebilir (default 5s, min 1s, max 60s)
- WebSocket/SSE şu an yok — backend'de henüz endpoint yok. Gelecekte `GET /events` (SSE) eklenirse polling yerine geçer

### 4.6 Hata İşleme

- Global plugin (`error-handler.ts`): `$fetch` 4xx/5xx → toast + ilgili composable'da `error` ref'i set
- Network error → ConnectionStatus banner kırmızıya döner, failover tetiklenir
- 401 → otomatik logout
- 403 → toast: "You don't have permission" (B7 role-aware için hazır)
- 5xx → toast: "Backend error: {message}", retry butonu
- Form validation: VeeValidate **gerekmez** — basit form'lar için manuel; karmaşık olursa eklenir

---

## 5. 11 Backlog Feature için UI'da Yer Tutma

UI'ı şimdi inşa ederken, B1-B11 backend hazır olduğunda **sıfırdan refactor olmasın** diye yapılacaklar:

| Feature | UI'da şimdi ne yapılmalı |
|---|---|
| B1 Silence Rules | `/silences` route'u eklensin, "Coming soon" placeholder. Label matcher parser util'i (`utils/matchers.ts`) hazır olsun — gelecekte hem silence hem alert filtreleme için kullanılır |
| B2 Severity Levels | `SeverityBadge.vue` componenti ŞİMDİ yapılsın (critical=red, warning=yellow, info=blue). Backend payload'unda `severity` yokken default "info" gösterir. Backend gelince zaten doluyor olacak |
| B3 Latency-Based Alerting | Target detail sayfasında latency grafiği için **alan ayrılsın** (`LatencyChart.vue` boş şimdilik). B3 gelince threshold çizgisi ekleyeceğiz |
| B4 Recurring Maintenance | `/maintenance` formunda **"Recurrence"** field'i ileride eklenecek — şu an "One-time" hard-coded. Layout esnek bırak |
| B5 Alert Ack/Mute | `alerts.vue`'da her satıra `actions` sütunu (ack/mute butonları disabled). Tablo schema'sı genişleyebilir olsun |
| B6 gRPC Health Check Probe | Target oluşturma formu yok (UI'dan target eklenmiyor şu an, config'den geliyor). Eklenirse `type` dropdown'una `grpc` opsiyonu eklemek yeter |
| B7 Audit Log | `/audit` route'u rezerve edilsin (placeholder). Layout sidebar'da slot var |
| B8 Synthetic Transaction Probe | `type: synthetic` — B6 ile aynı, dropdown opsiyonu |
| B9 JSONPath Body Assertion | HTTP target options editor'ünde "Body assertion" alanı hazır olsun (placeholder textarea) |
| B10 Grafana Dashboard | UI ilgilenmez — sadece `/settings` sayfasında "Open in Grafana" linki için config alanı |
| B11 PagerDuty/OpsGenie | Notification channel form'unda dropdown'a entry'ler eklenir gelecekte. Form esnek olsun |

**Hiçbiri için backend bağlılığı yaratmıyoruz** — UI tarafında route + boş bileşen + placeholder. Backend hazır olunca `composables/use<Feature>.ts` ekleyip bileşeni doldururuz.

---

## 6. Inşa Sırası (Sprint Planı)

> Her madde test edilebilir bir aşama. Sırayla yapılır, her aşama sonrası smoke test + commit.

### Sprint 0 — Repo Reorganizasyonu (§3)
- Backend'i `backend/` altına taşı
- `.gitignore` + top-level README + CLAUDE.md güncelle
- Smoke test: backend hâlâ çalışıyor

### Sprint 1 — Frontend İskelet
- `frontend/` Nuxt 3 init (`npx nuxi@latest init frontend`)
- Tailwind, Pinia, ofetch ayarları
- `app.vue`, `layouts/default.vue`, `layouts/auth.vue`
- Sidebar + topbar + ConnectionStatus (boş)
- `nuxt.config.ts` runtimeConfig (default backend URL)
- `package.json` scripts: `dev`, `build`, `preview`, `lint`
- **Smoke:** `pnpm dev` → boş app açılır

### Sprint 2 — Auth + Node Connection
- `pages/setup.vue` form
- `composables/useAuth.ts`, `useApi.ts`, `useNodeConnection.ts`
- `stores/auth.ts`, `stores/nodes.ts` (persisted)
- `middleware/auth.global.ts`, `middleware/node-health.global.ts`
- Backend'e `GET /auth/whoami` endpoint'i ekleme **kararı** (Sprint 2'nin parçası — minor backend değişikliği, tek dosya)
- **Smoke:** Setup formu → token ile cluster'a bağlan → `/` boş cluster overview açılır

### Sprint 3 — Cluster Overview + Targets List
- `pages/index.vue` (cluster overview)
- `pages/targets/index.vue`
- `composables/useCluster.ts`, `useFleet.ts`
- `components/cluster/*`, `components/targets/TargetRow.vue`
- `components/common/StatusBadge.vue`, `SeverityBadge.vue`
- Polling 5s
- **Smoke:** Çalışan backend'de target listesi görünür, status renkleri doğru

### Sprint 4 — Target Detail + Topology
- `pages/targets/[id].vue` (by-node breakdown, scope, classification, deps)
- `pages/topology.vue` (ilk MVP: tablo + transitive impact)
- `components/targets/ByNodeBreakdown.vue`, `ScopeClassificationCard.vue`, `DependencyChip.vue`
- **Smoke:** Bir target'a tıkla → detay açılır, deps listesi gözükür

### Sprint 5 — SLO + Apps + Geo
- `pages/slo.vue`, `pages/apps.vue`, `pages/geo.vue`
- Composables + components
- **Smoke:** SLO breach senaryosu test edilir

### Sprint 6 — Maintenance (yazma yetkisi olan ilk sayfa)
- `pages/maintenance.vue` — list + form + delete
- `composables/useMaintenance.ts`
- `components/maintenance/*`
- **Smoke:** UI'dan maintenance window aç → backend gossip'le tüm node'lara yayar → alarm bastırılır

### Sprint 7 — Config Management
- `pages/config/index.vue` (view + drift)
- `pages/config/push.vue` (PUT /cluster/config form)
- `pages/config/keyring.vue` (rotate)
- **Smoke:** Shared config field değiştir → tüm node'larda effect görülür

### Sprint 8 — Alerts feed (placeholder) + Settings + Polish
- `pages/alerts.vue` (in-memory feed, son 100 alert)
- `pages/settings/*`
- B1-B11 placeholder route'ları (disabled badge'li)
- Theme switcher, polling interval ayarı
- **Smoke:** Tam tur — setup, browse, ack, logout

### Sprint 9 — Production-Hardening
- Error boundaries, loading states, empty states, retry logic
- a11y kontrolü (focus, keyboard nav, ARIA)
- `pnpm build` → `.output/` deploy
- `deploy-systemd/netwatch-frontend.service` + `netwatch.target`
- README "Production deploy"
- **Smoke:** systemd ile başlat → her iki servis healthy

### Sprint 10 — Tests
- Vitest unit tests (utils, composables)
- Playwright e2e (login, targets list, maintenance create)
- CI gate (frontend-test target Makefile'a eklenir)

---

## 7. Backend Tarafından Eklenmesi Gerekenler (Minik)

UI'ı temiz inşa edebilmek için backend'e şu küçük eklemeler yararlı (kritik değil, opsiyonel):

1. **`GET /auth/whoami`** (Sprint 2) — token doğru mu kontrolü; admin token varsa `{"role":"admin"}`, yoksa 401. ~20 satır kod.
2. **CORS desteği** — Frontend ayrı port/subdomain'de olacak. `cmd/linux/main.go`'a `Access-Control-Allow-Origin` header (config'den okur, default `*` veya allowlist). ~30 satır.
3. **`GET /version`** — Backend version + build time. UI footer'da gösterir. Trivial.

Bunlar UI başlamadan **önce** veya Sprint 2 sırasında eklenebilir. Repo reorganizasyonu commit'ine dahil edilmesi temiz olur.

---

## 8. Açık Kararlar (UI inşa başlarken netleşecek)

| Konu | Şu anki kararım | Esneklik |
|---|---|---|
| Component library | Tailwind + custom components | Headless UI (Nuxt) eklenebilir; ilk MVP'de manuel |
| Graph library (topology) | İlk MVP'de yok (tablo). v2'de **vue-flow** | TBD; D3 alternatif ama daha kompleks |
| Chart library (latency) | **Chart.js + vue-chartjs** (basit, küçük) | ECharts alternatif (daha güçlü) |
| Form library | Manuel (basit form'lar) | VeeValidate karmaşıklaşırsa |
| Date/time | `date-fns` (small bundle) | Day.js alternatif |
| Icons | Heroicons (Tailwind ekosistemi) | — |
| Dark mode | `@nuxtjs/color-mode` modülü ile | — |

---

## 9. Best-Practice Özet

1. **SSR'ı kapat** (`ssr: false`) — admin UI, SEO gereksiz. Build daha hızlı, deploy daha basit.
2. **Pinia + persistence** — token + node listesi kalıcı, geri kalan veri runtime.
3. **Composable + Pinia ayrımı** — composable HTTP'i yapar, store cache + UI state'i tutar.
4. **Tip güvenliği** — `types/api.ts` backend response'ların TS karşılıkları. Manuel yazılır (backend Go struct'larından elle). v2'de openapi spec → gen ile otomatik.
5. **Optimistic UI** — Maintenance create/cancel için optimistic update + rollback on error.
6. **Polling pause on hidden tab** — `visibilitychange` event ile bandwidth tasarrufu.
7. **No global axios** — `$fetch` (ofetch) zaten Nuxt içinde, SSR-safe.
8. **Env config** — `runtimeConfig.public.defaultBackendUrl` (env'den), build-time leak yok.

---

## 10. Inşa Başlangıç Komutu

Hazır olduğunda, bu komut zinciri ile başlanacak:

```bash
cd "/Users/saidtaylan/Documents/network cluster"
mkdir backend
git mv cmd internal test tests deploy helm demo notifications backend/
git mv Dockerfile Makefile go.mod go.sum config.example.yaml config.yaml credentials.env backend/
# .gitignore update + top-level README
git commit -m "refactor: backend/ dizinine taşı, frontend için yer aç"

cd "/Users/saidtaylan/Documents/network cluster"
npx nuxi@latest init frontend
cd frontend
pnpm add -D @nuxtjs/tailwindcss @pinia/nuxt pinia-plugin-persistedstate @nuxtjs/color-mode
pnpm add date-fns @heroicons/vue chart.js vue-chartjs
# nuxt.config.ts, tailwind config, klasör yapısı
git add frontend/
git commit -m "feat(frontend): Nuxt 3 iskeleti"
```

---

## 11. Dosyaya Eklenmemiş Notlar

- **Aynı `developments.md`, `sprint.md`, `system_map.md`** kullanılacak (kullanıcı tercihi). Frontend girdileri tarih bazlı ekleniyor; başlığa `(frontend)` veya `(backend)` etiketi konursa filtre kolaylaşır.
- **`CLAUDE.md`** — UI inşası başladığında "Frontend" bölümü eklenir; build komutları, klasör yapısı, kararlar.
- **`todo.md`** — Backend backlog (B1-B11). UI bittikten sonra döneceğiz.
- **`sprint.md`** — Şu anki sprint UI sprint'i olacak; F1-F4 tamam, yeni sprint açılır.

---

## Sonuç

Plan tam. Onay verirseniz **Sprint 0 — Repo Reorganizasyonu** ile başlarım. Tek commit, backend hâlâ çalışır, ardından `frontend/` boş klasör hazır. Sonra Sprint 1 ile Nuxt iskeletini kurarız.

Dokümantasyon, mimari kararlar, sprint sırası ve tüm endpoint eşlemeleri burada. İnşa sırasında bu dosyaya dönüp adım adım takip edilecek.
