# netwatch — Distributed Probe Ownership: Soru & Cevap

Bu belge, cluster modunda dağıtık probe atamasının nasıl çalıştığına dair en sık sorulan soruları ve teknik mekanizmaları açıklar. README'deki ilgili bölümlerin daha derin açıklamasıdır.

---

## Temel Çalışma Prensibi

### "50 node varsa sadece 3'ü probe eder" derken tam olarak ne oluyor?

Diyelim ki cluster'da 50 node var ve `probe_replication_factor: 3`. `payments-db` target'ı için sistem şu üç şeyi yapar:

1. **Aday listesi (candidate set):** `payments-db`'yi kendi config'inde tanımlayan ve o an alive olan tüm node'lar aday olur. Gossip'te her node kendi target listesini yayar, bu yüzden ekstra mesaj gerektirmez.

2. **Hash ring seçimi:** Aday listesi isim sırasına göre sıralanır. `FNV-32a("payments-db")` hash'i hesaplanır. Sıralı listenin neresinden başlanacağı bu hash'le belirlenir. Başlangıç noktasından itibaren zone-aware algoritma `probe_replication_factor` kadar node seçer. Bu hesaplama her node'da bağımsız yapılır — ağ konuşması gerekmez, her node aynı sonuca ulaşır.

3. **Probe ya da dinle:** Seçilen 3 node kendi probe goroutine'ini başlatır ve `payments-db`'ye bağlantı açar. Seçilmeyen 47 node ise `startProbeLoop` çağrısına bile girmez — goroutine başlatmazlar.

---

### Seçilmeyen 47 node ne yapar?

Pasif dinleyicidirler. Yapabilecekleri şunlardır:

- Gossip kanalından gelen `GossipPayload` mesajlarını alırlar: "node-A, payments-db'yi hard-down gördü, seq=3"
- Bu bilgiyi kendi `peerStates` map'lerine kaydederler
- `/status`, `/fleet/status`, `/cluster/state` endpoint'lerinde bu bilgiyi gösterirler
- `SCOPE` ve `CLASSIFICATION` hesaplamalarına katkı sağlarlar (peer state'lerini okuyarak)

**Yapamazlar:**
- `payments-db`'ye bağlantı açmazlar
- Alarm gönderemezler (hem responsible değiller hem de local probe'ları yok)
- Probe sonucuna bağlı olarak goroutine başlatmazlar

Kısacası: 47 node olayı *gözlemler*, 3 node *ölçer*.

---

### Target down olduğunda 47 node haberdar mı oluyor? Onlar da probe etmeye başlıyor mu?

Haberdar oluyorlar ama probe etmeye başlamıyorlar.

Şu olur:

1. 3 probe node'undan biri (diyelim node-A) `payments-db`'yi hard-down ilan eder.
2. Node-A `Broadcast()` çağırır — gossip kanalına bir `GossipPayload` kuyruklar.
3. Sonraki gossip round'unda (~200ms) bu payload 3 random peer'a UDP olarak gönderilir. Onlar da 3'er peer'a. Birkaç round içinde tüm 50 node haberdar olur.
4. Her node `NotifyMsg()` callback'inde bu payload'ı alır, `peerStates["payments-db"]["node-A"] = hard_down, seq=3` şeklinde kaydeder.
5. **Hiçbir node bu noktada probe goroutine başlatmaz.** Probe atama `recomputeProberAssignments()` tarafından yönetilir ve bu fonksiyon yalnızca cluster membership değiştiğinde çalışır (node join/leave). Target'ın durumu ne olursa olsun probe ataması değişmez.

**İstisna: Prober node'u cluster'dan ayrılırsa ne olur?**

Eğer 3 prober'dan biri cluster'dan ayrılırsa (`NotifyLeave` tetiklenir), `updateRing()` çağrılır ve tüm node'lar probe atamalarını yeniden hesaplar. Bu durumda daha önce pasif olan bir node artık prober listesine girebilir ve probe goroutine'ini başlatır. Bu otomatik failover mekanizmasıdır.

---

### 3 probe node'u birbirine "sen de probe et" diye haber veriyor mu?

Hayır. Üç node birbirinden tamamen bağımsız hareket eder.

Her birinin kendi probe goroutine'i vardır. Her biri kendi `probe_interval_sec`'i beklip kendi bağlantısını açar. Birinin probe sonucu diğerini tetiklemez. Birbirlerine "şu an probe et" veya "sen de bak" diye mesaj gönderilmez.

Koordinasyon sadece şu konuda gerçekleşir:
- **Gossip:** "Ben payments-db'yi down gördüm" (bilgi paylaşımı)
- **Alert kararı:** Consistent hash primary'si toplanan bilgilere bakarak tek alarm gönderir

Probing faaliyetinin kendisi koordinasyonsuz, bağımsız, paralel çalışır.

---

## Atama Matrisi ve Ölçek

### 1000 target varsa cluster bunu nasıl yönetir?

Her node için hesaplama şudur:

```
bu node, target X için prober mı?
= IsLocalProber("target-X")
= "target-X"'in hash ring seçiminde bu node var mı?
```

Bu hesaplama saf bir CPU işlemidir. 1000 target için 1000 kez hash + ring lookup yapılır — milisaniyeler içinde tamamlanır, ağ konuşması gerekmez.

**Bellek kullanımı:**

Her node, tüm target'lar için peer state'lerini tutar. 1000 target × 3 prober = en fazla 3000 gossip kaydı. Her kayıt ~100 byte. Toplam: **~300 KB per node** — önemsiz.

**Goroutine kullanımı:**

10 node'lu cluster'da: ortalama `1000 × 3 / 10 = 300` probe goroutine per node.
50 node'lu cluster'da: ortalama `1000 × 3 / 50 = 60` probe goroutine per node.

Go goroutine'leri uyurken neredeyse sıfır CPU harcar. 300 uyuyan goroutine ~2 MB stack, %0 CPU.

**Gossip trafiği:**

1000 target'ın her biri en fazla 3 node tarafından broadcast edilir. `probe_interval_sec: 60` ile dakikada 3000 gossip mesajı üretilir. Gossip fanout faktörü (3 peer per round) ile bu 50 node'a ~2 saniyede yayılır.

---

### Her target için ayrı bir matris tutulmuyor mu?

Tutulmuyor. "Matris" kavramı yanıltıcı olabilir.

Gerçekte şu var:

```
peerStates map[targetID]map[nodeName]GossipPayload
```

Bu tüm cluster'ın bildiği şeyler. 1000 target × 3 prober = 3000 entry. Her node bu map'in kendi kopyasını tutar. `peerStates["payments-db"]["node-A"]` erişimi O(1).

Prober ataması ise map'te saklanmaz — `IsLocalProber("payments-db")` her çağrıldığında anında hesaplanır. Saklanan bir atama tablosu yoktur.

---

## Konfigürasyon ve Senkronizasyon

### `probe_replication_factor`'ı değiştirebilir miyim? Tüm node'larda tek tek mi değiştireceğim?

Değiştirebilirsin. Ama **tüm node'lar aynı değeri kullanmalıdır**.

**Neden?**

Factor, hash ring'in kaç node seçeceğini belirler. Farklı node'lar farklı factor kullanırsa, seçilen prober set'leri farklılaşır:

```
node-1 (factor=3): payments-db için prober = [A, B, C]
node-2 (factor=5): payments-db için prober = [A, B, C, D, E]
```

Bu durumda:
- Node-A primary. Factor=3 dünyasında: primary, sorumlu, alarm gönderir. Factor=5 dünyasında da primary, sorumlu, alarm gönderir. İlk alarm tek gider — sorun yok gibi görünür.
- Sonra node-A crash olur. Factor=3 dünyasında ring yeniden hesaplanır: yeni primary B. Factor=5 dünyasında: yeni primary hâlâ B ama şimdi D ve E de prober — iki farklı dünyada "primary" farklı node olabilir.
- Sonuç: çift alarm riski.

**Değişiklik nasıl yapılır?**

*Kubernetes / ConfigMap:* ConfigMap'i güncelle, pod'lar hot-reload ile alır. Tüm pod'lar `reload_interval_sec` içinde yeni değere geçer.

*Ansible / Puppet:* Config'i tüm node'larda aynı anda yayın. `reload_interval_sec` bekleme süresi içinde tüm node'lar geçiş yapar.

*Manuel:* Config dosyasını tüm node'larda hızlıca güncelle. Node'lar `reload_interval_sec` periyodunda pickup yapar. Birkaç saniyelik tutarsızlık penceresi kabul edilebilirdir — dakikalar boyunca tutarsızlık bırakma.

Restart gerekmez. Hot-reload yeterlidir.

**Doğrulama:** `GET /cluster/probers` endpoint'i her node'un hangi factor'ı kullandığını gösterir. Güncelleme sonrası kontrol et.

---

### `probe_from` ve `probe_from_regions` arasındaki fark ne?

**`probe_from`** — node ismine göre tam pin:

```yaml
probe_from: ["frankfurt-1", "frankfurt-2"]
```

Sadece bu iki node probe eder. VPN erişimi, özel credentials, firewall kuralları gibi durumlar için kullanılır. Node isimleri node_name config değeriyle eşleşmelidir.

**`probe_from_regions`** — coğrafi bölgeye göre esnek pin:

```yaml
probe_from_regions: ["eu-central", "us-east"]
```

Bu bölgelerdeki herhangi bir node probe edebilir. Belirli node'ları değil bölgeyi hedeflediğin için node ekleme/çıkarma durumunda config güncellemeye gerek yok.

**İkisi birlikte kullanılabilir:** `probe_from` önce filtreleme yapar, `probe_from_regions` ikinci katman filtredir.

**Önemli sözleşme:** Aynı target'ı taşıyan her node aynı `probe_from` listesini beyan etmek zorundadır. Node-1'de `probe_from: ["A", "B"]`, node-2'de `probe_from: ["A", "B", "C"]` ise candidate set'ler farklılaşır → exactly-once garantisi bozulur.

---

### Hiçbir node target'ı probe etmezse ne olur?

Bu duruma "orphan" denir. Tetikleyiciler:

- `probe_from` listesindeki tüm node'lar offline
- `probe_from_regions` listesindeki bölgelerde hiç alive node yok
- Target sadece birkaç node'da tanımlı ve hepsi crash oldu

Ne olur:
- `network_probe_target_orphaned{name="...", target="...", type="..."}` metriği 1'e çıkar
- Log'a uyarı düşer: `[ORPHAN] no prober assigned for target ...`
- Target hakkında hiçbir probe sonucu toplanmaz — ne up ne de down
- `/fleet/status`'ta target "unknown" olarak görünür

**Düzeltme:** Ya `probe_from` listesini düzelt ya da o bölgeye yeni node ekle.

---

## Alarm Akışı ve Exactly-Once Garantisi

### 3 node da aynı target'ı down görürse kaç alarm gider?

Tam olarak 1 alarm gider. Bu exactly-once garantisinin çekirdeğidir.

Akış:

1. 3 probe node'u bağımsız olarak `payments-db` → hard-down ilan eder, gossip'te yayar.
2. Her node `GossipPayload` alır ve kendi `peerStates`'ini günceller.
3. Her node `shouldAlert("payments-db")` çağırır:
   - `quorum sağlıklı mı?` → evet
   - `ben responsible node muyum?` → hash ring'e göre sadece primary node için evet
   - `syncing mi?` → hayır
4. Sadece primary node alarm gönderir. Diğer ikisi `not responsible` olduğu için suppress eder.

---

### Primary node crash olursa ne olur?

Primary node crash olunca `NotifyLeave` callback'i tetiklenir. Her node `updateRing()` çağırır — alive node listesi güncellenir, hash ring yeniden hesaplanır. Bir sonraki node artık primary olur.

Eğer target hâlâ hard-down ise yeni primary `shouldAlert()` kontrolünden geçer ve alarm gönderir mi?

Hayır, çünkü `peerStates`'te zaten `alarm_sent=true` bilgisi var. Yeni primary bu bilgiyi görür ve "bu incident için alarm zaten gönderildi" der. Yeni alarm gitmez.

Eğer primary crash olmadan önce alarm gönderemediyse (örneğin ağ kesintisi sırasında crash), yeni primary alarm gönderir — bu doğru davranıştır, ilk alarm hiç gitmemişti.

---

### Primary node'un config'inde o target yoksa ne olur?

Bu senaryo için "primary-forwards-peer-alert" mekanizması devreye girer.

Örnek: node-A consistent hash primary'si ama `payments-db` sadece node-B ve node-C'nin config'inde. Node-B hard-down gossip'i yayar. Node-A alır:

1. `HasLocalProbe("payments-db")` → false (local probe yok)
2. `IsResponsible("payments-db")` → true (primary)
3. `DispatchPeerAlert(payload)` çağrılır — gossip payload'undaki `TargetName`, `TargetType` bilgileriyle alert env oluşturulur, node-A'nın kendi kanal listesinden gönderilir
4. `NODE_NAME` env'de node-B görünür (gerçekte detect eden node)

Sonuç: tek alarm, doğru bilgiyle, doğru kanaldan.

---

## Gözlemlenebilirlik

### Hangi node neyi probe ediyor, nasıl görürüm?

`GET /cluster/probers` endpoint'i her target için şunu gösterir:
- Seçilen prober node'ları
- Primary node
- Candidate set (seçilebilir tüm adaylar)
- Aktif `probe_from` kısıtlaması (varsa)
- Her üyenin zone bilgisi

```json
{
  "targets": {
    "payments-db": {
      "probers": ["node-1", "node-2", "node-3"],
      "primary": "node-1",
      "candidates": ["node-1", "node-2", "node-3", "node-4", "node-5"],
      "probe_from_pin": null,
      "replication_factor": 3
    }
  }
}
```

Prometheus'tan tek node bazında kontrol için:

```promql
network_probe_local_assigned{name="payments-db"} == 1
```

Bu sorgu, `payments-db`'yi aktif olarak probe eden node'ları gösterir.

---

### Cluster genelinde kaç node toplamda probe yapıyor?

```promql
sum(network_probe_local_assigned{name="payments-db"})
```

Bu sonuç normalde `probe_replication_factor` (tipik olarak 3) olmalıdır. Eğer 3'ten az çıkıyorsa bazı prober'lar offline demektir. Eğer 3'ten fazla çıkıyorsa `probe_from` kısıtlaması olmayan ve factor güncellenmemiş node'lar olabilir.

`network_probe_prober_count{name="payments-db"}` — her node'un kendi görüşüne göre cluster'da bu target için kaç prober var.

---

### Bir target'ın orphan olduğunu nasıl anlarım?

```promql
network_probe_target_orphaned == 1
```

`GET /cluster/probers` endpoint'inde `probers: []` olan target'lar orphan'dır.

Log'da `[ORPHAN]` prefix'iyle uyarı düşer.

---

## Operasyonel Senaryolar

### Yeni bir node cluster'a katılırsa probe atamaları değişir mi?

Evet, değişir. `NotifyJoin` callback'i tetiklenir → `updateRing()` → `recomputeProberAssignments()` → her node yeni atamayı hesaplar.

Etki: bazı target'lar için yeni node prober listesine girebilir (zone diversification iyileşebilir), bazı node'lar prober listesinden çıkabilir.

Yeni atama sonrası:
- Yeni prober olan node'lar o target'ların probe goroutine'lerini başlatır
- Prober listesinden çıkan node'lar goroutine'i durdurur

Bu geçiş sessizce olur. Probe gap oluşmaz çünkü eski prober'lar henüz goroutine'lerini durdurmadan yeni prober'lar başlatmış olur (birkaç saniyelik çakışma zararsızdır).

---

### Rolling restart sırasında probe sahipliği bozulur mu?

Node'ları birer birer restart edersen şu olur:

1. Node-A kapanır → `NotifyLeave` → ring yeniden hesaplanır → node-A'nın probe ettiği target'lar başka node'lara devredilir.
2. Node-A yerine kalkan node başlar, cluster'a katılır → `NotifyJoin` → ring tekrar hesaplanır → node-A muhtemelen eski sorumluluklarını geri alır.
3. Restart süresi boyunca (tipik olarak saniyeler) o target'lar başka node'lar tarafından probe edilir.

Anti-entropy:
- Node-A restart sonrası rejoin yapar
- `MergeRemoteState(join=true)` çalışır, cluster state alınır
- `syncing=true` iken probe goroutine başlamaz, alarm gitmez
- Sync tamamlanır, probe'lar başlar — hiçbir alarm fırtınası olmaz

---

### Cluster'a zone etiketi ekledim ama düzgün çalışmıyor gibi, nasıl debug ederim?

Adım adım kontrol:

1. `GET /cluster/state` — her node'un `zone` alanını gör. Boşsa config'te `cluster.zone` tanımlı değil.

2. `GET /cluster/probers` — bir target için prober seçimine bak. `zone_coverage` alanı hangi zone'lardan seçim yapıldığını gösterir.

3. `network_probe_target_orphaned == 1` — `probe_from_regions` kısıtlaması olan target'lar için hiçbir alive node o region'da değilse orphan olur. Region isimlerini typo için kontrol et.

4. Hot-reload zone değişikliklerini alıyor mu? Engine `cluster.UpdateNodeMeta(zone, region)` çağırır — `NotifyUpdate` peerlara yayılır, 1-2 saniye içinde diğer node'lar yeni zone'u görür.

---

## Hızlı Referans

| Konfigürasyon | Nerede | Tüm Node'larda Aynı Mı? |
|---|---|---|
| `probe_replication_factor` | `cluster.probe_replication_factor` | **EVET — zorunlu** |
| `zone` | `cluster.zone` | Hayır — per-node label |
| `region` | `cluster.region` | Hayır — per-node label |
| `probe_from` | `target.probe_from` | **EVET — aynı target için zorunlu** |
| `probe_from_regions` | `target.probe_from_regions` | **EVET — aynı target için zorunlu** |
| `expected_node_count` | `cluster.expected_node_count` | Tavsiye edilir aynı olması |
| `min_quorum_ratio` | `cluster.min_quorum_ratio` | Tavsiye edilir aynı olması |

| Durum | Metrik / Endpoint |
|---|---|
| Bu node target X'i probe ediyor mu? | `network_probe_local_assigned{name="X"} == 1` |
| Kaç node target X'i probe ediyor? | `network_probe_prober_count{name="X"}` |
| Hangi node'lar probe ediyor? | `GET /cluster/probers` |
| Orphan var mı? | `network_probe_target_orphaned == 1` veya `GET /cluster/probers` |
| Config drift var mı? | `network_probe_config_drift == 1` veya `GET /cluster/config` |
| Cluster quorum sağlıklı mı? | `network_prober_quorum_healthy == 1` |
| Bu node izole mi? | `network_prober_isolated == 1` |
