# Phase 6 Report — Multi-Line Simulation

```text
PHASE: 6 — Multi-Line Simulation
STATUS: COMPLETED — awaiting user approval for Phase 7

IMPLEMENTED:
- Line presets (internal/simulation/presets.go), each consistent with its product's mass
  balance:
  * Line 1 hdpe-110: HDPE PE100 Ø110 × 10.0 mm, Ø75 single screw, 100 rpm, 450 kg/h,
    2.50 m/min (Phase 3 reference line)
  * Line 2 hdpe-63: HDPE PE100 Ø63 × 5.8 mm, Ø60 single screw, 80 rpm, 240 kg/h, 4.02 m/min,
    smaller heaters/zones, 200 bar nominal
  * Line 3 pvc-110: PVC-U Ø110 × 3.2 mm, counter-rotating twin screw, 30 rpm × 10 kg/h/rpm =
    300 kg/h, 3.33 m/min, zones 175/180/185 °C, die 190 °C, density 1400 kg/m³, no latent heat
    (amorphous), stronger shear thinning (n = 0.3) and temperature sensitivity (0.03/°C),
    250 bar nominal, vacuum 500 mbar
- internal/plant:
  * Line = its own engine, real-time runner goroutine, RNG seed (base seed + line − 1),
    PLC memory, register-map instance, command queue, counters and alarms; nothing shared
  * Router = unit id → line (modbus.Handler); unknown unit → exception 0B; optional
    "any unit" default for a single-line endpoint
- CLI: -lines 1..3 (default 1 = previous behaviour), -isolation unit|port
  * unit (default): one endpoint, line i = unit id (-unit-id + i − 1) → 1, 2, 3
  * port: line i on port (+ i − 1), unit id -unit-id each; -accept-any-unit allowed;
    port 0 gives every line its own OS-assigned port
  * validation: unit id / port overflow, -accept-any-unit only with one line per endpoint
  * all endpoints bound before anything starts; every line logged with preset, address,
    unit id and seed; shutdown: all servers, then all simulations
- archtest: plant rules (protocol/simulation/plc/mapping must not import plant)

SPIKES:
- 008 Multi-line isolation (docs/spikes/008-multi-line-isolation.md): unit id vs port vs
  both → both, unit id default. PoC on real TCP with our server + grid-x client: unit id
  switched per request on ONE connection always hit the right line; a write to unit 2
  changed only line 2; unknown unit 9 → 0x0B; 30 clients × 300 reads across 3 units
  race-free in 79 ms.

FILES CREATED:
- internal/simulation/{presets.go,presets_test.go}
- internal/plant/{plant.go,plant_test.go}
- docs/spikes/008-multi-line-isolation.md, docs/reports/phase-6-report.md
FILES CHANGED:
- cmd/simulator/{main.go,main_test.go}
- internal/config/{config.go,config_test.go}
- internal/archtest/archtest_test.go
- docs/plans/phase-plan.md, docs/architecture/overview.md, README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=5 ./... : all 9 packages ok
- Coverage: config 100 %, simulation 97.5 %, models 97.1 %, mapping 96.7 %, plc 96.3 %,
  modbus 91.5 %, plant 89.8 %, cmd 85.1 %
- Presets (30 min steady production each, no alarms):
      hdpe-110: 450 kg/h, 209.3 bar, 160.1 A (70 %), water 16.4 °C, OD 109.99, wall 10.001, 131.6 kW
      hdpe-63 : 240 kg/h, 198.0 bar,  88.6 A (56 %), water 15.5 °C, OD  63.00, wall  5.798,  78.1 kW
      pvc-110 : 300 kg/h, 239.0 bar,  82.0 A (63 %), water 13.2 °C, OD 110.00, wall  3.196,  81.2 kW
- Plant: routing per unit (each line's own HR defaults; a write to unit 2 changes only
  line 2), 0B for unknown units on all 6 handler methods, router configuration rules,
  E-Stop on line 2 leaves lines 1 and 3 RUNNING without alarms and still producing,
  distinct throughput/wall registers per product, 3 lines in real time at 1000× with
  concurrent readers under -race, invalid line configurations rejected
- End-to-end (grid-x client):
  * -lines 3 (unit ids 1..3 on one endpoint): preset setpoints per unit, unit 4 → 0B,
    lines 1 and 3 started, line 2 stays STOPPED, line 1 (HDPE) ≈ 450 kg/h and line 3
    (PVC) ≈ 300 kg/h, E-Stop on line 1 → line 3 keeps RUNNING, fault-free, producing
  * -lines 2 -isolation port: two distinct endpoints, unit 1 on each with its own line,
    unit 2 → 0B
  * -lines 2 -isolation port -accept-any-unit: units 0, 1, 255 answered on both endpoints
- Manual: binary with -lines 3 -time-scale 300 and a Python stdlib client on ONE
  connection: all three lines started and read by unit id (values above); HR8 = 5 on
  unit 2 → line 2 FAULT 40 while lines 1 and 3 continue; unit 9 → 84 0B

ACCEPTANCE CRITERIA:
- Lines run independently ............................. PASS
- A fault on one line does not affect the others ...... PASS (plant test, E2E, manual)
- Different process parameters per line .............. PASS (3 presets, different products)
- Correct unit id reads the correct line's data ...... PASS (unit routing; port mode too)
- Unit id vs TCP endpoint isolation researched ........ DONE (Spike 008, both supported)
- Multiple unit ids on the same endpoint verified .... PASS (one connection, per-request unit id)

KNOWN ISSUES / ASSUMPTIONS:
- Line composition is fixed (presets in order, 1..3) and all lines share one register map
  file (each line its own instance); per-line YAML definitions and maps are Phase 7.
- Setpoint and fault limits are global (e.g. 0..260 °C for PVC too); per-line limits
  come with Phase 7 configuration.
- Alarm bits of RUNNING-gated conditions (cooling water, vacuum, haul-off, zone low)
  clear once the line is in FAULT; the fault code (IR15/IR28) keeps the information.
- PVC coefficients are plausible but not validated; PVC thermal degradation is not modelled.
- Carried over: no git repository; no third-party Modbus tool test.

NEXT PHASE:
- Phase 7 — Configuration & Scenario Engine (YAML: server, machines/lines, register
  maps, intervals, process parameters, noise, faults; time-based scenario files;
  start-up validation).
```

## Kullanım
```bash
go run ./cmd/simulator -lines 3 -time-scale 100
```
Unit ID 1 = HDPE Ø110, 2 = HDPE Ø63, 3 = PVC Ø110, hepsi `127.0.0.1:1502` üzerinde. Hat başına ayrı port için:
```bash
go run ./cmd/simulator -lines 3 -isolation port -addr 127.0.0.1:1502
```
(hat 1 → 1502, hat 2 → 1503, hat 3 → 1504; her biri Unit ID 1)
