# ADR-0005 — Katmanlama ve paket bağımlılık yönü

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-17

## Karar
```
cmd/simulator ──► config, simulation, mapping, plc, modbus (wiring only)

simulation  (Modbus'tan habersiz: Snapshot üretir, Command tüketir)
    ▲
mapping     (Snapshot → register image; register yazması → Command; scaling/encoding)
    ▲
plc         (Virtual PLC Memory: 4 alan, lock, unit/line registry, command latch)
    ▲
modbus      (yalnızca protokol: framing, FC dispatch, exception; plc'yi Handler arayüzü üzerinden görür)
```
- Ok yönü "bilir" anlamındadır: `modbus` paketi `plc`'yi import etmez; `plc` `modbus.Handler` arayüzünü implemente eder
  ve `cmd` ikisini birbirine bağlar. `simulation` hiçbir üst katmanı import etmez.
- Kural Phase 1'den itibaren bir test (`go list -deps` kontrolü) ile korunur.
- OPC UA / MQTT ileride `mapping` veya doğrudan `Snapshot` üzerinden ayrı bir adaptör olarak eklenebilir.

## Gerekçe
Simülasyonu protokolden bağımsız test etmek ve ikinci bir protokol eklemeyi engine'e dokunmadan mümkün kılmak.
