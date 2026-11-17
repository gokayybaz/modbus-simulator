# Phase 0 Report — Research & Architecture

```text
PHASE: 0 — Research & Architecture
STATUS: COMPLETED — awaiting user approval (ADR-0001 decision required)

IMPLEMENTED:
- Throwaway PoC module (spikes/poc, separate go.mod): Modbus TCP server on
  simonvetter/modbus v1.6.4 with a minimal 4-area virtual memory, unit-id check,
  holding-register value validation, UINT32/FLOAT32 ABCD + scaling helpers.
- Simulation PoC (spikes/poc/simspike): fixed timestep, wall-clock-free engine,
  exact first-order lag, seeded PCG noise on measurements only, ctx-cancelled runner.

SPIKES:
- 001 Modbus library: 6 modules evaluated (versions from proxy.golang.org, repo
  metadata from GitHub API, source read). Recommendation: own stdlib server;
  fallback: simonvetter/modbus. grid-x/modbus selected as test-only interop client.
- 002 Memory model: 0-based PDU addressing, ABCD word order, scaling w/ saturation,
  exception policy (01/02/03/04/0B), per-request atomicity verified.
- 003 Simulation engine: fixed dt, tick-boundary commands, per-line RNG streams,
  lag/dead-time/rate-limit primitives, state machine + process dependency draft.

FILES CREATED:
- docs/spikes/001-modbus-library.md, 002-memory-model.md, 003-simulation-engine.md
- docs/architecture/overview.md
- docs/architecture/adr/0001..0005
- docs/registers/convention.md
- docs/plans/phase-plan.md
- docs/reports/phase-0-report.md
- spikes/poc/{go.mod,go.sum,main.go,server.go,poc_test.go}
- spikes/poc/simspike/{simspike.go,simspike_test.go}
- README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- cd spikes/poc && go vet ./... : clean
- go test -race -count=1 ./... : ok poc (6 tests, 10 subtests), ok simspike (4 tests)
  * FC01–FC06 via grid-x/modbus client: PASS
  * UINT32/FLOAT32 ABCD decode + raw byte layout: PASS
  * Exceptions over raw TCP frames: 02, 03 (setpoint limit), 01 (FC 0x07, 0x2B), 0B: PASS
  * Library deviation confirmed: qty=0, qty=126, FC05 value 0x1234 -> connection
    closed instead of exception 03 (test asserts current behaviour)
  * 16 concurrent clients x 200 reads with concurrent writer, no torn 2-register
    reads, race detector clean: PASS
  * Stop() closes clients; goroutines return to baseline: PASS
  * Deterministic replay (same seed => identical), noise not leaking into true state,
    lag 63.2% at 1 tau, no production while stopped, runner exits on cancel: PASS
- End-to-end: PoC binary on 127.0.0.1:1502 read by an independent Python 3 stdlib
  socket client: live value changing every 500 ms, UINT32=123456, FLOAT32=3.14159,
  out-of-range read -> 0x84 0x02.

ACCEPTANCE CRITERIA:
- ADRs ......................................... DONE (5)
- Technology evaluation ........................ DONE (Spike 001)
- Modbus register convention ................... DONE (draft v0)
- Project folder structure ..................... DONE (overview §2)
- Phase-based plan ............................. DONE (phase-plan.md, Phase 1 tasks)
- Risks & assumptions .......................... DONE (overview §4, R1–R9)
- Working minimal Modbus TCP PoC ............... DONE (tested via 2 independent clients)

KNOWN ISSUES / NOT VERIFIED:
- Not tested with a third-party GUI/CLI tool (mbpoll, modpoll, Modbus Poll, pymodbus
  are not installed here).
- aldas/go-modbus-client server package evaluated by source reading only.
- GitHub stars/issue counts are point-in-time (2026-11-17).
- Pipe OD register (UINT16 x100) saturates above 655.35 mm (risk R3).
- Process coefficients are placeholders, not validated against plant data.
- No git repository yet (directory was empty; not initialised without approval).

NEXT PHASE:
- Phase 1 — Modbus TCP Server MVP (task list: docs/plans/phase-plan.md).
  Needs decision: ADR-0001 own stdlib server (recommended) vs simonvetter/modbus.
```
