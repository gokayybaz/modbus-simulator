# Fault Table (v1)

> Kaynak kod: [internal/simulation/faults.go](../../internal/simulation/faults.go),
> [scenarios.go](../../internal/simulation/scenarios.go). Bu tablonun kodla tutarlılığı
> `TestFaultTableDocumented` ile korunur. Tasarım: [Spike 007](../spikes/007-fault-model.md).

## Ortak kurallar
- **Algılama ölçülen değerlere bakar** (sensör gürültüsü dahil, deterministik).
- **Uyarı (warning):** değer uyarı eşiğini geçtiği sürece alarm biti 1 (IR26 bit n = DI 5+n). Durum değişmez.
- **Trip:** trip eşiği (ve varsa ek koşul) gecikme boyunca kesintisiz aşılırsa makine **FAULT**'a geçer, IR15 = kod,
  IR28 = son kod. Aynı tick'te birden çok trip → tablodaki sıra; FAULT'ta ilk kod kilitli kalır.
- **Algılama:** otomatik trip tick'in sonunda (ölçümden sonra) algılanır ve tahrikler aynı tick'te kesilir
  (PLC'nin aynı taramada çıkışlarını kapatması gibi); o tick'in ölçüm değerleri kesme öncesine aittir.
- **Ekipman etkisi (her trip):** vida ve çekici anında durur, vakum pompası kapanır, üretim sayaçları donar
  (silinmez; yeniden başlatmada kaldığı yerden devam eder). Isıtıcılar: aşağıdaki "Isıtıcı" kolonuna göre.
- **Reset gereksinimi:** her arıza `reset_fault` (coil 2) ister → STOPPED; üretime dönmek için yeniden `start`.
  Reset, E-Stop basılıysa veya arızanın kurtarma koşulu sağlanmamışsa **reddedilir** (coil yine 0'a döner, durum FAULT kalır).
- **Sebep (HR8 senaryosu)** reset ile kalkmaz; HR8 = 0 yazılarak "onarılır". Sebep sürerken resetlenip yeniden
  başlatılan hat tekrar trip eder.

## Arızalar

| Kod | Ad | Önem | Alarm biti (DI) | Uyarı | Trip | Kurtarma (reset için) | Isıtıcı |
|---|---|---|---|---|---|---|---|
| 1 | emergency_stop | critical | 8 (DI 13) | E-Stop basılı | E-Stop basılı (anında) | E-Stop bırakılmış | kapalı |
| 10 | extruder_motor_overload | trip | 0 (DI 5) | motor yükü > %95 | motor yükü > %110, 5 s | motor yükü ≤ %95 (tahrik durunca hemen) | açık |
| 11 | melt_pressure_high | trip | 1 (DI 6) | basınç > 300 bar | basınç > 350 bar, 1 s | basınç ≤ 300 bar | açık |
| 60 | sensor_failure | trip | 7 (DI 12) | herhangi bir zone/die okuması −20..500 °C dışında | aynı, 1 s | tüm okumalar makul (sensör onarılmış) | kapalı |
| 21 | zone1_temp_high | trip | 2 (DI 7) | zone 1 > SP + 15 °C | zone 1 > SP + 30 °C **ve ısıtıcı akım çekiyor**, 60 s | zone 1 ≤ SP + 15 °C | kapalı |
| 22 | zone2_temp_high | trip | 2 (DI 7) | zone 2 > SP + 15 °C | zone 2 > SP + 30 °C ve ısıtıcı akım çekiyor, 60 s | zone 2 ≤ SP + 15 °C | kapalı |
| 23 | zone3_temp_high | trip | 2 (DI 7) | zone 3 > SP + 15 °C | zone 3 > SP + 30 °C ve ısıtıcı akım çekiyor, 60 s | zone 3 ≤ SP + 15 °C | kapalı |
| 24 | die_temp_high | trip | 2 (DI 7) | die > SP + 15 °C | die > SP + 30 °C ve ısıtıcı akım çekiyor, 60 s | die ≤ SP + 15 °C | kapalı |
| 31 | zone1_temp_low | trip | 3 (DI 8) | RUNNING ve zone 1 < SP − 10 °C | RUNNING, zone 1 < SP − 20 °C **ve ısıtıcı < %50 veriyor**, 10 s | zone 1 ≥ SP − 10 °C | açık |
| 32 | zone2_temp_low | trip | 3 (DI 8) | RUNNING ve zone 2 < SP − 10 °C | RUNNING, zone 2 < SP − 20 °C ve ısıtıcı < %50, 10 s | zone 2 ≥ SP − 10 °C | açık |
| 33 | zone3_temp_low | trip | 3 (DI 8) | RUNNING ve zone 3 < SP − 10 °C | RUNNING, zone 3 < SP − 20 °C ve ısıtıcı < %50, 10 s | zone 3 ≥ SP − 10 °C | açık |
| 34 | die_temp_low | trip | 3 (DI 8) | RUNNING ve die < SP − 10 °C | RUNNING, die < SP − 20 °C ve ısıtıcı < %50, 10 s | die ≥ SP − 10 °C | açık |
| 40 | cooling_water_temp_high | trip | 4 (DI 9) | RUNNING ve su > 25 °C | RUNNING ve su > 30 °C, 30 s | su ≤ 25 °C | açık |
| 41 | vacuum_low | trip | 5 (DI 10) | RUNNING ≥ 60 s ve vakum < 300 mbar | RUNNING ≥ 60 s ve vakum < 200 mbar, 10 s | yok (pompa FAULT'ta kapalı) | açık |
| 50 | haul_off_speed_deviation | trip | 6 (DI 11) | RUNNING ve gerçek hız tahrik referansının > %5 altında | RUNNING ve sapma > %10, 5 s | yok (çekici FAULT'ta durmuş) | açık |

Isıtıcı akım koşulları neden var? Operatör çalışırken bir setpoint'i 30 °C değiştirirse bu bir arıza değildir:
sağlam ısıtıcı ya tam güçte ısıtır (düşük sapma) ya da tamamen kapanır (yüksek sapma). Bu durumda yalnızca uyarı biti
yanar. Trip yalnızca ısıtıcı beklenenin tersini yaptığında (kaynaklı SSR, yanık eleman) oluşur.

## Sebep senaryoları (HR8)

| HR8 | Ad | Fiziksel etki (başlama) | Ölçülen uyarı / trip (referans hat, kararlı çalışmadan) | Kod |
|---|---|---|---|---|
| 0 | none | — (onarım) | — | — |
| 1 | screw_blockage | vida torku ×1.7 (τ 20 s) | 15 s / 40.5 s | 10 |
| 2 | screen_pack_clogging | kafa direnci ×1.8 (τ 30 s) | 26 s / 59.5 s | 11 |
| 3 | zone2_heater_stuck_on | zone 2 ısıtıcı tam güç (anında) | 171 s / 441.5 s | 22 |
| 4 | zone1_heater_failure | zone 1 ısıtıcı 0 W (anında) | 43.5 s / 99.5 s | 31 |
| 5 | cooling_water_failure | soğutma suyu debisi %15 (τ 10 s) | 98 s / 193.5 s | 40 |
| 6 | vacuum_leak | vakum pompası kapasitesi %30 (τ 5 s) | 9 s / 28.5 s | 41 |
| 7 | haul_off_slip | çekici %15 kayma (τ 5 s) | 2 s / 10.5 s | 50 |
| 8 | zone2_thermocouple_open | zone 2 999.9 °C okur, kontrolör ısıtıcıyı keser | 0.5 s / 1 s | 60 |

Süreler, senaryonun yazıldığı tick'in başından itibaren simülasyon zamanıdır (seed 1, varsayılan konfigürasyon,
100 rpm kararlı üretim) ve `TestScenarioChains` ile ±%25 bantla doğrulanır.
