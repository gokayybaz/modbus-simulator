package simulation

import (
	"math"
	"strings"
	"testing"
	"time"
)

func newEngine(t *testing.T, mutate ...func(*Config)) *Engine {
	t.Helper()
	cfg := DefaultConfig()
	for _, m := range mutate {
		m(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// stepN advances n ticks without commands and returns the last snapshot.
func stepN(e *Engine, n int) Snapshot {
	s := e.Snapshot()
	for range n {
		s, _ = e.Step(nil)
	}
	return s
}

// stepUntil advances until cond holds or max ticks pass.
func stepUntil(t *testing.T, e *Engine, max int, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	for range max {
		if s, _ := e.Step(nil); cond(s) {
			return s
		}
	}
	t.Fatalf("condition not reached in %d ticks; last %+v", max, e.Snapshot())
	return Snapshot{}
}

// engineIn returns an engine driven into the requested state.
func engineIn(t *testing.T, s MachineState) *Engine {
	t.Helper()
	e := newEngine(t)
	switch s {
	case Heating:
		e.Step([]Command{Start()})
	case Running:
		e.Step([]Command{Start()})
		stepUntil(t, e, 5000, func(s Snapshot) bool { return s.State == Running })
		stepN(e, 40) // let drives ramp up
	case Fault:
		e.Step([]Command{TriggerFault(42)})
	}
	if got := e.Snapshot().State; got != s {
		t.Fatalf("setup: state %v, want %v", got, s)
	}
	return e
}

func TestConfigValidation(t *testing.T) {
	tests := map[string]func(*Config){
		"zero period":        func(c *Config) { c.Period = 0 },
		"zero band":          func(c *Config) { c.HeatingBand = 0 },
		"negative hold":      func(c *Config) { c.HeatingHold = -time.Second },
		"rpm setpoint high":  func(c *Config) { c.Setpoints.ExtruderRPM = 151 },
		"zone setpoint NaN":  func(c *Config) { c.Setpoints.ZoneTemps[1] = math.NaN() },
		"zone heat capacity": func(c *Config) { c.Process.Zones[1].HeatCapacity = 0 },
		"die heater":         func(c *Config) { c.Process.Die.HeaterMax = 0 },
		"extruder ramp":      func(c *Config) { c.Process.Extruder.RampRate = 0 },
		"power factor":       func(c *Config) { c.Process.Extruder.PowerFactor = 1.5 },
		"melt flow exponent": func(c *Config) { c.Process.Melt.FlowExponent = 2 },
		"vacuum target":      func(c *Config) { c.Process.Vacuum.Target = 0 },
		"cooling flow":       func(c *Config) { c.Process.Cooling.FlowRate = 0 },
		"pipe density":       func(c *Config) { c.Process.Pipe.Density = 0 },
		"haul-off nominal":   func(c *Config) { c.Process.NominalHaulOff = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if _, err := New(DefaultConfig()); err != nil {
		t.Fatalf("default config: %v", err)
	}
}

func TestInitialState(t *testing.T) {
	e := newEngine(t)
	s := e.Snapshot()
	if s.Tick != 0 || s.State != Stopped || s.FaultCode != FaultNone || e.Period() != 500*time.Millisecond {
		t.Fatalf("%+v", s)
	}
	if s.True.ZoneTemps != [3]float64{20, 20, 20} || s.True.DieTemp != 20 {
		t.Fatalf("not at ambient: %+v", s.True)
	}
}

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      MachineState
		cmds      []Command
		want      MachineState
		wantFault FaultCode
		wantErr   string // substring; "" = no error
	}{
		{"stopped start", Stopped, []Command{Start()}, Heating, 0, ""},
		{"stopped stop", Stopped, []Command{Stop()}, Stopped, 0, ""},
		{"stopped reset is harmless", Stopped, []Command{ResetFault()}, Stopped, 0, ""},
		{"start+stop: stop wins", Stopped, []Command{Start(), Stop()}, Stopped, 0, ""},
		{"stop+start: stop wins", Stopped, []Command{Stop(), Start()}, Stopped, 0, ""},
		{"heating stop", Heating, []Command{Stop()}, Stopped, 0, ""},
		{"heating start no-op", Heating, []Command{Start()}, Heating, 0, ""},
		{"running stop", Running, []Command{Stop()}, Stopped, 0, ""},
		{"running start no-op", Running, []Command{Start()}, Running, 0, ""},
		{"running fault", Running, []Command{TriggerFault(42)}, Fault, 42, ""},
		{"running estop", Running, []Command{EmergencyStop(true)}, Fault, FaultEmergencyStop, ""},
		{"heating estop", Heating, []Command{EmergencyStop(true)}, Fault, FaultEmergencyStop, ""},
		{"stopped estop", Stopped, []Command{EmergencyStop(true)}, Fault, FaultEmergencyStop, ""},
		{"estop pressed+released same tick", Running, []Command{EmergencyStop(true), EmergencyStop(false)}, Running, 0, ""},
		{"estop before fault in batch", Running, []Command{TriggerFault(7), EmergencyStop(true)}, Fault, FaultEmergencyStop, ""},
		{"fault start rejected", Fault, []Command{Start()}, Fault, 42, "reset first"},
		{"fault stop no-op", Fault, []Command{Stop()}, Fault, 42, ""},
		{"fault reset", Fault, []Command{ResetFault()}, Stopped, 0, ""},
		{"fault reset+start restarts", Fault, []Command{Start(), ResetFault()}, Heating, 0, ""},
		{"first fault code latched", Fault, []Command{TriggerFault(99)}, Fault, 42, ""},
		{"estop keeps first fault code", Fault, []Command{EmergencyStop(true)}, Fault, 42, ""},
		{"fault code 0 rejected", Running, []Command{TriggerFault(0)}, Running, 0, "reserved"},
		{"bad setpoint rejected", Stopped, []Command{SetSetpoint(SPExtruderRPM, 151), Start()}, Heating, 0, "outside"},
		{"unknown setpoint", Stopped, []Command{SetSetpoint(99, 1)}, Stopped, 0, "unknown setpoint"},
		{"unknown command", Stopped, []Command{{Kind: 200}}, Stopped, 0, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := engineIn(t, tt.from)
			s, err := e.Step(tt.cmds)
			if s.State != tt.want || s.FaultCode != tt.wantFault {
				t.Fatalf("state %v fault %d, want %v fault %d", s.State, s.FaultCode, tt.want, tt.wantFault)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// E-Stop is maintained: reset is refused until it is released (ADR-0003).
func TestEmergencyStopSequence(t *testing.T) {
	e := engineIn(t, Running)
	steps := []struct {
		cmds    []Command
		want    MachineState
		wantErr bool
	}{
		{[]Command{EmergencyStop(true)}, Fault, false},
		{[]Command{ResetFault()}, Fault, true},
		{[]Command{Start()}, Fault, true},
		{nil, Fault, false},
		{[]Command{EmergencyStop(false)}, Fault, false}, // releasing alone does not reset
		{[]Command{ResetFault()}, Stopped, false},
		{[]Command{Start()}, Heating, false},
	}
	for i, st := range steps {
		s, err := e.Step(st.cmds)
		if s.State != st.want || (err != nil) != st.wantErr {
			t.Fatalf("step %d %v: state %v err %v", i, st.cmds, s.State, err)
		}
	}
	// Release + reset in the same tick also works.
	e.Step([]Command{EmergencyStop(true)})
	if s, err := e.Step([]Command{ResetFault(), EmergencyStop(false)}); s.State != Stopped || err != nil {
		t.Fatalf("release+reset: %v %v", s.State, err)
	}
}

func TestHeatingToRunning(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	s := stepUntil(t, e, 5000, func(s Snapshot) bool { return s.State == Running })

	cfg := DefaultConfig()
	// Heat-up of the reference line takes 10–30 min (12 kW into ~42 kJ/K).
	if s.SimTime < 10*time.Minute || s.SimTime > 30*time.Minute {
		t.Fatalf("RUNNING after %v", s.SimTime)
	}
	// Automatic transitions happen at the end of a tick: no time spent yet.
	if s.StateTime != 0 {
		t.Fatalf("StateTime %v right after automatic transition", s.StateTime)
	}
	// Command transitions happen at the start of a tick: one period spent.
	if s, _ := e.Step([]Command{Stop()}); s.StateTime != cfg.Period {
		t.Fatalf("StateTime %v after commanded transition", s.StateTime)
	}
}

// HEATING→RUNNING happens exactly when all zones have been within the band
// for HeatingHold (60 ticks) without interruption.
func TestHeatingHoldIsExact(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	inBand := 0
	for range 5000 {
		s, _ := e.Step(nil)
		if e.line.zonesWithin(s.Setpoints, 5) {
			inBand++
		} else {
			inBand = 0
		}
		if s.State == Running {
			if inBand != 60 {
				t.Fatalf("RUNNING after %d in-band ticks, want 60", inBand)
			}
			return
		}
	}
	t.Fatal("never reached RUNNING")
}

func TestHeatingHoldResetsWhenLeavingBand(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	stepUntil(t, e, 5000, func(Snapshot) bool { return e.line.zonesWithin(e.sp, 5) })
	stepN(e, 30) // half of the 60-tick hold
	e.Step([]Command{SetSetpoint(SPZone1Temp, 230)})
	if s := stepN(e, 40); s.State != Heating {
		t.Fatalf("state %v: hold must restart after leaving the band", s.State)
	}
	if s := stepUntil(t, e, 5000, func(s Snapshot) bool { return s.State == Running }); s.True.ZoneTemps[0] < 225 {
		t.Fatalf("running with zone1 at %v", s.True.ZoneTemps[0])
	}
}

func TestTemperaturesChangeGradually(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	prev := e.Snapshot().True
	// Largest possible step: full heater power into the heat capacity,
	// 12 kW / 42 kJ/K × 0.5 s ≈ 0.14 °C per tick.
	const maxStep = 0.2
	for range 3000 {
		s, _ := e.Step(nil)
		for i := range 3 {
			if d := math.Abs(s.True.ZoneTemps[i] - prev.ZoneTemps[i]); d > maxStep {
				t.Fatalf("zone %d jumped %.3f °C in one tick", i+1, d)
			}
		}
		prev = s.True
	}
}

func TestCountersOnlyAdvanceWhileRunning(t *testing.T) {
	e := newEngine(t)
	// STOPPED for 20 min.
	s := stepN(e, 2400)
	if s.TotalLength != 0 || s.TotalWeight != 0 || s.Measured.ExtruderRPM != 0 || s.Measured.Throughput != 0 {
		t.Fatalf("STOPPED produced: %+v", s)
	}
	// HEATING never produces.
	e.Step([]Command{Start()})
	for s.State != Running {
		s, _ = e.Step(nil)
		if s.State == Heating && (s.TotalLength != 0 || s.TotalWeight != 0) {
			t.Fatal("HEATING produced")
		}
	}
	// RUNNING produces.
	s = stepN(e, 1200) // 10 min
	if s.TotalLength <= 0 || s.TotalWeight <= 0 {
		t.Fatalf("RUNNING produced nothing: %+v", s)
	}
	// After Stop: counters frozen even while drives ramp down.
	frozen, _ := e.Step([]Command{Stop()})
	if s := stepN(e, 2000); s.TotalLength != frozen.TotalLength || s.TotalWeight != frozen.TotalWeight {
		t.Fatal("counters moved after stop")
	}
	if s := e.Snapshot(); s.True.ExtruderRPM != 0 || s.Measured.ExtruderRPM != 0 {
		t.Fatalf("drive not stopped: %+v", s.True)
	}
}

func TestRunningProductionMatchesIntegral(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	stepUntil(t, e, 5000, func(s Snapshot) bool { return s.State == Running })
	s := stepN(e, 1200) // 600 s

	// Haul-off ramps 0→2.5 m/min at 0.5 (m/min)/s: 5 s ramp costs 2.5 s of output.
	wantLen := 2.5 / 60 * (600 - 2.5)
	if math.Abs(s.TotalLength-wantLen) > 0.05 {
		t.Fatalf("length %.3f m, want %.3f", s.TotalLength, wantLen)
	}
	// Steady throughput 100 rpm × 4.5 = 450 kg/h, minus ramp (20 s → 10 s) and lag (8 s).
	wantW := 450.0 / 3600 * (600 - 10 - 8)
	if math.Abs(s.TotalWeight-wantW)/wantW > 0.01 {
		t.Fatalf("weight %.3f kg, want ~%.3f", s.TotalWeight, wantW)
	}
	if math.Abs(s.True.Throughput-450) > 0.01 || s.True.ExtruderRPM != 100 || s.True.HaulOffSpeed != 2.5 {
		t.Fatalf("not at steady state: %+v", s.True)
	}
}

func TestSetpointChangeRampsDrive(t *testing.T) {
	e := engineIn(t, Running)
	e.Step([]Command{SetSetpoint(SPExtruderRPM, 80)})
	s := stepN(e, 1)              // second tick after the change
	if s.True.ExtruderRPM != 95 { // 100 − 2 ticks × 2.5 rpm
		t.Fatalf("rpm %v, want 95", s.True.ExtruderRPM)
	}
	s = stepN(e, 100)
	if s.True.ExtruderRPM != 80 || s.Setpoints.ExtruderRPM != 80 {
		t.Fatalf("rpm %v", s.True.ExtruderRPM)
	}
}

// Every trip stops the drives at once and freezes the counters; heaters are
// switched off only for faults whose impact says so (Phase 5 fault table).
func TestFaultIsSafeState(t *testing.T) {
	for _, tc := range []struct {
		code       FaultCode
		heatersOff bool
	}{
		{FaultMeltPressureHigh, false}, // process fault: heaters hold temperature
		{5, false},                     // unknown manual code: heaters hold
		{FaultSensorFailure, true},
		{FaultZone2TempHigh, true},
	} {
		e := engineIn(t, Running)
		before := e.Snapshot()
		s, _ := e.Step([]Command{TriggerFault(tc.code)})
		if s.True.ExtruderRPM != 0 || s.True.HaulOffSpeed != 0 || s.State != Fault {
			t.Fatalf("code %d: drives still moving in FAULT: %+v", tc.code, s.True)
		}
		later := stepN(e, 600)
		dropped := later.True.ZoneTemps[0] < before.True.ZoneTemps[0]-10
		if dropped != tc.heatersOff {
			t.Fatalf("code %d: zone 1 %.1f → %.1f, heaters off expected %v",
				tc.code, before.True.ZoneTemps[0], later.True.ZoneTemps[0], tc.heatersOff)
		}
		if later.TotalLength != s.TotalLength || later.TotalWeight != s.TotalWeight {
			t.Fatalf("code %d: counters moved in FAULT", tc.code)
		}
		if later.True.VacuumMagnitude > 1 {
			t.Fatalf("code %d: vacuum pump still running", tc.code)
		}
	}
}

// script is a fixed command sequence keyed by tick (applied before that tick).
var script = map[int][]Command{
	1:    {Start()},
	2600: {SetSetpoint(SPExtruderRPM, 120), SetSetpoint(SPHaulOffSpeed, 3.0)},
	3000: {TriggerFault(12)},
	3020: {ResetFault(), Start()},
	3600: {SetSetpoint(SPZone2Temp, 210)},
	4200: {EmergencyStop(true)},
	4210: {EmergencyStop(false), ResetFault()},
	4300: {Start(), Stop()},
	4350: {SetSetpoint(SPDieTemp, 500)}, // rejected
}

func runScript(t *testing.T, seed uint64, ticks int) []Snapshot {
	t.Helper()
	e := newEngine(t, func(c *Config) { c.Seed = seed })
	out := make([]Snapshot, 0, ticks)
	for i := 1; i <= ticks; i++ {
		s, _ := e.Step(script[i])
		out = append(out, s)
	}
	return out
}

func TestDeterministicReplay(t *testing.T) {
	const ticks = 4500
	a, b := runScript(t, 42, ticks), runScript(t, 42, ticks)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs:\n%+v\n%+v", i+1, a[i], b[i])
		}
	}
	// The script must actually exercise every state.
	seen := map[MachineState]bool{}
	for _, s := range a {
		seen[s.State] = true
	}
	if len(seen) != 4 {
		t.Fatalf("script visited only %v", seen)
	}

	// Another seed changes only the measurements, never the process itself.
	c := runScript(t, 43, ticks)
	measuredDiffer := false
	for i := range a {
		if a[i].True != c[i].True || a[i].State != c[i].State ||
			a[i].TotalLength != c[i].TotalLength || a[i].TotalWeight != c[i].TotalWeight {
			t.Fatalf("tick %d: seed changed the process state", i+1)
		}
		measuredDiffer = measuredDiffer || a[i].Measured != c[i].Measured
	}
	if !measuredDiffer {
		t.Fatal("different seeds produced identical noise")
	}
}

func TestNoNegativePhysicalValues(t *testing.T) {
	nonNegative := func(v ProcessValues) []float64 {
		return []float64{v.ExtruderRPM, v.MotorCurrent, v.MotorLoad, v.MeltPressure, v.VacuumMagnitude,
			v.HaulOffSpeed, v.Throughput, v.PowerConsumption, v.PipeOD, v.WallThickness}
	}
	for _, s := range runScript(t, 7, 4500) {
		vals := append(nonNegative(s.True), nonNegative(s.Measured)...)
		vals = append(vals, s.TotalLength, s.TotalWeight, s.True.CoolingWaterTemp, s.Internals.ViscosityRatio)
		for _, v := range vals {
			if v < 0 || math.IsNaN(v) {
				t.Fatalf("tick %d: invalid value %v in %+v", s.Tick, v, s)
			}
		}
	}
}

func TestMeasurementsTrackTrueValues(t *testing.T) {
	e := engineIn(t, Running)
	s := stepN(e, 200)
	for i := range 3 {
		if d := math.Abs(s.Measured.ZoneTemps[i] - s.True.ZoneTemps[i]); d > 3 {
			t.Fatalf("zone %d measured off by %.2f °C", i+1, d)
		}
	}
	if s.Measured == s.True {
		t.Fatal("no noise applied")
	}
}

func TestStringers(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{Running.String(), "RUNNING"},
		{MachineState(9).String(), "MachineState(9)"},
		{SetSetpoint(SPExtruderRPM, 60).String(), "SetSetpoint(extruder_rpm=60)"},
		{EmergencyStop(true).String(), "EmergencyStop(true)"},
		{TriggerFault(3).String(), "TriggerFault(3)"},
		{Start().String(), "Start"},
		{CommandKind(77).String(), "CommandKind(77)"},
		{SetpointID(77).String(), "SetpointID(77)"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q want %q", tc.got, tc.want)
		}
	}
}

func TestProductionTarget(t *testing.T) {
	e := engineIn(t, Running)
	start := e.Snapshot().TotalLength
	if _, err := e.Step([]Command{SetSetpoint(SPProductionTarget, start+5)}); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().TargetReached {
		t.Fatal("reached too early")
	}
	s := stepUntil(t, e, 1000, func(s Snapshot) bool { return s.TargetReached })
	if s.TotalLength < start+5 || s.TotalLength > start+5+0.05 || s.State != Running {
		t.Fatalf("reached at %.3f m (target %.3f), state %v", s.TotalLength, start+5, s.State)
	}
	if s, _ := e.Step([]Command{SetSetpoint(SPProductionTarget, 0)}); s.TargetReached {
		t.Fatal("target 0 must disable the flag")
	}
	if _, err := e.Step([]Command{SetSetpoint(SPProductionTarget, -1)}); err == nil {
		t.Fatal("negative target accepted")
	}
}

func TestSetpointLookup(t *testing.T) {
	id, ok := SetpointByName("zone2_temp")
	if !ok || id != SPZone2Temp {
		t.Fatalf("%v %v", id, ok)
	}
	if _, ok := SetpointByName("nope"); ok {
		t.Fatal("unknown name found")
	}
	if lo, hi, ok := SPHaulOffSpeed.Limits(); !ok || lo != 0 || hi != 30 {
		t.Fatal("limits")
	}
	if v, ok := DefaultConfig().Setpoints.Get(SPExtruderRPM); !ok || v != 100 {
		t.Fatal("get")
	}
	if _, ok := (Setpoints{}).Get(99); ok {
		t.Fatal("unknown id")
	}
}

// Cold-start interlock: at soak setpoints below MinRunTemp the line stays in
// HEATING; raising the setpoints lets it run.
func TestColdStartInterlock(t *testing.T) {
	e := newEngine(t)
	cmds := []Command{Start()}
	for _, id := range []SetpointID{SPZone1Temp, SPZone2Temp, SPZone3Temp, SPDieTemp} {
		cmds = append(cmds, SetSetpoint(id, 120))
	}
	e.Step(cmds)
	s := stepN(e, 4000) // 33 min at 120 °C
	if s.State != Heating || s.True.ZoneTemps[0] < 115 {
		t.Fatalf("state %v at %.1f °C, want HEATING (interlock)", s.State, s.True.ZoneTemps[0])
	}
	e.Step([]Command{SetSetpoint(SPZone1Temp, 180), SetSetpoint(SPZone2Temp, 190),
		SetSetpoint(SPZone3Temp, 200), SetSetpoint(SPDieTemp, 205)})
	stepUntil(t, e, 5000, func(s Snapshot) bool { return s.State == Running })
}

func TestLineSetpointLimits(t *testing.T) {
	e := newEngine(t, func(c *Config) {
		c.SetpointLimits = map[SetpointID]Range{SPZone1Temp: {Min: 100, Max: 200}}
	})
	if _, err := e.Step([]Command{SetSetpoint(SPZone1Temp, 210)}); err == nil {
		t.Fatal("210 °C accepted with a 200 °C line limit")
	}
	if _, err := e.Step([]Command{SetSetpoint(SPZone1Temp, 195)}); err != nil {
		t.Fatal(err)
	}
	if lo, hi, ok := e.SetpointLimits(SPZone1Temp); !ok || lo != 100 || hi != 200 {
		t.Fatalf("limits %v %v %v", lo, hi, ok)
	}
	if lo, hi, _ := e.SetpointLimits(SPZone2Temp); lo != 0 || hi != 260 {
		t.Fatal("other limits must stay global")
	}
	for name, lim := range map[string]map[SetpointID]Range{
		"wider":   {SPZone1Temp: {Min: 0, Max: 300}},
		"inverse": {SPZone1Temp: {Min: 150, Max: 100}},
		"unknown": {99: {Min: 0, Max: 1}},
	} {
		cfg := DefaultConfig()
		cfg.SetpointLimits = lim
		if _, err := New(cfg); err == nil {
			t.Errorf("%s limits accepted", name)
		}
	}
}

func TestNoiseAndFaultLimitValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Noise.Vacuum.Sigma = -1
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "noise vacuum") {
		t.Fatalf("negative sigma: %v", err)
	}
	for name, mut := range map[string]func(*FaultLimits){
		"high order":  func(l *FaultLimits) { l.MeltPressureHigh.Trip = 100 },
		"low order":   func(l *FaultLimits) { l.VacuumLow.Trip = 350 },
		"delay":       func(l *FaultLimits) { l.CoolingWaterHigh.Delay = -time.Second },
		"sensor span": func(l *FaultLimits) { l.SensorMin = 600 },
	} {
		cfg := DefaultConfig()
		mut(&cfg.FaultLimits)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Zero noise: measurements equal the true values.
	cfg = DefaultConfig()
	cfg.Noise = NoiseConfig{}
	e, _ := New(cfg)
	s, _ := e.Step([]Command{Start()})
	if s.Measured != s.True {
		t.Fatal("zero noise must measure the true values")
	}
}

// Scripted commands come first in the tick, client commands after them (and
// win "last value" conflicts).
type fixedSource map[uint64][]Command

func (f fixedSource) Commands(tick uint64) []Command { return f[tick] }

func TestRunnerSourceOrder(t *testing.T) {
	e := newEngine(t)
	r := NewRunner(e, RunnerOptions{Source: fixedSource{1: {SetSetpoint(SPExtruderRPM, 60)}}})
	_ = r.Submit(SetSetpoint(SPExtruderRPM, 70))
	r.step()
	if got := r.Snapshot().Setpoints.ExtruderRPM; got != 70 {
		t.Fatalf("rpm setpoint %v, want the client's 70", got)
	}
	snaps := Replay(newEngine(t), fixedSource{3: {Start()}}, 3)
	if snaps[1].State != Stopped || snaps[2].State != Heating {
		t.Fatal("Replay must apply source commands at their tick")
	}
}
