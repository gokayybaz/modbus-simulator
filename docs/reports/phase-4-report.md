# Phase 4 Report — Register Mapping & PLC Integration

```text
PHASE: 4 — Register Mapping & PLC Integration
STATUS: COMPLETED — awaiting user approval for Phase 5

IMPLEMENTED:
- internal/mapping/codec.go: BOOL, UINT16, INT16, UINT32, INT32, FLOAT32; scale; word order
  ABCD (default) / CDAB; rounding; saturation with "clamped" flag; NaN → 0
- internal/mapping/map.go: YAML register map (go.yaml.in/yaml/v3, strict: unknown keys are
  errors), semantic validation at start-up with every problem reported at once and its
  location (section[index] (name)): names unique, addresses required/in range, overlap
  (incl. 32-bit width), types per area, scale > 0, word_order only for 32-bit, known
  sources/setpoints/commands, min/max within setpoint limits and representable, area sizes
- configs/register-map.yaml (embedded default, also the documented example):
  IR 0..25 (brief's map + motor load, melt temp, FLOAT32 pressure, sim time),
  DI 0..4 (+5..15 reserved), HR 0..8, coils 0..3
- internal/mapping/device.go — modbus.Handler for the line:
  * reads from PLC memory
  * holding writes: whole fields only (partial UINT32 → 03), engineering range (→ 03),
    unmapped (→ 02), all-or-nothing; valid writes → SetSetpoint command + stored
    (read back immediately); reserved register stored only
  * coils (ADR-0003): start/stop/reset momentary — queued once, coil stays 1 until the
    engine applied the command, then 0 (exact handshake via the runner's applied-command
    list); writing 0 has no effect; emergency_stop maintained level
  * Publish per tick: full IR + DI image encoded and written with the coil acknowledgements
    under ONE lock (atomic snapshot); saturation logged on change only
  * full queue → exception 06 (Server Device Busy), memory unchanged
- internal/modbus: FC15 (Write Multiple Coils) and FC16 (Write Multiple Registers) with
  quantity/byte-count validation (→ 03); new exception 06
- internal/plc: CheckUnit, Layout, SetHoldingRegisters, atomic Publish
- internal/simulation:
  * Production target setpoint (m, 0..1e7, 0 = none) + Snapshot.TargetReached
  * SetpointByName / Limits / Setpoints.Get for map validation
  * Runner.OnTick now receives the commands applied in that tick
  * Runner Speed (time acceleration: wall pacing only, results identical)
  * Runner coalesces queued SetSetpoint (same setpoint) and EmergencyStop commands
- cmd/simulator: map → engine → PLC → device → server wiring; runner started/stopped
  with the server; new flags -register-map, -seed, -sim-period, -time-scale

SPIKES:
- 006 YAML library (docs/spikes/006-yaml-library.md): go.yaml.in/yaml/v3 v3.0.6
  (official successor of the archived gopkg.in/yaml.v3; MIT + Apache-2.0; no
  transitive dependencies; strict decoding with line numbers verified). v4.0.0 is
  < 2 weeks old → re-evaluate in Phase 7.

DECISIONS (beyond the brief, please confirm):
- FC15/FC16 added now (brief allowed "later"): needed to write 32-bit setpoints atomically.
- -time-scale added: without it an end-to-end Start→RUNNING check takes ~18 real minutes.
- Holding registers are written only by clients (+ initial setpoints); the publisher does
  not overwrite them. Phase 7 scenarios that change setpoints must update them too.
- Reaching the production target only raises DI 4; it does not stop the line.

ISSUES FOUND AND FIXED DURING TESTING:
- Map validation caught a duplicate name in the default map (DI and coil both
  "emergency_stop") → DI renamed emergency_stop_active.
- 48 concurrent clients writing setpoints filled the 256-entry command queue between
  500 ms ticks → exception 06. Fixed by coalescing level-type commands in the runner
  (proved result-equivalent by a test); setpoint writes can no longer fill the queue.
- End-to-end test initially read the first pipe section at the gauge (thin start-up
  scrap, 2.08 mm) as steady state — test now waits for the wall to settle (model correct).

FILES CREATED:
- internal/mapping/{codec.go,map.go,device.go,codec_test.go,map_test.go,device_test.go}
- configs/{embed.go,register-map.yaml}
- docs/spikes/006-yaml-library.md, docs/reports/phase-4-report.md
FILES CHANGED:
- internal/modbus/{functions.go,exception.go,functions_test.go,server_test.go}
- internal/plc/{memory.go,memory_test.go}
- internal/simulation/{state.go,engine.go,runner.go,engine_test.go,runner_test.go}
- internal/config/{config.go,config_test.go}, internal/archtest/archtest_test.go
- cmd/simulator/{main.go,main_test.go}; cmd/simulator/layout.go removed (map defines layout)
- docs/registers/convention.md (final v1 map), ADR-0003 (implemented), phase-plan, README
- go.mod: + go.yaml.in/yaml/v3 v3.0.6 (runtime), grid-x/modbus (tests only)

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=5 ./... : all 8 packages ok (no flakiness)
- Coverage: config 100 %, mapping 97.0 %, simulation 97.0 %, models 96.9 %, plc 96.3 %,
  modbus 91.5 %, cmd/simulator 79.5 %
- Codec: 19 encode cases (all types, both word orders, rounding, saturation, NaN,
  float32 overflow) + round-trip for every type and order
- Map: default map checked field by field; 33 invalid-map cases each with the expected
  message; multi-error report; size reservation; type-narrowed ranges
- Device: init from setpoints; snapshot encoding of all 26 IRs incl. UINT32 and FLOAT32;
  13 holding-write cases (all-or-nothing, partial 32-bit, reserved, unit, unmapped);
  queue full → 06; momentary-coil handshake; FC15 on all coils; E-Stop level
- Atomic snapshot: 8 readers × 3000 reads of IR 0..25 while a writer publishes as fast as
  possible — no read ever mixes two ticks (-race)
- Concurrency: writes, reads and publishing in parallel (-race)
- FC15/FC16: 12 new PDU cases + interop with grid-x client
- End-to-end (real wiring, grid-x client): IR/HR/coil/DI map, read-back, 01/02/03/0B;
  full production cycle over Modbus at 1000×: start coil acknowledged → HEATING →
  RUNNING → steady 100 rpm / 450 kg/h / 10.0 mm → HR0 = 120 rpm → throughput > 530 kg/h,
  length increasing → E-Stop → FAULT, code 1, DI fault+estop → reset refused while
  pressed → FC15 release+reset → STOPPED, all coils 0; 48 concurrent clients mixing
  reads and setpoint writes; bad/missing map files rejected at start-up
- Manual: built binary at -time-scale 200, Python stdlib client: same cycle plus FC16
  production target 50 m → DI target_reached = 1; log shows state transitions and
  "ResetFault rejected: emergency stop still active"

ACCEPTANCE CRITERIA:
- Client reads real-time process values ............. PASS (e2e + manual)
- Client writes setpoints ............................ PASS (FC06/FC16, validated, applied)
- Client sends Start/Stop commands ................... PASS (coils with handshake)
- Multi-register values read correctly ............... PASS (UINT32 ABCD/CDAB, FLOAT32)
- No data races on snapshot/register access .......... PASS (atomic publish test, -race)
- go test -race ./... ................................ PASS (×5)

KNOWN ISSUES / NOT VERIFIED:
- Still no third-party Modbus tool test (pymodbus / mbpoll / Modbus Poll): clients used are
  grid-x (Go) and a hand-written Python socket client. Installing one needs your approval.
- Command acknowledgement is per command kind: a Start written while another Start is
  pending is merged (by design, ADR-0003).
- Single line only (Phase 6); fault scenario register stored but inert (Phase 5).
- No git repository yet.

NEXT PHASE:
- Phase 5 — Fault Injection Engine (fault table with code/severity/trigger/recovery/reset,
  automatic + manual triggering via HR8, alarm bits DI 5..15, deterministic replay).
```

## Hızlı deneme
```bash
go run ./cmd/simulator -time-scale 100
```
Ardından bir Modbus client ile: coil 0 = 1 (Start), IR 0..25 okuyun; ~11 s sonra durum 2 (RUNNING).
