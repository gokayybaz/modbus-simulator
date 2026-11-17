# Spike 003 — Simulation Engine Design

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı (tasarım + küçük PoC)
- **İlgili ADR:** [ADR-0004](../architecture/adr/0004-simulation-time-model.md), [ADR-0005](../architecture/adr/0005-layering.md)
- **PoC:** `spikes/poc/simspike/`

## 1. Zaman modeli

| Seçenek | Açıklama | Değerlendirme |
|---|---|---|
| Değişken timestep (`dt = now - last`) | Wall clock'tan dt | Deterministik değil; GC/zamanlayıcı jitter'ı sonuçları değiştirir — **red** |
| **Sabit timestep, sim saati ayrık** | `simTime = tick × dt`; motor saati hiç okumaz | Deterministik, test edilebilir — **seçildi** |
| Sabit timestep + catch-up | Gecikince birden çok `Step` | Yük altında "spiral of death" riski; gerek yok — **red (şimdilik)** |

- **Engine** yalnızca `Step()` ile ilerler; `time.Now()` çağırmaz.
- **Runner** (ayrı bileşen) `time.Ticker(dt)` ile `Step()`'i tetikler. Go ticker'ı yavaş alıcıda tick düşürür →
  sim saati wall clock'un gerisinde kalabilir; sayaçlar sim zamanına göre integre edildiği için **fiziksel tutarlılık
  korunur**. Gecikme bir metrik olarak raporlanacak (Phase 8).
- Testler runner olmadan `RunScripted(n, commands)` ile çalışır — 1 saatlik proses milisaniyelerde test edilir.
- Varsayılan `dt = 500ms` (konfigüre edilebilir).

## 2. Komutların uygulanması
Komutlar (Modbus yazmaları, senaryo olayları) bir kanala/kuyruğa düşer ve **yalnızca tick sınırında** uygulanır.
Aynı seed + aynı (tick, komut) dizisi ⇒ birebir aynı çıktı. Gerçek zamanlı modda komutun hangi tick'e düştüğü
deterministik değildir (doğası gereği); replay için komutlar tick numarasıyla kaydedilebilir.

## 3. Deterministik RNG
- `math/rand/v2` + `rand.NewPCG(seed, stream)` — stdlib, hızlı, seed'lenebilir.
- Her hat (ve gerekirse her alt sistem) **ayrı RNG akışı** alır: `stream = hash(lineID, subsystem)`. Böylece bir hatta
  sensör eklemek diğer hattın gürültü dizisini kaydırmaz.
- **Gürültü yalnızca ölçüme eklenir, gerçek proses durumuna eklenmez.** PoC: farklı seed'lerde ölçüm farklı,
  `Produced` birebir aynı (`TestDeterministicReplay`).

## 4. Atalet ve dinamik modeller

- **Birinci derece gecikme (first-order lag):** `dy/dt = (u − y)/τ`. Kesin ayrıklaştırma
  `y += (1 − e^(−dt/τ))(u − y)` — her `dt` için kararlı. PoC: 1τ sonra %63.21 (analitik değerle ±0.05),
  ilk adımda sıçrama yok (`TestLagNoJumpAndConverges`).
- **Ölü zaman (transport delay):** `N = delay/dt` uzunluğunda halka tampon (ör. eriyik basıncının debiye gecikmeli tepkisi).
- **Isıtıcı bölge:** ısıtıcı gücü (PID değil, basit oransal + satürasyon) → bölge sıcaklığı lag; komşu bölge kuplajı opsiyonel.
- **Rate limiter:** motor rampaları için (RPM/s sınırı).
- **Gürültü:** Gauss (`NormFloat64 × σ`) + opsiyonel yavaş drift (random walk, sınırlandırılmış) + kuantizasyon register ölçeğinde zaten var.
- Fiziksel olarak anlamsız negatif değerler: modeller sonrası `max(0, x)` / alan bazlı sınırlar.

## 5. Proses bağımlılık zinciri (Phase 3 için taslak)

```
RPM_SP ─rate-limit─► RPM ─lag─► Throughput(kg/h) ─dead-time+lag─► MeltPressure
                      │                │
                      └─► MotorCurrent ◄┘ (yük ∝ throughput, viskozite(T))
ZoneSP ─► Heater ─lag(τ≈dakikalar)─► ZoneTemp ─► viskozite ─► MeltPressure, MotorCurrent
HaulOffSpeed + Throughput ─► WallThickness  (kütle korunumu: ṁ = ρ·v·π·(OD−s)·s)
CoolingWaterTemp ─► soğuma verimi ─► OD sapması / kalite
Throughput × Δt ─► TotalWeight ; HaulOffSpeed × Δt ─► TotalLength (yalnızca RUNNING)
```
Her ilişkinin varsayımları Phase 3'te `docs/architecture/process-model.md` içinde belgelenecek.

## 6. State machine (Phase 2 için taslak)

```
STOPPED ──Start──► HEATING ──(tüm bölgeler SP±band, t_hold süresince)──► RUNNING
   ▲                  │                                                    │
   └──────Stop────────┴──────────────────────Stop──────────────────────────┘
any ──fault / E-Stop──► FAULT ──Reset (koşul temizse ve E-Stop bırakılmışsa)──► STOPPED
```
- Stop ve Start aynı tick'te gelirse **Stop kazanır**.
- FAULT'tan çıkış her zaman STOPPED'a (otomatik yeniden başlatma yok — güvenli durum).
- STOPPED/HEATING/FAULT'ta üretim sayaçları artmaz (PoC: `TestStoppedProducesNothing`).

## 7. Engine ⇄ dış dünya arayüzü (taslak)
- Engine çıktısı: değişmez `Snapshot` struct'ı (Go değer tipi; kopyalanarak yayınlanır).
- Engine girdisi: `Command` (Start, Stop, Reset, SetSetpoint{param, value}, EStop{on}).
- Engine Modbus paketini import etmez; mapper Snapshot → register image dönüşümünü yapar (ADR-0005).

## 8. Eşzamanlılık ve kapanış
- Bir hat = bir goroutine (runner). `context` iptaliyle döner; PoC: `TestRealtimeRunnerStopsCleanly` goroutine sızıntısı yok.
- Snapshot yayını tek yönlü: runner → PLC belleği (lock altında kopya). Engine'in kendi durumu yalnızca runner goroutine'inde değişir → engine içinde lock gerekmez.

## 9. Doğrulanmamış varsayımlar
- Proses katsayıları (τ değerleri, 2.5 kg/h/rpm vb.) gerçek tesis verisiyle doğrulanmadı; makul literatür mertebesinde seçilecek.
- Ticker'ın yüksek yük altındaki davranışı ölçülmedi (Phase 8).
