package mapping

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"modbus-simulator/configs"
	"modbus-simulator/internal/modbus"
	"modbus-simulator/internal/plc"
	"modbus-simulator/internal/simulation"
)

// fakeSink records submitted commands; it can simulate a full queue.
type fakeSink struct {
	mu   sync.Mutex
	cmds []simulation.Command
	full bool
}

func (f *fakeSink) Submit(c simulation.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return simulation.ErrQueueFull
	}
	f.cmds = append(f.cmds, c)
	return nil
}

func (f *fakeSink) take() []simulation.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cmds
	f.cmds = nil
	return c
}

func newDevice(t *testing.T) (*Device, *fakeSink, *plc.Memory) {
	t.Helper()
	m, err := Parse(configs.DefaultRegisterMap)
	if err != nil {
		t.Fatal(err)
	}
	mem, err := plc.New(plc.Config{UnitID: 1, Layout: m.Layout()})
	if err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	e, err := simulation.New(simulation.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDevice(m, mem, sink, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Init(e.Snapshot()); err != nil {
		t.Fatal(err)
	}
	return d, sink, mem
}

func code(err error) modbus.ExceptionCode {
	var c modbus.ExceptionCode
	if errors.As(err, &c) {
		return c
	}
	return 0
}

func TestNewDeviceLayoutMismatch(t *testing.T) {
	m, _ := Parse(configs.DefaultRegisterMap)
	mem, _ := plc.New(plc.Config{UnitID: 1, Layout: plc.Layout{Coils: 4}})
	if _, err := NewDevice(m, mem, &fakeSink{}, nil, nil); err == nil {
		t.Fatal("want layout mismatch error")
	}
}

func TestInitHoldingFromSetpoints(t *testing.T) {
	d, _, _ := newDevice(t)
	hr, err := d.ReadHoldingRegisters(1, 0, 9)
	// Defaults: 100 rpm, 180/190/200 °C, die 205 °C, 2.5 m/min, no target, scenario 0.
	if err != nil || !slices.Equal(hr, []uint16{1000, 1800, 1900, 2000, 2050, 250, 0, 0, 0}) {
		t.Fatalf("%v %v", hr, err)
	}
	ir, _ := d.ReadInputRegisters(1, 4, 1)
	if ir[0] < 190 || ir[0] > 210 { // 20 °C ambient ±1 °C noise
		t.Fatalf("zone 1 raw %d", ir[0])
	}
}

func TestPublishEncodesSnapshot(t *testing.T) {
	d, _, _ := newDevice(t)
	s := simulation.Snapshot{
		State: simulation.Running, FaultCode: 7, TargetReached: true, SimTime: 90 * time.Second,
		Measured: simulation.ProcessValues{
			ExtruderRPM: 99.96, MotorCurrent: 160.24, MeltPressure: 209.35, ZoneTemps: [3]float64{180, 190, 200},
			DieTemp: 205.04, VacuumMagnitude: 400.4, CoolingWaterTemp: 16.43, HaulOffSpeed: 2.504,
			Throughput: 450.1, PowerConsumption: 131.66, PipeOD: 109.987, WallThickness: 9.996,
			MotorLoad: 69.55, MeltTemp: 212.5,
		},
		TotalLength: 12345.67, TotalWeight: 98765.4,
	}
	d.Publish(s, nil)
	ir, err := d.ReadInputRegisters(1, 0, 26)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{2, 1000, 1602, 2094, 1800, 1900, 2000, 2050, 400, 164, 250, 4501, 1317, 10999, 1000, 7,
		0x0001, 0xE241, 0x000F, 0x1206, 696, 2125}
	if !slices.Equal(ir[:22], want) {
		t.Fatalf("IR\n got %v\nwant %v", ir[:22], want)
	}
	if f := math.Float32frombits(uint32(ir[22])<<16 | uint32(ir[23])); math.Abs(float64(f)-209.35) > 1e-4 {
		t.Fatalf("float32 %v", f)
	}
	if ir[24] != 0 || ir[25] != 90 {
		t.Fatalf("sim_time %v", ir[24:])
	}
	di, _ := d.ReadDiscreteInputs(1, 0, 16)
	if !slices.Equal(di[:5], []bool{true, false, false, false, true}) {
		t.Fatalf("DI %v", di)
	}
}

func TestPublishSaturates(t *testing.T) {
	d, _, _ := newDevice(t)
	d.Publish(simulation.Snapshot{Measured: simulation.ProcessValues{MeltPressure: 9000}}, nil)
	ir, _ := d.ReadInputRegisters(1, 3, 1)
	if ir[0] != 65535 || !d.clamped["melt_pressure"] {
		t.Fatalf("raw %d clamped %v", ir[0], d.clamped)
	}
	d.Publish(simulation.Snapshot{}, nil)
	if d.clamped["melt_pressure"] {
		t.Fatal("clamp flag not cleared")
	}
}

func TestHoldingWrites(t *testing.T) {
	d, sink, _ := newDevice(t)
	tests := []struct {
		name     string
		unit     uint8
		addr     uint16
		vals     []uint16
		wantCode modbus.ExceptionCode
		wantCmds []simulation.Command
	}{
		{"rpm", 1, 0, []uint16{455}, 0, []simulation.Command{simulation.SetSetpoint(simulation.SPExtruderRPM, 45.5)}},
		{"rpm max", 1, 0, []uint16{1500}, 0, []simulation.Command{simulation.SetSetpoint(simulation.SPExtruderRPM, 150)}},
		{"rpm above max", 1, 0, []uint16{1501}, modbus.IllegalDataValue, nil},
		{"zones block", 1, 1, []uint16{1700, 1750, 1800, 1850}, 0, []simulation.Command{
			simulation.SetSetpoint(simulation.SPZone1Temp, 170), simulation.SetSetpoint(simulation.SPZone2Temp, 175),
			simulation.SetSetpoint(simulation.SPZone3Temp, 180), simulation.SetSetpoint(simulation.SPDieTemp, 185)}},
		{"block with one bad value is all-or-nothing", 1, 3, []uint16{1900, 9999}, modbus.IllegalDataValue, nil},
		{"uint32 target", 1, 6, []uint16{0x0001, 0x86A0}, 0, []simulation.Command{simulation.SetSetpoint(simulation.SPProductionTarget, 10000)}},
		{"uint32 above max", 1, 6, []uint16{0x05F5, 0xE101}, modbus.IllegalDataValue, nil},
		{"partial uint32 low word", 1, 7, []uint16{1}, modbus.IllegalDataValue, nil},
		{"partial uint32 high word", 1, 6, []uint16{0}, modbus.IllegalDataValue, nil},
		{"span ending mid uint32", 1, 5, []uint16{250, 0}, modbus.IllegalDataValue, nil},
		{"scenario", 1, 8, []uint16{3}, 0, []simulation.Command{simulation.SetScenario(simulation.ScenarioHeaterStuckOn)}},
		{"scenario repaired", 1, 8, []uint16{0}, 0, []simulation.Command{simulation.SetScenario(simulation.ScenarioNone)}},
		{"scenario out of range", 1, 8, []uint16{9}, modbus.IllegalDataValue, nil},
		{"unmapped", 1, 9, []uint16{1}, modbus.IllegalDataAddress, nil},
		{"wrong unit", 2, 0, []uint16{1}, modbus.GatewayTargetFailed, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := d.ReadHoldingRegisters(1, 0, 9)
			err := d.WriteHoldingRegisters(tt.unit, tt.addr, tt.vals)
			if code(err) != tt.wantCode || (tt.wantCode == 0) != (err == nil) {
				t.Fatalf("err = %v, want code %d", err, tt.wantCode)
			}
			if got := sink.take(); !slices.Equal(got, tt.wantCmds) {
				t.Fatalf("commands %v, want %v", got, tt.wantCmds)
			}
			after, _ := d.ReadHoldingRegisters(1, 0, 9)
			if tt.wantCode != 0 && !slices.Equal(before, after) {
				t.Fatalf("rejected write changed memory: %v → %v", before, after)
			}
			if tt.wantCode == 0 && !slices.Equal(after[tt.addr:int(tt.addr)+len(tt.vals)], tt.vals) {
				t.Fatalf("not stored: %v", after)
			}
		})
	}
}

func TestHoldingWriteQueueFull(t *testing.T) {
	d, sink, _ := newDevice(t)
	sink.full = true
	if err := d.WriteHoldingRegisters(1, 0, []uint16{500}); code(err) != modbus.ServerDeviceBusy {
		t.Fatalf("err = %v", err)
	}
	if hr, _ := d.ReadHoldingRegisters(1, 0, 1); hr[0] != 1000 {
		t.Fatalf("memory changed: %v", hr)
	}
}

// Momentary coils: 1 queues the command once, the coil reads 1 until the
// engine applied the command, then 0 (ADR-0003).
func TestMomentaryCoilHandshake(t *testing.T) {
	d, sink, _ := newDevice(t)
	coils := func() []bool { c, _ := d.ReadCoils(1, 0, 4); return c }

	if err := d.WriteCoils(1, 0, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteCoils(1, 0, []bool{true}); err != nil { // repeat while pending
		t.Fatal(err)
	}
	if err := d.WriteCoils(1, 0, []bool{false}); err != nil { // 0 does not cancel
		t.Fatal(err)
	}
	if got := sink.take(); !slices.Equal(got, []simulation.Command{simulation.Start()}) {
		t.Fatalf("commands %v", got)
	}
	if !slices.Equal(coils(), []bool{true, false, false, false}) {
		t.Fatalf("coils %v", coils())
	}
	d.Publish(simulation.Snapshot{}, nil) // tick without the command: still pending
	if !coils()[0] {
		t.Fatal("cleared before the engine applied the command")
	}
	d.Publish(simulation.Snapshot{}, []simulation.Command{simulation.Start()})
	if coils()[0] {
		t.Fatal("not cleared after the engine applied the command")
	}
	// Can be triggered again.
	_ = d.WriteCoils(1, 0, []bool{true})
	if got := sink.take(); len(got) != 1 {
		t.Fatalf("second trigger: %v", got)
	}
}

func TestCoilWrites(t *testing.T) {
	d, sink, _ := newDevice(t)
	// FC15 across all four: start, stop, reset, e-stop.
	if err := d.WriteCoils(1, 0, []bool{true, true, true, true}); err != nil {
		t.Fatal(err)
	}
	want := []simulation.Command{simulation.Start(), simulation.Stop(), simulation.ResetFault(), simulation.EmergencyStop(true)}
	if got := sink.take(); !slices.Equal(got, want) {
		t.Fatalf("commands %v", got)
	}
	// E-Stop is maintained: survives the acknowledgement of the others.
	d.Publish(simulation.Snapshot{}, want)
	if c, _ := d.ReadCoils(1, 0, 4); !slices.Equal(c, []bool{false, false, false, true}) {
		t.Fatalf("coils %v", c)
	}
	_ = d.WriteCoils(1, 3, []bool{false})
	if got := sink.take(); !slices.Equal(got, []simulation.Command{simulation.EmergencyStop(false)}) {
		t.Fatalf("release %v", got)
	}
	if err := d.WriteCoils(1, 3, []bool{true, true}); code(err) != modbus.IllegalDataAddress {
		t.Fatalf("unmapped coil: %v", err)
	}
	if len(sink.take()) != 0 {
		t.Fatal("rejected request queued commands")
	}
	if err := d.WriteCoils(9, 0, []bool{true}); code(err) != modbus.GatewayTargetFailed {
		t.Fatalf("unit: %v", err)
	}
	sink.full = true
	if err := d.WriteCoils(1, 1, []bool{true}); code(err) != modbus.ServerDeviceBusy {
		t.Fatalf("queue full: %v", err)
	}
	if c, _ := d.ReadCoils(1, 1, 1); c[0] {
		t.Fatal("coil set although the command was not queued")
	}
}

// Readers never see input registers from two different ticks: every field is
// derived from the same tick number n.
func TestPublishIsAtomic(t *testing.T) {
	d, _, _ := newDevice(t)
	snap := func(n int) simulation.Snapshot {
		v := float64(n % 600)
		return simulation.Snapshot{
			Measured:    simulation.ProcessValues{ZoneTemps: [3]float64{v, v, v}, DieTemp: v, ExtruderRPM: v / 10},
			TotalLength: v * 1000, TotalWeight: v * 1000,
		}
	}
	d.Publish(snap(0), nil) // replace the noisy Init image before readers start
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Go(func() {
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
				d.Publish(snap(n), nil)
			}
		}
	})
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 3000 {
				r, err := d.ReadInputRegisters(1, 0, 26)
				if err != nil {
					t.Error(err)
					return
				}
				z := r[4]
				length := uint32(r[16])<<16 | uint32(r[17])
				weight := uint32(r[18])<<16 | uint32(r[19])
				if r[5] != z || r[6] != z || r[7] != z || r[1] != z/10 || length != uint32(z)*1000 || weight != length {
					t.Errorf("torn snapshot: %v", r[:20])
					return
				}
			}
		})
	}
	readers.Wait()
	close(stop)
	writer.Wait()
}

// Writes, reads and publishing run concurrently without races (-race).
func TestConcurrentWritesAndPublish(t *testing.T) {
	d, sink, _ := newDevice(t)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 500 {
				_ = d.WriteHoldingRegisters(1, uint16(i%6), []uint16{uint16(j % 1500)})
				_ = d.WriteCoils(1, uint16(i%4), []bool{j%2 == 0})
				_, _ = d.ReadCoils(1, 0, 4)
			}
		})
	}
	wg.Go(func() {
		for range 2000 {
			d.Publish(simulation.Snapshot{}, sink.take())
		}
	})
	wg.Wait()
}

func TestReservedAndScaledScenario(t *testing.T) {
	m, err := Parse([]byte(`version: 1
holding_registers:
  - {address: 0, name: spare, reserved: true}
  - {address: 1, name: sc, scenario: true, scale: 10}
`))
	if err != nil {
		t.Fatal(err)
	}
	mem, _ := plc.New(plc.Config{UnitID: 1, Layout: m.Layout()})
	sink := &fakeSink{}
	d, _ := NewDevice(m, mem, sink, nil, nil)
	if err := d.WriteHoldingRegisters(1, 0, []uint16{1234}); err != nil || len(sink.take()) != 0 {
		t.Fatalf("reserved: %v", err)
	}
	if hr, _ := d.ReadHoldingRegisters(1, 0, 1); hr[0] != 1234 {
		t.Fatal("reserved value not stored")
	}
	if err := d.WriteHoldingRegisters(1, 1, []uint16{25}); code(err) != modbus.IllegalDataValue { // 2.5
		t.Fatalf("fractional scenario: %v", err)
	}
	if err := d.WriteHoldingRegisters(1, 1, []uint16{50}); err != nil {
		t.Fatal(err)
	}
	if got := sink.take(); !slices.Equal(got, []simulation.Command{simulation.SetScenario(5)}) {
		t.Fatalf("%v", got)
	}
}

func TestPublishFaultRegisters(t *testing.T) {
	d, _, _ := newDevice(t)
	s := simulation.Snapshot{
		State: simulation.Fault, FaultCode: simulation.FaultCoolingWaterHigh, LastFaultCode: simulation.FaultCoolingWaterHigh,
		Scenario: simulation.ScenarioCoolingFailure,
		Alarms:   1<<simulation.AlarmCoolingWaterHigh | 1<<simulation.AlarmEmergencyStop,
	}
	d.Publish(s, nil)
	ir, _ := d.ReadInputRegisters(1, 26, 3)
	if !slices.Equal(ir, []uint16{0b1_0001_0000, 5, 40}) {
		t.Fatalf("IR26..28 %v", ir)
	}
	di, _ := d.ReadDiscreteInputs(1, 5, 9)
	want := []bool{false, false, false, false, true, false, false, false, true}
	if !slices.Equal(di, want) {
		t.Fatalf("DI5..13 %v", di)
	}
}

// limiter narrows setpoint ranges like a line with its own limits.
type limiter map[simulation.SetpointID][2]float64

func (l limiter) SetpointLimits(id simulation.SetpointID) (float64, float64, bool) {
	r, ok := l[id]
	return r[0], r[1], ok
}

func TestLineLimitsNarrowWrites(t *testing.T) {
	m, _ := Parse(configs.DefaultRegisterMap)
	mem, _ := plc.New(plc.Config{UnitID: 1, Layout: m.Layout()})
	sink := &fakeSink{}
	d, err := NewDevice(m, mem, sink, limiter{simulation.SPZone1Temp: {100, 210}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.WriteHoldingRegisters(1, 1, []uint16{2100}); err != nil {
		t.Fatalf("210 °C within line limit: %v", err)
	}
	if err := d.WriteHoldingRegisters(1, 1, []uint16{2200}); code(err) != modbus.IllegalDataValue {
		t.Fatalf("220 °C above line limit: %v", err)
	}
	if err := d.WriteHoldingRegisters(1, 1, []uint16{900}); code(err) != modbus.IllegalDataValue {
		t.Fatalf("90 °C below line limit: %v", err)
	}
	if _, err := NewDevice(m, mem, sink, limiter{simulation.SPZone1Temp: {300, 400}}, nil); err == nil {
		t.Fatal("non-overlapping limits accepted")
	}
}

// Setpoint, scenario and E-Stop changes made by other sources (a scenario)
// are reflected in holding registers and the E-Stop coil; a pending client
// write is not overwritten.
func TestRegisterSync(t *testing.T) {
	d, _, _ := newDevice(t)
	e, _ := simulation.New(simulation.DefaultConfig())
	s0 := e.Snapshot()
	d.Publish(s0, nil)

	// A client writes HR0 = 50 rpm; the engine has not applied it yet.
	if err := d.WriteHoldingRegisters(1, 0, []uint16{500}); err != nil {
		t.Fatal(err)
	}
	// Meanwhile a scenario changes zone 1, the cause and presses E-Stop.
	s1, _ := e.Step([]simulation.Command{simulation.SetSetpoint(simulation.SPZone1Temp, 170),
		simulation.SetScenario(simulation.ScenarioVacuumLeak), simulation.EmergencyStop(true)})
	d.Publish(s1, nil)
	hr, _ := d.ReadHoldingRegisters(1, 0, 9)
	if hr[0] != 500 || hr[1] != 1700 || hr[8] != 6 {
		t.Fatalf("HR %v: client value kept, scenario values synced", hr)
	}
	if c, _ := d.ReadCoils(1, 3, 1); !c[0] {
		t.Fatal("E-Stop coil not synced")
	}
	s2, _ := e.Step([]simulation.Command{simulation.EmergencyStop(false), simulation.SetSetpoint(simulation.SPExtruderRPM, 50)})
	d.Publish(s2, nil)
	hr, _ = d.ReadHoldingRegisters(1, 0, 1)
	if c, _ := d.ReadCoils(1, 3, 1); c[0] || hr[0] != 500 {
		t.Fatalf("after release: coil %v HR0 %d", c, hr[0])
	}
}
