# netwatch — Kullanıcı Rehberi

Bu rehber netwatch'ı hiç görmemiş birisinin uygulamayı anlayıp kurabilmesi için yazılmıştır.
Teknik ayrıntılar için `README.md`, geliştirici notları için `CLAUDE.md`.

---

## netwatch ne yapar?

netwatch, ağ üzerindeki hizmetleri (HTTP, TCP, ping, DNS, SQL veritabanı) belirli aralıklarla kontrol eden ve
bir sorun tespit ettiğinde sizi haberdar eden bir izleme ajanıdır.

Temel özellikler:

- **Probe tipleri:** HTTP/HTTPS, TCP, ICMP ping, DNS, PostgreSQL/MySQL/MSSQL/Oracle
- **Bildirim kanalları:** Kabuk scripti, e-posta (SMTP), webhook (Alertmanager uyumlu dahil)
- **Prometheus metrikleri:** `/metrics` endpoint'inden tüm durum verileri
- **Cluster modu:** Birden fazla node gossip protokolüyle haberleşir, quorum bazlı karar verir,
  aynı alarmı tek kez atar
- **SLO takibi:** Uptime hedefi tanımlayıp ihlal anında bildirim alabilirsiniz
- **Uygulama bağlamı:** Hangi servisin hangi altyapıya bağlı olduğunu tanımlayıp
  alarmlarınızı "payment-gateway etkilendi, ekip: fintech-sre" şeklinde zenginleştirebilirsiniz

---

## Hızlı Başlangıç (tek node)

```bash
# Binary'yi derleyin
go build -o netwatch ./cmd/linux/

# Örnek config'i kopyalayın
cp config.example.yaml config.yaml

# Çalıştırın
./netwatch -config config.yaml

# Sağlık kontrolü
curl http://localhost:10240/health

# Tüm target'ların durumu
curl http://localhost:10240/status
```

---

## Kurulum

### Linux — systemd

```bash
# Binary'yi kopyala
sudo cp netwatch /usr/local/bin/netwatch
sudo chmod +x /usr/local/bin/netwatch

# Config ve bildirim klasörlerini oluştur
sudo mkdir -p /etc/netwatch /etc/netwatch/notifications
sudo cp config.yaml /etc/netwatch/config.yaml

# netwatch init ile systemd unit dosyası oluştur
sudo netwatch init --config-dir /etc/netwatch

# Servisi etkinleştir
sudo systemctl enable --now netwatch
```

> ICMP ping tipi için `CAP_NET_RAW` gereklidir. `deploy/netwatch.service` dosyasında
> `AmbientCapabilities=CAP_NET_RAW` zaten ayarlıdır.

### Docker

```bash
docker run -d \
  -p 10240:10240 \
  -v $(pwd)/config.yaml:/etc/netwatch/config.yaml \
  -v $(pwd)/notifications:/etc/netwatch/notifications \
  ghcr.io/saidtaylan/netwatch:latest \
  -config /etc/netwatch/config.yaml
```

### Kubernetes (DaemonSet)

```bash
helm install netwatch ./helm/netwatch \
  --set config.clusterEnabled=true \
  --set config.nodeName=$(hostname)
```

---

## Temel Konfigürasyon

```yaml
# config.yaml — minimum çalışır yapılandırma

port: "10240"
app_name: "production-monitor"
timeout: 5
max_retries: 3
retry_interval_sec: 30
probe_interval_sec: 60

notifications:
  ops-pager:
    type: script
    parameters:
      script: "/etc/netwatch/notifications/alert.sh"

default_notify: ["ops-pager"]

targets:
  - name: "api-gateway"
    type: http
    target: "https://api.company.com/health"
    options:
      expected_status:
        in: [200, 204]

  - name: "database"
    type: tcp
    target: "db.internal:5432"

  - name: "redis"
    type: tcp
    target: "redis.internal:6379"
    notify: ["ops-pager"]   # target'a özel kanal
```

---

## Probe Tipleri

### HTTP / HTTPS

```yaml
- name: "checkout-api"
  type: http
  target: "https://api.company.com/checkout/health"
  options:
    method: "GET"
    expected_status:
      in: [200]           # ya da: eq: 200 / between: [200, 299]
    body_contains: "ok"   # opsiyonel — response body kontrolü
    follow_redirects: true
    timeout_sec: 10
```

### TCP

```yaml
- name: "postgres"
  type: tcp
  target: "db.internal:5432"
```

### ICMP Ping

```yaml
- name: "core-router"
  type: ping
  target: "10.0.0.1"
```

> `CAP_NET_RAW` yetkisi gerektirir. Docker'da `--cap-add NET_RAW`.

### DNS

```yaml
- name: "internal-dns"
  type: dns
  target: "api.company.com"
  options:
    nameserver: "8.8.8.8:53"      # opsiyonel
    expected_ips: ["203.0.113.10"] # opsiyonel — IP doğrulama
```

### SQL (PostgreSQL / MySQL / MSSQL / Oracle)

```yaml
- name: "primary-db"
  type: sql
  target: "db.internal:5432"
  options:
    driver: "postgres"
    username: "${DB_USER}"     # credentials.env'den inject
    password: "${DB_PASS}"
    database: "production"
    query: "SELECT 1"          # opsiyonel — sorgu çalıştır
    ssl_mode: "require"
```

---

## Bildirim Kanalları

### Script

En esnek kanal. netwatch her alarm için scripti env değişkenleriyle çağırır:

```yaml
notifications:
  ops-pager:
    type: script
    parameters:
      script: "/etc/netwatch/notifications/alert.sh"
```

Script'e gelen başlıca env değişkenleri:

| Değişken | İçerik |
|---|---|
| `NAME` | target adı |
| `TARGET` | host:port veya URL |
| `STATUS` | `unreachable` veya `reachable` |
| `TYPE` | tcp / http / ping / dns / sql |
| `ERROR_CODE` | hata mesajı (recovery'de boş) |
| `SEQ` | Lamport seq — her geçişte artar |
| `SCOPE` | GLOBAL / NODE\_LOCAL / PARTIAL / STANDALONE |
| `AFFECTED_APPS` | etkilenen uygulamalar (virgülle) |
| `OWNER_TEAMS` | ilgili ekipler (virgülle) |
| `ROOT_CAUSE` | en derin bağımlı arızalanan target |
| `CLASSIFICATION` | REAL\_OUTAGE / NETWORK\_PARTITION / LOCAL\_FAILURE |

### E-posta (SMTP)

```yaml
notifications:
  mail-ops:
    type: mail
    parameters:
      from: "netwatch@company.com"
      to: "ops@company.com,on-call@company.com"
      smtp_host: "smtp.company.com"
      smtp_port: "587"
      username: "${SMTP_USER}"
      password: "${SMTP_PASS}"
      tls: "starttls"
```

### Webhook (generic JSON)

```yaml
notifications:
  slack-hook:
    type: webhook
    parameters:
      url: "https://hooks.slack.com/services/T000/B000/xxx"
      timeout_sec: "10"
```

### Webhook (Alertmanager uyumlu)

```yaml
notifications:
  alertmanager:
    type: webhook
    parameters:
      url: "http://alertmanager:9093/api/v2/alerts"
      format: "alertmanager"
      header_Authorization: "Bearer ${AM_TOKEN}"
```

---

## Uygulama → Altyapı Haritası (Apps)

Hangi uygulamanın hangi altyapıya bağlı olduğunu tanımlayabilirsiniz.
Target down olduğunda alarm mesajı otomatik olarak "payment-gateway etkilendi" bilgisini taşır.

```yaml
targets:
  - id: "payments-db"
    type: tcp
    target: "db-payments:5432"
    name: "Payments DB"

apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["payments-db"]
    notifications: ["slack-fintech"]

  - name: "fraud-detection"
    owner_team: "security-team"
    uses: ["payments-db"]
    notifications: ["pagerduty-sec"]
```

`payments-db` down olduğunda:
- `AFFECTED_APPS=payment-gateway,fraud-detection`
- `OWNER_TEAMS=fintech-sre,security-team`
- Bildirim kanalları: `ops-pager ∪ slack-fintech ∪ pagerduty-sec` (union, dedupe)

---

## Bağımlılık Grafiği ve Kök Neden Tespiti

Bir target başka bir target'a bağımlıysa `depends_on` ile tanımlayın.
Ana veritabanı down olduğunda üstüne kurulu tüm servislerin alarmında "root cause: primary-db" yazar.

```yaml
targets:
  - id: "primary-db"
    type: tcp
    target: "db:5432"
    name: "Primary DB"

  - id: "api-gateway"
    type: http
    target: "https://api.company.com/health"
    name: "API Gateway"
    depends_on: ["primary-db"]

  - id: "checkout-service"
    type: http
    target: "https://checkout.company.com/health"
    name: "Checkout Service"
    depends_on: ["api-gateway"]
```

`primary-db` down olduğunda:
- `api-gateway` alarmında: `ROOT_CAUSE=primary-db`, `DEPENDENCY_DEPTH=1`
- `checkout-service` alarmında: `ROOT_CAUSE=primary-db`, `DEPENDENCY_DEPTH=2`, `CASCADING_IMPACT=checkout-service`

`GET /topology` endpoint'i tüm bağımlılık grafiğini JSON olarak döner.

---

## SLO Takibi

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops-pager"]
  targets:
    - id: "api-gateway"
      target_uptime: 0.999   # 99.9%
      window: "30d"
    - id: "primary-db"
      target_uptime: 0.9999  # 99.99%
      window: "7d"
```

- `GET /slo` — her target için uptime oranı, kalan hata bütçesi, aktif ihlal durumu
- `GET /slo?format=text` — terminal dostu ASCII tablo
- İhlal edge-triggered: aynı SLO döneminde yalnızca bir kez alarm atar

---

## HTTP Endpointleri

| Endpoint | Açıklama |
|---|---|
| `GET /health` | Her zaman 200 döner — liveness check |
| `GET /metrics` | Prometheus metrikleri |
| `GET /status` | Tüm target'ların JSON durumu (name, status, seq, error_code) |
| `GET /fleet/status` | Zengin görünüm: scope, classification, affected apps, root cause, incidents |
| `GET /fleet/status?format=text` | Aynısı, terminal ASCII tablo |
| `GET /topology` | Bağımlılık grafiği (depends\_on ilişkileri) |
| `GET /slo` | SLO metrikleri: uptime, budget, breach |
| `GET /slo?format=text` | Terminal ASCII tablo |
| `GET /cluster/state` | Gossip üyeleri + peer target durumları |
| `GET /cluster/probers` | Her target için seçilen prober node'ları ve nedenler |
| `GET /cluster/config` | Config hash karşılaştırması — drift tespiti |
| `GET /geo/latency/{targetID}` | Target için per-node latency + anomali flag |
| `GET /cluster/keyring/rotate` | Aktif key bilgisi |
| `POST /cluster/keyring/rotate` | Sıfır-kesinti AES key rotasyonu |
| `PUT /cluster/config` | Ortak config alanlarını tüm node'lara dağıt (JSON/YAML body) |
| `POST /cluster/config/sync` | Bu node'un shared config'ini tüm peer'lara gönder |
| `POST /cluster/leave` | Cluster'dan graceful ayrılış + process sonlanma |

---

## Prometheus Metrikleri (özet)

| Metrik | Açıklama |
|---|---|
| `network_probe_local_status` | 1=UP, 0=DOWN (bu node'da) |
| `network_probe_local_latency_seconds` | Son probe süresi |
| `network_probe_prometheus_connected` | 1=scrape normal, 0=watchdog tetiklendi |
| `network_probe_cluster_status` | Konsensüs: tüm node'lar UP=1, herhangi biri DOWN=0 |
| `network_probe_local_assigned` | 1=bu node target'ı probe ediyor |
| `network_probe_prober_count` | Bu target için atanan toplam prober sayısı |
| `network_probe_prober_underreplicated` | 1=prober sayısı factor'dan az (degraded) |
| `network_probe_target_orphaned` | 1=hiçbir node probe etmiyor (config hatası) |
| `network_prober_quorum_healthy` | 1=quorum tamam |
| `network_prober_isolated` | 1=bu node izole (alarmlar baskılandı) |
| `network_probe_slo_uptime_ratio` | Gerçek uptime oranı |
| `network_probe_slo_breached` | 1=SLO ihlali aktif |
| `network_probe_config_drift` | 1=en az bir peer farklı config'e sahip |
| `network_probe_geo_latency_seconds` | Per-region probe latency |

---

## State Machine — Nasıl Davranır?

```
UP → SOFT_DOWN → HARD_DOWN → UP
```

**UP:** Son probe başarılı. Alarm yok.

**SOFT_DOWN:** İlk başarısız probe. Henüz alarm atılmaz, retry'lar bekleniyordur.
Bu durum yalnızca RAM'de tutulur — `state.json`'a yazılmaz.
Geçici ağ sorunları (paket kaybı, kısa yeniden başlatmalar) burada absorbe edilir.

**HARD_DOWN:** `max_retries` sayısınca başarısız probe. Alarm atılır, `state.json`'a yazılır.
Uygulama yeniden başlatılsa bile bu durum hatırlanır.

**Recovery:** Bir sonraki başarılı probe "reachable" alarmı atar ve seq'i artırır.
`seq=1 unreachable` + `seq=2 reachable` aynı incident'a aittir.

**Restart güvencesi:** `state.json` okunur. Zaten hard-down olan bir target restart sonrasında
tekrar alarm üretmez — alarm `AlarmSent=true` olarak kaydedilmiştir.

---

## Cluster Modu

Birden fazla netwatch node'u aynı altyapıyı izliyorsa cluster modu devreye girer.
Temel garantiler:
- Aynı alarm **tek kez** atılır (gossip + consistent hash primary)
- **Quorum kaybında** alarmlar baskılanır (izole node yanlış alarm atmaz)
- Node restart edildiğinde **alarm fırtınası olmaz** (anti-entropy sync)

### Minimal 3-node cluster

`node-1/config.yaml`:
```yaml
cluster:
  enabled: true
  node_name: "node-1"
  bind_addr: "0.0.0.0"
  bind_port: 7946
  advertise_addr: "192.168.1.101"
  peers:
    - "192.168.1.101:7946"
    - "192.168.1.102:7946"
    - "192.168.1.103:7946"
  keyring:
    - "base64_aes256_key_here=="
  expected_node_count: 3
  min_quorum_ratio: 0.5
```

`node-2` ve `node-3`: aynı yapı, sadece `node_name` ve `advertise_addr` farklı.

### Distributed Probe Ownership

50 node'lu bir cluster'da her target yalnızca 3 node tarafından probe edilir.
Diğer 47 node gossip'ten sonuçları alır, probe açmaz.

```yaml
cluster:
  probe_replication_factor: 3   # varsayılan
```

**Zone-aware dağılım:** Farklı veri merkezlerinden redundant coverage:

```yaml
# node-1 config
cluster:
  zone: "istanbul"

# node-2 config
cluster:
  zone: "ankara"
```

Sistem otomatik olarak farklı zone'lardan prober seçer.

**Manuel pin** (belirli node'lar probe etsin):

```yaml
targets:
  - id: "internal-vpn"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "amsterdam-1"]
```

**Yanlış alarm koruması** (`min_probe_confirmations`):
Bir node'un kendi ağ bağlantısı bozuksa, tek başına hard-down görmesi alarmı tetiklememeli:

```yaml
cluster:
  min_probe_confirmations: 2   # 2 prober aynı fikirde olmalı
```

### Quorum

`expected_node_count: 3` ve `min_quorum_ratio: 0.5` ise en az 2 node gerekir.
2 node down olduğunda kalan node izole moda girer, alarm atmaz.
Quorum döndüğünde otomatik olarak anti-entropy sync yapılır, alarmlar devam eder.

### Gossip Şifrelemesi

```yaml
cluster:
  keyring:
    - "yeniKeyBase64=="   # şifrelemek için ilk key kullanılır
    - "eskiKeyBase64=="   # her iki key de çözme için denenir
```

Sıfır-kesinti key rotasyonu: önce yeni key'i listenin başına ekle, tüm node'lara dağıt,
sonra eski key'i kaldır.

### Config Dağıtımı (Push / Sync)

Cluster'daki tüm node'ların ortak konfigürasyonu paylaşması gerektiğinde tek bir node'dan dağıtım yapabilirsiniz.

**Eşitlenen alanlar:** `timeout`, `max_retries`, `retry_interval_sec`, `ticker_interval_sec`, `probe_interval_sec`, `reload_interval_sec`, `watchdog_threshold_sec`, `notifications`, `default_notify`, `cluster.keyring`, `cluster.peers`, `cluster.expected_node_count`, `cluster.min_quorum_ratio`, `cluster.probe_replication_factor`, `cluster.min_probe_confirmations`

**Hiçbir zaman eşitlenmeyen alanlar:** `port`, `node_alias`, `log_path`, `state_file`, `credentials_file`, `targets`, `apps`, `slo`, `cluster.node_name/bind_*/advertise_*/zone/region`

**Belirli alanları dağıt (`PUT /cluster/config`):**

```bash
# Tüm node'lardaki notification kanallarını ve default_notify'ı güncelle
curl -X PUT http://node-1:10240/cluster/config \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "notifications": {
      "ops-pager": {"type": "script", "parameters": {"script": "/etc/netwatch/alert.sh"}}
    },
    "default_notify": ["ops-pager"]
  }'
```

YAML formatı da desteklenir (`Content-Type: application/x-yaml`):

```bash
curl -X PUT http://node-1:10240/cluster/config \
  -H "Content-Type: application/x-yaml" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  --data-binary @shared-config.yaml
```

**Bu node'un tüm ortak alanlarını dağıt (`POST /cluster/config/sync`):**

Bir node'u doğru kurduysanız ve diğerlerini sıfırdan başlatmak istiyorsanız:

```bash
curl -X POST http://node-1:10240/cluster/config/sync \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

Node-1'in `notifications`, `default_notify`, timing alanları ve cluster ortak ayarları tüm peer'lara yayılır. Her peer kendi `config.yaml`'ını atomik yazar ve `Reload()` tetikler — restart gerekmez.

**Yanıt:**

```json
{
  "applied_locally": true,
  "broadcast_to": ["node-2", "node-3"],
  "failed_nodes": {},
  "fields_applied": ["notifications", "default_notify", "cluster.*"],
  "pushed_at": "2026-05-14T10:00:00Z"
}
```

**Admin auth:**

```yaml
# config.yaml
admin:
  token: "${ADMIN_TOKEN}"  # boşsa tüm write endpoint'ler açık
```

Token ayarlıysa `Authorization: Bearer <token>` header zorunludur. Token olmadan çağrı yapılırsa `401 Unauthorized`. Yanlış token: `403 Forbidden`.

---

## Sık Sorulan Sorular

### Alarm neden geç geliyor?

`max_retries` × `retry_interval_sec` kadar bekler. Örnek: `max_retries: 3`, `retry_interval_sec: 30` → 90 saniye.
Acil uyarılar için bu değerleri küçültün; false alarm toleransı için büyük bırakın.

### Uygulama restart olunca mevcut alarm tekrar atar mı?

Hayır. `state.json` dosyasında `AlarmSent: true` kayıtlıdır.
Restart sonrasında aynı target için ikinci bir "unreachable" alarmı atılmaz.
Target iyileşip tekrar down olursa o zaman yeni bir alarm atılır (seq artar).

### Cluster'da 3 prober varken biri çöktü, alarm kaybolur mu?

Hayır. Prober ilk başarısız probe'da co-prober'lara `soft_down` gossip sinyali yayar.
Diğer 2 prober bu sinyali alır ve anında (ticker'ı beklemeden) kendi problarını başlatır.
Çöken node daha hard-down'a geçemese bile diğer iki prober bağımsız olarak sonuca ulaşır.

### İzole mod ne zaman devreye girer?

`floor(expected_node_count × min_quorum_ratio) + 1` kadar alive node yoksa.
Örneğin 3 node cluster'da 2 node down → kalan 1 node izole moda girer.
İzole moddaki node probe yapmaya devam eder ama alarm atmaz.
Quorum döndüğünde otomatik çıkar.

### Cluster'da birden fazla node aynı alarmı atar mı?

Hayır. Consistent hash ring her target için bir "primary" node belirler.
Yalnızca primary alarm atar. Primary leave olursa bir sonraki node otomatik devralır.

### `probe_replication_factor` değiştirirsem tüm node'ları güncellemem gerekiyor mu?

Evet. Tüm node'lar aynı değeri kullanmalı — aksi hâlde farklı prober setleri hesaplanır
ve exactly-once garantisi bozulur.
Hot-reload desteklenir: `config.yaml`'ı güncelleyin, `reload_interval_sec` içinde otomatik alınır.

### Prometheus scrape durduğunda ne olur?

`watchdog_threshold_sec` ayarlıysa (varsayılan: devre dışı), o süre boyunca scrape gelmezse
`network_probe_prometheus_connected` metriği 0'a düşer ve log'a uyarı yazar.
Probe'lar ve alarmlar etkilenmez — ajan özerk çalışmaya devam eder.

### `/fleet/status` ile `/status` farkı nedir?

`/status`: basit liste — her target için name, status, seq, error_code.

`/fleet/status`: zengin görünüm — scope (GLOBAL/NODE\_LOCAL/PARTIAL),
classification (REAL\_OUTAGE/NETWORK\_PARTITION/LOCAL\_FAILURE/AMBIGUOUS),
affected apps, root cause, active incidents, by-node breakdown.
Standalone modda da çalışır.

### `network_probe_target_orphaned` metriği neden 1 oldu?

`probe_from` veya `probe_from_regions` içinde hiçbir alive node yok demektir.
Örnek: `probe_from: ["typo-node-name"]` — bu node hiç join olmamış.
Ya da bölge etiketleri hiçbir alive node'la örtüşmüyor.
Log'da `[CLUSTER] target orphaned` satırı ve ipucu mesajı görünür.

### Config değişikliği nasıl uygulanır?

`reload_interval_sec` (varsayılan: 30) her N saniyede config dosyasının mtime'ını kontrol eder.
Değişiklik varsa yeni hedeflere goroutine açılır, kaldırılanlar iptal edilir.
Restart gerekmez. `reload_interval_sec: 0` ile hot-reload kapatılabilir.

---

## CLI Komutları

```bash
# Konfigürasyonu doğrula (çalıştırmadan)
netwatch validate -config config.yaml

# Systemd unit dosyası + örnek config oluştur
netwatch init --config-dir /etc/netwatch

# Cluster'dan graceful ayrıl (çalışan agent'a HTTP gönderir)
netwatch leave --addr http://localhost:10240

# Tümünü kaldır (leave + servis + dosyalar)
netwatch uninstall

# Windows Servis yönetimi
netwatch service install
netwatch service remove
```

---

## Güvenlik Notları

- Gossip trafiği AES-256 ile şifrelenir (`cluster.keyring`).
- SQL şifreleri `credentials.env` dosyasında tutulur ve config'e `${VAR}` olarak enjekte edilir.
- ICMP ping için `CAP_NET_RAW` gereklidir — diğer tipler için root yetkisi gerekmez.
- `/metrics` endpoint'i hassas bilgi içermez; Prometheus scrape için ağ erişimi yeterlidir.
- `cluster.keyring` değerleri base64 kodlu AES anahtarıdır (16, 24 veya 32 byte ham boyut).
