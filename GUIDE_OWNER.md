# netwatch — Owner/Developer Reference Guide

> Hızlı referans. Her bölüm "neden böyle çalışıyor" ile başlar — API dokümantasyonu, mimari kararlar ve operasyonel ipuçları.

---

## İçindekiler

1. [Mimari Özet](#mimari-özet)
2. [State Machine — Nasıl Çalışır](#state-machine)
3. [Scope & Classification — Karar Algoritması](#scope--classification)
4. [Topology — Root Cause Nasıl Bulunur](#topology--root-cause)
5. [SLO Tracker](#slo-tracker)
6. [Alert Feed — Neden Client-Side](#alert-feed)
7. [Geo Latency — Ne İşe Yarar](#geo-latency)
8. [Probe Timing — Son Probe'u Nasıl Görürsün](#probe-timing)
9. [API Quick Reference](#api-quick-reference)
10. [Demo Cluster Kurma ve Test Senaryoları](#demo-cluster)
11. [Planlanan Backend Geliştirmeleri](#planlanan-geliştirmeler)

---

## Mimari Özet

```
┌───────────────────────────────────────────────────────────────┐
│  Browser (Nuxt 4 SPA, port 3000)                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐│
│  │ useFleet     │  │ useCluster   │  │ useMaintenance       ││
│  │ poll 5s      │  │ poll 5s      │  │ poll 15s             ││
│  └──────┬───────┘  └──────┬───────┘  └──────────┬───────────┘│
│         │ /fleet/status   │ /cluster/state       │            │
└─────────┼─────────────────┼──────────────────────┼────────────┘
          │                 │                       │
          ▼                 ▼                       ▼
┌─────────────────────────────────────────────────────────────┐
│  Backend Node (Go, port 10240)                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  HTTP Mux — cmd/linux/main.go                        │   │
│  │  /fleet/status  /cluster/state  /cluster/maintenance │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌────────────────────┐  ┌──────────────────────────────┐   │
│  │ Engine (loop.go)   │  │ Cluster (cluster.go)         │   │
│  │ probe goroutines   │  │ memberlist gossip UDP/TCP    │   │
│  │ state machine      │  │ hash ring, quorum, anti-ent  │   │
│  └────────────────────┘  └──────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
          │ gossip (7946/udp+tcp)
          ▼
┌─────────────────────────────────┐
│  Other Backend Nodes            │
│  node-2 (10242)  node-3 (10243) │
└─────────────────────────────────┘
```

**Neden SPA (ssr: false)?** Admin UI, SEO gerektirmiyor. SPA daha basit deploy, pinia/localStorage persistence sorunsuz çalışıyor, CORS kuralları net.

**Neden polling, SSE/WebSocket değil?** Backend'de event stream endpoint yok (B7'ye kadar). 5s polling yeterli — Prometheus da scrape'de benzer aralık kullanır. SSE gelince `usePolling` → `useEventSource` geçişi tek composable değişikliği.

---

## State Machine

```
            probe fails
   UP ──────────────────► SOFT_DOWN  ────max_retries doldu──► HARD_DOWN
    ▲                         │                                   │
    │                         │ probe succeeds (recovery_probes?) │
    │                         │                                   │
    └──── SOFT_UP ◄───────────┴───────────────────────────────────┘
               │  recovery_probes ardışık başarı
               └──────────────────────────────────► UP
```

**Kritik timing parametreleri:**

| Parametre | Varsayılan | Ne Yapar |
|---|---|---|
| `probe_interval_sec` | 60 | Her target'ı kaç saniyede bir probe eder |
| `retry_interval_sec` | 30 | Probe fail'den sonra retry'a kadar bekleme |
| `max_retries` | 1 | Kaç fail → HARD_DOWN (1 = ilk fail'de) |
| `ticker_interval_sec` | 5 | Ana loop tick sıklığı — minimum probe granülaritesi |
| `recovery_probes` | 1 | Kaç ardışık başarı → UP (1 = ilk başarıda) |

**Seq numarası:** Her HARD_DOWN ve her UP geçişinde artar. Gossip'te iki node aynı target için çelişkili state söylerse yüksek seq kazanır. Seq == 0 → "henüz hiç state geçişi olmadı" (yeni başladı veya hep UP).

**State.json v2 formatı:**
```json
{
  "version": 2,
  "targets": {
    "db-primary": {
      "state": "hard_down",
      "seq": 3,
      "error_code": "dial tcp: connection refused",
      "owner_node": "node-1"
    }
  }
}
```
Node restart → state.json'dan yükle → anti-entropy ile cluster'la kıyasla → gereksiz alarm üretme.

---

## Scope & Classification

### Scope — Kaç Node Aynı Fikirde?

```
Tüm N node'un vote'u toplanır:

  up_votes   = byNode'da state=up olan node sayısı
  down_votes = byNode'da state=hard_down olan node sayısı
  total      = up_votes + down_votes

  GLOBAL     → down_votes == total && offline_nodes == 0
  NODE_LOCAL → down_votes == 1 (sadece bu node)
  PARTIAL    → 0 < down_votes < total
  STANDALONE → cluster disabled veya total == 0
```

### Classification — Neden Down?

```
REAL_OUTAGE:
  - down_votes >= quorum_threshold
  - offline_nodes == 0 (tüm monitoring node'ları sağlıklı)
  - yani monitoring altyapısı tamam, servisin kendisi çökmüş

NETWORK_PARTITION:
  - PARTIAL scope + bazı node'lar up görüyor
  - Monitoring node'ları arası veya monitoring→target arası ağ sorunu
  - Senaryonuz: node-3 UP görüyor ama node-1 + node-2 DOWN → partition

LOCAL_FAILURE:
  - NODE_LOCAL scope
  - Sadece bir monitoring node'un bakış açısından down
  - Büyük ihtimalle o monitoring node'un kendi ağ bağlantısı sorunu

AMBIGUOUS:
  - Yeterli veri yok (startup, single-node, offline monitoring node'ları)

Confidence = (down_votes / total) * (1 - offline_ratio)
```

### Network Partition Örneği — Sizin Durumunuz

Demo'da `db-primary`'yi 500 döndürünce:
- node-1, node-2: 500 alıyor → hard_down
- node-3: farklı timing'de probe yaptı, 200 aldı (mock server stateful değil, ya da probe timing) → up

Sonuç: PARTIAL scope + NETWORK_PARTITION classification.
**Gerçek hayatta bu şunu söyler:** "2 monitoring node'u db-primary'ye ulaşamıyor ama 1'i ulaşabiliyor. Tam bir outage değil, ağ yolu sorunu olabilir."

---

## Topology & Root Cause

```yaml
# config.yaml
targets:
  - id: db-primary   # ROOT (depends_on: [])
  - id: api-gateway
    depends_on:
      - db-primary   # api-gateway DB'ye bağımlı
  - id: checkout
    depends_on:
      - api-gateway  # checkout → api-gateway → db-primary
```

**Root Cause algoritması** (BFS/DFS):
1. Bir target DOWN olduğunda, onun `depends_on` listesine bak
2. Bir dependency de DOWN ise, o dependency'nin de dependency'lerine bak
3. En derin DOWN dependency = root cause

**Cascade Impact** (ters yön):
1. Bir target DOWN olduğunda, kim buna depend ediyor?
2. O bağımlılar da DOWN olacak → cascade listesi

**UI'da nasıl görürsün:**
- Targets listesinde: scope, classification, affected_apps
- Target detail sayfasında: Root Cause chip (kırmızı), Dep chip (turuncu), Impact chip (sarı)
- Topology sayfasında: Tam grafik tablo

**Root cause özelliği için önemli:**
- `depends_on` her node'un config'inde aynı olmalı (cluster-wide konsistens)
- Circular dependency → config validation hatası (başlarken yakalanır)
- Bilinmeyen target ID → config validation hatası

---

## SLO Tracker

### Nasıl Hesaplanır

```
window = 30d (2592000 saniye)
downtime = window içindeki tüm hard_down periyotların toplam süresi
uptime_ratio = (window - downtime) / window
error_budget_sec = window * (1 - target_uptime) - downtime
```

**incidents.json** dosyası `state_file` ile aynı dizinde oluşturulur:
```json
[
  {
    "target_id": "db-primary",
    "started_at": "2026-05-20T10:00:00Z",
    "ended_at": "2026-05-20T10:15:00Z"
  }
]
```

**SLO Breach Alert:** Edge-triggered. Bir SLO dönemi içinde tek bir alert. `slo_notify` kanalları yoksa `default_notify` kullanılır. Breach düzelince `breachAlerted` flag temizlenir (bir sonraki ihlalde tekrar alert gider).

### Mevcut Limitasyon (Planlanan: Backlog B-item)

Config'de yeni SLO target eklemek için config.yaml'ı elle düzenlemek gerekiyor. UI'dan add/edit/remove için backend endpoint'leri planlanmış (sprint.md'ye eklendi — aşağıya bak).

---

## Alert Feed — Neden Client-Side

Backend'in `/alerts` gibi bir endpoint'i yok (B7). Şu anki yaklaşım:

```
useFleet polling (5s) → state.consensus_state değişti mi?
  hayır → hiçbir şey yapma
  evet  → AlertEntry oluştur → alerts store'a push (max 100)
```

**Limitasyonlar:**
- Tab kapalıysa state değişikliği kaçırılır
- Multi-tab açıksa alert duplike olabilir (her tab kendi store'u)
- Sayfa yenilenince sıfırlanır (persist edilmiyor)

**B7 gelince ne değişir:**
- Backend `GET /alerts` endpoint'i: son N alert, filter'lı, persistent
- `useAlertsStore` sadece cache tutar, backend primary source olur
- Sayfa yenilense de geçmiş kaybolmaz

---

## Geo Latency — Ne İşe Yarar

Her cluster node farklı zone'da çalışır. Probe sırasında `elapsed` süresi gossip payload'una eklenir. `/geo/latency/{targetID}` bu verileri per-node gösterir.

```
Örnek senaryo:
  db-primary latency:
    eu-west-1a: 3ms   ← normal
    eu-west-1b: 4ms   ← normal
    eu-west-1c: 890ms ← ANOMALİ (>3× minimum)

Anlam: eu-west-1c'nin db-primary'ye ağ yolu kötü.
Aksiyon: eu-west-1c'deki node'un routing tablosunu kontrol et.
```

**Anomaly flag kriteri:** `max_latency > 3 × min_latency` (en az 2 non-zero değer gerekli).

**Zone bilgisi nasıl eklenir:**
```yaml
cluster:
  zone: "eu-west-1a"   # node-level label
  region: "eu-west"    # opsiyonel, daha geniş bölge
```

---

## Probe Timing — Son Probe'u Nasıl Görürsün

**Şu an yok.** Backend `FleetTarget` struct'ında `down_since` var ama `last_probed_at` yok.

**By-node breakdown'dan ne anlarsın:**
- `seq` → state değişimi sayısı. seq=1 = tek state geçişi. seq=10 = 10 kez up/down olmuş.
- `error_code` → son probe hata metni

**Workaround:** `/status` endpoint'i her target'ın anlık state'ini gösterir. Ama timestamp yok.

**Planlanmış iyileştirme:** Backend'e `last_probed_at` ve `last_state_change_at` eklenmesi önerilmiş. Bu sprint'e eklendi (aşağıya bak).

---

## API Quick Reference

Tüm endpoint'ler `admin.token` varsa `Authorization: Bearer <token>` gerektirir (okuma endpoint'leri genellikle açık, yazma endpoint'leri korumalı).

### Okuma Endpoint'leri

```
GET /health                    → 200 OK (liveness)
GET /version                   → { version, build_time }
GET /auth/whoami               → { role: "admin"|"anonymous" }
GET /status                    → [ { name, target, type, status, seq, error_code } ]
GET /fleet/status              → FleetSnapshot (cluster + summary + targets[])
GET /topology                  → { targets: { id: TopologyNode } }
GET /slo                       → { targets: SLOTargetResult[] }
GET /cluster/state             → ClusterState (members + peer states)
GET /cluster/config            → ConfigSyncSnapshot (hashes + drift)
GET /cluster/probers           → per-target prober assignments
GET /cluster/maintenance       → MaintenanceWindow[]
GET /cluster/keyring/rotate    → { key_count, primary_prefix }
GET /geo/latency/{targetID}    → GeoLatencySnapshot
GET /fleet/status?format=text  → ASCII table (terminal-friendly)
```

### Yazma Endpoint'leri (admin token gerekli)

```
PUT  /cluster/config           → SharedConfig'i tüm node'lara dağıt
POST /cluster/config/sync      → Bu node'un config'ini peer'lara gönder
POST /cluster/keyring/rotate   → { action: "add"|"use"|"remove", key: "base64..." }
PUT  /cluster/maintenance      → { target_id, duration_ms, reason, created_by }
DELETE /cluster/maintenance/{id}
POST /cluster/leave            → Graceful cluster leave + exit
```

### Demo'da db-primary'yi DOWN/UP Yapmak

```bash
# DOWN
curl -X POST http://127.0.0.1:9999/control \
  -H 'Content-Type: application/json' \
  -d '{"target":"db-primary","status":500}'

# UP
curl -X POST http://127.0.0.1:9999/control \
  -H 'Content-Type: application/json' \
  -d '{"target":"db-primary","status":200}'

# 30s bekle (probe_interval_sec: 30) → UI güncellenir
```

---

## Demo Cluster

### Başlatma

```bash
# 1. Mock HTTP server (probe target'ları simüle eder)
python3 /path/to/test_cluster/mock_server.py &

# 2. 3 node başlat (config'ler /tmp/nw-demo/n1,n2,n3/)
/tmp/netwatch-v2 -config /tmp/nw-demo/n1/config.yaml &
/tmp/netwatch-v2 -config /tmp/nw-demo/n2/config.yaml &
/tmp/netwatch-v2 -config /tmp/nw-demo/n3/config.yaml &

# 3. Frontend dev server
cd frontend
NUXT_PUBLIC_DEFAULT_BACKEND_URL=http://127.0.0.1:10241 pnpm dev

# 4. Açılış: http://localhost:3000
#    Backend URL: http://127.0.0.1:10241
#    Token: demo-token
```

### Test Senaryoları

**Senaryo 1: Single target down (GLOBAL, REAL_OUTAGE)**
```bash
# db-primary'yi tüm node'lardan down yap
curl -X POST http://127.0.0.1:9999/control -H 'Content-Type: application/json' -d '{"target":"db-primary","status":500}'
# 30s bekle
# Sonuç: db-primary HARD_DOWN, scope=GLOBAL, class=REAL_OUTAGE
# api-gateway, checkout → root_cause=db-primary
```

**Senaryo 2: Network partition (PARTIAL, NETWORK_PARTITION)**
```bash
# db-primary'yi sadece kısa süre için 500 yap, hemen geri al
# → Bazı node'lar 500 görür, bazıları 200 → PARTIAL + NETWORK_PARTITION
curl -X POST http://127.0.0.1:9999/control -H 'Content-Type: application/json' -d '{"target":"db-primary","status":500}'
sleep 15
curl -X POST http://127.0.0.1:9999/control -H 'Content-Type: application/json' -d '{"target":"db-primary","status":200}'
```

**Senaryo 3: Node down (quorum testi)**
```bash
# node-2'yi öldür
lsof -ti :10242 | xargs kill
# UI: Cluster Nodes: 2, quorum hâlâ healthy (2/3 > 0.5)
# node-2'yi geri başlat:
/tmp/netwatch-v2 -config /tmp/nw-demo/n2/config.yaml &
```

**Senaryo 4: Quorum kaybı (isolated mode)**
```bash
# node-2 ve node-3'ü öldür
lsof -ti :10242 :10243 | xargs kill
# UI: Cluster Nodes: 1, isolated=true, alertler suppress
```

**Senaryo 5: Maintenance window**
```bash
# UI'dan: Maintenance → New Window → db-primary, 1h, "DB upgrade"
# db-primary'yi down yap:
curl -X POST http://127.0.0.1:9999/control -H 'Content-Type: application/json' -d '{"target":"db-primary","status":500}'
# Beklenen: state DOWN görünür ama alert gitmez
```

**Senaryo 6: Config push**
```bash
# UI'dan: Config → Push Config
# probe_interval_sec: 15  (30'dan 15'e düşür)
# Push → 3 node'a da uygulanır → her 15s'de probe
```

### Durdurma

```bash
lsof -ti :10241 :10242 :10243 :9999 :3000 | xargs kill 2>/dev/null
```

---

## Planlanan Geliştirmeler

Sprint dosyasına eklendi — öncelik sırasıyla:

### Backend — B-Items (todo.md)

1. **B2 — Severity Levels** (3-4h): Her target'a severity ekle (critical/warning/info). Notification routing severity'e göre değişsin. UI'da SeverityBadge zaten hazır.

2. **B1 — Silence Rules** (4-6h): Label-based alarm susturma (Alertmanager tarzı). Maintenance window'un genelleştirilmiş versiyonu.

3. **B7 — Persistent Alert History** (5-7h): `GET /alerts` endpoint, son N alert. Alert feed client-side olmaktan çıkar.

4. **B3 — Latency-Based Alerting** (5-7h): Probe latency belirli eşiği aştığında alarm.

### Backend — Yeni İstekler (bu konuşmadan)

5. **SLO Target CRUD API** (2-3h): SLO target'larını config.yaml'a dokunmadan ekle/güncelle/sil.
   - `GET /slo/targets` — mevcut SLO target listesi
   - `PUT /slo/targets/{id}` — ekle veya güncelle
   - `DELETE /slo/targets/{id}` — sil
   - Cluster'a gossip ile yayılsın veya config push ile senkron olsun.

6. **last_probed_at + last_state_change_at** (1-2h): `FleetTarget`'a timestamp'lar ekle.
   - Backend: `Engine.lastProbeAt map[string]time.Time`, `atomic.Pointer[sync.Map]`
   - API: `fleet/status` response'una `last_probed_at`, `last_state_change_at` alanları
   - UI: Target listesinde "5m ago" timestamp göster, Target detail'de history

7. **down_since** (30min): `FleetTarget.DownSince` zaten var backend'de (`*time.Time`). Sadece populate edilmiyor. Fleet snapshot'ta `down_since` alanı doldurulursa UI "DOWN for 2h 15m" gösterebilir.

### Frontend — Sonraki İterasyon

- Topology görsel graph (vue-flow veya d3)
- Latency sparkline (target detail'de son N probe latency'si)
- B1 Silences CRUD UI
- B7 alert history (persistent)
- SLO Target edit modal (backend endpoint'leri hazır olunca)
