# Konfigürasyon Referansı

Simülatör hiçbir dosya olmadan çalışır (tek HDPE hattı, gömülü register haritası). Bir YAML dosyası `-config` ile
verilir; tam örnek: [configs/simulator.example.yaml](../configs/simulator.example.yaml).

**Öncelik:** varsayılan < konfigürasyon dosyası < açıkça verilen komut satırı bayrağı.
**Doğrulama:** her şey (dosya, preset'ler, override'lar, register haritaları, senaryolar) başlangıçta, hiçbir şey
çalışmadan doğrulanır; tüm sorunlar konumlarıyla (`dosya: lines[2] (ad): bölüm: line N: ...`) tek seferde raporlanır.
Yalnızca doğrulamak için:

```bash
go run ./cmd/simulator -config configs/simulator.example.yaml -validate
```

## Üst seviye

| Anahtar | Tip | Varsayılan | Bayrak | Açıklama |
|---|---|---|---|---|
| `version` | int | — (zorunlu, 1) | | |
| `server.addr` | host:port | `127.0.0.1:1502` | `-addr` | |
| `server.unit_id` | 0..255 | 1 | `-unit-id` | ilk hattın Unit ID'si |
| `server.isolation` | `unit` \| `port` | `unit` | `-isolation` | unit: tek endpoint, hat başına Unit ID (+1); port: hat başına port (+1) |
| `server.accept_any_unit` | bool | false | `-accept-any-unit` | yalnızca endpoint başına tek hatta |
| `server.max_clients` | ≥ 1 | 32 | `-max-clients` | endpoint başına |
| `server.idle_timeout` | süre | 60s | `-idle-timeout` | 0 = kapalı |
| `server.shutdown_timeout` | süre | 5s | `-shutdown-timeout` | |
| `log.level` | debug/info/warn/error | info | `-log-level` | |
| `log.format` | text/json | text | `-log-format` | |
| `simulation.period` | 1ms..10s | 500ms | `-sim-period` | sabit adım (dt) |
| `simulation.time_scale` | 1..1000 | 1 | `-time-scale` | yalnızca gerçek zaman hızı; sonuçlar aynı |
| `simulation.seed` | uint64 | 1 | `-seed` | hat i: seed + i − 1 |
| `register_map` | dosya | gömülü | `-register-map` | tüm hatlar için |
| `scenario` | ad / dosya | — | `-scenario` | tüm hatlar için (bayrak, hat ayarını da ezer) |
| `lines` | liste | `-lines N` ile preset'ler | `-lines` (dosyada `lines` yoksa) | en çok 32 hat |

Süreler Go biçimindedir: `500ms`, `30s`, `2m`.

## Hat (`lines[]`)

| Anahtar | Açıklama |
|---|---|
| `name` | benzersiz ad (varsayılan `line-N`); log'larda `line_name` |
| `preset` | **zorunlu**: `hdpe-110`, `hdpe-63`, `pvc-110` |
| `unit_id` | varsayılan: unit modunda `server.unit_id + i`, port modunda `server.unit_id` |
| `port` | yalnızca `isolation: port`; varsayılan `server.addr` portu + i (port 0 → her hat OS'tan port alır) |
| `seed` | hat seed'i |
| `register_map` | bu hattın haritası |
| `scenario` | bu hattın senaryosu (yerleşik ad veya dosya) |
| `min_run_temp` | soğuk başlatma kilidi, °C (preset: HDPE 160, PVC 150) |
| `setpoints` | başlangıç setpoint'leri: `extruder_rpm`, `zone1_temp`, `zone2_temp`, `zone3_temp`, `die_temp`, `haul_off_speed`, `production_target_length` |
| `setpoint_limits` | hat limitleri `{ad: {min, max}}`; yalnızca global limitleri daraltabilir; Modbus yazmaları ve senaryolar da bu limitlere tabidir |
| `process` | preset'in proses parametrelerine **kısmi** override (aşağıda) |
| `noise` | sensör gürültüsü override'ı |
| `fault_limits` | arıza eşikleri override'ı |

### `process` (yalnızca yazılan anahtarlar değişir)
`ambient_temp`, `vacuum_pump`, `haul_off_drive`, `nominal_haul_off` ve bölümler:

- `zones:` (3 elemanlı liste) ve `die:` — `heat_capacity`, `loss_coeff`, `heater_max`, `kp`, `ti`, `process_heat_nominal`
- `extruder:` — `nominal_rpm`, `ramp_rate`, `output_per_rpm`, `throughput_tau`, `specific_energy`, `no_load_power`, `rated_power`, `voltage`, `power_factor`, `efficiency`
- `melt:` — `ref_temp`, `viscosity_temp_coef`, `min_viscosity_ratio`, `max_viscosity_ratio`, `shear_heat_per_rpm`, `melt_temp_tau`, `nominal_pressure`, `flow_exponent`, `pressure_dead_time`, `pressure_tau`
- `vacuum:` — `target`, `pull_tau`, `decay_tau`
- `cooling:` — `supply_temp`, `flow_rate`, `tank_tau`, `polymer_cp`, `latent_heat`, `pipe_exit_temp`
- `pipe:` — `calibrator_od`, `density`, `haul_off_ramp_rate`, `max_vacuum_shrink`, `nominal_vacuum`, `shrink_per_deg_c`, `ref_water_temp`, `gauge_distance`, `sample_resolution`

Anlamlar ve birimler: [Spike 005](spikes/005-process-model.md). Not: `zones:` bir liste olduğundan verilirse üç
bölgenin tamamı yazılmalıdır.

### `noise`
Gruplar: `temperature`, `extruder_rpm`, `motor_current`, `motor_load`, `melt_pressure`, `vacuum`, `cooling_water`,
`haul_off_speed`, `throughput`, `power`, `pipe_od`, `wall_thickness`. Her biri:
`{sigma, drift_step, drift_max, non_negative, zero_at_zero}`. Bir grup verildiğinde o grubun tüm alanları yeniden
tanımlanır (verilmeyen bool'lar false olur).

### `fault_limits`
`motor_overload`, `melt_pressure_high`, `zone_temp_high`, `zone_temp_low` (SP'den sapma, pozitif sayı),
`cooling_water_high`, `vacuum_low`, `haul_off_deviation` → `{warning, trip, delay}`; `sensor_min`, `sensor_max`,
`sensor_delay`. Yüksek limitlerde trip ≥ warning, düşük limitlerde (`vacuum_low`) trip ≤ warning olmalıdır.
Varsayılanlar: [fault table](faults/fault-table.md).
