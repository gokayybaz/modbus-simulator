# Mimari Genel Bakış, Klasör Yapısı, Riskler

## 1. Bileşenler ve veri akışı

```
               ┌──────────── per production line ─────────────┐
 Scenario ──►  │ Runner (ticker, ctx) ──Step()──► Engine        │
 Engine        │      ▲  Command queue             │ Snapshot   │
               │      │                            ▼            │
               │   mapping (decode writes)    mapping (encode)  │
               │      ▲                            │            │
               │      └──────── plc.Memory ◄───────┘            │
               └──────────────────▲───────────────────────────────┘
                                  │ Handler interface (unit id → line)
                         modbus.Server (TCP, MBAP, FC dispatch)
                                  ▲
                         Modbus TCP clients (SCADA/MES/tests)
```
Bağımlılık kuralları: [ADR-0005](adr/0005-layering.md).

## 2. Önerilen klasör yapısı (spike sonrası)

```text
modbus-simulator/
├── cmd/simulator/main.go          # wiring, flags, signal handling
├── internal/
│   ├── modbus/                    # Phase 1: protokol (stdlib) — ADR-0001
│   ├── plc/                       # Phase 1: Virtual PLC Memory; Phase 4: command latch, line registry
│   ├── simulation/                # Phase 2: engine, runner, state machine; Phase 5: faults, scenarios; Phase 6: presets
│   │   └── models/                # Phase 3: extruder, heating, pressure, cooling, haul-off, production
│   ├── mapping/                   # Phase 4: snapshot ↔ register, scaling, encoding, device
│   ├── plant/                     # Phase 6: Line (engine+runner+PLC+device), unit id router
│   ├── scenarios/                 # Phase 7: YAML senaryolar, oynatıcı
│   └── config/                    # Phase 1 (flags) → Phase 7 (YAML)
├── configs/                       # gömülü harita + senaryolar (Go paketi), örnek simulator.yaml
├── docs/{architecture,spikes,registers,plans,reports}
├── examples/client/               # Phase 9
├── spikes/poc/                    # Phase 0 PoC — ayrı go.mod, ana koda import edilmez
├── go.mod
└── README.md
```
Önerilen yapıdan sapmalar: arızalar `internal/faults` yerine `internal/simulation` içinde (Spike 007);
`internal/plant` eklendi (Phase 6); `docs/plans`, `docs/reports`, `docs/faults` eklendi; `tests/` ayrı klasörü yerine Go idiomuna uygun olarak
testler paketlerin yanında (`_test.go`), uçtan uca testler `internal/e2e` veya `cmd/simulator` altında tutulacak.
`spikes/poc` kendi modülünde olduğundan `go test ./...` (kök) onu çalıştırmaz.

## 3. Teknoloji seçimleri
| Konu | Seçim | Kaynak |
|---|---|---|
| Dil | Go 1.26.2 (yerel kurulum, darwin/arm64) | `go version` |
| Modbus server | Kendi stdlib implementasyonu (öneri) / yedek: simonvetter/modbus v1.6.4 | ADR-0001 |
| Interop test client | github.com/grid-x/modbus v1.5.1 (test-only) | Spike 001 |
| Logging | `log/slog` | stdlib |
| RNG | `math/rand/v2` PCG | ADR-0004 |
| YAML | `go.yaml.in/yaml/v3` v3.0.6 (strict decoding, sıfır bağımlılık) | Spike 006, 009 |
| Metrics | Phase 8'de değerlendirilecek (stdlib `expvar` vs Prometheus client) | — |

## 4. Riskler ve teknik varsayımlar

| # | Risk / varsayım | Etki | Azaltma |
|---|---|---|---|
| R1 | Kendi protokol implementasyonumuzda hata | Client uyumsuzluğu | Raw-frame tablo testleri, grid-x interop, fuzz, Python stdlib client; gerekirse simonvetter'a geri dönüş |
| R2 | Bazı client'lar Unit ID 0 / 255 gönderir | Bağlantı "çalışmıyor" görünür | `accept_any_unit_id` config; troubleshooting dokümanı |
| R3 | IR13 Pipe OD UINT16 × 100 → max 655.35 mm; HDPE borular 1600+ mm olabilir | Büyük çaplarda satürasyon | Phase 4'te scale 10 veya UINT32; şimdilik hedef ürün aralığı ≤ 630 mm varsayıldı |
| R4 | Modbus istekleri arası tutarlılık yok | 32-bit değer farklı tick yarılarından okunabilir | 32-bit değerleri tek istekte okuma kuralı; dokümantasyon |
| R5 | Ticker yük altında tick düşürür | Sim zamanı wall clock'un gerisinde kalır | Sayaçlar sim zamanına göre; gecikme metriği (Phase 8) |
| R6 | Proses katsayıları gerçek tesis verisiyle doğrulanmadı | Gerçekçilik sınırlı | Varsayımlar belgelenir, parametreler YAML'dan ayarlanabilir |
| R7 | `127.0.0.1` bind Docker'da dışarıdan erişimi engeller | Phase 9 | Docker profilinde `0.0.0.0` |
| R8 | Harici GUI/CLI client (mbpoll, Modbus Poll, pymodbus) ile test edilmedi | Gerçek araç uyumu kanıtlanmadı | Phase 1 validation'da en az bir harici araçla test (kurulum için kullanıcı onayı) |
| R9 | 1502 portu dolu olabilir | Başlatma hatası | Açık hata mesajı + `--addr` flag'i |
