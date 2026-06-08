# netwatch — Kullanıcı Rehberi

Bu rehber netwatch'ı hiç görmemiş birisinin uygulamayı anlayıp kurabilmesi için yazılmıştır.
Teknik ayrıntılar için `README.md` ve `backend/README.md`.

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
Garantiler:
- Aynı alarm **tek kez** atılır (gossip + consistent hash primary)
- **Quorum kaybında** alarmlar baskılanır (izole node yanlış alarm atmaz)
- Node restart edildiğinde **alarm fırtınası olmaz** (anti-entropy sync)
- Her target yalnızca `probe_replication_factor` (varsayılan 3) node tarafından probe edilir

---

### N-Node Cluster Kurulumu — Adım Adım Talimat

> **Bu bölüm Gemini, ChatGPT, Claude veya operator bir insan için aynı şekilde uygulanabilir.** Hiçbir adımda "acaba?" diye düşünme — her şey net.

**Senaryo:** N node (örnek N=20). Her node ayrı bir makinede veya container'da. Aşağıdaki adımları sırayla uygula.

#### Ön Koşullar (her makinede)

1. **Binary**: `netwatch` derlenmiş olmalı (`go build -o /usr/local/bin/netwatch ./cmd/linux/`)
2. **Açık portlar (firewall)**: Her makinede şu portlar açık olmalı:
   - **TCP 10240** (HTTP API — `/metrics`, `/cluster/state`, vb.) — bu port operatörün ve Prometheus'un eriştiği port
   - **TCP 7946 + UDP 7946** (memberlist gossip) — bu portlar diğer cluster üyelerinin eriştiği port
   - macOS test'inde: `sudo pfctl` kuralı yoksa zaten açıktır
   - Linux production: `firewall-cmd --permanent --add-port=10240/tcp --add-port=7946/tcp --add-port=7946/udp && firewall-cmd --reload`
3. **Saat senkronu**: NTP açık olmalı (memberlist'in suspicion timer'ları doğru çalışsın diye)
4. **Her makinenin diğerlerine ulaşılabilir IP'si olmalı** (NAT/VPN engelleri yok)

#### Adım 1 — İlk node'u kur (`node-01`)

```bash
# 1.1 — Cluster için config + random keyring üret
sudo netwatch init --cluster --config-dir /etc/netwatch

# Bu komut şunları yapar:
#  - /etc/netwatch/config.yaml dosyasını yazar (cluster.enabled=true)
#  - Random AES-256 keyring üretir
#  - /etc/netwatch/credentials.env iskelet dosyası oluşturur
#  - /etc/systemd/system/netwatch.service yazar
#  - Stdout'a tam `netwatch join ...` komutu basar — KOPYALA, sakla
```

**Çıktı şu şekilde olur:**
```
─────────────────────────────────────────────────────────
  Cluster enabled — keep this keyring SECRET

  Keyring: xR7tK9pQ2sV8mN4jL6hF3wE1cB5yU0gA7zP9oI2nQ4=
  Node   : node-01
  Addr   : 10.0.1.10:7946   (auto-detected; override in config if needed)

  To add another node, run on it:

    netwatch join \
      --keyring xR7tK9pQ2sV8mN4jL6hF3wE1cB5yU0gA7zP9oI2nQ4= \
      --addr 10.0.1.10:7946
─────────────────────────────────────────────────────────
```

> **DİKKAT:** `Addr` satırı non-loopback IPv4'tür. Eğer **yanlış bir IP otomatik tespit edildiyse** (örn. Docker bridge, VPN aralığı), `/etc/netwatch/config.yaml` içinde `cluster.advertise_addr` alanını **doğru IP** ile elle güncelle.

```bash
# 1.2 — node-01 config'inde mutlaka kontrol et / ayarla:
sudo vi /etc/netwatch/config.yaml

# Yapılması gereken değişiklikler:
#  (a) expected_node_count: 20      ← Cluster'da toplam KAÇ node olacaksa o sayı
#  (b) advertise_addr: "10.0.1.10"  ← Bu node'un diğer makinelerden erişilebilir IP'si
#  (c) targets: [...]                ← İzlenecek hedefler (örnekler aşağıda)
#  (d) notifications: [...]          ← Alarm kanalları
```

Minimum config örneği (`/etc/netwatch/config.yaml` üzerine yazılacak değer):

```yaml
node_alias: "node-01"          # OPSİYONEL — atlanırsa hostname kullanılır
port: "10240"                  # HTTP API portu (10240 önerilen)
state_file: "/etc/netwatch/state.json"
log_path: ""                   # Boş = stdout (systemd journal'a düşer)
timeout: 5                     # probe başına saniye
max_retries: 2                 # soft → hard down öncesi tekrar sayısı
retry_interval_sec: 30         # retry'lar arası saniye (en az 5)
probe_interval_sec: 60         # iki probe arası saniye
ticker_interval_sec: 5         # retry scheduler ticker (en az 1)
reload_interval_sec: 30        # config dosyası mtime tarama aralığı (saniye, 0=kapalı)

admin:
  token: "BURAYA_RASTGELE_BIR_SECRET_YAZ"   # PUT/POST endpoint'leri için zorunlu

notifications:
  ops-alert:
    type: script
    parameters:
      script: "/etc/netwatch/alert.sh"

default_notify: ["ops-alert"]

targets:
  - id: "example-tcp"
    name: "Example TCP"
    type: tcp
    target: "example.com:443"
  # Daha fazla target için Probe Tipleri bölümüne bak

cluster:
  enabled: true
  node_name: "node-01"
  bind_addr: "0.0.0.0"               # Bütün ağ arayüzlerinde dinle
  bind_port: 7946                    # Gossip portu (TCP+UDP)
  advertise_addr: "10.0.1.10"        # ZORUNLU — peer'ların eriştiği IP. ASLA "127.0.0.1" YAZMA.
  advertise_port: 7946
  peers: []                          # İlk node'da boş bırak — join eden node'lar bu listeyi kendileri tamamlar
  keyring:
    - "INIT_KOMUTUNDAN_GELEN_KEYRING"
  expected_node_count: 20            # ZORUNLU — Cluster'daki TOPLAM node sayısı
  min_quorum_ratio: 0.5              # Çoğunluk eşiği — 0.5 = floor(20*0.5)+1 = 11 alive node
  probe_replication_factor: 3        # Her target'ı en fazla 3 node probe eder
```

```bash
# 1.3 — Alert script (en basit hâli; kendi entegrasyonun olduğu yerlerde değiştirebilirsin)
sudo cat > /etc/netwatch/alert.sh << 'EOF'
#!/usr/bin/env bash
# netwatch alarm script — env değişkenlerinde alarm bilgisi gelir
# STATUS: unreachable | reachable
# NAME, TARGET, TYPE, SEQ, ERROR_CODE, SCOPE, AFFECTED_APPS, ROOT_CAUSE
echo "$(date -u +%FT%TZ) $STATUS $NAME ($TARGET) [$SCOPE]" >> /var/log/netwatch-alerts.log
EOF
sudo chmod +x /etc/netwatch/alert.sh

# 1.4 — Config'i validate et
sudo netwatch validate --config /etc/netwatch/config.yaml
# Çıktı: "OK  config is valid" görmeli; yoksa hata mesajına göre düzelt

# 1.5 — Servisi başlat
sudo systemctl daemon-reload
sudo systemctl enable --now netwatch
sudo systemctl status netwatch     # active (running) olmalı

# 1.6 — Çalıştığını doğrula
curl http://localhost:10240/health           # → 200
curl http://localhost:10240/cluster/state | jq '.members | length'   # → 1
journalctl -u netwatch -f                    # → "netwatch cluster ready" banner görmeli
```

**Başarı kriterleri (Adım 1 sonunda):**
- ✅ `systemctl status netwatch` → `active (running)`
- ✅ `/health` → `200`
- ✅ `/cluster/state` → `members.length == 1`
- ✅ Journal'da `netwatch cluster ready` banner var

#### Adım 2 — Diğer 19 node'u join et (`node-02..node-20`)

Her node makinesinde sırayla:

```bash
# 2.1 — node-01'in init çıktısındaki tam komutu çalıştır
sudo netwatch join \
  --keyring xR7tK9pQ2sV8mN4jL6hF3wE1cB5yU0gA7zP9oI2nQ4= \
  --addr 10.0.1.10:7946 \
  --config /etc/netwatch/config.yaml \
  --node-name "node-NN"     # Her node için UNIQUE — node-02, node-03, ... node-20

# Bu komut şunları yapar:
#  - /etc/netwatch/config.yaml dosyasını yazar (cluster.enabled=true, peer=node-01)
#  - keyring'i embed eder
#  - node_name'i belirler
#  - Agent'i BAŞLATMAZ — sonra systemctl ile başlatacaksın

# 2.2 — Config'te bu node için node-specific ayarları kontrol et
sudo vi /etc/netwatch/config.yaml
# Sadece şu alanları doğrula/güncelle:
#  (a) cluster.advertise_addr: "10.0.1.NN"   ← BU node'un diğerlerinden erişilebilir IP'si
#  (b) cluster.expected_node_count: 20        ← Toplam node sayısı (node-01 ile aynı)
#  (c) node_alias: "node-NN"                  ← Opsiyonel etiket
# DİĞER ŞEYLERİ ELLEME — config sync ile node-01'den dağıtılacak (Adım 3).

# 2.3 — Servisi başlat
sudo systemctl daemon-reload    # Eğer init önceden çalıştırılmadıysa unit dosyası eksik olabilir;
                                 # `netwatch init --config-dir /etc/netwatch` ile sadece unit dosyası
                                 # üretip diğer dosyaları yok edebilirsin (ama burada zaten join attı,
                                 # daha kolay yol: unit dosyasını node-01'den scp ile kopyala).
sudo systemctl enable --now netwatch

# 2.4 — Cluster'a katıldığını doğrula
curl http://localhost:10240/cluster/state | jq '.members | length'
# Beklenen: her join'den sonra +1 artmalı (node-02 join ettikten sonra 2, node-03 sonrası 3, ...)

# Aynı kontrolü node-01'den de yap:
curl http://10.0.1.10:10240/cluster/state | jq '.members | length'
# Aynı sayıyı vermeli.
```

> **Önemli — node_name UNIQUE olmalı:** İki node'un `cluster.node_name`'i aynı olursa memberlist onları aynı node sanır, biri ezilir. **Hostname'i kullanırsan otomatik unique olur.**

> **Önemli — advertise_addr asla `127.0.0.1` veya `0.0.0.0` olmamalı:** Diğer node'lar bu IP üzerinden geri bağlantı kurar. Loopback yazarsan peer bağlanamaz. Her node'a kendi gerçek IP'sini yaz. `bind_addr: "0.0.0.0"` OK (dinleme), ama `advertise_addr` gerçek IP olmalı.

> **Önemli — keyring her node'da AYNEN aynı olmalı:** Tek karakter farkı bile cluster'ın bölünmesine yol açar (iki ayrı küçük cluster oluşur). `join` komutu bunu otomatik halleder; elle değiştirme.

#### Adım 3 — Ortak config'i tüm node'lara dağıt (önerilen)

Her node'da config'i ayrı ayrı kurgulamak yerine, node-01'i tam kur, sonra **tek komutla** notifications, default_notify, targets dışındaki ortak ayarları diğerlerine yay:

```bash
# Bu komut: node-01'in config'indeki "shared" alanları (timeout, retries, intervals,
# keyring, expected_node_count, probe_replication_factor, min_quorum_ratio, peers)
# diğer 19 node'a gossip TCP ile dağıtır.
# Her node config.yaml'ını atomik yazar ve hot-reload tetikler — restart gerekmez.

curl -X POST http://10.0.1.10:10240/cluster/config/sync \
  -H "Authorization: Bearer SECRET_TOKEN_NODE_01_ADMIN_TOKEN"

# Çıktı:
# {"applied_locally":false,"broadcast_to":["node-02",...,"node-20"],"failed_nodes":{}, ...}

# NOT: targets, apps, slo, node_alias, port, advertise_addr DAĞITILMAZ.
# Bunlar her node'a özel veya cluster-wide olarak start'tan önce ayarlanmalı.
```

#### Adım 4 — Cluster sağlığını doğrula

```bash
# 4.1 — Tüm 20 node'u tek tek kontrol et
for ip in 10.0.1.{10..29}; do
  SIZE=$(curl -sf http://$ip:10240/cluster/state 2>/dev/null | jq '.members | length')
  echo "$ip → $SIZE members"
done
# Beklenen: hepsi "20 members" yazsın

# 4.2 — Quorum sağlıklı mı
curl -s http://10.0.1.10:10240/metrics | grep -E "^network_prober_(quorum_healthy|isolated|cluster_size)"
# Beklenen:
#   network_prober_quorum_healthy 1
#   network_prober_isolated 0
#   network_prober_cluster_size 20

# 4.3 — Probe assignment'ları dağıldı mı (stabilize olması ~30 sn sürebilir)
sleep 30
curl -s http://10.0.1.10:10240/cluster/probers | jq '.targets[0] | {target_id, selected_probers}'
# Beklenen: her target için 3 prober (factor=3) seçilmiş olmalı

# 4.4 — Fleet özeti
curl -s http://10.0.1.10:10240/fleet/status?format=text
# Tüm target'lar ve durumları okunaklı tablo halinde
```

**Cluster hazır ✓** — Bu noktada N=20 node bağlı, gossip çalışıyor, probe'lar dağıtık koşuyor.

---

### Sık Karşılaşılan Sorunlar

**P: Cluster boyutu 20 yerine düşük (örn. 17) gösteriyor**
- `cluster.advertise_addr`'ı bağlanamayan node'larda kontrol et — loopback veya yanlış IP olabilir
- Firewall'ları kontrol et: TCP 7946 + UDP 7946 her node'a açık mı
- `journalctl -u netwatch | grep -i "memberlist\|join"` ile join hatalarına bak
- `cluster.peers` listesinde en az 1 alive node olmalı (node-01 down ise bağlanamazlar)

**P: `network_probe_target_orphaned` metriği 1**
- `probe_from` listesinde alive node yok
- `probe_from_regions` listesi hiçbir node'un `cluster.region` etiketiyle eşleşmiyor
- `cluster.zone` typoları için config'leri kontrol et

**P: Aynı alarm 2 kere geliyor**
- `cluster.keyring` farklı olabilir — iki ayrı cluster'a bölündüyseniz her cluster'ın kendi primary'si var
- `cluster.expected_node_count` tüm node'larda aynı olmalı (yoksa quorum hesabı bozulur)
- `cluster.probe_replication_factor` tüm node'larda aynı olmalı

**P: Hiç alarm gelmiyor**
- Servis gerçekten down mu? `/status` endpoint'inde target durumlarına bak (`hard_down` görmen lazım)
- `cluster.min_quorum_ratio` çok yüksek olabilir — `network_prober_isolated` metriği 1 ise alarmlar baskılanır
- `admin.token` set edildiyse alarm script'in env'inde lazım değil, ama `PUT /cluster/config` çağrılarında Authorization header'ı zorunlu
- `journalctl -u netwatch | grep -E "sending alert|alert suppressed"` ile alarm akışını izle

**P: Bir node'u temiz silmek istiyorum**
```bash
# Hedef node'da:
sudo netwatch leave --port 10240    # gossip leave yayar, sonra servisi durdurur
sudo systemctl disable --now netwatch
# Diğer node'lar 5-10 sn içinde "cluster member left" log'u basar ve ringi yeniden hesaplar.
```

**P: Bir node'un keyring'i değişirse**
- Sıfır kesintili keyring rotation için `POST /cluster/keyring/rotate` API'sini kullan (Keyring Rotation bölümüne bak)
- Manuel değiştirip restart edersen O NODE küme'den izole olur; sadece aynı yeni keyring'e sahip node'larla konuşur

---

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

# Standalone config skeleton + systemd unit
netwatch init --config-dir /etc/netwatch

# Cluster ilk node: random keyring + join komutu çıktısı
netwatch init --cluster
netwatch init --cluster --bind-port 7946 --force   # üzerine yaz, prompt'suz

# Mevcut cluster'a katıl
netwatch join \
  --keyring <base64-key> \
  --addr <peer-host>:<gossip-port> \
  --node-name <opsiyonel>

# Yeni AES-256 keyring base64 üret (rotation için)
netwatch keyring generate

# Cluster'dan graceful ayrıl (çalışan agent'a HTTP gönderir)
netwatch leave --port 10240

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
