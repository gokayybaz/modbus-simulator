# Phase 5 Report — Fault Injection Engine

```text
PHASE: 5 — Fault Injection Engine
STATUS: COMPLETED — awaiting user approval for Phase 6

IMPLEMENTED:
- Fault model = cause injection + automatic detection (Spike 007, option C):
  * 8 injectable causes (HR8 / SetScenario): screw blockage, screen pack clogging,
    zone 2 heater stuck on, zone 1 heater failure, cooling water failure, vacuum leak,
    haul-off slip, zone 2 thermocouple open. Continuous effects develop through lags
    (onset and repair); a cause persists until HR8 = 0 ("repair").
  * Model hooks (zero value = healthy): heater SupplyOff/Stuck/Failed/SensorFailed,
    extruder TorqueFactor, melt PressureFactor, pipe Slip + drive Reference, StopNow.
  * 14 monitors on MEASURED values (as a PLC would see them) → 15 fault codes incl.
    E-Stop: motor overload 10, melt pressure high 11, zone/die temp high 21–24,
    zone/die temp low 31–34, cooling water high 40, vacuum low 41, haul-off deviation 50,
    sensor failure 60, emergency stop 1. Each: code, name, severity (trip/critical),
    alarm bit, warning limit, trip limit + delay, gate (e.g. RUNNING only), recovery
    condition, heater impact.
  * Warning → alarm bit (no state change); trip → FAULT with drives and vacuum pump cut
    in the same tick (PLC scan semantics), counters frozen (kept, resume after restart).
  * Heaters in FAULT per fault: off for E-Stop, over-temperature, sensor failure; holding
    temperature for process faults.
  * Reset rules: E-Stop released AND the latched fault's recovery condition met (e.g. melt
    pressure ≤ 300 bar, sensor plausible); otherwise rejected with a logged reason.
  * First fault latched; IR15 active code, IR28 last code (kept after reset), IR27 active
    scenario, IR26 alarm word, DI 5–13 alarm bits; HR8 validated 0..8.
- docs/faults/fault-table.md — the fault and scenario tables (a test keeps it in sync
  with the code).
- Runner coalesces queued SetScenario like setpoints.

SPIKES:
- 007 Fault model (docs/spikes/007-fault-model.md): forced faults vs cause injection vs
  both → both (cause via HR8, direct TriggerFault kept in the engine API for Phase 7).
  Faults live in internal/simulation (not internal/faults) to avoid a dependency cycle
  with the engine's tick state — deliberate deviation from the suggested layout.

ISSUES FOUND AND FIXED DURING TESTING:
- False trip: raising a zone setpoint by 30 °C while RUNNING tripped "zone temp low".
  Low-temperature trips now also require the heater to deliver < 50 % (heater current
  monitoring); symmetric to over-temperature (trip only while the heater still draws
  current). Setpoint changes now give a warning bit only. Verified by a 2 h normal-
  operation test (150 rpm, ±30/60 °C setpoint changes, stop/start): no trips.
- Automatic trips were detected at the end of a tick but the drives were cut only in
  the next tick; for non-blocking faults a reset in that next tick skipped the
  immediate stop (drives ramped down instead). Drives are now cut in the trip tick.
- Documented trip times were measured from one tick after the scenario was applied;
  corrected (+0.5 s) and the test now uses the same origin.

FILES CREATED:
- internal/simulation/{faults.go,scenarios.go,faults_test.go}
- docs/spikes/007-fault-model.md, docs/faults/fault-table.md, docs/reports/phase-5-report.md
FILES CHANGED:
- internal/simulation/models/{heating.go,extruder.go,melt.go,pipe.go,models_test.go}
- internal/simulation/{engine.go,line.go,state.go,runner.go,engine_test.go}
- internal/mapping/{map.go,device.go,map_test.go,device_test.go}
- configs/register-map.yaml (HR8 scenario, IR26–28, DI5–13)
- cmd/simulator/main_test.go
- docs/registers/convention.md, docs/plans/phase-plan.md, README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=5 ./... : all 8 packages ok
- Coverage: simulation 97.3 %, models 97.1 %, mapping 96.7 %, plc 96.3 %, modbus 91.5 %,
  cmd 79.5 %, config 100 %
- Scenario chains (8 sub-tests), each from steady production: warning bit before or
  with the trip; expected code; trip time within ±25 % of the documented value; drives
  stopped in the trip tick; reset refused right after the trip where the recovery
  condition applies (6 of 8) and accepted where it does not (vacuum, haul-off); heater
  impact as documented; repair → reset accepted → STOPPED with last code kept;
  counters unchanged during FAULT; restart → RUNNING, counters continue
- Measured trip times (sim time from the scenario write, seed 1):
  screw blockage 40.5 s, screen clogging 59.5 s, heater stuck 441.5 s, heater failure
  99.5 s, cooling failure 193.5 s, vacuum leak 28.5 s, haul-off slip 10.5 s,
  thermocouple open 1 s
- Unrepaired cause trips again after reset + restart (motor overload)
- No false trips in 2 h of normal operation; only expected warnings
  (overload pre-alarm at 150 rpm, deviation alarms during setpoint changes)
- Deterministic replay: 7000-tick script with two scenarios, E-Stop, repairs and
  resets — two runs identical tick by tick (incl. alarms and fault codes)
- Sensor failure is detected from the measurement while the true temperature is normal
- Fault table: 15 unique codes, every alarm covered, every code/name/scenario present
  in docs/faults/fault-table.md
- Model hooks: stuck/failed/no-supply/open-TC heater power, torque ×1.7, pressure ×1.8,
  slip (reference unchanged, pipe slower, wall thicker), StopNow
- Mapping: HR8 → SetScenario (0..8, fractional rejected, 9 → 03), IR26–28 and DI5–13
  encoding
- End-to-end over Modbus at 1000×: start → RUNNING → HR8 = 8 → FAULT 60, IR28 = 60,
  IR27 = 8, zone 2 reads 999.9 °C, alarm bit + DI → reset refused (coil acknowledged)
  → HR8 = 0 → reset → STOPPED, IR15 = 0, IR28 = 60; HR8 = 9 → exception 03

ACCEPTANCE CRITERIA:
- Fault register updated on a fault ...................... PASS (IR15, IR28, IR26, DI)
- Machine state changes appropriately .................... PASS (FAULT, safe state, impact)
- Alarm reset rules enforced ............................. PASS (E-Stop + recovery conditions)
- Production counter behaviour after a fault tested ...... PASS (frozen, kept, resume)
- Fault scenarios replay deterministically ............... PASS (7000-tick replay)
- Faults triggerable automatically and manually .......... PASS (monitors; HR8, E-Stop, TriggerFault API)

KNOWN ISSUES / ASSUMPTIONS:
- Thresholds/delays are engineering choices for the reference line, not plant data;
  configurable thresholds come with the Phase 7 YAML configuration.
- Each scenario targets one fixed zone (zone 1 heater failure, zone 2 stuck heater /
  open thermocouple); per-zone selection could be added in Phase 7.
- Direct forced faults (TriggerFault) are not exposed over Modbus; Phase 7 scenarios use them.
- Carried over: no git repository; no third-party Modbus tool test.

NEXT PHASE:
- Phase 6 — Multi-Line Simulation (Line 1/2 HDPE, Line 3 PVC; isolation via unit id on
  one endpoint vs separate ports — spike first).
```
