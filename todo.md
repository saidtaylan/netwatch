# netwatch — Sonra Yapılacak Backend Özellikler

Bu dosya UI yapımı sırasında **yapılmayacak** ama UI sonrası backend gelişimi için aday özellikleri tutar.

> Önceki içerik (Distributed Probe Ownership Q&A) git history'de korunuyor. Eğer o içeriğe ihtiyaç varsa `git log --all -- todo.md` ile bulunabilir, GUIDE.md'ye taşınabilir.

Notlar:
- Tahmini eforlar 1 kişilik geliştirme süresidir (test + doküman dahil)
- "Etki" = "operatörün gerçekten hissettiği değer"
- "Bağımlılık" = bu feature'ın çalışması için önce başka neyin lazım olduğu

---

## Top Tier — Operatörler gerçekten hisseder

### B1. Silence Rules (Alertmanager tarzı)

**Tanım:** Maintenance window'un genelleştirilmiş hâli. Label-based selector ile alarmları geçici olarak bastır.

**API tasarımı (öneri):**
```
PUT /cluster/silences
{
  "matchers": [
    {"key": "severity", "value": "warning", "op": "eq"},
    {"key": "team", "value": "fintech", "op": "eq"},
    {"key": "target_id", "value": "db-.*", "op": "regex"}
  ],
  "duration": "2h",
  "reason": "Known issue with monitoring",
  "started_by": "ops@company"
}
```

**Maintenance window ile ilişki:** Maintenance bu özelliğin "by target_id" özel hâlidir. İkisi aynı altyapıyı paylaşabilir. F3 (maintenance) zaten gossip + persist çatısını verdi.

**Etki:** Yüksek. Maintenance'tan daha esnek (incident sırasında "tüm db-* alarmlarını sustur" gibi).

**Tahmini efor:** 4-6 saat. F3'ün altyapısını genellersek hızlanır.

**Bağımlılık:** Label sistemine ihtiyaç var — Target'ların label'a sahip olması (şu an `tags`/`labels` alanı yok). Önce target'a `labels: {key: value}` ekle.

---

### B2. Severity Levels (critical / warning / info)

**Tanım:** Her alarm "eşit önemli" değil. Her target'a severity ata, notification routing severity'ye göre değişsin.

**Config tasarımı:**
```yaml
targets:
  - id: "db-primary"
    severity: critical
    notify: ["pagerduty"]

  - id: "test-env-api"
    severity: warning
    notify: ["slack-warnings"]

  - id: "cert-expiry-30d"
    severity: info
    notify: ["email-weekly"]
```

**Alert env'ine ek:**
```
SEVERITY=critical
```

**Notification kanalları severity'ye göre route edilebilsin:**
```yaml
notifications:
  pagerduty:
    type: webhook
    parameters:
      url: "..."
      # OPSİYONEL — sadece bu severity'lerdeki alarmları kabul et
      severity_filter: ["critical"]
```

**Etki:** Yüksek. Production'da olmazsa olmaz.

**Tahmini efor:** 3-4 saat.

**Bağımlılık:** Yok. Geriye uyumlu (severity belirtilmezse "info" default).

---

### B3. Latency-Based Alerting

**Tanım:** Şu an binary up/down. "Response time > 500ms" diye alarm yok. Prometheus metric'i (`network_probe_local_latency_seconds`) ölçülüyor ama netwatch kendisi bundan alarm üretmiyor.

**Config tasarımı:**
```yaml
targets:
  - id: "api"
    type: http
    target: "https://api.company.com/health"
    options:
      expected_status: {in: [200]}
      latency_thresholds:
        warning_ms: 500              # 500ms üstü warning
        critical_ms: 1000            # 1s üstü critical (target=hard_down eşdeğeri)
        consecutive_breaches: 3      # 3 ardışık ihlal sonrası alarm
```

**Yeni state:** `latency_breach` (UP ama yavaş)
- Up + latency içinde: UP
- Up + latency_warning_ms aşılmış: LATENCY_WARNING (her ihlal alarm değil, eşik gerekli)
- Up + latency_critical_ms aşılmış: LATENCY_CRITICAL (= hard_down ile aynı pipeline)

**Etki:** Yüksek. "Servis up gözüküyor ama kullanıcılar yavaş diyor" en yaygın gerçek dünya senaryosu.

**Tahmini efor:** 5-7 saat. Yeni state machine path + metric + alert env enrichment.

**Bağımlılık:** Severity (B2) ile birlikte daha temiz olur (latency_warning → warning severity).

---

## Middle Tier — Faydalı

### B4. Recurring Maintenance (cron-based)

**Tanım:** F3 sadece ad-hoc maintenance. Cron-based recurring desteği yok.

**API tasarımı:**
```
PUT /cluster/maintenance
{
  "target_ids": ["db-primary"],
  "recurring": {
    "cron": "0 2 * * 0",          # her pazar 02:00
    "duration": "2h",
    "timezone": "Europe/Istanbul"
  },
  "reason": "Weekly backup window"
}
```

**Etki:** Orta. Operasyonel ekipler için kullanışlı ama mevcut ad-hoc API ile aynı sonuç manuel olarak alınabilir.

**Tahmini efor:** 3-4 saat. `github.com/robfig/cron/v3` dependency + maintenance manager'a cron evaluator ekle.

---

### B5. Alert Acknowledge / Mute

**Tanım:** Operatör "şu anki alarmı gördüm, X süre sus" der.

```
POST /alerts/{alert_id}/ack
{
  "acknowledged_by": "alice",
  "mute_duration": "1h"           # opsiyonel
}
```

**Storage:** Acknowledged alert ID'leri RAM + disk (silence rules ile aynı altyapı).

**Workflow:**
1. Alarm gelir → channel'a gönderilir
2. Operatör UI/CLI ile ack eder
3. Mute süresi içinde aynı target için yeni alarm gönderilmez
4. Yeni hard_down geçişi (recovery sonrası tekrar down) ack'i sıfırlar

**Etki:** Orta. UI olmadan zor kullanılır (CLI ack mümkün ama awkward).

**Tahmini efor:** 4-5 saat.

**Bağımlılık:** Alert history endpoint (yeni — şu an alarmlar disk'e yazılmıyor, sadece script log'a). Önce alert persistence gerek.

---

### B6. gRPC Health Check Probe Type

**Tanım:** Modern microservice'ler için gRPC health check protocol. Standart bir protokol var: `grpc.health.v1.Health/Check`.

**Config:**
```yaml
targets:
  - id: "user-service"
    type: grpc
    target: "user-service:50051"
    options:
      service: "user.UserService"   # opsiyonel: belirli service
      tls: false
```

**Implementation:** `google.golang.org/grpc/health/grpc_health_v1` dependency.

**Etki:** Orta — Yüksek (k8s ortamlarında çok yaygın).

**Tahmini efor:** 2-3 saat.

---

### B7. Audit Log (cluster için kritik)

**Tanım:** Kim ne zaman ne yaptı: config push, maintenance, leave, keyring rotation, silence rule.

**Storage:** `<state_file_dir>/audit.log` — JSON lines, append-only.

**Entry örneği:**
```json
{
  "ts": "2026-05-20T15:30:00Z",
  "node": "node-01",
  "action": "maintenance.set",
  "actor": "ops@company",
  "target_ids": ["db-primary"],
  "duration": "2h",
  "ip": "10.0.1.5"
}
```

**Gossip yayımı:** Audit entry'ler de gossip ile diğer node'lara replicate edilir (eventual consistency). Her node kendi audit.log'unu tutar; istenirse `/cluster/audit` endpoint'i aggregated view döner.

**Etki:** Yüksek (compliance/post-mortem için).

**Tahmini efor:** 5-7 saat. Her admin endpoint'i hook'lamak + gossip + endpoint.

---

## Lower Tier — Nice to have

### B8. Synthetic Transaction Probe

**Tanım:** Multi-step HTTP probe. "POST /login → token al → GET /profile (Bearer token) → 200 ve body kontrolü."

**Config:**
```yaml
targets:
  - id: "user-login-flow"
    type: synthetic
    target: "https://api.company.com"
    options:
      steps:
        - method: POST
          path: /login
          body: '{"user":"test","pass":"secret"}'
          extract:
            token: "$.access_token"
        - method: GET
          path: /profile
          headers:
            Authorization: "Bearer {{.token}}"
          expected_status: [200]
```

**Etki:** Yüksek (gerçek user journey'leri test etmek), ama config karmaşıklığı yüksek. Genellikle ayrı bir tool (Cypress, Playwright) tercih edilir.

**Tahmini efor:** 1-2 gün. JSONPath + template engine + multi-step state.

---

### B9. JSONPath Body Assertion

**Tanım:** HTTP probe için `body_contains` substring match. JSONPath ile structured response assertion eksik.

**Config:**
```yaml
options:
  body_json_match:
    "$.status": "healthy"
    "$.version": "1.0"
    "$.replicas[*].state": "ready"   # all elements
```

**Etki:** Orta. `body_contains` + regex çoğu durumu kapsar.

**Tahmini efor:** 2-3 saat. `github.com/PaesslerAG/jsonpath` dependency.

---

### B10. Pre-built Grafana Dashboard

**Tanım:** netwatch metric'lerini gösteren hazır Grafana dashboard JSON. Operatörün sıfırdan tasarlamasına gerek kalmaz.

**Dashboard panel'leri:**
- Cluster overview (size, quorum, isolated)
- Target up/down map
- SLO uptime ratio + budget burndown
- Latency heatmap
- Alert rate over time

**Etki:** Düşük-Orta. Sadece JSON dosyası, kod değişimi yok. Tools/scripts ile auto-generate edilebilir.

**Tahmini efor:** 4-6 saat. JSON manuel + iterasyon.

---

### B11. PagerDuty/OpsGenie Native Integrations

**Tanım:** Şu an webhook ile gidiyor — operatör JSON formatı elle yazıyor (Events API v2 format). Direkt integration daha kolay olur.

**Config:**
```yaml
notifications:
  pagerduty:
    type: pagerduty
    parameters:
      integration_key: "${PD_KEY}"
      auto_resolve: true              # recovery alarmında PD incident'i resolve et

  opsgenie:
    type: opsgenie
    parameters:
      api_key: "${OPSGENIE_KEY}"
      region: "eu"
      team: "fintech-sre"
```

**Etki:** Düşük (webhook zaten çalışıyor), ama operatör deneyimi daha iyi.

**Tahmini efor:** 3-4 saat / integration. Her biri test ortamı gerektirir.

---

## Önceliklendirme Matrisi

| ID | İsim | Tier | Efor | Etki | Bağımlılık |
|----|------|------|------|------|-----------|
| B1 | Silence Rules | Top | 4-6h | Yüksek | Target labels |
| B2 | Severity Levels | Top | 3-4h | Yüksek | — |
| B3 | Latency Alerting | Top | 5-7h | Yüksek | B2 (öneri) |
| B4 | Recurring Maintenance | Mid | 3-4h | Orta | cron lib |
| B5 | Alert Ack/Mute | Mid | 4-5h | Orta | Alert history endpoint |
| B6 | gRPC Probe | Mid | 2-3h | Orta-Y | grpc lib |
| B7 | Audit Log | Mid | 5-7h | Yüksek | — |
| B8 | Synthetic Probe | Low | 8-16h | Yüksek | JSONPath + template |
| B9 | JSONPath Body | Low | 2-3h | Orta | JSONPath lib |
| B10 | Grafana Dashboard | Low | 4-6h | Düşük-Orta | — (JSON only) |
| B11 | PD/OpsGenie native | Low | 3-4h | Düşük | — |

Ayrıca bekleyen önceki sprint:
| F5 | Kubernetes Service Discovery | Detay sprint.md'de | 3-4 gün | Yüksek (k8s) | client-go dep |

---

## UI Sonrası Önerilen Sıra

UI bittikten sonra ilk turda alınacak 3 madde (**Production Polish** sprint'i):

1. **B2 (Severity)** — UI'ın target listesinde renk kodu için zaten lazım
2. **B1 (Silence Rules)** — UI'da "Silence" butonu olmalı, bunsuz maintenance gibi tek tek girilir
3. **B7 (Audit log)** — UI'da "Activity feed" / "Recent changes" göstergesi için lazım

İkinci tur (**Probe Expansion** sprint'i):

4. **B3 (Latency Alerting)** — UI'da SLO panel'inin latency burndown'u için
5. **B6 (gRPC Probe)** — k8s deploymentlerinde sıkça istenir

Üçüncü tur (**Operational** sprint'i):

6. **B5 (Ack/Mute)** — UI alert detail page'inde "Acknowledge" butonu
7. **B4 (Recurring Maintenance)** — maintenance UI page'inde "Schedule" tab'ı

Geri kalanlar (B8/B9/B10/B11) opportunistic — ihtiyaç ortaya çıktıkça.
