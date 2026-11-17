package simulation

import (
	"math"
	"testing"
)

// steady returns an engine at steady RUNNING state (pipe at the gauge).
func steady(t *testing.T) (*Engine, Snapshot) {
	t.Helper()
	e := engineIn(t, Running)
	return e, stepN(e, 2400) // 20 min
}

// The reference operating point matches the hand calculation in Spike 005.
func TestReferenceOperatingPoint(t *testing.T) {
	_, s := steady(t)
	v := s.True
	checks := []struct {
		name      string
		got, want float64
		tol       float64
	}{
		{"throughput kg/h", v.Throughput, 450, 0.01},
		{"wall mm", v.WallThickness, 10.0, 0.02},
		{"OD mm", v.PipeOD, 110.0, 0.05},
		{"melt temp °C", v.MeltTemp, 212.5, 0.1},
		{"melt pressure bar", v.MeltPressure, 220 * math.Exp(-0.02*2.5), 0.5},
		{"water °C", v.CoolingWaterTemp, 16.4, 0.1},
		{"vacuum mbar", v.VacuumMagnitude, 400, 0.1},
		{"motor current A", v.MotorCurrent, 160, 2},
		{"zone 1 °C", v.ZoneTemps[0], 180, 0.2},
		{"die °C", v.DieTemp, 205, 0.2},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > c.tol {
			t.Errorf("%s = %.3f, want %.3f ± %.2f", c.name, c.got, c.want, c.tol)
		}
	}
	if v.PowerConsumption < 100 || v.PowerConsumption > 160 {
		t.Errorf("power %.1f kW", v.PowerConsumption)
	}
}

// Screw speed up → throughput, pressure, current, power, water temperature
// and (after transport) wall thickness all rise; temperatures stay controlled.
func TestRPMIncreasePropagates(t *testing.T) {
	e, before := steady(t)
	e.Step([]Command{SetSetpoint(SPExtruderRPM, 120)})
	after := stepN(e, 2400)
	b, a := before.True, after.True
	rises := map[string][2]float64{
		"throughput":   {b.Throughput, a.Throughput},
		"pressure":     {b.MeltPressure, a.MeltPressure},
		"current":      {b.MotorCurrent, a.MotorCurrent},
		"power":        {b.PowerConsumption, a.PowerConsumption},
		"water temp":   {b.CoolingWaterTemp, a.CoolingWaterTemp},
		"wall":         {b.WallThickness, a.WallThickness},
		"melt temp":    {b.MeltTemp, a.MeltTemp},
		"total weight": {before.TotalWeight, after.TotalWeight},
	}
	for name, v := range rises {
		if v[1] <= v[0] {
			t.Errorf("%s did not rise: %.3f → %.3f", name, v[0], v[1])
		}
	}
	if math.Abs(a.ZoneTemps[0]-180) > 1 {
		t.Errorf("zone 1 not held: %.2f", a.ZoneTemps[0])
	}
}

// Throughput lags the screw and pressure lags throughput (response delay).
func TestPressureRespondsWithDelay(t *testing.T) {
	e, s0 := steady(t)
	s1, _ := e.Step([]Command{SetSetpoint(SPExtruderRPM, 120)})
	if s1.True.ExtruderRPM <= s0.True.ExtruderRPM {
		t.Fatal("rpm did not start ramping")
	}
	// Dead time: the speed change cannot reach the pressure in the first tick
	// (only the residual settling of the previous operating point remains).
	if d := math.Abs(s1.True.MeltPressure - s0.True.MeltPressure); d > 0.01 {
		t.Fatalf("pressure moved %.4f bar in the first tick", d)
	}
	// No inverse response: pressure never dips while the speed-up propagates.
	for range 120 {
		if s, _ := e.Step(nil); s.True.MeltPressure < s0.True.MeltPressure-0.01 {
			t.Fatalf("pressure dipped to %.3f (from %.3f) at tick %d", s.True.MeltPressure, s0.True.MeltPressure, s.Tick)
		}
	}
	s10 := e.Snapshot()
	if s10.True.MeltPressure-s0.True.MeltPressure < 1 {
		t.Fatalf("pressure did not follow after 60 s: %.3f → %.3f", s0.True.MeltPressure, s10.True.MeltPressure)
	}
}

// Haul-off speed up → thinner wall at the gauge after transport, same throughput.
func TestHaulOffChangesWallThickness(t *testing.T) {
	e, before := steady(t)
	e.Step([]Command{SetSetpoint(SPHaulOffSpeed, 3.0)})
	early := stepN(e, 40) // 20 s: pipe has moved < 1 m
	if math.Abs(early.True.WallThickness-before.True.WallThickness) > 0.01 {
		t.Fatalf("gauge changed before transport: %.3f → %.3f", before.True.WallThickness, early.True.WallThickness)
	}
	if early.Internals.ExitWall >= before.Internals.ExitWall {
		t.Fatal("wall at the calibrator did not thin immediately")
	}
	after := stepN(e, 600)
	want := 450.0 / 60 / (955 * 3.0) * 1e6 // mm², then solve for s
	s := (110 - math.Sqrt(110*110-4*want/math.Pi)) / 2
	if math.Abs(after.True.WallThickness-s) > 0.02 {
		t.Fatalf("wall %.3f, want %.3f", after.True.WallThickness, s)
	}
	if math.Abs(after.True.Throughput-before.True.Throughput) > 0.01 {
		t.Fatal("haul-off speed must not change extruder output")
	}
}

// Colder barrel → more viscous melt → higher pressure and motor current.
func TestLowerTemperatureRaisesPressure(t *testing.T) {
	e, before := steady(t)
	e.Step([]Command{SetSetpoint(SPZone3Temp, 185), SetSetpoint(SPDieTemp, 190)})
	after := stepN(e, 3600)
	if after.Internals.ViscosityRatio <= before.Internals.ViscosityRatio ||
		after.True.MeltPressure <= before.True.MeltPressure ||
		after.True.MotorCurrent <= before.True.MotorCurrent {
		t.Fatalf("η %.3f→%.3f P %.1f→%.1f I %.1f→%.1f",
			before.Internals.ViscosityRatio, after.Internals.ViscosityRatio,
			before.True.MeltPressure, after.True.MeltPressure,
			before.True.MotorCurrent, after.True.MotorCurrent)
	}
}

// Production counters advance with simulated time: rate × Δt.
func TestCountersFollowSimulatedTime(t *testing.T) {
	e, s0 := steady(t)
	s1 := stepN(e, 7200) // 1 h
	dLen := s1.TotalLength - s0.TotalLength
	dW := s1.TotalWeight - s0.TotalWeight
	if math.Abs(dLen-150) > 0.01 || math.Abs(dW-450) > 0.01 { // 2.5 m/min × 60, 450 kg/h × 1 h
		t.Fatalf("1 h: %.3f m, %.3f kg", dLen, dW)
	}
}

// Stop: vacuum releases, water returns to supply, power drops to zero once
// drives stop and heaters are off (decision: STOPPED = everything off).
func TestStopSettlesToIdle(t *testing.T) {
	e, _ := steady(t)
	e.Step([]Command{Stop()})
	s := stepN(e, 7200)
	v := s.True
	if v.VacuumMagnitude > 0.1 || math.Abs(v.CoolingWaterTemp-12) > 0.1 || v.PowerConsumption != 0 || v.MeltPressure != 0 {
		t.Fatalf("not idle: %+v", v)
	}
	if v.PipeOD == 0 || v.WallThickness == 0 {
		t.Fatal("gauge must keep showing the last pipe section")
	}
}
