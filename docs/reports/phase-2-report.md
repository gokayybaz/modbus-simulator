# Phase 2 Report — Simulation Engine

```text
PHASE: 2 — Simulation Engine
STATUS: COMPLETED — awaiting user approval for Phase 3

IMPLEMENTED (internal/simulation, no protocol imports — enforced by archtest):
- Stepper interface (Step / Snapshot / Period) — contract for runner, Phase 4 mapping,
  Phase 6 line types
- Engine: fixed timestep (default 500 ms), sim time = tick × period, never reads the
  wall clock; commands applied only at tick boundaries
- State machine STOPPED / HEATING / RUNNING / FAULT
  * Start: STOPPED→HEATING; Stop: HEATING|RUNNING→STOPPED; Stop beats Start in a tick
  * HEATING→RUNNING when all zones + die stay within ±5 °C of setpoint for 30 s
    (hold restarts if any zone leaves the band)
  * TriggerFault(code) / EmergencyStop(true) → FAULT; first fault code is latched
  * E-Stop is maintained: Reset refused while pressed; release + Reset → STOPPED
  * Reset handled before Start in a batch ("Reset + Start" restarts a cleared line)
  * Rejected commands returned as errors; the tick still happens
- Commands are comparable values (recordable/replayable); setpoints validated against
  the convention limits (rpm 0–150, temps 0–260 °C, haul-off 0–30 m/min)
- Dynamics: exact first-order lag (step-size invariant), rate limiter
- Deterministic RNG: math/rand/v2 PCG, one stream per sensor = (seed, FNV-64a(name))
- Sensor noise: Gaussian + bounded random-walk drift, NonNegative, ZeroAtZero;
  noise applied to measurements only, never to process state
- Placeholder equipment (to be replaced in Phase 3): 3 barrel zones + die (lag),
  screw rpm and haul-off speed (ramps, RUNNING only), throughput lag ∝ rpm,
  length/weight integrated in RUNNING only; FAULT = drives stop at once, heaters off
- Snapshot: comparable struct with True and Measured process values, state, state time,
  fault code, E-Stop, setpoints, counters
- Runner: time.Ticker pacing, thread-safe bounded command queue (Submit), atomic latest
  snapshot, OnTick hook, Stats (ticks, dropped ticks, last step time), logs state
  changes and rejected commands, ctx cancellation, no extra goroutines

SPIKES:
- 004 Runner pacing (docs/spikes/004-runner-pacing.md): measured Go ticker — no
  cumulative drift (300 ticks = 300 periods ±1 ms), σ ≈ 0.5 ms jitter, missed ticks
  dropped not queued. Decision: keep ADR-0004 "no catch-up", report DroppedTicks.

FILES CREATED:
- internal/simulation/{engine.go,state.go,equipment.go,dynamics.go,noise.go,runner.go}
- internal/simulation/{engine_test.go,dynamics_test.go,noise_test.go,runner_test.go}
- docs/spikes/004-runner-pacing.md, docs/reports/phase-2-report.md
FILES CHANGED:
- docs/plans/phase-plan.md (Phase 2 plan + status), README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=1 ./... : all 6 packages ok
- go test -race -count=20 ./internal/simulation : ok (no flakiness)
- Coverage internal/simulation: 96.7 %
- Key tests:
  * 25-case state transition table + E-Stop sequence (7 steps) + release/reset same tick
  * Deterministic replay: 4000-tick script visiting all 4 states, two runs with seed 42
    → every Snapshot identical (==); seed 43 → identical true state/counters, different
    measurements
  * STOPPED 20 min and all of HEATING: counters 0, measured rpm/throughput exactly 0;
    after Stop and in FAULT counters frozen
  * HEATING→RUNNING tick matches analytic τ·ln(185/5) + hold (±1 tick)
  * RUNNING 600 s: length within 0.05 m and weight within 1 % of the analytic integral
  * Zone temperature per-tick change bounded (no jumps); setpoint 45→80 rpm ramps 2.5 rpm/tick
  * Lag = 1−1/e at 1τ (1e-9), step-size invariance, rate limiter exact
  * Noise: mean ≈ 0, σ within 0.05 of spec over 50k samples; drift bounded; stream
    alignment after suppressed readings
  * Runner: commands batched per tick in order, queue full, concurrent Run rejected,
    dropped ticks reported, concurrent Snapshot/Stats reads under -race,
    goroutine count back to baseline after cancel
- Demo trajectory (default config, measured values; temporary program, deleted):
      t[s]   state    Z1     Z2     Z3    Die   rpm  m/min  kg/h  len[m]   kg
      0.5   HEATING  20.7   20.5   20.4  20.6   0.0  0.00    0.0   0.00   0.00
    225.0   HEATING 155.6  164.1  172.5 176.2   0.0  0.00    0.0   0.00   0.00
    463.0   RUNNING 177.1  187.0  196.9 201.3   0.0  0.00    0.0   0.00   0.00
    600.0   RUNNING 179.2  189.4  199.1 203.3  44.9  2.50  112.2   5.61   3.91
    975.0   RUNNING 179.8  189.9  200.6 205.3  70.0  2.50  175.5  21.24  16.76  (SP 70 rpm @900 s)
   1200.5   STOPPED 179.4  189.0  198.6 203.5  65.0  2.00  172.3  30.59  27.68  (Stop @1200 s)
   1350.0   STOPPED  65.7   68.9   71.7  73.0   0.0  0.00    0.0  30.59  27.68

ACCEPTANCE CRITERIA:
- Testable without Modbus server .......................... PASS (package imports no protocol code)
- Same seed + same commands ⇒ same results ............... PASS (4000-tick replay, ==)
- Counters do not increase in STOPPED .................... PASS (also HEATING, FAULT, after Stop)
- State transitions testable ............................. PASS (table + sequences)
- No goroutine leak on shutdown .......................... PASS (runner test, -race)
- Default period 500 ms .................................. PASS (DefaultConfig)

KNOWN ISSUES / ASSUMPTIONS (to revisit in Phase 3):
- Placeholder physics: heating and cooling share one time constant (cooling in the
  demo is unrealistically fast); zones are uncoupled; Stop switches heaters off
  immediately — real lines often hold temperature after a stop. Needs your
  preference for Phase 3.
- Throughput is purely ∝ rpm (no melt temperature/viscosity effect yet).
- FaultEmergencyStop = 1 is provisional; the fault code table is Phase 5.
- Real-time runner is not wired into cmd/simulator yet (Phase 4 connects it to
  PLC memory and coils); Modbus registers still read 0.
- Real-time command arrival is not deterministic by nature; determinism is
  guaranteed per (tick, command) sequence — replay must record tick numbers.
- Not git-initialised; no third-party Modbus tool test yet (carried over).

NEXT PHASE:
- Phase 3 — Extrusion Process Models (motor current/load, melt pressure with flow
  dependency and delay, vacuum & cooling, haul-off → OD and wall thickness, power,
  production model), replacing the placeholder equipment behind the same Snapshot.
```
