# Modbus Register Convention ve Register Haritası (v1)

> Kaynak: [configs/register-map.yaml](../../configs/register-map.yaml) (binary'ye gömülü varsayılan).
> Farklı bir harita `-register-map dosya.yaml` ile verilebilir; başlangıçta doğrulanır.
> Kurallar: [ADR-0002](../architecture/adr/0002-register-convention.md), komut semantiği: [ADR-0003](../architecture/adr/0003-command-coil-semantics.md).

## Genel kurallar
- **Adres** = 0 tabanlı PDU adresi. "Display" kolonu yalnızca bilgi amaçlıdır (Modicon 1 tabanlı gösterim).
- **Bayt sırası** register içinde her zaman big-endian. **32-bit word order:** ABCD (yüksek word düşük adreste);
  haritada alan bazında `word_order: cdab` seçilebilir.
- **Ölçek:** `raw = round(değer × scale)`; okurken `değer = raw / scale`. Tipe sığmayan değer **satürasyona**
  uğrar (ör. UINT16 → 0 veya 65535) ve simülatör bir uyarı log'lar.
- **32-bit değerler tek istekte okunmalı/yazılmalıdır.** Okuma: her istek tek bir tick'in tutarlı görüntüsünü görür.
  Yazma: UINT32 setpoint'in yalnızca bir word'ünü yazmak (FC06) → exception 03; FC16 ile iki word birlikte yazılır.
- Varsayılan Unit ID `1` (`-unit-id`, `-accept-any-unit`). Port `1502`, bind `127.0.0.1`.
- Desteklenen FC: 01, 02, 03, 04, 05, 06, 15, 16.

## Input Registers (FC04, salt okunur) — her tick (varsayılan 500 ms) güncellenir

| Addr | Display | Ad | Tip | Scale | Birim | Not |
|---|---|---|---|---|---|---|
| 0 | 30001 | machine_state | UINT16 | 1 | enum | 0 STOPPED, 1 HEATING, 2 RUNNING, 3 FAULT |
| 1 | 30002 | extruder_rpm | UINT16 | 10 | rpm | |
| 2 | 30003 | motor_current | UINT16 | 10 | A | |
| 3 | 30004 | melt_pressure | UINT16 | 10 | bar | |
| 4 | 30005 | zone1_temp | UINT16 | 10 | °C | |
| 5 | 30006 | zone2_temp | UINT16 | 10 | °C | |
| 6 | 30007 | zone3_temp | UINT16 | 10 | °C | |
| 7 | 30008 | die_temp | UINT16 | 10 | °C | |
| 8 | 30009 | vacuum | UINT16 | 1 | mbar | atmosfer altı büyüklük |
| 9 | 30010 | cooling_water_temp | UINT16 | 10 | °C | |
| 10 | 30011 | haul_off_speed | UINT16 | 100 | m/min | |
| 11 | 30012 | throughput | UINT16 | 10 | kg/h | |
| 12 | 30013 | power_consumption | UINT16 | 10 | kW | motor + ısıtıcılar + vakum + çekici |
| 13 | 30014 | pipe_outer_diameter | UINT16 | 100 | mm | ölçüm cihazında; 0 = henüz boru yok; max 655.35 mm |
| 14 | 30015 | pipe_wall_thickness | UINT16 | 100 | mm | ölçüm cihazında; 0 = henüz boru yok |
| 15 | 30016 | active_fault_code | UINT16 | 1 | kod | 0 = yok; kodlar: [fault table](../faults/fault-table.md) |
| 16–17 | 30017 | total_produced_length | UINT32 | 10 | m | yalnızca RUNNING'de artar |
| 18–19 | 30019 | total_produced_weight | UINT32 | 10 | kg | yalnızca RUNNING'de artar |
| 20 | 30021 | motor_load | UINT16 | 10 | % | |
| 21 | 30022 | melt_temp | UINT16 | 10 | °C | |
| 22–23 | 30023 | melt_pressure_f32 | FLOAT32 | 1 | bar | FLOAT32 örneği |
| 24–25 | 30025 | sim_time | UINT32 | 1 | s | simülasyon zamanı |
| 26 | 30027 | alarm_word | UINT16 | 1 | bit | bit n = DI 5+n (uyarı eşiği aşılmış koşullar) |
| 27 | 30028 | active_scenario | UINT16 | 1 | kod | enjekte edilmiş arıza sebebi, 0 = yok |
| 28 | 30029 | last_fault_code | UINT16 | 1 | kod | reset sonrası da korunur |

Proses değerleri **ölçüm** değerleridir (sensör gürültüsü dahil); sayaçlar gürültüsüzdür.

## Holding Registers (FC03 / FC06 / FC16)

Başlangıçta engine setpoint'leriyle doldurulur. Yazılan değer doğrulanır, engine'e komut olarak gider (bir sonraki
tick'te uygulanır) ve hemen geri okunabilir. Limit dışı → exception 03, tanımsız adres → 02; istek ya tamamen
uygulanır ya hiç.

| Addr | Display | Ad | Tip | Scale | Birim | Aralık | Varsayılan |
|---|---|---|---|---|---|---|---|
| 0 | 40001 | extruder_rpm_sp | UINT16 | 10 | rpm | 0–150 | 100.0 |
| 1 | 40002 | zone1_temp_sp | UINT16 | 10 | °C | 0–260 | 180.0 |
| 2 | 40003 | zone2_temp_sp | UINT16 | 10 | °C | 0–260 | 190.0 |
| 3 | 40004 | zone3_temp_sp | UINT16 | 10 | °C | 0–260 | 200.0 |
| 4 | 40005 | die_temp_sp | UINT16 | 10 | °C | 0–260 | 205.0 |
| 5 | 40006 | haul_off_speed_sp | UINT16 | 100 | m/min | 0–30 | 2.50 |
| 6–7 | 40007 | production_target | UINT32 | 10 | m | 0–10 000 000 | 0 (yok) |
| 8 | 40009 | fault_scenario | UINT16 | 1 | kod | 0–8 | 0 — arıza sebebi enjekte eder; 0 = onarıldı ([tablo](../faults/fault-table.md)) |

## Coils (FC01 / FC05 / FC15)

| Addr | Display | Ad | Davranış |
|---|---|---|---|
| 0 | 00001 | start | 1 yaz → komut kuyruğa; engine işlediği tick'te coil 0'a döner. 0 yazmak etkisiz. |
| 1 | 00002 | stop | aynı; aynı tick'te Start ile gelirse Stop kazanır |
| 2 | 00003 | reset_fault | aynı; E-Stop basılıyken veya arıza koşulu sürerken reddedilir (coil yine 0'a döner, durum FAULT kalır) |
| 3 | 00004 | emergency_stop | maintained: 1 = basılı (FAULT, kod 1). 0 = bırakıldı; çıkmak için ayrıca reset_fault |

## Discrete Inputs (FC02)

| Addr | Display | Ad | Anlam |
|---|---|---|---|
| 0 | 10001 | running | state = RUNNING |
| 1 | 10002 | heating | state = HEATING |
| 2 | 10003 | fault_active | state = FAULT |
| 3 | 10004 | emergency_stop_active | E-Stop basılı |
| 4 | 10005 | target_reached | production_target > 0 ve üretilen uzunluk ≥ hedef |
| 5 | 10006 | alarm_motor_overload | motor yükü > %95 |
| 6 | 10007 | alarm_melt_pressure_high | basınç > 300 bar |
| 7 | 10008 | alarm_zone_temp_high | herhangi bir zone/die > SP + 15 °C |
| 8 | 10009 | alarm_zone_temp_low | RUNNING ve herhangi bir zone/die < SP − 10 °C |
| 9 | 10010 | alarm_cooling_water_high | RUNNING ve su > 25 °C |
| 10 | 10011 | alarm_vacuum_low | RUNNING ≥ 60 s ve vakum < 300 mbar |
| 11 | 10012 | alarm_haul_off_deviation | RUNNING ve çekici sapması > %5 |
| 12 | 10013 | alarm_sensor_failure | makul olmayan sıcaklık okuması |
| 13 | 10014 | alarm_emergency_stop | E-Stop basılı |
| 14–15 | — | (rezerve) | |

## Exception kodları
01 Illegal Function · 02 Illegal Data Address · 03 Illegal Data Value (quantity, coil değeri, limit, kısmi 32-bit yazma) ·
04 Server Device Failure · 06 Server Device Busy (komut kuyruğu dolu) · 0B Gateway Target Failed (tanımsız Unit ID).
