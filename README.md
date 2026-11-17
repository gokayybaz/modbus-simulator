# Industrial Pipe Extrusion Modbus TCP Simulator

HDPE/PE100 boru ekstrüzyon hattını taklit eden, Go ile yazılmış Modbus TCP server (laboratuvar simülatörü).

**Durum:** Phase 7 tamamlandı — 1–32 bağımsız üretim hattı (preset'ler: HDPE Ø110, HDPE Ø63, PVC-U Ø110), YAML
konfigürasyon ve zamanlı senaryolar. Her hat Modbus'a bağlı sanal bir PLC: canlı proses değerleri, setpoint yazma,
Start/Stop/Reset/E-Stop coil'leri, HR8 ile arıza sebebi enjeksiyonu ([arıza tablosu](docs/faults/fault-table.md)).

## Docker ile kurulum (uçtan uca)
Tüm sistem tek komutla ayağa kalkar: **simülatör → collector → TimescaleDB → dashboard**.

Gereksinimler: Docker (Docker Desktop veya Engine) ve Docker Compose v2. Go/Node kurulumu gerekmez.

1. Repoyu klonlayın:
   ```bash
   git clone https://github.com/gokayybaz/modbus-simulator.git
   ```
   ```bash
   cd modbus-simulator
   ```
2. (İsteğe bağlı) Ayarları kopyalayıp düzenleyin — hat sayısı, zaman hızı, senaryo, portlar, DB şifresi:
   ```bash
   cp .env.example .env
   ```
3. Derleyip başlatın (ilk derleme birkaç dakika sürebilir):
   ```bash
   docker compose up -d --build
   ```
4. Durumu kontrol edin:
   ```bash
   docker compose ps
   ```
   ```bash
   docker compose logs -f collector
   ```
5. Kullanın:
   - Dashboard: <http://localhost:8080>
   - Modbus TCP: `localhost:1502` (Unit ID 1, 2, 3 → HDPE Ø110, HDPE Ø63, PVC Ø110)
   - TimescaleDB: `localhost:5432` (kullanıcı/şifre/DB: `collector`/`collector`/`machine`)

   Hattı başlatmak için herhangi bir Modbus istemcisiyle ilgili Unit ID'de coil 0'a 1 yazın; durum
   STOPPED → HEATING → RUNNING olarak dashboard'da görünür. Hızlı deneme için `.env` içinde `SIM_TIME_SCALE=100`.
6. Durdurma:
   ```bash
   docker compose down
   ```
   Veritabanı verisini de silmek için:
   ```bash
   docker compose down -v
   ```

| `.env` değişkeni | Varsayılan | Açıklama |
|---|---|---|
| `SIM_LINES` | `3` | Hat sayısı (Unit ID 1..N) |
| `SIM_TIME_SCALE` | `1` | Zaman hızlandırma (1..1000) |
| `SIM_SCENARIO` | boş | Tüm hatlar için senaryo, ör. `cooling-failure` ([senaryolar](docs/scenarios.md)) |
| `UNIT_IDS` | `1,2,3` | Collector'ın okuduğu hatlar (`SIM_LINES` ile uyumlu olmalı) |
| `POLL_INTERVAL` | `1s` | Collector okuma aralığı |
| `MODBUS_PORT` / `DB_PORT` / `DASHBOARD_PORT` | `1502` / `5432` / `8080` | Host portları (yalnızca 127.0.0.1) |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | `collector` / `collector` / `machine` | Veritabanı bilgileri |

Portlar yalnızca `127.0.0.1`'e açılır. Simülatörü host'ta `go run` ile çalıştırıp yalnızca DB + collector + dashboard'u
Docker'da kullanmak isterseniz: [collector/docker-compose.yml](collector/docker-compose.yml).

## Çalıştırma (yerel, Docker'sız)
Go 1.26+ gerekir.
```bash
go run ./cmd/simulator
```
Varsayılan: `127.0.0.1:1502`, Unit ID `1`, gerçek zaman (500 ms adım). Isınma ~18 simülasyon dakikası sürer;
denemeler için zamanı hızlandırın (sonuçlar aynı kalır, yalnızca daha hızlı akar):
```bash
go run ./cmd/simulator -time-scale 100
```
Hızlı deneme: coil 0'a 1 yazın (Start) → IR 0 = 1 (HEATING) → ~11 s sonra 2 (RUNNING); IR 0..25 canlı değerler.
Tüm seçenekler:
```bash
go run ./cmd/simulator -h
```
Birden fazla hat (Unit ID 1 = HDPE Ø110, 2 = HDPE Ø63, 3 = PVC Ø110, aynı endpoint):
```bash
go run ./cmd/simulator -lines 3 -time-scale 100
```
Hat başına ayrı port (1502, 1503, 1504; her biri Unit ID 1): `-lines 3 -isolation port`.

Konfigürasyon dosyası ve senaryolar ([konfigürasyon](docs/configuration.md), [senaryolar](docs/scenarios.md)):
```bash
go run ./cmd/simulator -config configs/simulator.example.yaml -time-scale 100
```
```bash
go run ./cmd/simulator -scenario cooling-failure -time-scale 100
```
Yalnızca doğrulama: `-validate`.

Önemli seçenekler: `-register-map dosya.yaml` (varsayılan: gömülü [configs/register-map.yaml](configs/register-map.yaml)),
`-config`, `-scenario`, `-validate`, `-lines`, `-isolation`, `-seed`, `-sim-period`, `-time-scale`, `-unit-id`, `-accept-any-unit`, `-addr`. Ctrl+C / SIGTERM ile graceful shutdown.

## Testler
```bash
go test -race ./...
```

## Modbus özeti
FC 01, 02, 03, 04, 05, 06, 15, 16. Tam harita: [docs/registers/convention.md](docs/registers/convention.md).

| Alan | Adresler (0 tabanlı) | İçerik |
|---|---|---|
| Input Registers (FC04) | 0–28 | durum, devir, akım, basınç, sıcaklıklar, vakum, su, hız, debi, güç, OD, et, arıza kodu, sayaçlar (UINT32), FLOAT32 örneği, alarm word, aktif senaryo, son arıza |
| Holding Registers (FC03/06/16) | 0–8 | setpoint'ler (limit dışı → 03), üretim hedefi (UINT32, FC16 ile), arıza senaryosu 0–8 |
| Coils (FC01/05/15) | 0–3 | Start, Stop, Reset (1 yaz, işlenince 0'a döner), E-Stop (basılı tutulan) |
| Discrete Inputs (FC02) | 0–13 | running, heating, fault, e-stop, target reached, 9 alarm biti |

## Dokümanlar
- Faz raporları: [docs/reports/](docs/reports/)
- Mimari & riskler: [docs/architecture/overview.md](docs/architecture/overview.md)
- ADR'ler: [docs/architecture/adr/](docs/architecture/adr/)
- Spike'lar: [docs/spikes/](docs/spikes/) — PoC kodu `spikes/poc/` (ayrı Go modülü)
- Faz planı: [docs/plans/phase-plan.md](docs/plans/phase-plan.md)
