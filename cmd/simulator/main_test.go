package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gx "github.com/grid-x/modbus"
)

// TestMain lets the test binary act as the simulator process for the
// signal-handling test (re-exec with SIMULATOR_E2E_CHILD=1).
func TestMain(m *testing.M) {
	if os.Getenv("SIMULATOR_E2E_CHILD") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// lastAddrs holds the per-line endpoint addresses of the latest startSim.
var lastAddrs []string

// startSim runs the simulator in-process and returns the address of line 1.
func startSim(t *testing.T, args ...string) (addr string, logs *syncBuffer, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logs = &syncBuffer{}
	ready := make(chan []string, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, append([]string{"-addr", "127.0.0.1:0"}, args...), logs, func(a []string) { ready <- a })
	}()
	select {
	case addrs := <-ready:
		addr = addrs[0]
		lastAddrs = addrs
	case err := <-done:
		cancel()
		t.Fatalf("run exited early: %v\n%s", err, logs)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("simulator not ready")
	}
	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() { cancel(); stopErr = <-done })
		return stopErr
	}
	t.Cleanup(func() { _ = stop() })
	return addr, logs, stop
}

func client(t *testing.T, addr string, unit byte) gx.Client {
	t.Helper()
	h := gx.NewTCPClientHandler(addr)
	h.SlaveID = unit
	h.Timeout = 3 * time.Second
	if err := h.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return gx.NewClient(h)
}

func exceptionCode(err error) byte {
	var e *gx.Error
	if errors.As(err, &e) {
		return e.ExceptionCode
	}
	return 0
}

// End-to-end: the real wiring (map → simulation → plc → modbus) with an
// external client, at normal speed (the line stays STOPPED here).
func TestEndToEndReadWrite(t *testing.T) {
	addr, logs, stop := startSim(t)
	c := client(t, addr, 1)
	ctx := context.Background()

	// Input registers 0..25 per the default map; machine STOPPED, zones at ambient.
	b, err := c.ReadInputRegisters(ctx, 0, 26)
	if err != nil || len(b) != 52 {
		t.Fatalf("FC04 IR0..25: % X %v", b, err)
	}
	if state := binary.BigEndian.Uint16(b[0:]); state != 0 {
		t.Fatalf("machine state %d", state)
	}
	if z1 := binary.BigEndian.Uint16(b[8:]); z1 < 190 || z1 > 210 {
		t.Fatalf("zone 1 raw %d, want ~200 (20.0 °C)", z1)
	}
	if _, err := c.ReadInputRegisters(ctx, 29, 1); exceptionCode(err) != 0x02 {
		t.Fatalf("IR29: want exception 02, got %v", err)
	}

	// Holding registers start at the engine's setpoints.
	if b, err := c.ReadHoldingRegisters(ctx, 0, 9); err != nil ||
		!bytes.Equal(b, []byte{0x03, 0xE8, 0x07, 0x08, 0x07, 0x6C, 0x07, 0xD0, 0x08, 0x02, 0x00, 0xFA, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("FC03 HR0..8: % X %v", b, err)
	}
	if _, err := c.WriteSingleRegister(ctx, 0, 455); err != nil {
		t.Fatalf("HR0=455: %v", err)
	}
	if b, err := c.ReadHoldingRegisters(ctx, 0, 1); err != nil || binary.BigEndian.Uint16(b) != 455 {
		t.Fatalf("read back: % X %v", b, err)
	}
	if _, err := c.WriteSingleRegister(ctx, 0, 1501); exceptionCode(err) != 0x03 {
		t.Fatalf("HR0=1501: want exception 03, got %v", err)
	}
	if _, err := c.WriteSingleRegister(ctx, 6, 1); exceptionCode(err) != 0x03 {
		t.Fatalf("FC06 on half of a UINT32: want exception 03, got %v", err)
	}
	if _, err := c.WriteMultipleRegisters(ctx, 6, 2, []byte{0x00, 0x01, 0x86, 0xA0}); err != nil {
		t.Fatalf("FC16 production target: %v", err)
	}
	if b, err := c.ReadHoldingRegisters(ctx, 6, 2); err != nil || binary.BigEndian.Uint32(b) != 100000 {
		t.Fatalf("target read back: % X %v", b, err)
	}
	if _, err := c.WriteSingleRegister(ctx, 9, 1); exceptionCode(err) != 0x02 {
		t.Fatalf("HR9: want exception 02, got %v", err)
	}

	// Coils and discrete inputs.
	if _, err := c.WriteSingleCoil(ctx, 4, 0xFF00); exceptionCode(err) != 0x02 {
		t.Fatalf("C4: want exception 02, got %v", err)
	}
	if b, err := c.ReadDiscreteInputs(ctx, 0, 16); err != nil || !bytes.Equal(b, []byte{0, 0}) {
		t.Fatalf("FC02: % X %v", b, err)
	}
	if _, err := client(t, addr, 7).ReadInputRegisters(ctx, 0, 1); exceptionCode(err) != 0x0B {
		t.Fatalf("unit 7: want exception 0B, got %v", err)
	}

	if err := stop(); err != nil {
		t.Fatalf("run returned %v", err)
	}
	for _, want := range []string{"modbus server listening", "simulation started", "client connected", "shutting down", "simulation stopped", "shutdown complete"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
}

// readState polls IR0 until it equals want.
func waitState(t *testing.T, c gx.Client, want uint16, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		b, err := c.ReadInputRegisters(context.Background(), 0, 1)
		if err == nil && binary.BigEndian.Uint16(b) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("machine state %v (err %v), want %d", b, err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func coilBits(t *testing.T, c gx.Client) byte {
	t.Helper()
	b, err := c.ReadCoils(context.Background(), 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	return b[0]
}

// A full production cycle driven only over Modbus, at 1000× speed.
func TestEndToEndProductionCycle(t *testing.T) {
	addr, _, _ := startSim(t, "-time-scale", "1000")
	c := client(t, addr, 1)
	ctx := context.Background()
	ir := func() []uint16 {
		b, err := c.ReadInputRegisters(ctx, 0, 26)
		if err != nil {
			t.Fatal(err)
		}
		r := make([]uint16, 26)
		for i := range r {
			r[i] = binary.BigEndian.Uint16(b[2*i:])
		}
		return r
	}

	// Start: coil acknowledged (back to 0), HEATING, then RUNNING.
	if _, err := c.WriteSingleCoil(ctx, 0, 0xFF00); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, 1, 5*time.Second)
	if bits := coilBits(t, c); bits != 0 {
		t.Fatalf("start coil not acknowledged: %04b", bits)
	}
	waitState(t, c, 2, 20*time.Second)

	// Let the line reach steady state: the first pipe at the gauge (6 m
	// downstream) is thin start-up scrap made while throughput ramped up.
	deadline := time.Now().Add(20 * time.Second)
	for r := ir(); r[14] < 990 || r[14] > 1010 || r[11] < 4400; r = ir() {
		if time.Now().After(deadline) {
			t.Fatalf("no steady production: %v", ir())
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := ir()
	if r[1] < 990 || r[1] > 1010 || r[11] < 4400 || r[11] > 4600 || r[14] < 990 || r[14] > 1010 {
		t.Fatalf("not at the reference point: rpm %d kg/h %d wall %d", r[1], r[11], r[14])
	}
	len1 := uint32(r[16])<<16 | uint32(r[17])

	// Setpoint over Modbus: 120 rpm raises throughput (and wall thickness).
	if _, err := c.WriteSingleRegister(ctx, 0, 1200); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for ir()[11] < 5300 {
		if time.Now().After(deadline) {
			t.Fatalf("throughput did not follow the setpoint: %v", ir())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len2 := uint32(ir()[16])<<16 | uint32(ir()[17]); len2 <= len1 {
		t.Fatalf("produced length did not increase: %d → %d", len1, len2)
	}

	// E-Stop (maintained): FAULT with code 1, DI fault + e-stop; reset refused while pressed.
	if _, err := c.WriteSingleCoil(ctx, 3, 0xFF00); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, 3, 5*time.Second)
	if r := ir(); r[15] != 1 {
		t.Fatalf("fault code %d", r[15])
	}
	if b, err := c.ReadDiscreteInputs(ctx, 0, 4); err != nil || b[0] != 0b1100 {
		t.Fatalf("DI: %08b %v", b, err)
	}
	_, _ = c.WriteSingleCoil(ctx, 2, 0xFF00)
	time.Sleep(50 * time.Millisecond)
	waitState(t, c, 3, time.Second)

	// Release + reset in one FC15 request → STOPPED, coils back to 0.
	if _, err := c.WriteMultipleCoils(ctx, 2, 2, []byte{0b01}); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, 0, 5*time.Second)
	time.Sleep(20 * time.Millisecond)
	if bits := coilBits(t, c); bits != 0 {
		t.Fatalf("coils after reset: %04b", bits)
	}
}

func TestEndToEndAcceptAnyUnit(t *testing.T) {
	addr, _, _ := startSim(t, "-accept-any-unit")
	for _, u := range []byte{0, 1, 17, 255} {
		if _, err := client(t, addr, u).ReadInputRegisters(context.Background(), 0, 1); err != nil {
			t.Fatalf("unit %d: %v", u, err)
		}
	}
}

func TestEndToEndConcurrentClients(t *testing.T) {
	addr, _, _ := startSim(t, "-max-clients", "64")
	var wg sync.WaitGroup
	errs := make(chan error, 48)
	for i := range 48 {
		wg.Go(func() {
			h := gx.NewTCPClientHandler(addr)
			h.SlaveID = 1
			h.Timeout = 5 * time.Second
			if err := h.Connect(context.Background()); err != nil {
				errs <- err
				return
			}
			defer h.Close()
			c := gx.NewClient(h)
			for j := range 50 {
				var err error
				switch j % 3 {
				case 0:
					_, err = c.ReadInputRegisters(context.Background(), 0, 20)
				case 1:
					_, err = c.ReadHoldingRegisters(context.Background(), 0, 9)
				case 2:
					_, err = c.WriteSingleRegister(context.Background(), uint16(1+i%4), uint16(j))
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()
	if err := run(ctx, []string{"-h"}, io.Discard, nil); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h: %v", err)
	}
	if err := run(ctx, []string{"-unit-id", "999"}, io.Discard, nil); err == nil {
		t.Fatal("invalid flag: want error")
	}
	if err := run(ctx, []string{"-register-map", "does-not-exist.yaml"}, io.Discard, nil); err == nil || !strings.Contains(err.Error(), "register map") {
		t.Fatalf("missing map: %v", err)
	}
	bad := t.TempDir() + "/bad.yaml"
	if err := os.WriteFile(bad, []byte("version: 1\ncoils: [{address: 0, name: a, command: fly}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"-register-map", bad}, io.Discard, nil); err == nil || !strings.Contains(err.Error(), `unknown command "fly"`) {
		t.Fatalf("invalid map: %v", err)
	}
	addr, _, _ := startSim(t)
	if err := run(ctx, []string{"-addr", addr}, io.Discard, nil); err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("port in use: %v", err)
	}
}

// The real binary exits with status 0 on SIGINT and SIGTERM.
func TestSignalShutdown(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-addr", "127.0.0.1:0")
			cmd.Env = append(os.Environ(), "SIMULATOR_E2E_CHILD=1")
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			addrRe := regexp.MustCompile(`addr=(\S+)`)
			sc := bufio.NewScanner(stderr)
			var addr string
			var all strings.Builder
			for sc.Scan() {
				all.WriteString(sc.Text() + "\n")
				if m := addrRe.FindStringSubmatch(sc.Text()); m != nil && strings.Contains(sc.Text(), "listening") {
					addr = m[1]
					break
				}
			}
			if addr == "" {
				_ = cmd.Process.Kill()
				t.Fatalf("no listening line:\n%s", all.String())
			}
			// Keep a client connected across the signal.
			if _, err := client(t, addr, 1).ReadInputRegisters(context.Background(), 0, 1); err != nil {
				t.Fatal(err)
			}
			rest := make(chan string, 1)
			go func() {
				var b strings.Builder
				for sc.Scan() {
					b.WriteString(sc.Text() + "\n")
				}
				rest <- b.String()
			}()
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			waitErr := make(chan error, 1)
			go func() { waitErr <- cmd.Wait() }()
			select {
			case err := <-waitErr:
				out := all.String() + <-rest
				if err != nil {
					t.Fatalf("exit: %v\n%s", err, out)
				}
				if !strings.Contains(out, "shutdown complete") {
					t.Fatalf("missing shutdown log:\n%s", out)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("process did not exit after signal")
			}
		})
	}
}

// Fault injection over Modbus: HR8 selects a cause, the fault is detected
// automatically, reset is refused until the cause is repaired.
func TestEndToEndFaultScenario(t *testing.T) {
	addr, logs, _ := startSim(t, "-time-scale", "1000")
	c := client(t, addr, 1)
	ctx := context.Background()
	if _, err := c.WriteSingleCoil(ctx, 0, 0xFF00); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, 2, 20*time.Second)

	// Inject: zone 2 thermocouple open.
	if _, err := c.WriteSingleRegister(ctx, 8, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteSingleRegister(ctx, 8, 9); exceptionCode(err) != 0x03 {
		t.Fatalf("scenario 9: want exception 03, got %v", err)
	}
	waitState(t, c, 3, 5*time.Second)
	b, err := c.ReadInputRegisters(ctx, 0, 29)
	if err != nil {
		t.Fatal(err)
	}
	reg := func(i int) uint16 { return binary.BigEndian.Uint16(b[2*i:]) }
	if reg(15) != 60 || reg(28) != 60 || reg(27) != 8 || reg(5) != 9999 || reg(26)&(1<<7) == 0 {
		t.Fatalf("fault 60 / last 60 / scenario 8 / zone2 9999 / alarm bit 7: %d %d %d %d %b", reg(15), reg(28), reg(27), reg(5), reg(26))
	}
	if di, err := c.ReadDiscreteInputs(ctx, 0, 16); err != nil || di[0]&0b100 == 0 || di[1]&0b10000 == 0 {
		t.Fatalf("DI fault_active / alarm_sensor_failure: %08b %v", di, err)
	}

	// Reset refused while the thermocouple is still open; coil acknowledged anyway.
	if _, err := c.WriteSingleCoil(ctx, 2, 0xFF00); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	waitState(t, c, 3, time.Second)
	if bits := coilBits(t, c); bits != 0 {
		t.Fatalf("reset coil not acknowledged: %04b", bits)
	}

	// Repair, reset → STOPPED; last fault code is kept.
	if _, err := c.WriteSingleRegister(ctx, 8, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := c.WriteSingleCoil(ctx, 2, 0xFF00); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, 0, 5*time.Second)
	if b, err := c.ReadInputRegisters(ctx, 15, 1); err != nil || binary.BigEndian.Uint16(b) != 0 {
		t.Fatalf("active fault after reset: % X %v", b, err)
	}
	if b, err := c.ReadInputRegisters(ctx, 28, 1); err != nil || binary.BigEndian.Uint16(b) != 60 {
		t.Fatalf("last fault after reset: % X %v", b, err)
	}
	for _, want := range []string{"fault scenario selected", "state changed", "condition still present"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q", want)
		}
	}
}

func readReg(t *testing.T, c gx.Client, addr uint16) uint16 {
	t.Helper()
	b, err := c.ReadInputRegisters(context.Background(), addr, 1)
	if err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint16(b)
}

// Three lines behind one endpoint, selected by unit id; a fault on one line
// leaves the others running.
func TestEndToEndThreeLinesByUnitID(t *testing.T) {
	addr, logs, _ := startSim(t, "-lines", "3", "-time-scale", "1000")
	l1, l2, l3 := client(t, addr, 1), client(t, addr, 2), client(t, addr, 3)
	ctx := context.Background()

	// Each line starts with its own preset setpoints (rpm ×10).
	for i, c := range []gx.Client{l1, l2, l3} {
		b, err := c.ReadHoldingRegisters(ctx, 0, 1)
		if want := []uint16{1000, 800, 300}[i]; err != nil || binary.BigEndian.Uint16(b) != want {
			t.Fatalf("line %d HR0: % X %v, want %d", i+1, b, err, want)
		}
	}
	if _, err := client(t, addr, 4).ReadInputRegisters(ctx, 0, 1); exceptionCode(err) != 0x0B {
		t.Fatalf("unit 4: want exception 0B, got %v", err)
	}

	// Start lines 1 and 3 only.
	for _, c := range []gx.Client{l1, l3} {
		if _, err := c.WriteSingleCoil(ctx, 0, 0xFF00); err != nil {
			t.Fatal(err)
		}
	}
	waitState(t, l1, 2, 20*time.Second)
	waitState(t, l3, 2, 20*time.Second)
	if s := readReg(t, l2, 0); s != 0 {
		t.Fatalf("line 2 state %d, want STOPPED", s)
	}
	deadline := time.Now().Add(10 * time.Second)
	for readReg(t, l1, 11) < 4400 || readReg(t, l3, 11) < 2900 {
		if time.Now().After(deadline) {
			t.Fatalf("throughput: line 1 %d, line 3 %d", readReg(t, l1, 11), readReg(t, l3, 11))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tp3 := readReg(t, l3, 11); tp3 > 3100 {
		t.Fatalf("line 3 (PVC) throughput raw %d, want ~3000", tp3)
	}

	// E-Stop line 1: line 3 keeps running and producing.
	len3 := readReg(t, l3, 17)
	if _, err := l1.WriteSingleCoil(ctx, 3, 0xFF00); err != nil {
		t.Fatal(err)
	}
	waitState(t, l1, 3, 5*time.Second)
	time.Sleep(50 * time.Millisecond)
	if s, code := readReg(t, l3, 0), readReg(t, l3, 15); s != 2 || code != 0 {
		t.Fatalf("line 3 affected by line 1 E-Stop: state %d fault %d", s, code)
	}
	if readReg(t, l3, 17) == len3 {
		t.Fatal("line 3 stopped producing")
	}
	if s := readReg(t, l2, 0); s != 0 {
		t.Fatalf("line 2 state %d", s)
	}
	for _, want := range []string{"line ready", "line=1", "line=3", "preset=pvc-110", "lines=3"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q", want)
		}
	}
}

// One port per line: each line is its own endpoint with unit id 1.
func TestEndToEndLinesByPort(t *testing.T) {
	startSim(t, "-lines", "2", "-isolation", "port")
	addrs := lastAddrs
	if len(addrs) != 2 || addrs[0] == addrs[1] {
		t.Fatalf("addresses %v", addrs)
	}
	for i, addr := range addrs {
		c := client(t, addr, 1)
		b, err := c.ReadHoldingRegisters(context.Background(), 0, 1)
		if want := []uint16{1000, 800}[i]; err != nil || binary.BigEndian.Uint16(b) != want {
			t.Fatalf("line %d on %s: % X %v", i+1, addr, b, err)
		}
		if _, err := client(t, addr, 2).ReadInputRegisters(context.Background(), 0, 1); exceptionCode(err) != 0x0B {
			t.Fatalf("unit 2 on %s: %v", addr, err)
		}
	}
}

// Port isolation + accept-any-unit: every endpoint answers any unit id.
func TestEndToEndPortsAcceptAnyUnit(t *testing.T) {
	startSim(t, "-lines", "2", "-isolation", "port", "-accept-any-unit")
	for _, addr := range lastAddrs {
		for _, u := range []byte{0, 1, 255} {
			if _, err := client(t, addr, u).ReadInputRegisters(context.Background(), 0, 1); err != nil {
				t.Fatalf("%s unit %d: %v", addr, u, err)
			}
		}
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := t.TempDir() + "/" + name
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A configuration file with a scripted line: scenario-driven setpoint, cause
// and E-Stop changes are visible over Modbus; the other line is untouched.
func TestEndToEndConfigWithScenario(t *testing.T) {
	scen := writeTemp(t, "s.yaml", `scenario:
  name: e2e
  events:
    - {at_seconds: 0, action: start_machine}
    - {at_seconds: 2, action: set_extruder_rpm, value: 90}
    - {at_seconds: 3, action: inject_cause, cause: vacuum_leak}
    - {at_seconds: 4, action: emergency_stop}
`)
	cfg := writeTemp(t, "sim.yaml", `version: 1
simulation: {time_scale: 100}
lines:
  - {name: scripted, preset: hdpe-110, scenario: `+scen+`}
  - {name: idle, preset: hdpe-63}
`)
	addr, logs, _ := startSim(t, "-config", cfg)
	c1, c2 := client(t, addr, 1), client(t, addr, 2)
	ctx := context.Background()
	waitState(t, c1, 3, 5*time.Second)
	time.Sleep(20 * time.Millisecond)
	hr, err := c1.ReadHoldingRegisters(ctx, 0, 9)
	if err != nil {
		t.Fatal(err)
	}
	if rpm, sc := binary.BigEndian.Uint16(hr[0:]), binary.BigEndian.Uint16(hr[16:]); rpm != 900 || sc != 6 {
		t.Fatalf("HR0 %d (want 900 from the scenario), HR8 %d (want 6)", rpm, sc)
	}
	if bits := coilBits(t, c1); bits&0b1000 == 0 {
		t.Fatalf("E-Stop coil not reflected: %04b", bits)
	}
	if readReg(t, c1, 15) != 1 || readReg(t, c2, 0) != 0 {
		t.Fatal("line 1 fault 1 and line 2 STOPPED expected")
	}
	for _, want := range []string{"scenario=e2e", "scenario event", "line_name=scripted", "scenario finished"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q", want)
		}
	}
}

func TestValidateMode(t *testing.T) {
	var out syncBuffer
	start := time.Now()
	if err := run(context.Background(), []string{"-config", "../../configs/simulator.example.yaml", "-validate"}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "configuration valid") || strings.Contains(out.String(), "listening") {
		t.Fatalf("validate output:\n%s", out.String())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("validate must not run the simulation")
	}
	bad := writeTemp(t, "bad.yaml", "version: 1\nlines: [{name: x, preset: abs-50}]\n")
	err := run(context.Background(), []string{"-config", bad, "-validate"}, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid configuration") || !strings.Contains(err.Error(), `"abs-50"`) {
		t.Fatalf("invalid config: %v", err)
	}
	if err := run(context.Background(), []string{"-scenario", "no-such", "-validate"}, io.Discard, nil); err == nil {
		t.Fatal("unknown scenario accepted")
	}
}

// Built-in scenario by name from the command line.
func TestEndToEndScenarioFlag(t *testing.T) {
	addr, _, _ := startSim(t, "-scenario", "pressure-test", "-time-scale", "1000")
	c := client(t, addr, 1)
	waitState(t, c, 3, 5*time.Second)
	if code := readReg(t, c, 15); code != 11 {
		t.Fatalf("fault %d, want 11 (inject_fault melt_pressure_high)", code)
	}
}
