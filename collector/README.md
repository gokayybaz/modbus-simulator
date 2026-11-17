# Modbus Collector

Simülatördeki ekstrüzyon hattını Modbus TCP üzerinden düzenli aralıklarla okuyup **PostgreSQL + TimescaleDB**'ye yazan Go servisi.

## Çalıştırma
Simülatör host'ta `127.0.0.1:1502`'de çalışırken:
```bash
docker compose up -d --build
```
Dashboard: <http://127.0.0.1:8080> ([../dashboard](../dashboard/README.md)) · Loglar: `docker compose logs -f collector` · DB: `docker compose exec db psql -U collector -d machine`
(host'tan: `postgres://collector:collector@127.0.0.1:5432/machine`).

## Ayarlar (ortam değişkenleri)
| Değişken | Varsayılan (compose) | Açıklama |
|---|---|---|
| `MODBUS_ADDR` | `host.docker.internal:1502` | makine adresi |
| `UNIT_IDS` | `1,2,3` | virgülle ayrılmış Unit ID'ler (örnek konfigürasyondaki 3 hat; tek hat için `UNIT_IDS=1`) |
| `POLL_INTERVAL` | `1s` | okuma aralığı (min 100ms) |
| `MODBUS_TIMEOUT` | `2s` | istek zaman aşımı |
| `LOG_LEVEL` | `info` | `debug` her okumayı loglar |

Ör.: `UNIT_IDS=1 POLL_INTERVAL=500ms docker compose up -d`

## Her döngüde okunanlar
Her hat için tek istekte (32-bit değerler aynı tick'ten gelsin diye): IR 0–28 (FC04), DI 0–13 (FC02), HR 0–8 (FC03).
Ölçekler [register haritasına](../docs/registers/convention.md) göre uygulanır; DB'de mühendislik birimleri durur.

## Şema
- `machine_readings` — hypertable (1 günlük chunk), PK `(unit_id, ts)`; tüm proses değerleri, sayaçlar, alarm bitleri, setpoint'ler.
  7 günden eski chunk'lar sıkıştırılır (`segmentby unit_id`).
- `machine_readings_1m` — 1 dakikalık continuous aggregate (ortalama/maks, son sayaç değerleri, `alarm_word` OR'u), dakikada bir yenilenir.
- `machine_events` — `collector_start`, `state_change`, `fault` / `fault_cleared` (kod: [arıza tablosu](../docs/faults/fault-table.md)), `comm_lost` / `comm_restored`.
- `schema_migrations` — gömülü migration'lar (`internal/store/migrations`) yalnızca bir kez uygulanır.

Örnek sorgular:
```sql
SELECT bucket, extruder_rpm_avg, melt_pressure_max, die_temp_avg FROM machine_readings_1m
WHERE unit_id = 1 ORDER BY bucket DESC LIMIT 30;

SELECT * FROM machine_events ORDER BY ts DESC LIMIT 20;
```

## Geliştirme
```bash
go test -race ./...
go run ./cmd/collector   # yerelde: MODBUS_ADDR=127.0.0.1:1502, DATABASE_URL varsayılanı localhost:5432
```
