# netwatch — Test Suite

Bu dizin netwatch'ın birim ve domain testlerini içerir.  
Mevcut `test/integration/` testlerinden bağımsız olarak çalışır.

---

## Dizin Yapısı

```
tests/
  run.sh              ← tek giriş noktası (tüm testleri çalıştırır)
  README.md           ← bu dosya
  engine/             ← engine paketinin siyah-kutu birim testleri
    config_test.go      ValidateConfigFile: tüm validation dalları
    apps_test.go        App→Target indirection: duplicate, missing ref, valid
    shared_config_test.go  AppliedFields, SharedConfig struct alanları
    join_test.go        GenerateKeyringKey: format, uzunluk, benzersizlik
  cluster/            ← cluster paketinin birim testleri
    config_test.go      cluster.Config.Validate: port, keyring, zone, factor
    configsync_test.go  ConfigHashOf: deterministik hash, hex format
    gossip_test.go      GossipPayload & ConfigPushPayload serileştirme
  domain/             ← domain-driven uçtan uca test senaryoları
    alert_flow_test.go         TCP down→hard_down→alert→recovery tam döngüsü
    state_persistence_test.go  state.json v2 formatı, restart güvencesi
    shared_config_test.go      Config push/sync lifecycle, credential isolation
```

---

## Testleri Çalıştırma

### Tek komut — tümü

```bash
./tests/run.sh
```

veya Makefile ile:

```bash
make test-unit      # tests/engine + tests/cluster (hızlı, I/O yok)
make test-domain    # tests/domain (gerçek TCP portları, ~90s)
make test-all       # tüm suit (internal + tests/ + integration)
```

### Seçici çalıştırma

```bash
# Sadece hızlı birim testleri
./tests/run.sh unit

# Sadece domain testleri
./tests/run.sh domain

# Dahili testler (internal/engine + internal/cluster, 195 test)
./tests/run.sh internal

# Tek dosya
go test -race -count=1 -timeout 60s ./tests/engine/ -run TestConfig_

# Tek test fonksiyonu
go test -race -v -run TestAlertFlow_TCPDown_TriggersUnreachable ./tests/domain/
```

---

## Test Kategorileri

### Birim Testleri (`tests/engine/`, `tests/cluster/`)

Ağ bağlantısı yoktur, goroutine başlatılmaz. Milisaniyelerde tamamlanır.

| Dosya | Test Sayısı | Ne Testler |
|---|---|---|
| `engine/config_test.go` | ~20 | Config yükleme, validation dalları (node_alias, admin token, cluster enabled/disabled, keyring format, timeout/retry aralıkları) |
| `engine/apps_test.go` | ~8 | App tekrar adı, bilinmeyen target referansı, boş uses, tanımsız kanal, geçerli apps |
| `engine/shared_config_test.go` | ~12 | AppliedFields doğruluğu, SharedClusterConfig alanları, AlertChannelConfig |
| `engine/join_test.go` | ~6 | GenerateKeyringKey: base64, 32 byte uzunluk, benzersizlik, cluster keyring uyumu |
| `cluster/config_test.go` | ~10 | Cluster.Config.Validate: enabled/disabled, bind port, replication factor, keyring uzunluğu |
| `cluster/configsync_test.go` | ~9 | ConfigHashOf: aynı→aynı hash, farklı→farklı, boş, büyük config, tek byte fark |
| `cluster/gossip_test.go` | ~7 | GossipPayload ve ConfigPushPayload JSON round-trip, soft_down durumu, unknown fields |

### Domain Testleri (`tests/domain/`)

Gerçek `net.Listener` başlatır ve engine'i çalıştırır. Her test ~5-15 saniye sürer.

| Dosya | Test Sayısı | Ne Testler |
|---|---|---|
| `domain/alert_flow_test.go` | ~9 | TCP down→unreachable, recovery→reachable, NODE_ALIAS env, APP_NAME backward compat, SEQ artışı, ERROR_CODE temizlenmesi, AFFECTED_APPS enrichment |
| `domain/state_persistence_test.go` | ~4 | state.json v2 formatı, restart sonrası duplicate alarm yok, restart+recovery→reachable, SEQ sürekliliği |
| `domain/shared_config_test.go` | ~4 | ApplySharedConfigJSON node-specific alanları korur, shared alanları günceller, credential sızdırmaz, AppliedFields tüm alanlar |

**Toplam: ~90 test** (ayrıca `internal/` altında 195 test + `test/integration/` altında 31 test)

---

## Domain Test Mimarisi

Domain testleri şu örüntüyü izler:

```go
// 1. Gerçek bir TCP listener başlat (ya da kasıtlı olarak başlatma → port kapalı)
port := freePort(t)
addr := "127.0.0.1:" + strconv.Itoa(port)

// 2. Alert'ları yakalayan runner oluştur
runner, alerts := alertCapture()

// 3. Engine'i başlat
startEngine(t, domainConfig(t, addr), runner)

// 4. Beklenen alert'ı bekle
a := waitAlert(t, alerts, "unreachable", 15*time.Second)
```

`t.Cleanup(func() { e.Shutdown() })` sayesinde test goroutine sızıntısı yoktur.

---

## Timing Parametreleri

Domain testleri şu değerleri kullanır:

```yaml
timeout:            2    # probe timeout saniye
max_retries:        1    # 1 retry → hızlı hard-down
retry_interval_sec: 5    # minimum geçerli değer
probe_interval_sec: 5    # minimum geçerli değer
ticker_interval_sec: 1   # iç scheduler çözünürlüğü
```

Hard-down için beklenen süre: `probe_interval_sec + (max_retries × retry_interval_sec) + overhead` ≈ 11-15 saniye.

---

## Yeni Test Yazma Kılavuzu

### Birim testi (tests/engine/)

```go
package engine_test

import (
    "testing"
    "github.com/saidtaylan/netwatch/internal/engine"
)

func TestConfig_MyNewBehavior(t *testing.T) {
    path := writeConfig(t, `
port: "19900"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications: {}
default_notify: []
targets: []
# ... ek alanlar
`)
    cfg, err := engine.ValidateConfigFile(path)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    // assert cfg fields...
}
```

### Domain testi (tests/domain/)

```go
package domain_test

func TestAlertFlow_MyScenario(t *testing.T) {
    port := freePort(t)
    addr := "127.0.0.1:" + strconv.Itoa(port)
    runner, alerts := alertCapture()
    startEngine(t, domainConfig(t, addr), runner)
    a := waitAlert(t, alerts, "unreachable", 15*time.Second)
    // assert a.env["MY_VAR"] ...
}
```

### Internal white-box testi

İç fonksiyon (lowercase) testlemek için `internal/engine/` veya `internal/cluster/` altına `*_test.go` ekle, `package engine` bildirimini kullan:

```go
// internal/engine/myfeature_test.go
package engine

func TestMyInternalFunc(t *testing.T) {
    result := myInternalFunc(...)
    // ...
}
```

---

## Coverage Durumu

| Paket | Mevcut Kapsam | Kritik Düşük Alanlar |
|---|---|---|
| `internal/engine` | ~17% | `loop.go` %4, `notify.go` %18 |
| `internal/cluster` | ~54% | Genel iyi |

`tests/domain/` testleri `loop.go` ve `notify.go`'yu gerçek çalışma yolu üzerinden kapsar (probe döngüsü + alert gönderim). Daha yüksek satır kapsama için `internal/engine/loop_test.go` ve `internal/engine/notify_test.go` white-box test dosyaları eklenebilir.
