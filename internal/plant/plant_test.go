package plant

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"modbus-simulator/configs"
	"modbus-simulator/internal/mapping"
	"modbus-simulator/internal/modbus"
	"modbus-simulator/internal/simulation"
)

func newLines(t *testing.T) ([]*Line, *Router) {
	t.Helper()
	r := NewRouter()
	var lines []*Line
	for i, p := range simulation.Presets() {
		m, err := mapping.Parse(configs.DefaultRegisterMap)
		if err != nil {
			t.Fatal(err)
		}
		cfg := p.Config()
		cfg.Seed = uint64(i + 1)
		l, err := NewLine(LineSpec{ID: i + 1, Preset: p.Name, UnitID: uint8(i + 1), Sim: cfg, Map: m, Speed: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Add(uint8(i+1), l.Device); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	return lines, r
}

// step advances a line's engine manually (no real time) and publishes.
func step(l *Line, n int, cmds ...simulation.Command) simulation.Snapshot {
	s, _ := l.Engine.Step(cmds)
	l.Device.Publish(s, cmds)
	for range n - 1 {
		s, _ = l.Engine.Step(nil)
		l.Device.Publish(s, nil)
	}
	return s
}

func code(err error) modbus.ExceptionCode {
	var c modbus.ExceptionCode
	errors.As(err, &c)
	return c
}

func TestRouterDispatch(t *testing.T) {
	_, r := newLines(t)
	// Holding registers start at each preset's own setpoints: rpm ×10.
	for unit, wantRPM := range map[uint8]uint16{1: 1000, 2: 800, 3: 300} {
		hr, err := r.ReadHoldingRegisters(unit, 0, 1)
		if err != nil || hr[0] != wantRPM {
			t.Fatalf("unit %d HR0 = %v %v, want %d", unit, hr, err, wantRPM)
		}
	}
	if err := r.WriteHoldingRegisters(2, 0, []uint16{700}); err != nil {
		t.Fatal(err)
	}
	for unit, want := range map[uint8]uint16{1: 1000, 2: 700, 3: 300} {
		if hr, _ := r.ReadHoldingRegisters(unit, 0, 1); hr[0] != want {
			t.Fatalf("after writing unit 2: unit %d HR0 = %d", unit, hr[0])
		}
	}
	for _, call := range []func() error{
		func() error { _, err := r.ReadInputRegisters(9, 0, 1); return err },
		func() error { _, err := r.ReadHoldingRegisters(9, 0, 1); return err },
		func() error { _, err := r.ReadCoils(9, 0, 1); return err },
		func() error { _, err := r.ReadDiscreteInputs(9, 0, 1); return err },
		func() error { return r.WriteCoils(9, 0, []bool{true}) },
		func() error { return r.WriteHoldingRegisters(9, 0, []uint16{1}) },
	} {
		if code(call()) != modbus.GatewayTargetFailed {
			t.Fatal("unknown unit must give exception 0B")
		}
	}
	if r.Units() != 3 {
		t.Fatalf("units %d", r.Units())
	}
}

func TestRouterConfiguration(t *testing.T) {
	lines, _ := newLines(t)
	r := NewRouter()
	if err := r.Add(1, lines[0].Device); err != nil {
		t.Fatal(err)
	}
	if r.Add(1, lines[1].Device) == nil {
		t.Fatal("duplicate unit accepted")
	}
	if r.SetDefault(lines[1].Device) == nil {
		t.Fatal("default on a shared endpoint accepted")
	}
	solo := NewRouter()
	if err := solo.SetDefault(lines[2].Device); err != nil {
		t.Fatal(err)
	}
	if solo.Add(5, lines[0].Device) == nil || solo.SetDefault(lines[0].Device) == nil {
		t.Fatal("second handler accepted on an accept-any endpoint")
	}
}

// Lines are independent: a fault on line 2 does not touch lines 1 and 3,
// and the same commands give each line its own process values.
func TestLinesAreIndependent(t *testing.T) {
	lines, r := newLines(t)
	for _, l := range lines {
		step(l, 1, simulation.Start())
	}
	for _, l := range lines {
		for l.Engine.Snapshot().State != simulation.Running {
			step(l, 1)
		}
		step(l, 2400) // 20 min of production
	}
	before := []simulation.Snapshot{lines[0].Engine.Snapshot(), lines[1].Engine.Snapshot(), lines[2].Engine.Snapshot()}

	// E-Stop on line 2 only, through the router.
	if err := r.WriteCoils(2, 3, []bool{true}); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		cmds := []simulation.Command(nil)
		if l.Spec.ID == 2 {
			cmds = []simulation.Command{simulation.EmergencyStop(true)}
		}
		step(l, 600, cmds...)
	}
	after := []simulation.Snapshot{lines[0].Engine.Snapshot(), lines[1].Engine.Snapshot(), lines[2].Engine.Snapshot()}
	if after[1].State != simulation.Fault || after[1].FaultCode != simulation.FaultEmergencyStop {
		t.Fatalf("line 2: %v %d", after[1].State, after[1].FaultCode)
	}
	for _, i := range []int{0, 2} {
		if after[i].State != simulation.Running || after[i].FaultCode != 0 || after[i].Alarms != 0 {
			t.Fatalf("line %d affected: %v fault %d alarms %b", i+1, after[i].State, after[i].FaultCode, after[i].Alarms)
		}
		if after[i].TotalLength <= before[i].TotalLength {
			t.Fatalf("line %d stopped producing", i+1)
		}
	}
	// Different products → different process values on the registers.
	read := func(unit uint8, addr uint16) uint16 {
		v, err := r.ReadInputRegisters(unit, addr, 1)
		if err != nil {
			t.Fatal(err)
		}
		return v[0]
	}
	if tp1, tp3 := read(1, 11), read(3, 11); math.Abs(float64(tp1)-4500) > 30 || math.Abs(float64(tp3)-3000) > 30 {
		t.Fatalf("throughput raw: line 1 %d, line 3 %d", tp1, tp3)
	}
	if w1, w3 := read(1, 14), read(3, 14); math.Abs(float64(w1)-1000) > 5 || math.Abs(float64(w3)-320) > 5 {
		t.Fatalf("wall raw: line 1 %d, line 3 %d", w1, w3)
	}
	if read(2, 0) != 3 || read(2, 15) != 1 || read(1, 0) != 2 {
		t.Fatal("state registers")
	}
}

// All lines run concurrently in real time without races (-race) and stop
// cleanly.
func TestLinesRunConcurrently(t *testing.T) {
	lines, r := newLines(t)
	for _, l := range lines {
		l.Runner = simulation.NewRunner(l.Engine, simulation.RunnerOptions{Speed: 1000,
			OnTick: func(s simulation.Snapshot, a []simulation.Command) { l.Device.Publish(s, a) }})
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, l := range lines {
		wg.Go(func() { _ = l.Run(ctx) })
	}
	var readers sync.WaitGroup
	for unit := uint8(1); unit <= 3; unit++ {
		readers.Go(func() {
			for range 500 {
				if _, err := r.ReadInputRegisters(unit, 0, 29); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	readers.Wait()
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()
	for _, l := range lines {
		if l.Runner.Stats().Ticks == 0 {
			t.Fatalf("line %d never ticked", l.Spec.ID)
		}
	}
}

func TestNewLineErrors(t *testing.T) {
	m, _ := mapping.Parse(configs.DefaultRegisterMap)
	bad := simulation.DefaultConfig()
	bad.Period = 0
	if _, err := NewLine(LineSpec{ID: 7, Sim: bad, Map: m}, nil); err == nil {
		t.Fatal("invalid simulation config accepted")
	}
	tooHigh := simulation.DefaultConfig()
	tooHigh.Setpoints.ExtruderRPM = 150
	small, _ := mapping.Parse([]byte("version: 1\nholding_registers: [{address: 0, name: a, setpoint: extruder_rpm, scale: 10, max: 120}]"))
	if _, err := NewLine(LineSpec{ID: 8, Sim: tooHigh, Map: small}, nil); err == nil {
		t.Fatal("initial setpoint outside the map range accepted")
	}
}
