# Spike 007 — Fault model: sebep enjeksiyonu + otomatik algılama

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı (tasarım; doğrulama Phase 5 testlerinde)
- **İlgili:** Phase 5, [fault tablosu](../faults/fault-table.md), ADR-0003

## Soru
"Arızalar hem otomatik hem manuel tetiklenebilmeli." Manuel tetikleme ne demek olmalı, otomatik algılama neye bakmalı?

## Alternatifler

| | A: Doğrudan zorlama | B: Sebep enjeksiyonu + algılama | **C: B + doğrudan zorlama (API)** |
|---|---|---|---|
| Manuel tetikleme | FAULT state + kod anında | Fiziksel sebep (ör. soğutma suyu debisi %15) | HR8 = sebep; `TriggerFault(code)` engine API'de |
| Uyarı → trip zinciri | Yok | Doğal (değer önce uyarı, sonra trip eşiğini geçer) | Var |
| Kurtarma koşulu | Yapay | Fiziksel (sebep giderilip değer normale dönmeli) | Var |
| SCADA/MES testi için değer | Düşük | Yüksek: trend, ön alarm, gecikme | Yüksek |
| Determinizm | Var | Var (sebep tick'te uygulanır, algılama ölçüme dayalı) | Var |

**Karar: C.**
- HR8 (`fault_scenario`) bir **sebep** seçer (aşağıdaki tablo). Sebep, proses modellerinde fiziksel bir parametreyi
  değiştirir (çoğu gerçekçi bir başlama süresiyle). Sebep, operatör HR8 = 0 yazana ("onarım") kadar sürer.
- **Algılama yalnızca ölçülen değerlere** bakar (bir PLC'nin göreceği gibi): uyarı eşiği → alarm biti; trip eşiği +
  gecikme → FAULT. Sensör gürültüsü deterministik olduğu için algılama zamanı da deterministiktir.
- `TriggerFault(code)` doğrudan zorlama olarak kalır (Phase 7 senaryo motoru için).

## Sebep (disturbance) senaryoları

| HR8 | Ad | Fiziksel etki | Başlama | Beklenen algılama |
|---|---|---|---|---|
| 0 | none | — | — | — |
| 1 | screw_blockage | vida torku ×1.7 | τ 20 s | 10 Motor overload |
| 2 | screen_pack_clogging | kafa basıncı ×1.8 | τ 30 s | 11 Melt pressure high |
| 3 | zone2_heater_stuck_on | zone 2 ısıtıcı SSR'ı kaynaklı → tam güç | anında | 22 Zone 2 temp high |
| 4 | zone1_heater_failure | zone 1 ısıtıcı yanık → 0 W | anında | 31 Zone 1 temp low |
| 5 | cooling_water_failure | soğutma suyu debisi %15 | τ 10 s | 40 Cooling water high |
| 6 | vacuum_leak | vakum pompası kapasitesi %30 | τ 5 s | 41 Vacuum low |
| 7 | haul_off_slip | çekici paletleri %15 kayıyor | τ 5 s | 50 Haul-off deviation |
| 8 | zone2_thermocouple_open | zone 2 termokupl kopuk → 999.9 °C okunur, kontrolör ısıtıcıyı keser (upscale burnout) | anında | 60 Sensor failure |

## Algılama kuralları (özet; tam tablo: `docs/faults/fault-table.md`)
- Her monitör: ölçülen değer, uyarı eşiği, trip eşiği, trip gecikmesi, devreye girme koşulu (gate).
- Gate örnekleri: düşük sıcaklık ve vakum yalnızca RUNNING'de (vakum: RUNNING'in ilk 60 s'si hariç).
- Aynı tick'te birden çok trip → tablo sırasındaki ilki; FAULT'ta ilk kod kilitli kalır (Phase 2 kuralı).
- **Aşırı sıcaklık** = sapma > SP+30 °C **ve ısıtıcı hâlâ güç çekiyor** (ısıtıcı akım izleme). Böylece operatörün
  setpoint'i düşürmesi trip etmez (yalnızca uyarı), kaynaklı SSR trip eder.
- **Reset kuralı:** E-Stop bırakılmış olmalı **ve** kilitli arızanın "kurtarma kontrolü" olan monitörleri (basınç,
  sıcaklıklar, su, sensör) uyarı eşiğinin içinde olmalı; aksi halde reset reddedilir ve sebebi log'lanır.
  Vakum/çekici gibi FAULT'ta zaten durmuş ekipmanın monitörleri reset'i engellemez.
- **Ekipman etkisi:** her trip'te tahrikler (vida, çekici) anında durur, vakum pompası kapanır. Isıtıcılar:
  E-Stop, aşırı sıcaklık ve sensör arızasında **kapanır** (ana kontaktör); diğerlerinde setpoint'i tutar
  (yeniden başlatma süresini kısaltır — yaygın uygulama).

## Neden ayrı `internal/faults` paketi yok?
Monitörler engine'in tick içi durumuna (ölçüm, state, state süresi, setpoint, ısıtıcı gücü) ve engine de monitör
sonucuna (state geçişi) bağlıdır; ayrı paket döngüsel bağımlılık veya geniş bir ara arayüz gerektirirdi.
`internal/simulation/faults.go` + `scenarios.go` olarak engine'in içinde tutulur; ADR-0005'in amacı (protokolden
bağımsızlık) korunur. Proje yapısındaki `internal/faults/` önerisinden bilinçli sapma.
