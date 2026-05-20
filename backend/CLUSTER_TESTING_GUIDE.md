# netwatch — 3-Node Cluster Kurulum ve Test Rehberi

Bu belge, netwatch'u 3 node'lu lokal cluster olarak nasıl kuracağını, çalıştıracağını ve test edeceğini adım adım açıklar. Her komutu tek başına çalıştırabilirsin.

---

## 1. Dizin Yapısı

```
/tmp/netwatch-demo/
  node1/          ← node-1 config + state + log
  node2/          ← node-2 config + state + log
  node3/          ← node-3 config + state + log
  logs/           ← ortak alerts.log
  alert.sh        ← tüm node'ların çağırdığı script
```

---

## 2. Hazırlık

### 2a. Binary derle (proje dizininde)

```bash
cd "/Users/saidtaylan/Documents/network cluster"
make build-darwin
# → bin/netwatch-darwin oluşur
```

### 2b. Dizinleri oluştur

```bash
rm -rf /tmp/netwatch-demo
mkdir -p /tmp/netwatch-demo/{node1,node2,node3,logs}
```

### 2c. Paylaşımlı alert script yaz

```bash
cat > /tmp/netwatch-demo/alert.sh << 'EOF'
#!/bin/sh
TIMESTAMP=$(date '+%Y-%m-%dT%H:%M:%S')
LOG=/tmp/netwatch-demo/logs/alerts.log
echo "[$TIMESTAMP] STATUS=$STATUS NODE=$NODE_NAME TARGET=$NAME TYPE=$TYPE SEQ=$SEQ SCOPE=${SCOPE:-STANDALONE} APPS=${AFFECTED_APPS:-none} ERROR=${ERROR_CODE:-}" >> "$LOG"
EOF
chmod +x /tmp/netwatch-demo/alert.sh
```

### 2d. Her node için config oluştur

Aşağıdaki bloğu çalıştır — 3 config dosyasını otomatik üretir:

```bash
BINARY="/Users/saidtaylan/Documents/network cluster/bin/netwatch-darwin"

for i in 1 2 3; do
  HTTP=$((10240 + i))
  GOSSIP=$((7940 + i))
  cat > /tmp/netwatch-demo/node${i}/config.yaml << EOF
app_name: "netwatch-agent"
port:     "${HTTP}"

state_file: "/tmp/netwatch-demo/node${i}/state.json"
log_path:   "/tmp/netwatch-demo/node${i}/agent.log"

timeout:             3
max_retries:         2
retry_interval_sec:  10
probe_interval_sec:  15
ticker_interval_sec: 3
reload_interval_sec: 0
watchdog_threshold_sec: 0

notifications:
  log-alert:
    type: script
    parameters:
      script: "/tmp/netwatch-demo/alert.sh"

default_notify:
  - log-alert

targets:
  - id:   "google-dns"
    name: "Google DNS"
    type: tcp
    target: "8.8.8.8:53"

  - id:   "fake-down"
    name: "Fake Down Target"
    type: tcp
    target: "127.0.0.1:19999"

cluster:
  enabled:        true
  node_name:      "node-${i}"
  bind_addr:      "127.0.0.1"
  bind_port:      ${GOSSIP}
  advertise_addr: "127.0.0.1"
  advertise_port: ${GOSSIP}
  peers:
    - "127.0.0.1:7941"
    - "127.0.0.1:7942"
    - "127.0.0.1:7943"
  keyring:
    - "+ioC2+ihDEDREdGHjiCT1yp5UwCSDFwSAUc5RQCPxec="
  expected_node_count: 3
  min_quorum_ratio:    0.5
EOF
done
```

---

## 3. Cluster'ı Başlat

Üç node'u arka planda çalıştır, PID'leri kaydet:

```bash
BINARY="/Users/saidtaylan/Documents/network cluster/bin/netwatch-darwin"

"$BINARY" -config /tmp/netwatch-demo/node1/config.yaml &; N1=$!
"$BINARY" -config /tmp/netwatch-demo/node2/config.yaml &; N2=$!
"$BINARY" -config /tmp/netwatch-demo/node3/config.yaml &; N3=$!

echo "$N1 $N2 $N3" > /tmp/netwatch-demo/pids
echo "node-1=$N1  node-2=$N2  node-3=$N3"
```

Cluster'ın oluşmasını bekle (~5 saniye):

```bash
sleep 5
for port in 10241 10242 10243; do
  echo -n ":$port → "
  curl -s http://localhost:$port/cluster/state | python3 -c "
import json,sys
d=json.load(sys.stdin)
alive=[m['name'] for m in d['members'] if m['status']=='alive']
print(f\"{d['local_node']} sees {len(alive)} alive: {alive}\")
"
done
```

Beklenen çıktı:
```
:10241 → node-1 sees 3 alive: ['node-1', 'node-2', 'node-3']
:10242 → node-2 sees 3 alive: ['node-3', 'node-2', 'node-1']
:10243 → node-3 sees 3 alive: ['node-3', 'node-2', 'node-1']
```

---

## 4. Test Senaryoları

### Senaryo 1: Hedef durumu kontrol et

```bash
for port in 10241 10242 10243; do
  echo "--- :$port ---"
  curl -s http://localhost:$port/status | python3 -c "
import json,sys
for t in json.load(sys.stdin):
    print(f\"  {t['name']:25s} {t['status']:12s} seq={t['seq']}\")
"
done
```

`fake-down` ~25 saniye sonra `HARD_DOWN` olur (2 retry × 10s).

---

### Senaryo 2: Exactly-once alerting

Tüm node'lar aynı target'ı izler ama alarm sadece 1 kez atılmalı:

```bash
# Alarm dosyasını izle
tail -f /tmp/netwatch-demo/logs/alerts.log
```

```bash
# Kim gönderdi, kim bastırdı?
for i in 1 2 3; do
  sent=$(grep -c "sending alert" /tmp/netwatch-demo/node${i}/agent.log 2>/dev/null || echo 0)
  supp=$(grep -c "suppressed" /tmp/netwatch-demo/node${i}/agent.log 2>/dev/null || echo 0)
  echo "node-$i: sent=$sent  suppressed=$supp"
done
```

Beklenen:
```
node-1: sent=0  suppressed=1
node-2: sent=1  suppressed=0   ← primary (consistent hash ile belirlenir)
node-3: sent=0  suppressed=1
```

---

### Senaryo 3: Prometheus metrikleri

```bash
curl -s http://localhost:10241/metrics | grep -E "^network_probe_|^network_prober_" | grep -v "^#"
```

Önemli metrikler:

| Metrik | Anlam |
|---|---|
| `network_probe_local_status` | Bu node'da UP=1 / DOWN=0 |
| `network_probe_cluster_status` | Cluster konsensüsü UP=1 / DOWN=0 |
| `network_probe_local_latency_seconds` | Son probe süresi |
| `network_prober_cluster_size` | Alive üye sayısı |
| `network_prober_quorum_healthy` | Quorum var mı? 1/0 |
| `network_prober_isolated` | İzole modda mı? 1/0 |

---

### Senaryo 4: Quorum kaybı

2 node'u öldür, kalan node'un izole moda geçtiğini gözlemle:

```bash
read N1 N2 N3 < /tmp/netwatch-demo/pids
kill $N2 $N3
```

Durumu kontrol et:

```bash
sleep 8
curl -s http://localhost:10241/metrics | grep -E "^network_prober_" | grep -v "^#"
# Beklenen: quorum_healthy=0, isolated=1, cluster_size=1
```

Quorum log:

```bash
grep "quorum" /tmp/netwatch-demo/node1/agent.log | grep '"msg"' | tail -5
```

---

### Senaryo 5: İzole modda alarm üretilmez

Quorum kaybolmuşken probe döngüleri çalışmaya devam eder ama alarm atılmaz:

```bash
# 30 saniye bekle — fake-down zaten hard_down ama yeni alarm gelmemeli
sleep 30
wc -l < /tmp/netwatch-demo/logs/alerts.log
# Beklenen: hâlâ 1

grep "isolated mode" /tmp/netwatch-demo/node1/agent.log | tail -3
```

---

### Senaryo 6: Quorum recovery

Ölü node'lardan birini geri başlat:

```bash
BINARY="/Users/saidtaylan/Documents/network cluster/bin/netwatch-darwin"
"$BINARY" -config /tmp/netwatch-demo/node2/config.yaml &
```

```bash
sleep 10
curl -s http://localhost:10241/metrics | grep -E "^network_prober_" | grep -v "^#"
# Beklenen: quorum_healthy=1, isolated=0, cluster_size=2
```

Quorum recovery log:

```bash
grep "quorum" /tmp/netwatch-demo/node1/agent.log | grep '"msg"'
```

---

### Senaryo 7: Anti-entropy (re-join alarm storm yok)

Ölü node'u geri başlat — cluster state'i sync etmeli, yeni alarm üretmemeli:

```bash
BINARY="/Users/saidtaylan/Documents/network cluster/bin/netwatch-darwin"
"$BINARY" -config /tmp/netwatch-demo/node3/config.yaml &
sleep 5

# Anti-entropy logu
grep "ANTI-ENTROPY" /tmp/netwatch-demo/node3/agent.log

# Alarm sayısı değişmemiş olmalı
wc -l < /tmp/netwatch-demo/logs/alerts.log
```

---

### Senaryo 8: Graceful leave

Node'u düzgünce cluster'dan çıkar:

```bash
curl -X POST http://localhost:10243/cluster/leave
# Yanıt: {"status":"leaving"}
```

```bash
sleep 3
# Kalan node'ların listesi
curl -s http://localhost:10241/cluster/state | python3 -c "
import json,sys
d=json.load(sys.stdin)
for m in d['members']:
    print(f\"  {m['name']} [{m['status']}]\")
"
```

Kill ile öldürmekten farkı: graceful leave, cluster'a "gidiyorum" mesajı gönderir ve diğer node'lar onu hemen çıkarır. Kill'de memberlist suspect→dead geçişini beklemek gerekir (~10-30s).

---

### Senaryo 9: Logları canlı izle

3 node'un logunu yan yana izlemek için 3 terminal aç:

```bash
# Terminal 1
tail -f /tmp/netwatch-demo/node1/agent.log | grep -v "memberlist"

# Terminal 2
tail -f /tmp/netwatch-demo/node2/agent.log | grep -v "memberlist"

# Terminal 3
tail -f /tmp/netwatch-demo/node3/agent.log | grep -v "memberlist"

# Terminal 4 - sadece alarmlar
tail -f /tmp/netwatch-demo/logs/alerts.log
```

---

## 5. Durdurma

```bash
# Graceful (tercih edilen — cluster'a bildirir)
curl -X POST http://localhost:10241/cluster/leave
curl -X POST http://localhost:10242/cluster/leave
curl -X POST http://localhost:10243/cluster/leave

# Zorla (test sonunda temizlik)
pkill -9 -f "netwatch-darwin"
```

---

## 6. Config Dosyası — Her Alanın Açıklaması

```yaml
# ── Agent kimliği ─────────────────────────────────────────────────────────────

app_name: "netwatch-agent"
# Prometheus metrik label'larında ve alert env var'larında görünür.
# Birden fazla agent kullanıyorsan bunları farklılaştırır.
# APP_NAME env değişkeni olarak alert script'e aktarılır.

port: "10240"
# HTTP sunucu portu. Dışarıdan erişilmesi gereken port:
#   /metrics  → Prometheus scrape
#   /health   → liveness probe (Kubernetes, load balancer)
#   /status   → insan okunabilir JSON durum
#   /cluster/state → cluster üyeleri ve peer state'leri

# ── Dosya yolları ─────────────────────────────────────────────────────────────

state_file: "state.json"
# Restart sonrasında hedef durumlarını (UP/DOWN/seq) hatırlamak için kullanılır.
# Olmadan: her restart'ta tüm target'lar "yeni down" gibi davranır → alarm storm.
# Cluster modunda: yeniden katılacak node bu dosyadan kendi son state'ini okur,
# anti-entropy ile karşılaştırır, gerekirse günceller.
# v2 formatı: {"version":2,"targets":{"id":{"state":"hard_down","seq":3,...}}}

log_path: ""
# Boş bırakılırsa stdout'a yazar. Dosya yolu verilirse o dosyaya yazar.
# Üretimde: "/var/log/netwatch/agent.log" veya journald için boş bırak.

credentials_file: "credentials.env"
# KEY=VALUE formatında satırlar içeren dosya.
# config.yaml içindeki ${VAR} ifadeleri buradan çözümlenir.
# Sonra sistem ortam değişkenlerine bakılır (os.LookupEnv).
# Şifre, token gibi hassas bilgileri config.yaml'a yazmamak için kullanılır.
# chmod 600 yapılmalı, .gitignore'a eklenmeli.

# ── Probe zamanlaması ─────────────────────────────────────────────────────────

timeout: 3
# Her probe için bağlantı zaman aşımı (saniye).
# TCP: connect timeout. HTTP: toplam istek süresi. DNS: sorgu süresi.
# Düşük tutulursa yavaş hedefler hatalı down görünebilir.
# Yüksek tutulursa geciken probe diğerlerini bloklamaz (her target ayrı goroutine).

max_retries: 2
# Probe başarısız olursa kaç kez daha denensin (soft-down süreci).
# max_retries=2 → toplam 3 başarısız probe → hard_down.
# Geçici ağ titremelerini alarm'a dönüştürmemek için en az 2 önerilir.

retry_interval_sec: 10
# Retry'lar arası bekleme süresi.
# hard_down'a geçiş süresi: max_retries × retry_interval_sec
# Örnek: 2 × 10 = 20 saniye sonra hard_down ve alarm.

probe_interval_sec: 15
# Her başarılı probdan sonra ne kadar beklensin.
# Her target için ayrıca interval_sec ile override edilebilir.
# Üretimde kritik servisler için 30-60s, DNS/ping için 60-300s önerilir.

ticker_interval_sec: 3
# İç zamanlayıcı çözünürlüğü. Probe zamanı gelen target'ları bu sıklıkta kontrol eder.
# 2'nin altına düşürme — CPU kullanımı artar, fayda sağlamaz.

reload_interval_sec: 30
# config.yaml'ı bu sıklıkta yeniden okur (hot-reload).
# 0 = devre dışı. Üretimde 30-60s önerilir.
# Cluster ayarları (keyring, peers) reload'da güncellenmez — restart gerekir.

watchdog_threshold_sec: 0
# Prometheus bu kadar saniye boyunca /metrics'i çekmeyi bırakırsa uyarı verir.
# 0 = devre dışı. Üretimde 2-3 × scrape_interval önerilir (örn. 120s).
# Probları durdurmaz — sadece "Prometheus körleşti" uyarısı verir.
# Log: [WATCHDOG] Prometheus scrape not detected
# Metrik: network_probe_prometheus_connected=0

# ── Bildirim kanalları ────────────────────────────────────────────────────────

notifications:
  kanal-adi:
    type: script       # script | mail | webhook

    # script: Shell script çalıştırır. Tüm alert bilgileri env var olarak aktarılır.
    parameters:
      script: "/path/to/alert.sh"
      # .sh uzantısı otomatik eklenir; belirtmesen de olur.
      # Script yoksa "alert script not found" hatası alırsın.

    # mail: Go SMTP istemcisi ile doğrudan e-posta gönderir.
    # parameters:
    #   smtp_host:    "smtp.example.com"
    #   smtp_port:    "587"
    #   from:         "netwatch@example.com"
    #   to:           "ops@example.com"
    #   tls_mode:     "starttls"   # starttls | tls | none
    #   tls_insecure: "false"
    #   username:     "${SMTP_USER}"
    #   password:     "${SMTP_PASS}"

    # webhook: HTTP POST gönderir.
    # parameters:
    #   url:          "https://hooks.slack.com/..."
    #   format:       "generic"   # generic | alertmanager
    #   timeout_sec:  "10"
    #   tls_insecure: "false"
    #   header_Authorization: "Bearer ${TOKEN}"   # header_<İsim> custom header
    #   username: "user"   # HTTP Basic Auth
    #   password: "${PASS}"

default_notify:
  - kanal-adi
# Target'ta notify: tanımlı değilse ve app referansı da yoksa bu kanallar kullanılır.
# Liste boş bırakılırsa alarm sessizce bastırılır (info log).

# ── Hedefler ─────────────────────────────────────────────────────────────────

targets:
  - id:   "stable-id"     # Opsiyonel ama önerilir. App.uses ve state.json'da anahtar olarak kullanılır.
                           # id yoksa name kullanılır — name değişirse state kaybedilir.
    name: "Görünen Ad"     # /status, alert ve metriklerde görünür.
    type: tcp              # tcp | http | ping | dns | sql
    target: "host:port"
    interval_sec: 30       # Bu target için probe_interval_sec'i override eder. Opsiyonel.
    notify: ["kanal-adi"]  # Bu target için özel kanallar. Opsiyonel; yoksa default_notify.
    options: {}            # Probe tipine özgü seçenekler (aşağıya bak).

# TCP seçenekleri: yok (sadece connect kontrolü yapılır).

# HTTP seçenekleri:
#   method:           "GET"        # varsayılan GET
#   expected_status:
#     eq:  200                     # tam eşleşme
#     in:  [200, 204]              # değer listesi
#     lt:  400                     # küçük
#     lte: 299                     # küçük veya eşit
#     gt:  199                     # büyük
#     gte: 200                     # büyük veya eşit
#     between: [200, 299]          # aralık (her ikisi dahil)
#   body_contains:    "\"ok\""     # yanıt gövdesinde bu string olmalı
#   body_not_contains: "error"
#   follow_redirects: true
#   timeout_sec: 10
#   headers:
#     Authorization: "Bearer ${TOKEN}"

# DNS seçenekleri:
#   nameserver:   "8.8.8.8:53"   # boş bırakılırsa OS resolver
#   expected_ips:
#     - "10.0.1.10"               # en az biri dönmeli

# SQL seçenekleri:
#   driver:      "postgres"       # postgres | mysql | oracle | mssql
#   username:    "${DB_USER}"
#   password:    "${DB_PASS}"
#   database:    "mydb"
#   ssl_mode:    "require"        # postgres: disable|require|verify-full
#   tls_insecure: "false"
#   query:       "SELECT 1"       # bağlantı sonrası doğrulama sorgusu (opsiyonel)
#   service_name: "PROD"          # oracle için

# ── Uygulamalar (opsiyonel) ───────────────────────────────────────────────────

# apps ile target'ları servis/ekip bazında gruplandırabilirsin.
# Alarm atılırken AFFECTED_APPS ve OWNER_TEAMS env var'ları eklenir.
# Bildirim kanalları: union(app.notifications, target.notify)

# apps:
#   - name:       "payment-gateway"
#     owner_team: "fintech-sre"
#     uses:
#       - "stable-id"         # target'ın id'si (veya name'i)
#     notifications:
#       - "email-ops"

# ── Cluster ───────────────────────────────────────────────────────────────────

cluster:
  enabled: true
  # false → tüm cluster kodu devre dışı; standalone modda çalışır.

  node_name: "node-1"
  # Cluster içinde benzersiz olmalı. /cluster/state'de ve gossip'te görünür.
  # Genellikle hostname veya IP ile aynı yapılır.

  bind_addr: "127.0.0.1"
  # Gossip socket'inin dinleyeceği IP.
  # Üretimde: "0.0.0.0" veya arayüzün IP'si.

  bind_port: 7946
  # TCP+UDP gossip portu. Her node farklı porta sahip olabilir.
  # Güvenlik duvarında hem TCP hem UDP açılmalı.

  advertise_addr: "127.0.0.1"
  advertise_port: 7946
  # Diğer node'ların bu node'a ulaşmak için kullanacağı adres.
  # NAT veya container arkasındaysa bind_addr'den farklı olabilir.
  # advertise_port mutlaka bind_port ile aynı olmalı — aksi halde
  # UDP probe'lar yanlış porta gider, node'lar birbirini "dead" görür.

  peers:
    - "192.168.1.101:7946"
    - "192.168.1.102:7946"
  # Seed node'lar — join sırasında bağlanmayı dener.
  # Hepsinin listelenmesi gerekmez; bir tane erişilebilir olsa yeter.
  # İlk başlatmada hepsi eş zamanlı kalkarsa "connection refused" normal —
  # arka planda 5s'de bir yeniden denenir (rejoin loop).

  keyring:
    - "+ioC2+ihDEDREdGHjiCT1yp5UwCSDFwSAUc5RQCPxec="
  # AES gossip şifreleme anahtarı (base64, 16/24/32 byte).
  # Yeni anahtar üret: python3 -c "import base64,os; print(base64.b64encode(os.urandom(32)).decode())"
  # Key rotation (sıfır kesinti):
  #   1. Tüm node config'lerine yeni anahtarı BAŞA ekle
  #   2. Reload (hot-reload ya da restart)
  #   3. Tüm node'lar güncellenince eski anahtarı kaldır

  expected_node_count: 3
  # Quorum hesabı için beklenen toplam node sayısı.
  # Gerekli minimum: floor(expected_node_count × min_quorum_ratio) + 1

  min_quorum_ratio: 0.5
  # Alarm atabilmek için gereken minimum alive node oranı.
  # 0.5 = basit çoğunluk (3 node'da en az 2 alive gerekir).
  # Bu eşiğin altına düşülürse: isolated=1, quorum_healthy=0, alarm bastırılır.
```

---

## 7. HTTP Endpoint Referansı

| Endpoint | Method | Açıklama |
|---|---|---|
| `/health` | GET | Her zaman 200 döner. Kubernetes liveness probe için. |
| `/metrics` | GET | Prometheus Exposition Format. |
| `/status` | GET | Tüm target'ların JSON durumu: name, status, seq, error_code. |
| `/cluster/state` | GET | Cluster üyeleri + peer'lardan gelen gossip state'leri. Cluster kapalıysa 503. |
| `/cluster/leave` | POST | Graceful leave — cluster'a bildirir, sonra kapanır. |

---

## 8. Alert Script Ortam Değişkenleri

Script çalıştırıldığında şu env var'lar her zaman mevcuttur:

| Değişken | Örnek Değer | Açıklama |
|---|---|---|
| `NAME` | `Payments DB` | Target'ın görünen adı |
| `TARGET` | `db.internal:5432` | Hedef adres |
| `HOST` | `db.internal` | Parse edilmiş host |
| `PORT` | `5432` | Parse edilmiş port |
| `STATUS` | `unreachable` veya `reachable` | Alarm türü |
| `TYPE` | `tcp` | Probe tipi |
| `SEQ` | `3` | Lamport sequence — her state geçişinde artar |
| `ERROR_CODE` | `connection refused` | Son hata; recovery'de boş |
| `NODE_NAME` | `saidtaylan.local` | Bu ajanı çalıştıran sunucunun hostname'i |
| `APP_NAME` | `netwatch-agent` | Config'deki app_name değeri |
| `SCOPE` | `GLOBAL`, `NODE_LOCAL`, `STANDALONE` | Cluster'da kaç node'un down gördüğü |
| `AFFECTED_APPS` | `payment-gateway,inventory` | apps: tanımlıysa dolu |
| `OWNER_TEAMS` | `fintech-sre,logistics` | apps: tanımlıysa dolu |

`SCOPE` değerleri:
- `STANDALONE` — cluster yok, tek node
- `NODE_LOCAL` — sadece bu node down görüyor (ağ bölünmesi?)
- `GLOBAL` — tüm node'lar down görüyor (servis gerçekten çöktü)

---

## 9. State Machine

```
İlk başlatma → UNKNOWN
    ↓ probe başarısız
SOFT_DOWN (RAM'de, henüz state.json'a yazılmaz)
    ↓ max_retries doldu
HARD_DOWN (state.json'a yazılır, alarm atılır)
    ↓ probe başarılı
UP (alarm "reachable" ile atılır, state güncellenir)
```

`/status` çıktısındaki `seq` alanı her UP↔DOWN geçişinde artar.  
Alert script'teki `$SEQ` ile hangi alarm dalgasında olduğunu takip edebilirsin.

---

## 10. Sık Yapılan Hatalar

| Belirti | Neden | Çözüm |
|---|---|---|
| Node'lar birbirini görmüyor | `advertise_port` bind_port'tan farklı | Her ikisini aynı değere eşitle |
| Başlarken "connection refused" | Tüm node'lar eş zamanlı kalktı | Normal — 5s'de bir rejoin dener, 30s içinde toparlar |
| Çift alarm geliyor | Eski binary — secondary de atıyordu | `make build-darwin` ile yeniden derle |
| `${VAR}` unresolved hatası | Yorum satırında `${...}` var | Satır başına `#` koy; inline yorum `value # ${VAR}` şeklinde olursa sorun yok |
| `alert script not found` | Script yolu yanlış veya +x yok | `chmod +x script.sh`; config'de tam yol ver |
| Port zaten kullanımda | Eski process arka planda çalışıyor | `pkill -9 -f netwatch-darwin` |

---

## 11. Yeni Şifreleme Anahtarı Üret

```bash
python3 -c "import base64, os; print(base64.b64encode(os.urandom(32)).decode())"
```

Çıktıyı config.yaml'daki `keyring` listesine yaz. Tüm cluster node'larının aynı anahtara sahip olması gerekir.
