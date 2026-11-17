# Faz Planı ve Phase 1 Görev Listesi

## Faz özeti
| Faz | Konu | Ön koşul spike/karar | Durum |
|---|---|---|---|
| 0 | Research & Architecture | Spike 001–003 | ✅ Tamamlandı |
| 1 | Modbus TCP Server MVP | ADR-0001 (kabul edildi) | ✅ Tamamlandı ([rapor](../reports/phase-1-report.md)) |
| 2 | Simulation Engine | ADR-0004, Spike 004 | ✅ Tamamlandı ([rapor](../reports/phase-2-report.md)) |
| 3 | Extrusion Process Models | Spike 005 | ✅ Tamamlandı ([rapor](../reports/phase-3-report.md)) |
| 4 | Register Mapping & PLC Integration | ADR-0002, ADR-0003, Spike 006 | ✅ Tamamlandı ([rapor](../reports/phase-4-report.md)) |
| 5 | Fault Injection | Spike 007 | ✅ Tamamlandı ([rapor](../reports/phase-5-report.md)) |
| 6 | Multi-Line | Spike 008 | ✅ Tamamlandı ([rapor](../reports/phase-6-report.md)) |
| 7 | Config & Scenario Engine | Spike 009 | ✅ Tamamlandı — onay bekliyor ([rapor](../reports/phase-7-report.md)) |
| 8 | Observability & Reliability | Spike: expvar vs Prometheus | — |
| 9 | Packaging & Docs | — | — |

---

## Phase 1 — Modbus TCP Server MVP (uygulanabilir görev listesi)

**Kapsam dışı:** proses simülasyonu, mapping/scaling, YAML, FC15/16 (FC dispatch 01 döner), command latch.

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 1.1 | Kök Go modülü, `.gitignore`, `git init` (kullanıcı onayıyla) | `go.mod`, `.gitignore` | `go build ./...` |
| 1.2 | MBAP/PDU decoder-encoder (saf fonksiyonlar) | `internal/modbus/frame.go` | Tablo testleri: header doğrulama, length sınırı (≤260), protocol id ≠ 0 → drop; `FuzzDecodeADU` |
| 1.3 | Exception tipi ve kodları | `internal/modbus/exception.go` | `errors.As` testleri |
| 1.4 | FC01–06 PDU işleme + quantity/değer doğrulama (spec sırası: FC→03→02→04) | `internal/modbus/functions.go` | Her FC için tablo testleri; quantity 0/limit+1 → 03, FC05 0x1234 → 03 (bağlantı **açık kalmalı**) |
| 1.5 | TCP server: listener, bağlantı başına goroutine, `MaxClients`, idle timeout, `Serve(ctx)`/`Shutdown(ctx)` + WaitGroup | `internal/modbus/server.go` | Goroutine sayısı kapanış sonrası başlangıca döner; açık bağlantılar kapanır |
| 1.6 | Virtual PLC Memory: 4 alan, `RWMutex`, sabit boyutlar, unit id kontrolü, holding register limit hook'u | `internal/plc/memory.go` | Tablo testleri; `-race` ile eşzamanlı okuma/yazma |
| 1.7 | Minimal config (struct + flag'ler): `--addr` (127.0.0.1:1502), `--unit-id` (1), `--accept-any-unit`, `--max-clients`, `--log-level` | `internal/config/config.go`, `cmd/simulator/main.go` | Flag parse testleri |
| 1.8 | `slog` structured logging (bağlantı aç/kapat, exception'lar debug) | `cmd/simulator/main.go`, server | Manuel + log çıktısı |
| 1.9 | Graceful shutdown: SIGINT/SIGTERM → ctx cancel → `Shutdown` (timeout'lu) | `cmd/simulator/main.go` | E2E: süreç sinyalle 0 kodla çıkar |
| 1.10 | Interop/integration testleri: grid-x client ile FC01–06; raw-frame exception testleri; 16+ eşzamanlı client | `internal/modbus/server_test.go`, `internal/e2e/` | `go test -race ./...` |
| 1.11 | Katman kuralı testi (`internal/modbus` → `internal/plc` import etmez) | `internal/archtest/` | `go test` |
| 1.12 | Harici araçla doğrulama (mbpoll veya pymodbus; kurulum için onay alınacak) + README çalıştırma talimatı | `README.md` | Manuel, rapora eklenir |

**Kabul kriterleri (prompt'tan):** `127.0.0.1:1502`'de çalışır · harici client bağlanır · okuma/yazma başarılı ·
geçersiz adres → exception · çoklu eşzamanlı client · `go test ./...` başarılı.
Ek: `go vet`, `go test -race ./...` temiz; quantity/değer hatalarında exception 03 ve bağlantı açık kalır.

---

## Phase 2 — Simulation Engine (plan)

**Amaç:** Modbus'tan bağımsız, sabit adımlı, deterministik, gerçek zamanlı çalışabilen üretim simülasyonu çekirdeği.

**Kapsam:** engine + runner, makine state machine (STOPPED/HEATING/RUNNING/FAULT), komut modeli, seed'li RNG
akışları, sensör gürültü modeli (Gauss + sınırlı drift), zaman tabanlı dinamik primitifleri (first-order lag,
rate limiter) ve bunları kullanan **minimal yer tutucu ekipman modeli** (3 bölge + kalıp sıcaklığı, vida devri,
çekici hızı, debi, üretim sayaçları).

**Kapsam dışı:** motor akımı / eriyik basıncı / çap / et kalınlığı / güç gibi proses modelleri (Phase 3),
register eşleme ve Modbus komut bağlantısı (Phase 4), otomatik arıza tespiti (Phase 5; yalnızca manuel
`TriggerFault` ve E-Stop var), YAML (Phase 7), metrik endpoint (Phase 8). `cmd/simulator` henüz engine'i başlatmaz.

**Bağımlılıklar:** ADR-0004 (zaman modeli), ADR-0003 (komut önceliği: Stop > Start, E-Stop maintained), ADR-0005.

**Riskler:** yer tutucu modelin Phase 3'te yeniden yazılması (arayüz bunu izole etmeli); real-time testlerin
CI'da zamanlamaya duyarlı olması (gerçek zamanlı testler yalnızca yaşam döngüsünü doğrular, değerleri değil).

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 2.1 | Spike 004: ticker pacing / yavaş tüketici davranışı | `docs/spikes/004-runner-pacing.md` | Ölçüm |
| 2.2 | Dinamik primitifler: FirstOrderLag, RateLimiter | `internal/simulation/dynamics.go` | Analitik değerlerle tablo testleri |
| 2.3 | RNG akışları (seed + isim → PCG) ve Sensor (Gauss + drift + clamp) | `internal/simulation/noise.go` | Determinizm, istatistik (ortalama/σ) testleri |
| 2.4 | State + komut tipleri, geçiş fonksiyonu | `internal/simulation/state.go`, `command.go` | Tüm geçiş tablosu testi |
| 2.5 | Engine: `Step(cmds)` + Snapshot + yer tutucu ekipman | `internal/simulation/engine.go`, `equipment.go` | Replay determinizmi, STOPPED'da sayaç artmaz, HEATING→RUNNING |
| 2.6 | Runner: ticker, komut kuyruğu, atomik son snapshot, ctx ile kapanış | `internal/simulation/runner.go` | Goroutine sızıntısı yok, komut tick sınırında uygulanır, `-race` |

**Kabul kriterleri:** Modbus olmadan test edilebilir · aynı seed + aynı komut dizisi ⇒ aynı sonuç · STOPPED'da
sayaçlar artmaz · state geçişleri test edilebilir · kapanışta goroutine sızıntısı yok · varsayılan periyot 500 ms.

---

## Phase 3 — Extrusion Process Models (plan)

**Amaç:** Phase 2'nin yer tutucu ekipmanını, birbirine nedensel bağlı basitleştirilmiş proses modelleriyle değiştirmek.
Engine, state machine, komutlar, RNG ve runner değişmez; yalnızca `line` (ekipman) katmanı ve Snapshot alanları genişler.

**Kapsam:** Extruder (devir, motor yükü, akım, debi, mekanik/elektrik güç), Heating (ısıl kapasite + kayıp +
PI kontrollü ısıtıcı, proses ısı akışı kuplajı), Melt (eriyik sıcaklığı, viskozite, akışa bağlı basınç, ölü zaman +
gecikme), Vacuum & Cooling (vakum çekişi/sönümü, soğutma suyu sıcaklığı = ısı yükü / akış, soğutma verimi),
Haul-off (hız rampası), Pipe (kalibratör OD, vakum/su sıcaklığına bağlı çekme, kütle korunumundan et kalınlığı,
ölçüm cihazına taşıma gecikmesi — mesafe tabanlı), Production (uzunluk, ağırlık, hız), toplam güç tüketimi.

**Kapsam dışı:** otomatik arıza tespiti/eşikler (Phase 5), register eşleme (Phase 4), YAML parametreleri (Phase 7),
PVC ürün/hat tipi (Phase 6).

**Kararlar (kullanıcıya raporda bildirilecek):** STOPPED'da ısıtıcılar kapalı kalır; ısınma/soğuma asimetrisi
enerji dengesinden doğal olarak çıkar (sabit τ yok).

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 3.1 | Spike 005: proses varsayımları ve katsayılar (110×10 mm PE100 SDR11 referans ürünü) | `docs/spikes/005-process-model.md` | Kütle/enerji dengesi el hesabı |
| 3.2 | Primitifler `models` paketine: FirstOrderLag, RateLimiter, DeadTime, PI | `internal/simulation/models/dynamics.go` | Analitik testler |
| 3.3 | Heating (ThermalZone) | `models/heating.go` | Isınma süresi, overshoot ≤ 3 °C, setpoint'te kararlı, doğal soğuma yavaş |
| 3.4 | Extruder + Melt | `models/extruder.go`, `models/melt.go` | RPM ↑ ⇒ debi/basınç/akım ↑ (gecikmeli); sıcaklık ↓ ⇒ viskozite/basınç ↑ |
| 3.5 | Vacuum, Cooling water, Pipe gauge | `models/cooling.go`, `models/pipe.go` | Çekici hızı ↑ ⇒ et kalınlığı ↓ (taşıma gecikmesiyle); su sıcaklığı ↑ ⇒ OD ↓ |
| 3.6 | Engine entegrasyonu (`line.go`), Snapshot alanları, sensörler | `internal/simulation/line.go`, `engine.go` | Phase 2 testleri (davranış) + uçtan uca nominal çalışma noktası |

**Kabul kriterleri:** modeller mantıksal olarak ilişkili · setpoint değişiklikleri proses değerlerini etkiler ·
sıcaklıklar sıçramaz · devir değişimi basınç ve debiyi etkiler · sayaçlar sim süresine göre artar · negatif fiziksel değer yok.

---

## Phase 4 — Register Mapping & PLC Integration (plan)

**Amaç:** Simülasyonu Modbus'a bağlamak: canlı proses değerleri, setpoint yazma, Start/Stop komutları.

**Kapsam:** `internal/mapping` (codec: BOOL/UINT16/INT16/UINT32/INT32/FLOAT32, scale, ABCD/CDAB; YAML harita +
başlangıç doğrulaması; Device = modbus.Handler: okuma → PLC belleği, yazma → doğrulama + engine komutu),
gömülü varsayılan harita (`configs/register-map.yaml`), self-clearing komut coil'leri + E-Stop level (ADR-0003),
her tick'te IR/DI/coil temizliğinin tek kilit altında atomik yayını, runner'ın `cmd/simulator`'a bağlanması,
FC15/FC16 (32-bit setpoint'lerin atomik yazımı için), Production Target setpoint'i + "target reached" DI.

**Kapsam dışı:** arıza senaryoları/alarm bitleri (Phase 5; HR8 rezerve), çoklu hat (Phase 6), tam YAML config (Phase 7).

**Kararlar:**
- Çok register'lı bir alanın **kısmi yazımı** (ör. UINT32'nin tek word'ü FC06 ile) → exception 03; FC16 ile bütün yazılmalı.
- Holding register imajı yalnızca client yazmalarıyla değişir (başlangıçta engine setpoint'lerinden doldurulur);
  yayıncı HR'ı ezmez → client yazdığını hemen geri okur.
- Momentary coil'e 0 yazmak etkisizdir (bekleyen komutu iptal etmez); coil, komut engine'de işlendiği tick'te 0'a döner.

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 4.1 | Spike 006 YAML | `docs/spikes/006-yaml-library.md` | PoC |
| 4.2 | FC15/FC16 | `internal/modbus/functions.go` | Tablo + interop testleri |
| 4.3 | Codec | `internal/mapping/codec.go` | Tüm tipler × word order × scale, satürasyon, round-trip |
| 4.4 | Harita + doğrulama | `internal/mapping/map.go`, `configs/` | Hatalı haritalar anlaşılır hata |
| 4.5 | Device | `internal/mapping/device.go`, `internal/plc` | Setpoint → komut, coil handshake, atomik yayın (-race) |
| 4.6 | CLI | `cmd/simulator` | E2E: start → HEATING → RUNNING, setpoint, stop |

---

## Phase 5 — Fault Injection Engine (plan)

**Amaç:** Gerçekçi arıza senaryoları: sebep enjeksiyonu (manuel, HR8) + ölçüme dayalı otomatik algılama, alarm
bitleri, reset kuralları, deterministik tekrar. Tasarım: [Spike 007](../spikes/007-fault-model.md).

**Kapsam:** 8 sebep senaryosu (+ E-Stop), 9 arıza tipi / 14 arıza kodu (zone başına ayrı kod), uyarı + trip eşikleri,
gecikmeler, gate'ler, kurtarma koşulları, ekipman etkisi (ısıtıcılar açık/kapalı), alarm word + DI 5..13,
son arıza kodu, aktif senaryo, `docs/faults/fault-table.md` (testle tabloyla tutarlılığı korunur).

**Kapsam dışı:** YAML ile eşik/senaryo tanımı ve zamanlı senaryolar (Phase 7), metrikler (Phase 8).

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 5.1 | Model kancaları: tork/basınç çarpanı, ısıtıcı stuck/failed/TC open, besleme, çekici kayması | `models/*.go` | Model birim testleri |
| 5.2 | Senaryolar + başlama dinamiği | `simulation/scenarios.go` | Her senaryonun fiziksel etkisi |
| 5.3 | Arıza tablosu + monitörler | `simulation/faults.go` | Normal çalışmada (2 sa, 150 rpm'e kadar) yanlış trip yok |
| 5.4 | Engine entegrasyonu, reset kuralları, ekipman etkisi | `simulation/engine.go`, `line.go` | Senaryo başına: uyarı → trip → reset reddi → onarım → reset → yeniden başlatma |
| 5.5 | Register haritası + Device | `configs/register-map.yaml`, `internal/mapping` | HR8 → senaryo; IR26–28, DI5–13; E2E |

---

## Phase 6 — Multi-Line Simulation (plan)

**Amaç:** Aynı uygulamada bağımsız hatlar: Line 1 HDPE Ø110×10, Line 2 HDPE Ø63×5.8, Line 3 PVC-U Ø110×3.2.
İzolasyon: [Spike 008](../spikes/008-multi-line-isolation.md) (Unit ID varsayılan, port opsiyonel).

**Kapsam:** hat preset'leri (`simulation`), `internal/plant` (Line = engine+runner+PLC+device; Unit ID router),
CLI `-lines 1..3`, `-isolation unit|port`, hat başına seed, hat etiketli log.
**Kapsam dışı:** YAML ile hat tanımı / hat başına harita dosyası (Phase 7), hat başına setpoint limitleri (Phase 7).
**Uyumluluk:** `-lines` varsayılanı 1 → mevcut tek hat davranışı ve testleri değişmez.

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 6.1 | Spike 008 | `docs/spikes/008-multi-line-isolation.md` | PoC (gerçek TCP) |
| 6.2 | Preset'ler: HDPE110 (mevcut), HDPE63, PVC110 | `internal/simulation/presets.go` | Her preset: kütle dengesi = nominal et, alarm yok, kararlı |
| 6.3 | Plant: Line, Router | `internal/plant/` | Unit yönlendirme, 0B, izolasyon |
| 6.4 | CLI | `internal/config`, `cmd/simulator` | E2E: 3 hat unit modu, 2 hat port modu, bir hattın arızası diğerlerini etkilemez |

---

## Phase 7 — Configuration & Scenario Engine (plan)

**Amaç:** Kod değiştirmeden sunucu, hatlar, register haritaları, periyot, proses parametreleri, sensör gürültüsü,
arıza limitleri ve zamanlı senaryoların YAML ile yönetilmesi. Tasarım: [Spike 009](../spikes/009-configuration-and-scenarios.md).

**Kapsam:** `-config simulator.yaml` (server, log, simulation, register_map, lines[] + override'lar), hat başına
setpoint limitleri, gürültü ve arıza limitleri; `internal/scenarios` (YAML senaryo, oynatıcı, loop); 8 hazır senaryo +
proje tanımındaki `pressure-test` örneği (gömülü, adla seçilebilir); `-scenario`, `-validate`; senaryo kaynaklı
setpoint/E-Stop/sebep değişikliklerinin register imajına yansıması.
**Kapsam dışı:** metrikler (Phase 8), Docker (Phase 9).

| # | Görev | Dosyalar | Doğrulama |
|---|---|---|---|
| 7.1 | Simülasyon: yaml etiketleri, `Noise`, `FaultLimits`, hat başına setpoint limitleri, `CommandSource` | `internal/simulation` | Varsayılanlar değişmez (tüm eski testler), override testleri |
| 7.2 | Senaryo motoru + hazır senaryolar | `internal/scenarios`, `configs/scenarios/` | Her hazır senaryonun beklenen sonucu, tekrar determinizmi, hatalı senaryo mesajları |
| 7.3 | Konfigürasyon dosyası + öncelik + doğrulama | `internal/config`, `configs/simulator.example.yaml` | Hata mesajı tablosu, örnek dosya geçerli |
| 7.4 | Hatları konfigürasyondan kurma, imaj senkronu | `internal/plant`, `internal/mapping`, `cmd/simulator` | E2E: config dosyası + senaryo ile çalışma, `-validate` |
