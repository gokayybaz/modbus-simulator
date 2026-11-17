# Senaryolar

Senaryo, bir hatta zamanlı olaylar uygulayan YAML dosyasıdır. Olaylar simülasyon zamanında, tick sınırında
uygulanır: **aynı seed + aynı senaryo ⇒ birebir aynı veri**, `-time-scale` ne olursa olsun.

```bash
go run ./cmd/simulator -scenario cooling-failure -time-scale 100        # yerleşik senaryo
go run ./cmd/simulator -scenario ./my-test.yaml                          # dosya
```
Konfigürasyon dosyasında: üst seviye `scenario:` (tüm hatlar) veya hat başına `lines[].scenario`.

## Biçim

```yaml
scenario:
  name: pressure-test
  description: optional
  loop: false            # true: son olaydan sonra baştan
  events:
    - at_seconds: 10
      action: start_machine
    - at_seconds: 60
      action: set_extruder_rpm
      value: 80
    - at_seconds: 120
      action: inject_fault
      fault: melt_pressure_high
```

`at_seconds` olayın uygulandığı simülasyon zamanıdır (tick = round(t / period) + 1). Olaylar zamana göre sıralanır;
aynı zamandakiler yazıldıkları sırada uygulanır ve aynı tick'teki Modbus yazmalarından önce gelir.

## Aksiyonlar

| Aksiyon | Parametre | Etki |
|---|---|---|
| `start_machine` / `stop_machine` / `reset_fault` | — | coil Start/Stop/Reset ile aynı |
| `emergency_stop` / `release_emergency_stop` | — | E-Stop bas / bırak (coil 3 da güncellenir) |
| `set_setpoint` | `setpoint`, `value` | herhangi bir setpoint (adlar: [configuration.md](configuration.md)) |
| `set_extruder_rpm` / `set_haul_off_speed` / `set_production_target` | `value` | kısayollar |
| `set_zone_temp` | `zone` (`1`, `2`, `3`, `die`), `value` | |
| `inject_fault` | `fault` (ad, [fault table](faults/fault-table.md)) | doğrudan trip |
| `inject_cause` | `cause` (ad, ör. `cooling_water_failure`) | fiziksel sebep (HR8 ile aynı); arıza otomatik algılanır |
| `clear_cause` | — | sebebi onar (HR8 = 0) |
| `note` | `message` | yalnızca log |

Senaryonun değiştirdiği setpoint, sebep ve E-Stop değerleri holding register'lara ve coil 3'e yansıtılır.
Setpoint değerleri hem global hem hattın kendi limitlerine göre başlangıçta doğrulanır.

## Yerleşik senaryolar ([configs/scenarios/](../configs/scenarios/))

Süreler `hdpe-110` referans hattına göre seçilmiştir (ısınma ~18 dk). Sonuçlar `TestBuiltinScenarios` ile doğrulanır.

| Ad | Akış | Doğrulanan sonuç |
|---|---|---|
| `normal-production` | başlat, üret, 30. dk'da 100 m hedef | HEATING → RUNNING, hedefe ulaşılır |
| `cold-startup` | 120 °C'de bekletme, 20. dk'da üretim sıcaklıkları | bekletmede vida başlamaz (soğuk başlatma kilidi), sonra RUNNING |
| `heating-delay` | 5. dk'da zone 1 ısıtıcı arızası, 30. dk'da onarım | 30. dk'dan önce RUNNING yok, sonra RUNNING |
| `pressure-increase` | 130 rpm, elek tıkanması, onarım, reset, yeniden başlatma | FAULT 11 → STOPPED → HEATING → RUNNING |
| `motor-overload` | vida tıkanması, onarım, reset, yeniden başlatma | FAULT 10 → … → RUNNING |
| `cooling-failure` | soğutma suyu %15, onarım, reset, yeniden başlatma | FAULT 40 → … → RUNNING |
| `emergency-stop` | E-Stop, basılıyken reset (reddedilir), bırak, reset, başlat | FAULT 1, reset reddi, … → RUNNING |
| `stop-restart` | 25. dk'da durdur, 35. dk'da yeniden başlat | RUNNING → STOPPED → HEATING → RUNNING |
| `pressure-test` | proje tanımındaki örnek | 120 s'de FAULT 11 (doğrudan) |
