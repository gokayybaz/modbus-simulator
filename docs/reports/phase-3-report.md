# Phase 3 Report — Extrusion Process Models

```text
PHASE: 3 — Extrusion Process Models
STATUS: COMPLETED — awaiting user approval for Phase 4

IMPLEMENTED:
- New package internal/simulation/models (no randomness, no wall clock):
  * dynamics: FirstOrderLag, RateLimiter (moved from Phase 2), DeadTime, PI controller
    with output limits and conditional-integration anti-windup
  * ThermalZone: energy balance C·dT/dt = P_heater + P_process − h·(T − T_amb), PI heater;
    process heat ∝ throughput (feed zone draws heat, metering zone/die receive shear heat)
  * Extruder: rpm ramp, throughput = k·rpm with lag, mechanical power =
    no-load + SEC·ṁ·√η_rel, electrical power, 3-phase motor current, load %
  * Melt: lagged melt temperature (zones + shear heat), viscosity ratio (Arrhenius-like,
    clamped), die pressure ∝ ṁ^0.4·η_rel with dead time + lag
  * Vacuum: pull/leak-down, capacity factor (hook for Phase 5)
  * Cooling: water temperature from released heat (sensible + latent) / water flow,
    tank lag, cooling efficiency, flow factor (hook for Phase 5)
  * Pipe: haul-off ramp, OD = calibrator·(1 − vacuum-deficit shrink − warm-water shrink),
    wall from mass conservation, distance-based transport to the gauge (6 m): reading
    appears only after the section has travelled, freezes when the line stops,
    0 before the first pipe arrives
- internal/simulation/line.go composes the models in causal order each tick and computes
  total power (motor + heaters + vacuum pump + haul-off); production counters in RUNNING
- Snapshot: ProcessValues extended to the full input-register set (rpm, motor current,
  motor load, melt pressure, melt temp, zones, die, vacuum, water temp, haul-off speed,
  throughput, power, OD, wall) — True and Measured; new Internals (heater powers,
  viscosity ratio, mech power, cooling load/efficiency, exit OD/wall)
- 14 independent sensor streams with noise specs per measurand
- Config.Equipment → Config.Process (DefaultProcessParams = 110×10 mm PE100 reference line);
  default rpm setpoint 45 → 100

DECISIONS TAKEN WITHOUT EXPLICIT INPUT (please confirm or override):
- STOPPED keeps heaters OFF (state stays "everything off").
- Heating/cooling asymmetry now comes from the energy balance: heat-up ~18 min,
  natural cool-down 200→90 °C ~20 min (previously symmetric and too fast).

SPIKES:
- 005 Process model (docs/spikes/005-process-model.md): reference product, model chain,
  coefficient table with hand calculations (mass balance s = 9.999 mm, motor 164 A,
  cooling load 77 kW → +4.4 °C), explicit simplifications.

ISSUES FOUND AND FIXED DURING TESTING:
- Feed-zone heater saturated at 120 rpm (needed 12.96 kW of 12 kW) → zone 1 fell to
  163 °C. Feed-zone process heat set to −4 kW (margin up to ~150 rpm). The saturation
  behaviour itself is kept and tested (useful for Phase 5 "zone temperature low").
- Inverse pressure response: an rpm increase first LOWERED pressure because shear heat
  reached melt viscosity instantly while flow lagged 8 s. Added a 20 s melt-temperature
  lag; a regression test asserts pressure never dips during a speed-up.
- Extruder/haul-off stopNow re-ramped in the same tick in the model API (engine was
  unaffected because it passes target 0); fixed and tested.

FILES CREATED:
- internal/simulation/models/{heating.go,extruder.go,melt.go,cooling.go,pipe.go,models_test.go}
- internal/simulation/{line.go,causality_test.go}
- docs/spikes/005-process-model.md, docs/reports/phase-3-report.md
FILES CHANGED / MOVED:
- internal/simulation/dynamics(.go,_test.go) → models/ (+ DeadTime, PI)
- internal/simulation/equipment.go removed (replaced by line.go)
- internal/simulation/engine.go (Process config, Internals), engine_test.go (numbers
  adapted to the new physics; all Phase 2 behavioural assertions kept)
- docs/plans/phase-plan.md, README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=5 ./... : all 7 packages ok
- Coverage: simulation 96.8 %, simulation/models 96.9 %
- Model tests: dead time exact, PI gain/anti-windup, zone reaches SP in 5–30 min with
  < 1 °C overshoot and zero offset, holding power = losses (5760 W), cooling ≥ 2× slower
  than heating, heater saturation lowers temp; extruder nominal 450 kg/h / 94 kW /
  164.0 A / 71.2 %; melt lag 63 % at 1τ, pressure 220 bar nominal, 2^0.4 at double flow,
  e^0.7 at −35 °C; vacuum pull/half capacity/decay; cooling 77.1 kW → 16.43 °C, 30 %
  flow → > 25 °C; mass balance 10.0 mm; transport delay 288–300 ticks for 6 m; wall at
  gauge unchanged for 5.5 m after a speed change, correct after 6.5 m; frozen when
  stopped; OD shrink −1 % at half vacuum, −0.3 % at +10 °C water
- Causality tests (engine level): reference point within tolerance of the hand
  calculation; rpm 100→120 raises throughput, pressure, current, power, water temp,
  melt temp, wall and weight while zone 1 is held; pressure has a dead time and no
  inverse response; haul-off 2.5→3.0 thins the wall only after transport, throughput
  unchanged, final wall equals the mass balance; colder zone 3/die raise viscosity,
  pressure and current; counters: exactly 150 m and 450 kg per simulated hour; after
  Stop everything settles to idle while the gauge keeps the last section
- Phase 2 behavioural tests (state machine, E-Stop, replay determinism over 4500 ticks
  visiting all 4 states, no counting outside RUNNING, no negative values — now over
  all 14 measurands) all pass

DEMO TRAJECTORY (true values, default config; temporary program, deleted):
     t  state     Z1    Z3   Die  Melt   rpm  I[A]   bar  vac   Tw  m/min  kg/h    kW     OD   wall    len
     0  HEATING  20.1  20.1  20.1  20.0    0    0     0    0  12.0  0.00     0   44.0    0     0      0
   600  HEATING 154.0 154.0 145.9 146.6    0    0     0    0  12.0  0.00     0   44.0    0     0      0
  1058  RUNNING 178.8 197.7 200.8 198.8    0    0     0    0  12.0  0.00     0   24.2    0     0      0
  1200  RUNNING 176.7 199.6 203.2 211.2  100  162  214.8  400  14.2  2.50   450  133.5    0     0    5.8  (pipe not at gauge yet)
  1500  RUNNING 178.9 199.9 204.4 212.1  100  161  210.9  400  16.0  2.50   450  132.3  110.02 10.00 18.3
  2400  RUNNING 180.0 200.0 205.0 212.5  100  160  209.3  400  16.4  2.50   450  131.6  109.99 10.00 55.8
  2730  RUNNING 179.7 200.1 205.0 214.0  120  189  218.0  400  16.5  2.50   538  149.3  109.99 10.00 69.6  (SP 120 rpm @2700 s)
  3450  RUNNING 179.9 200.0 205.0 214.5  120  189  216.2  400  17.3  3.00   540  150.0  109.96 10.00 100.8 (haul-off 3.0 @3300 s)
  4500  STOPPED 141.9 159.8 164.3 164.5    0    0     0    0  13.1  0.00     0    0.0  109.96 10.00 138.3 (Stop @4200 s)
  5400  STOPPED  76.4  84.6  88.2  87.5    0    0     0    0  12.0  0.00     0    0.0  109.96 10.00 138.3
  Between 2730 s and 3300 s (120 rpm at 2.5 m/min) the gauge showed 12.28 mm wall;
  max overshoot over setpoint during the whole run: 0.20 °C.

ACCEPTANCE CRITERIA:
- Models logically related ............................ PASS (causality tests)
- Setpoint changes affect process values .............. PASS (rpm, haul-off, zone/die SP)
- Temperatures do not jump to targets ................. PASS (≤ 0.2 °C/tick, PI no overshoot)
- RPM change affects pressure and throughput .......... PASS (with lag/dead time, no inverse response)
- Counters increase with simulated time ............... PASS (150 m + 450 kg per sim hour)
- No physically meaningless negative values .......... PASS (14 measurands + counters, 4500-tick script)

KNOWN ISSUES / ASSUMPTIONS:
- Coefficients are engineering-plausible, not validated against plant data.
- No pressure back-flow (throughput independent of die pressure), no zone-to-zone heat
  coupling, no ovality/eccentricity/draw-down.
- Heater/vacuum/cooling "health" factors exist (capacity, flowFactor) but are fixed at
  1 until Phase 5 drives them.
- Simulation still not wired to Modbus registers (Phase 4). Heat-up takes ~18 sim
  minutes; a time-acceleration option may be useful for demos (Phase 7 candidate).
- Carried over: no git repository; no third-party Modbus tool test.

NEXT PHASE:
- Phase 4 — Register Mapping & PLC Integration: Snapshot → input registers / discrete
  inputs with scaling, holding-register setpoints → commands, self-clearing command
  coils (ADR-0003), runner wired into cmd/simulator, atomic snapshot publishing.
```
