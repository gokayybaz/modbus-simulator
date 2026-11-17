package models

import (
	"math"
	"testing"
	"time"
)

const dt = 500 * time.Millisecond

func TestDeadTime(t *testing.T) {
	d := NewDeadTime(2*time.Second, dt, -1) // 4 samples
	var out []float64
	for i := range 7 {
		out = append(out, d.Step(float64(i)))
	}
	want := []float64{-1, -1, -1, -1, 0, 1, 2}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out %v want %v", out, want)
		}
	}
	if NewDeadTime(0, dt, 0).Step(5) != 5 {
		t.Fatal("zero delay must pass through")
	}
}

func TestPI(t *testing.T) {
	c := PI{Kp: 2, Ti: 10, Min: 0, Max: 100}
	if u := c.Step(5, time.Second); math.Abs(u-11) > 1e-9 { // 2·5 + 2/10·5·1
		t.Fatalf("u = %v", u)
	}
	// Anti-windup: a long saturated error must not grow the integral.
	sat := PI{Kp: 2, Ti: 10, Min: 0, Max: 100}
	for range 1000 {
		if u := sat.Step(500, time.Second); u != 100 {
			t.Fatalf("not saturated: %v", u)
		}
	}
	if sat.integral > 0 {
		t.Fatalf("integral wound up to %v", sat.integral)
	}
	// Leaving saturation responds at once to a negative error.
	if u := sat.Step(-1, time.Second); u != 0 {
		t.Fatalf("after saturation u = %v", u)
	}
	c.Reset()
	if c.integral != 0 {
		t.Fatal("reset")
	}
}

func zoneParams() ThermalZoneParams {
	return ThermalZoneParams{HeatCapacity: 42e3, LossCoeff: 36, HeaterMax: 12e3, Kp: 1200, Ti: 300, ProcessHeatNominal: -4000}
}

func TestThermalZoneHeatsWithoutOvershoot(t *testing.T) {
	z := NewThermalZone(zoneParams(), 20)
	var reached time.Duration
	peak := 0.0
	for i := 1; i <= 7200; i++ { // 1 h
		z.Step(true, 180, 20, 0, dt)
		peak = max(peak, z.Temp)
		if reached == 0 && z.Temp >= 175 {
			reached = time.Duration(i) * dt
		}
	}
	if reached < 5*time.Minute || reached > 30*time.Minute {
		t.Fatalf("reached 175 °C after %v", reached)
	}
	if peak > 181 {
		t.Fatalf("overshoot to %.2f °C", peak)
	}
	if math.Abs(z.Temp-180) > 0.05 {
		t.Fatalf("steady state %.3f °C (PI must remove offset)", z.Temp)
	}
	// Holding temperature needs exactly the losses: 36 W/K × 160 K.
	if math.Abs(z.HeaterPower-5760) > 20 {
		t.Fatalf("holding power %.0f W", z.HeaterPower)
	}
}

func TestThermalZoneCoolsSlowerThanItHeats(t *testing.T) {
	z := NewThermalZone(zoneParams(), 20)
	heat := 0
	for z.Temp < 150 {
		z.Step(true, 180, 20, 0, dt)
		heat++
	}
	z.Temp = 180
	cool := 0
	for z.Temp > 50 {
		z.Step(false, 180, 20, 0, dt)
		cool++
		if z.HeaterPower != 0 {
			t.Fatal("heater on while disabled")
		}
	}
	if cool < 2*heat {
		t.Fatalf("cooling 180→50 took %d ticks, heating 20→150 %d ticks", cool, heat)
	}
}

// Process heat drawn by the melt is compensated by the controller until the
// heater saturates; beyond that the zone temperature drops.
func TestThermalZoneProcessHeat(t *testing.T) {
	settle := func(flow float64) *ThermalZone {
		z := NewThermalZone(zoneParams(), 180)
		for range 20000 {
			z.Step(true, 180, 20, flow, dt)
		}
		return z
	}
	nominal := settle(1)
	if math.Abs(nominal.Temp-180) > 0.1 || math.Abs(nominal.HeaterPower-9760) > 20 {
		t.Fatalf("nominal: %.2f °C %.0f W", nominal.Temp, nominal.HeaterPower)
	}
	overload := settle(2.5) // needs 5760 + 10000 W > 12 kW
	if overload.HeaterPower != 12e3 || overload.Temp > 170 {
		t.Fatalf("overload: %.2f °C %.0f W", overload.Temp, overload.HeaterPower)
	}
}

func extruderParams() ExtruderParams {
	return ExtruderParams{NominalRPM: 100, RampRate: 5, OutputPerRPM: 4.5, ThroughputTau: 8 * time.Second,
		SpecificEnergy: 0.2, NoLoadPower: 4, RatedPower: 132, Voltage: 400, PowerFactor: 0.88, Efficiency: 0.94}
}

func TestExtruderNominalPoint(t *testing.T) {
	x := NewExtruder(extruderParams())
	for range 600 {
		x.Step(100, 1, false, dt)
	}
	// Hand calculation: 4 + 0.2·450 = 94 kW; /0.94 = 100 kW; I = 100e3/(√3·400·0.88) = 164.0 A.
	if math.Abs(x.Throughput-450) > 0.01 || math.Abs(x.MechPower-94) > 0.01 || math.Abs(x.Current-164.0) > 0.1 {
		t.Fatalf("%+v", x)
	}
	if math.Abs(x.Load-71.2) > 0.1 {
		t.Fatalf("load %.2f %%", x.Load)
	}
}

func TestExtruderCausality(t *testing.T) {
	x := NewExtruder(extruderParams())
	for range 600 {
		x.Step(100, 1, false, dt)
	}
	base := *x
	// Speed-up: rpm ramps, throughput lags behind rpm.
	x.Step(120, 1, false, dt)
	if x.RPM != 102.5 || x.Throughput >= 102.5*4.5 {
		t.Fatalf("rpm %v throughput %v", x.RPM, x.Throughput)
	}
	for range 600 {
		x.Step(120, 1, false, dt)
	}
	if x.Throughput <= base.Throughput || x.Current <= base.Current {
		t.Fatal("higher rpm must raise throughput and current")
	}
	// Colder (more viscous) melt needs more torque at the same output.
	cold := *x
	cold.Step(120, 2, false, dt)
	if cold.Current <= x.Current {
		t.Fatal("higher viscosity must raise current")
	}
	// Emergency stop: speed zero at once, throughput decays.
	x.Step(120, 1, true, dt)
	if x.RPM != 0 || x.Throughput <= 0 {
		t.Fatalf("stop: rpm %v throughput %v", x.RPM, x.Throughput)
	}
	for range 400 {
		x.Step(0, 1, false, dt)
	}
	if x.Throughput != 0 || x.Current != 0 {
		t.Fatalf("did not settle: %+v", x)
	}
}

func meltParams() MeltParams {
	return MeltParams{RefTemp: 210, ViscosityTempCoef: 0.02, MinViscosityRatio: 0.05, MaxViscosityRatio: 20,
		ShearHeatPerRPM: 0.1, MeltTempTau: 20 * time.Second, NominalPressure: 220, FlowExponent: 0.4, PressureDeadTime: time.Second, PressureTau: 2 * time.Second}
}

func TestMeltViscosityAndPressure(t *testing.T) {
	m := NewMelt(meltParams(), 450, dt)
	m.SetTemp(20)
	// Melt temperature lags: after one τ (40 ticks) it covers 63 % of the step.
	for range 40 {
		m.UpdateViscosity(205, 205, 50, dt) // target 205 + 5 = 210 °C
	}
	if math.Abs(m.Temp-(20+190*(1-math.Exp(-1)))) > 1e-6 {
		t.Fatalf("melt temp after 1 τ: %v", m.Temp)
	}
	for range 2000 {
		m.UpdateViscosity(205, 205, 50, dt)
	}
	if math.Abs(m.Temp-210) > 1e-9 || math.Abs(m.ViscosityRatio-1) > 1e-9 {
		t.Fatalf("T %v η %v", m.Temp, m.ViscosityRatio)
	}
	// Pressure response: dead time of 2 samples, then a lag.
	m.StepPressure(450, dt)
	m.StepPressure(450, dt)
	if m.Pressure != 0 {
		t.Fatalf("pressure moved during dead time: %v", m.Pressure)
	}
	for range 100 {
		m.StepPressure(450, dt)
	}
	if math.Abs(m.Pressure-220) > 0.01 {
		t.Fatalf("nominal pressure %v", m.Pressure)
	}
	// Double flow → 2^0.4 = 1.32× pressure (shear thinning, not 2×).
	for range 100 {
		m.StepPressure(900, dt)
	}
	if math.Abs(m.Pressure-220*math.Pow(2, 0.4)) > 0.01 {
		t.Fatalf("double-flow pressure %v", m.Pressure)
	}
	// 35 °C colder melt doubles viscosity (e^(0.02·35) ≈ 2.01) and pressure.
	for range 2000 {
		m.UpdateViscosity(170, 170, 50, dt)
	}
	for range 100 {
		m.StepPressure(450, dt)
	}
	if math.Abs(m.Pressure/220-math.Exp(0.7)) > 0.01 {
		t.Fatalf("cold pressure ratio %v", m.Pressure/220)
	}
	m.SetTemp(-500)
	if m.ViscosityRatio != 20 {
		t.Fatalf("viscosity clamp %v", m.ViscosityRatio)
	}
}

func TestVacuum(t *testing.T) {
	v := NewVacuum(VacuumParams{Target: 400, PullTau: 10 * time.Second, DecayTau: 5 * time.Second})
	for range 200 {
		v.Step(true, 1, dt)
	}
	if math.Abs(v.Magnitude-400) > 0.1 {
		t.Fatalf("pulled %v", v.Magnitude)
	}
	for range 200 {
		v.Step(true, 0.5, dt)
	}
	if math.Abs(v.Magnitude-200) > 0.1 {
		t.Fatalf("half capacity %v", v.Magnitude)
	}
	for range 100 {
		v.Step(false, 1, dt)
	}
	if v.Magnitude > 1 || v.Magnitude < 0 {
		t.Fatalf("decay %v", v.Magnitude)
	}
}

func TestCoolingWater(t *testing.T) {
	p := CoolingParams{SupplyTemp: 12, FlowRate: 15, TankTau: 180 * time.Second, PolymerCp: 2.3, LatentHeat: 220, PipeExitTemp: 40}
	c := NewCooling(p)
	for range 5000 {
		c.Step(450, 212.5, 1, dt)
	}
	// Q = 0.125 kg/s · (2.3·172.5 + 220) = 77.1 kW; water 4.167 kg/s · 4.18 → +4.43 °C.
	if math.Abs(c.HeatLoad-77.1) > 0.1 || math.Abs(c.WaterTemp-16.43) > 0.02 {
		t.Fatalf("load %.2f kW, water %.2f °C", c.HeatLoad, c.WaterTemp)
	}
	healthy := c.Efficiency
	for range 5000 {
		c.Step(450, 212.5, 0.3, dt) // cooling failure: 30 % flow
	}
	if c.WaterTemp < 25 || c.Efficiency >= healthy {
		t.Fatalf("reduced flow: %.2f °C, efficiency %.3f vs %.3f", c.WaterTemp, c.Efficiency, healthy)
	}
	for range 5000 {
		c.Step(0, 20, 1, dt)
	}
	if math.Abs(c.WaterTemp-12) > 0.01 || c.HeatLoad != 0 {
		t.Fatalf("idle: %.2f °C", c.WaterTemp)
	}
}

func TestWallFromMassBalance(t *testing.T) {
	// Reference product: 450 kg/h at 2.5 m/min, Ø110, PE100 → 10.0 mm.
	if s := WallFromMassBalance(450, 2.5, 110, 955); math.Abs(s-10.0) > 0.005 {
		t.Fatalf("wall %v", s)
	}
	// Faster haul-off → thinner wall; more throughput → thicker.
	if WallFromMassBalance(450, 3, 110, 955) >= 10 || WallFromMassBalance(540, 2.5, 110, 955) <= 10 {
		t.Fatal("mass balance direction")
	}
	if WallFromMassBalance(450, 0.0001, 110, 955) != 55 {
		t.Fatal("must clamp to solid rod")
	}
	if WallFromMassBalance(0, 2.5, 110, 955) != 0 || WallFromMassBalance(450, 0, 110, 955) != 0 {
		t.Fatal("no flow or no speed → 0")
	}
}

func pipeParams() PipeParams {
	return PipeParams{CalibratorOD: 110, Density: 955, HaulOffRampRate: 0.5, MaxVacuumShrink: 0.02,
		NominalVacuum: 400, ShrinkPerDegC: 0.0003, RefWaterTemp: 16, GaugeDistance: 6, SampleResolution: 0.01}
}

// The gauge sees a section only after it has travelled GaugeDistance.
func TestPipeTransportDelayIsDistance(t *testing.T) {
	p := NewPipe(pipeParams())
	ticks := 0
	for p.OD == 0 {
		p.Step(2.5, 450, 400, 16, false, dt)
		ticks++
		if ticks > 1000 {
			t.Fatal("pipe never reached the gauge")
		}
	}
	// 6 m at 2.5 m/min = 144 s = 288 ticks, plus the 5 s ramp (≈ 2.5 s lost).
	if ticks < 288 || ticks > 300 {
		t.Fatalf("first pipe at gauge after %d ticks", ticks)
	}
	// The first sections were made while the haul-off ramped (startup scrap,
	// thick wall). Pipe made after the 5 s ramp (~0.1 m) has the nominal wall.
	if p.Wall <= 10.5 {
		t.Fatalf("first section wall %.3f, expected thick startup scrap", p.Wall)
	}
	for p.position-pipeParams().GaugeDistance < 0.2 {
		p.Step(2.5, 450, 400, 16, false, dt)
	}
	if math.Abs(p.OD-110) > 0.01 || math.Abs(p.Wall-10) > 0.01 {
		t.Fatalf("OD %.3f wall %.3f", p.OD, p.Wall)
	}

	// Speed-up: thinner wall at the exit at once, at the gauge only after 6 m.
	p.Step(3, 450, 400, 16, false, dt)
	if p.ExitWall >= 10 || p.Wall != p.Wall {
		t.Fatalf("exit wall %.3f", p.ExitWall)
	}
	start := p.position
	for p.position-start < 5.5 {
		p.Step(3, 450, 400, 16, false, dt)
		if p.Wall < 9.99 {
			t.Fatalf("gauge changed after %.2f m", p.position-start)
		}
	}
	for p.position-start < 6.5 {
		p.Step(3, 450, 400, 16, false, dt)
	}
	if math.Abs(p.Wall-WallFromMassBalance(450, 3, 110, 955)) > 0.01 {
		t.Fatalf("gauge wall %.3f after 6.5 m", p.Wall)
	}

	// Stopped haul-off: the gauge reading freezes.
	p.Step(0, 0, 0, 16, true, dt)
	frozen := p.Wall
	for range 1000 {
		p.Step(0, 0, 0, 16, false, dt)
	}
	if p.Wall != frozen || p.Speed != 0 {
		t.Fatalf("gauge moved while stopped: %v → %v", frozen, p.Wall)
	}
}

func TestPipeODShrinkage(t *testing.T) {
	exitOD := func(vacuum, water float64) float64 {
		p := NewPipe(pipeParams())
		p.Step(2.5, 450, vacuum, water, false, dt)
		return p.ExitOD
	}
	if od := exitOD(400, 16); od != 110 {
		t.Fatalf("nominal OD %v", od)
	}
	if od := exitOD(200, 16); math.Abs(od-108.9) > 1e-9 { // half vacuum → 1 % shrink
		t.Fatalf("half vacuum OD %v", od)
	}
	if od := exitOD(400, 26); math.Abs(od-109.67) > 1e-9 { // +10 °C water → 0.3 %
		t.Fatalf("warm water OD %v", od)
	}
	if exitOD(600, 16) != 110 {
		t.Fatal("extra vacuum must not enlarge the pipe")
	}
}

func TestValidate(t *testing.T) {
	bad := []interface{ Validate() error }{
		ThermalZoneParams{},
		ExtruderParams{},
		func() ExtruderParams { p := extruderParams(); p.Efficiency = 0; return p }(),
		MeltParams{},
		func() MeltParams { p := meltParams(); p.FlowExponent = 1.5; return p }(),
		func() MeltParams { p := meltParams(); p.PressureTau = 0; return p }(),
		func() MeltParams { p := meltParams(); p.MeltTempTau = 0; return p }(),
		VacuumParams{},
		CoolingParams{},
		PipeParams{},
		func() PipeParams { p := pipeParams(); p.MaxVacuumShrink = 1; return p }(),
		func() PipeParams { p := pipeParams(); p.SampleResolution = 0; return p }(),
	}
	for i, v := range bad {
		if v.Validate() == nil {
			t.Errorf("case %d (%T): want error", i, v)
		}
	}
	good := []interface{ Validate() error }{zoneParams(), extruderParams(), meltParams(),
		VacuumParams{Target: 1, PullTau: 1, DecayTau: 1},
		CoolingParams{FlowRate: 1, TankTau: 1, PolymerCp: 1}, pipeParams()}
	for i, v := range good {
		if err := v.Validate(); err != nil {
			t.Errorf("good case %d: %v", i, err)
		}
	}
}

func TestFaultHooks(t *testing.T) {
	// Heater hooks, from a zone 5 °C below its 180 °C setpoint (controller ≈ 6 kW).
	hold := func(mut func(*ThermalZone)) *ThermalZone {
		z := NewThermalZone(zoneParams(), 175)
		mut(z)
		z.Step(true, 180, 20, 0, dt)
		return z
	}
	if z := hold(func(z *ThermalZone) {}); z.HeaterPower <= 0 || z.HeaterPower >= 12e3 {
		t.Fatalf("healthy %v", z.HeaterPower)
	}
	if z := hold(func(z *ThermalZone) { z.Stuck = true }); z.HeaterPower != 12e3 {
		t.Fatalf("stuck %v", z.HeaterPower)
	}
	if z := hold(func(z *ThermalZone) { z.Stuck, z.SupplyOff = true, true }); z.HeaterPower != 0 {
		t.Fatalf("stuck without supply %v", z.HeaterPower)
	}
	if z := hold(func(z *ThermalZone) { z.Failed = true }); z.HeaterPower != 0 {
		t.Fatalf("failed %v", z.HeaterPower)
	}
	if z := hold(func(z *ThermalZone) { z.SensorFailed = true }); z.HeaterPower != 0 {
		t.Fatalf("thermocouple open %v", z.HeaterPower)
	}

	// Torque and pressure factors.
	x := NewExtruder(extruderParams())
	for range 600 {
		x.Step(100, 1, false, dt)
	}
	base := x.MechPower
	x.TorqueFactor = 1.7
	x.Step(100, 1, false, dt)
	if math.Abs(x.MechPower/base-1.7) > 1e-9 {
		t.Fatalf("torque factor %v", x.MechPower/base)
	}
	x.StopNow()
	if x.RPM != 0 {
		t.Fatal("StopNow")
	}
	m := NewMelt(meltParams(), 450, dt)
	m.SetTemp(210)
	m.PressureFactor = 1.8
	for range 100 {
		m.StepPressure(450, dt)
	}
	if math.Abs(m.Pressure-220*1.8) > 0.01 {
		t.Fatalf("pressure factor %v", m.Pressure)
	}

	// Haul-off slip: drive reference unchanged, pipe slower, wall thicker.
	p := NewPipe(pipeParams())
	for range 20 {
		p.Step(2.5, 450, 400, 16, false, dt)
	}
	p.Slip = 0.2
	p.Step(2.5, 450, 400, 16, false, dt)
	if p.Reference != 2.5 || math.Abs(p.Speed-2.0) > 1e-9 || p.ExitWall <= 10 {
		t.Fatalf("slip: ref %v speed %v wall %v", p.Reference, p.Speed, p.ExitWall)
	}
	p.StopNow()
	if p.Speed != 0 || p.Reference != 0 {
		t.Fatal("pipe StopNow")
	}
}
