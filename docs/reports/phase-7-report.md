# Phase 7 Report — Configuration & Scenario Engine

```text
PHASE: 7 — Configuration & Scenario Engine
STATUS: COMPLETED — awaiting user approval for Phase 8

IMPLEMENTED:
- YAML configuration file (-config), precedence default < file < explicit flag:
  server (addr, unit id, isolation, accept-any, max clients, timeouts), log, simulation
  (period, time scale, seed), register map, scenario, lines[] (up to 32) with name, preset,
  unit id, port, seed, register map, scenario, min_run_temp, setpoints, setpoint_limits and
  partial overrides of process parameters, sensor noise and fault limits.
  Reference: docs/configuration.md; example: configs/simulator.example.yaml (3 lines).
- Start-up validation of everything (file, presets, overrides, maps, scenarios, scenario
  values vs line limits) before anything starts; all problems reported together with
  location, e.g. "sim.yaml: lines[0] (a): process: line 8: field output_per_rmp not found";
  -validate checks and exits.
- Simulation made configurable without changing defaults: yaml tags on all parameter
  structs, NoiseConfig (12 sensor groups), FaultLimits (warning/trip/delay per monitor,
  sensor range), per-line setpoint limits (narrowing only; enforced by the engine, the
  Modbus write path and scenario validation), min_run_temp.
- internal/scenarios: YAML scenarios (14 actions incl. the brief's inject_fault and
  inject_cause/clear_cause), strict validation, tick-exact player (event at t → tick
  round(t/dt)+1), loop option, 9 embedded scenarios selectable by name.
- Runner CommandSource (scenario commands first, then client commands in the same tick);
  simulation.Replay for offline deterministic runs.
- Register sync: setpoint, scenario (HR8) and E-Stop (coil 3) changes made by a scenario are
  reflected in the PLC image; a pending client write is never overwritten.
- plant.Build: config → isolated lines; cmd/simulator rebuilt on top of it.

SPIKES:
- 009 Configuration and scenarios (docs/spikes/009-configuration-and-scenarios.md):
  verified with go.yaml.in/yaml/v3 that a raw yaml.Node decoded strictly onto a preset
  struct changes only the given keys, decodes "12s" into time.Duration, rejects typos and
  "twelve"; node line numbers can be mapped back to the file. Scenario timing and flag
  precedence decided there. No new dependency.

ISSUES FOUND AND FIXED:
- The cold-startup scenario exposed a missing interlock: at a 120 °C soak the zones were
  "at setpoint", the screw started and the line tripped on melt pressure after 6 s. Added a
  cold-start interlock (min_run_temp: HDPE 160 °C, PVC 150 °C) — real extruders have one.
- Holding registers did not reflect scenario-driven setpoint changes (noted in Phase 4) —
  fixed by the register sync.
- plant.Build crashed with a nil logger (scenario player) — fixed and covered.
- Override errors reported line numbers relative to the section — mapped to file lines.

FILES CREATED:
- internal/scenarios/{scenarios.go,scenarios_test.go}
- internal/plant/{build.go,build_test.go}
- configs/simulator.example.yaml, configs/scenarios/*.yaml (9)
- docs/spikes/009-configuration-and-scenarios.md, docs/configuration.md, docs/scenarios.md,
  docs/reports/phase-7-report.md
FILES CHANGED (main):
- internal/config/{config.go,config_test.go} (rewritten: file + flags + line resolution)
- internal/simulation/{engine.go,state.go,line.go,faults.go,runner.go,presets.go,noise.go} + tests
- internal/simulation/models/*.go (yaml tags)
- internal/mapping/{device.go,device_test.go}, internal/plc/{memory.go,memory_test.go}
- internal/plant/{plant.go}, configs/embed.go, cmd/simulator/{main.go,main_test.go}
- internal/archtest (scenarios/config rules), README, phase plan

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt clean, go vet ./... clean
- go test -race -count=5 ./... : all 10 packages ok
- Coverage: config 100 %, scenarios 99.3 %, models 97.1 %, simulation 96.9 %, plc 96.6 %,
  mapping 96.2 %, plant 94.8 %, modbus 91.5 %, cmd 86.4 %
- Config: defaults, all flags, port isolation (incl. port 0), full file, flags overriding the
  file (per-line seed still wins), file without lines + -lines, 32 invalid cases each with
  its message, joined errors, file + line location
- Scenarios: 17 invalid-scenario cases, every action compiled to the right command, player
  tick mapping and loop, all 9 built-ins on the reference line (state/fault sequences),
  cold-startup never runs during the soak, heating-delay not RUNNING before repair,
  normal-production reaches its target, emergency-stop reset refused while pressed,
  bit-identical re-run of a scenario
- Build: example config (overrides, limits, noise reach the engine and the Modbus write
  path), overrides change physics (lowered overload limit trips; zero noise → measured =
  true), 13 build-error cases (incl. file line of a typo, scenario value beyond line limits),
  multi-line error report, scenario played through the source
- Simulation: line setpoint limits, noise/fault-limit validation, source-before-client
  order, Replay, cold-start interlock; all Phase 2–6 tests unchanged and passing
- Device: line limits narrow writes; register sync incl. pending client write kept
- End-to-end: config file with a scripted and an idle line (scenario rpm → HR0 = 900,
  cause → HR8 = 6, E-Stop → coil 3 = 1 and fault 1, idle line STOPPED, scenario logs);
  -validate (no listener, < 2 s; invalid config and unknown scenario rejected);
  -scenario pressure-test → fault 11
- Manual: -validate on the example (valid, 3 lines) and on a broken file (4 problems in
  3 lines reported at once, typo at its real file line 8)

ACCEPTANCE CRITERIA:
- Scenarios definable in YAML ......................... PASS
- The same scenario can be run again (identically) .... PASS (bit-identical re-run; loop option)
- Faulty configurations validated at start-up ......... PASS (-validate; nothing starts on error)
- Invalid parameters give clear error messages ........ PASS (file/line/field location, all at once)
- YAML: server, machines, register maps, intervals,
  process parameters, noise levels, fault scenarios ... PASS

KNOWN ISSUES / ASSUMPTIONS:
- Built-in scenario timings are tuned for hdpe-110; on other presets the events still apply
  but the narrative timing (e.g. "after heat-up") may differ.
- In `noise` a group given in YAML replaces the whole group (bools default to false);
  `zones:` overrides must list all three zones (YAML list).
- Configuration is read at start-up only (no hot reload).
- Carried over: no git repository; no third-party Modbus tool test; -lines default 1.

NEXT PHASE:
- Phase 8 — Observability & Reliability (metrics: ticks, dropped ticks, clients, requests,
  errors, register reads/writes; optional Prometheus endpoint; long-run, reconnect, load,
  goroutine/memory leak and benchmark tests).
```
