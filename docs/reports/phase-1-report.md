# Phase 1 Report — Modbus TCP Server MVP

```text
PHASE: 1 — Modbus TCP Server MVP
STATUS: COMPLETED — awaiting user approval for Phase 2

IMPLEMENTED:
- internal/modbus: stdlib Modbus TCP server (ADR-0001 accepted 2026-11-17)
  * MBAP framing (length 2..254 enforced, protocol id != 0 frames consumed and ignored,
    transaction id + unit id echoed)
  * FC01, FC02, FC03, FC04, FC05, FC06; any other FC -> exception 01
  * Spec validation order: FC (01) -> quantity/value (03) -> address (02) -> handler
  * Exceptions answered on the same connection; the connection stays usable
  * Handler interface (6 methods); handler errors mapped with errors.As
    (ExceptionCode, wrapping allowed); other errors -> 04
  * Connection per goroutine, MaxClients, idle timeout, write timeout, ClientCount()
  * Shutdown(ctx): stop accepting, finish in-flight requests, wake idle connections,
    wait for all goroutines; force-close on ctx expiry. Close(): immediate.
  * slog structured logging (connect/disconnect/reason, failed requests)
- internal/plc: Virtual PLC Memory (4 areas, one RWMutex, consistent per-request
  image, unit id check -> 0B, optional accept-any-unit, per-register holding limits ->
  03 with all-or-nothing writes, producer API SetInputRegisters/SetDiscreteInputs)
- internal/config: flags -addr, -unit-id, -accept-any-unit, -max-clients,
  -idle-timeout, -shutdown-timeout, -log-level, -log-format; all errors reported at once
- cmd/simulator: wiring, draft register layout (C4, DI16, HR9, IR20) + HR limits
  from docs/registers/convention.md, SIGINT/SIGTERM graceful shutdown
- internal/archtest: enforces ADR-0005 import rules (verified it fails on a violation)

SPIKES:
- None needed; Phase 0 spikes covered all Phase 1 uncertainties.

FILES CREATED:
- go.mod, go.sum, .gitignore
- cmd/simulator/{main.go,layout.go,main_test.go}
- internal/modbus/{exception.go,frame.go,functions.go,server.go}
  + {helpers_test.go,frame_test.go,functions_test.go,server_test.go}
- internal/plc/{memory.go,memory_test.go}
- internal/config/{config.go,config_test.go}
- internal/archtest/{doc.go,archtest_test.go}
- docs/reports/phase-1-report.md
FILES CHANGED:
- docs/architecture/adr/0001 (status -> accepted), docs/plans/phase-plan.md, README.md

TEST RESULTS (actually executed, Go 1.26.2 darwin/arm64):
- gofmt -l . : clean   |   go vet ./... : clean
- go test ./... : all 5 packages ok
- go test -race -count=10 ./... : all ok (flakiness check)
- Fuzz: FuzzProcessPDU 30s (~8.4M execs), FuzzDecodeHeader 15s (~4.3M execs): no failures
- Coverage: config 100%, plc 95.1%, modbus 90.7%, cmd/simulator 76.9%
- Notable tests:
  * 31 PDU table cases (normal responses, 01/02/03/04/0B, spec check order)
  * Exceptions keep the connection open (7 sequential requests on one socket)
  * Pipelined requests answered in order; non-Modbus protocol id skipped
  * MaxClients reject + slot reuse; idle timeout; graceful shutdown completes an
    in-flight request; shutdown timeout forces close; no goroutine leak
  * Interop with grid-x/modbus v1.5.1 (independent implementation): FC01–06 + 02/03
  * 32 (package) and 48 (end-to-end) concurrent clients mixing reads and writes
  * Re-exec'd real binary exits 0 on SIGINT and on SIGTERM with a client connected
- Manual end-to-end: built binary on 127.0.0.1:1502, Python 3 stdlib socket client,
  13 requests on one connection: FC01–06 OK, 03 (HR limit, qty 0, coil 0x1234),
  02 (IR20), 01 (FC16), 0B (unit 9), connection still usable afterwards; SIGTERM ->
  "shutdown complete".

ACCEPTANCE CRITERIA:
- Server runs on 127.0.0.1:1502 ..................... PASS (default config, manual run)
- External client can connect ........................ PASS (grid-x Go client, Python stdlib client)
- Register read and write succeed .................... PASS (FC01–06)
- Invalid addresses return an exception .............. PASS (02; plus 01/03/0B)
- Multiple clients can read simultaneously .......... PASS (48 concurrent, -race)
- go test ./... passes ............................... PASS
- Extra: go vet / -race clean; exception 03 keeps the connection open .. PASS

KNOWN ISSUES / NOT VERIFIED:
- Not yet tested with a third-party Modbus tool (mbpoll / pymodbus / Modbus Poll):
  none installed; installing one needs your approval. Both clients used are
  independent of our server code, but neither is a "standard SCADA tool".
- FC15/FC16 intentionally return exception 01 (planned for a later phase).
- Register values are static zeros until the simulation is wired in (Phase 2/4);
  coils are plain memory (command latch per ADR-0003 is Phase 4).
- No git repository yet (not initialised without approval).
- No metrics yet (Phase 8); ClientCount() is the only hook so far.

NEXT PHASE:
- Phase 2 — Simulation Engine (fixed timestep runner, state machine
  STOPPED/HEATING/RUNNING/FAULT, seeded RNG, noise, unit tests without Modbus).
```

## Çalıştırma
```bash
go run ./cmd/simulator
```
```bash
go test -race ./...
```
