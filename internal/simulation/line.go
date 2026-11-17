package simulation

import (
	"errors"
	"fmt"
	"time"

	"modbus-simulator/internal/simulation/models"
)

// ProcessParams parameterise all process models of one line.
// Assumptions and default values: docs/spikes/005-process-model.md.
type ProcessParams struct {
	AmbientTemp    float64                     `yaml:"ambient_temp"` // °C
	Zones          [3]models.ThermalZoneParams `yaml:"zones"`
	Die            models.ThermalZoneParams    `yaml:"die"`
	Extruder       models.ExtruderParams       `yaml:"extruder"`
	Melt           models.MeltParams           `yaml:"melt"`
	Vacuum         models.VacuumParams         `yaml:"vacuum"`
	Cooling        models.CoolingParams        `yaml:"cooling"`
	Pipe           models.PipeParams           `yaml:"pipe"`
	VacuumPump     float64                     `yaml:"vacuum_pump"`      // kW while running
	HaulOffDrive   float64                     `yaml:"haul_off_drive"`   // kW at NominalHaulOff speed
	NominalHaulOff float64                     `yaml:"nominal_haul_off"` // m/min
}

func (p ProcessParams) validate() error {
	var errs []error
	for i, z := range p.Zones {
		if err := z.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("zone %d: %w", i+1, err))
		}
	}
	if err := p.Die.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("die: %w", err))
	}
	for _, v := range []interface{ Validate() error }{p.Extruder, p.Melt, p.Vacuum, p.Cooling, p.Pipe} {
		if err := v.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.VacuumPump < 0 || p.HaulOffDrive < 0 || p.NominalHaulOff <= 0 {
		errs = append(errs, errors.New("auxiliary drive powers must be ≥ 0 and nominal haul-off speed > 0"))
	}
	return errors.Join(errs...)
}

// DefaultProcessParams returns the 110 × 10 mm PE100 reference line.
func DefaultProcessParams() ProcessParams {
	zone := func(processHeat float64) models.ThermalZoneParams {
		return models.ThermalZoneParams{HeatCapacity: 42e3, LossCoeff: 36, HeaterMax: 12e3, Kp: 1200, Ti: 300, ProcessHeatNominal: processHeat}
	}
	return ProcessParams{
		AmbientTemp: 20,
		Zones:       [3]models.ThermalZoneParams{zone(-4000), zone(-2000), zone(1500)},
		Die:         models.ThermalZoneParams{HeatCapacity: 30e3, LossCoeff: 25, HeaterMax: 8e3, Kp: 800, Ti: 300, ProcessHeatNominal: 500},
		Extruder: models.ExtruderParams{
			NominalRPM: 100, RampRate: 5, OutputPerRPM: 4.5, ThroughputTau: 8 * time.Second,
			SpecificEnergy: 0.20, NoLoadPower: 4, RatedPower: 132,
			Voltage: 400, PowerFactor: 0.88, Efficiency: 0.94,
		},
		Melt: models.MeltParams{
			RefTemp: 210, ViscosityTempCoef: 0.02, MinViscosityRatio: 0.05, MaxViscosityRatio: 20,
			ShearHeatPerRPM: 0.1, MeltTempTau: 20 * time.Second, NominalPressure: 220, FlowExponent: 0.4,
			PressureDeadTime: time.Second, PressureTau: 2 * time.Second,
		},
		Vacuum: models.VacuumParams{Target: 400, PullTau: 10 * time.Second, DecayTau: 5 * time.Second},
		Cooling: models.CoolingParams{
			SupplyTemp: 12, FlowRate: 15, TankTau: 180 * time.Second,
			PolymerCp: 2.3, LatentHeat: 220, PipeExitTemp: 40,
		},
		Pipe: models.PipeParams{
			CalibratorOD: 110, Density: 955, HaulOffRampRate: 0.5,
			MaxVacuumShrink: 0.02, NominalVacuum: 400, ShrinkPerDegC: 0.0003, RefWaterTemp: 16,
			GaugeDistance: 6, SampleResolution: 0.01,
		},
		VacuumPump: 5.5, HaulOffDrive: 1.5, NominalHaulOff: 2.5,
	}
}

// ProcessValues is one set of process quantities, either true or measured.
// Fields match the input register map in docs/registers/convention.md.
type ProcessValues struct {
	ExtruderRPM      float64    // rpm
	MotorCurrent     float64    // A
	MotorLoad        float64    // % of rated power
	MeltPressure     float64    // bar
	MeltTemp         float64    // °C
	ZoneTemps        [3]float64 // °C
	DieTemp          float64    // °C
	VacuumMagnitude  float64    // mbar below atmosphere
	CoolingWaterTemp float64    // °C
	HaulOffSpeed     float64    // m/min
	Throughput       float64    // kg/h
	PowerConsumption float64    // kW, total electrical
	PipeOD           float64    // mm at the gauge (0 = no pipe yet)
	WallThickness    float64    // mm at the gauge (0 = no pipe yet)
}

// Internals are model quantities useful for diagnostics and tests; they are
// not sensor readings.
type Internals struct {
	HeaterPower       [4]float64 // W, zones 1..3 and die
	ViscosityRatio    float64
	MechPower         float64 // kW at the screw
	CoolingHeatLoad   float64 // kW
	CoolingEfficiency float64 // 0..1
	ExitOD            float64 // mm at the calibrator
	ExitWall          float64 // mm at the calibrator
	HaulOffReference  float64 // m/min commanded to the haul-off drive
}

// line composes the process models. Causal order per tick:
// viscosity (from barrel temps) → extruder → zone energy balance →
// die pressure → vacuum → cooling water → haul-off and pipe → counters.
type line struct {
	p        ProcessParams
	zones    [4]*models.ThermalZone // 0..2 barrel, 3 die
	extruder *models.Extruder
	melt     *models.Melt
	vacuum   *models.Vacuum
	cooling  *models.Cooling
	pipe     *models.Pipe
	nomFlow  float64
	length   float64 // m produced in RUNNING
	weight   float64 // kg produced in RUNNING
	powerKW  float64
}

func newLine(p ProcessParams, dt time.Duration) *line {
	l := &line{
		p:        p,
		extruder: models.NewExtruder(p.Extruder),
		melt:     models.NewMelt(p.Melt, p.Extruder.NominalThroughput(), dt),
		vacuum:   models.NewVacuum(p.Vacuum),
		cooling:  models.NewCooling(p.Cooling),
		pipe:     models.NewPipe(p.Pipe),
		nomFlow:  p.Extruder.NominalThroughput(),
	}
	for i := range 3 {
		l.zones[i] = models.NewThermalZone(p.Zones[i], p.AmbientTemp)
	}
	l.zones[3] = models.NewThermalZone(p.Die, p.AmbientTemp)
	l.melt.SetTemp(p.AmbientTemp)
	return l
}

// step advances all models. heatersOn enables the temperature controllers;
// heaterSupply is the main heater contactor; d carries the active fault cause.
func (l *line) step(state MachineState, sp Setpoints, heatersOn, heaterSupply bool, d *disturbance, dt time.Duration) {
	running := state == Running
	fault := state == Fault

	l.melt.UpdateViscosity(l.zones[2].Temp, l.zones[3].Temp, l.extruder.RPM, dt)

	rpmTarget, speedTarget := 0.0, 0.0
	if running {
		rpmTarget, speedTarget = sp.ExtruderRPM, sp.HaulOffSpeed
	}
	l.extruder.TorqueFactor = d.torque.Value
	l.extruder.Step(rpmTarget, l.melt.ViscosityRatio, fault, dt)
	flowRatio := l.extruder.Throughput / l.nomFlow

	targets := [4]float64{sp.ZoneTemps[0], sp.ZoneTemps[1], sp.ZoneTemps[2], sp.DieTemp}
	for i, z := range l.zones {
		z.SupplyOff = !heaterSupply
		z.Stuck, z.Failed, z.SensorFailed = d.heaterStuck[i], d.heaterFailed[i], d.sensorFailed[i]
		z.Step(heatersOn, targets[i], l.p.AmbientTemp, flowRatio, dt)
	}

	l.melt.PressureFactor = d.pressure.Value
	l.melt.StepPressure(l.extruder.Throughput, dt)
	l.vacuum.Step(running, d.vacuumCap.Value, dt)
	l.cooling.Step(l.extruder.Throughput, l.melt.Temp, d.coolingFlow.Value, dt)
	l.pipe.Slip = d.slip.Value
	l.pipe.Step(speedTarget, l.extruder.Throughput, l.vacuum.Magnitude, l.cooling.WaterTemp, fault, dt)

	if running {
		l.length += l.pipe.Speed / 60 * dt.Seconds()
		l.weight += l.extruder.Throughput / 3600 * dt.Seconds()
	}

	heaters := 0.0
	for _, z := range l.zones {
		heaters += z.HeaterPower / 1000
	}
	pump := 0.0
	if running { // pump power is drawn whatever its capacity
		pump = l.p.VacuumPump
	}
	l.powerKW = l.extruder.ElecPower + heaters + pump + l.p.HaulOffDrive*l.pipe.Speed/l.p.NominalHaulOff
}

// safeStop cuts the drives at once when a fault is detected at the end of a
// tick, so the snapshot of the trip tick already shows them stopped (the PLC
// detects and switches its outputs in the same scan).
func (l *line) safeStop() {
	l.extruder.StopNow()
	l.pipe.StopNow()
}

// coldestZone returns the lowest zone/die temperature.
func (l *line) coldestZone() float64 {
	t := l.zones[0].Temp
	for _, z := range l.zones[1:] {
		t = min(t, z.Temp)
	}
	return t
}

// zonesWithin reports whether every zone and the die are within band of
// their setpoints.
func (l *line) zonesWithin(sp Setpoints, band float64) bool {
	targets := [4]float64{sp.ZoneTemps[0], sp.ZoneTemps[1], sp.ZoneTemps[2], sp.DieTemp}
	for i, z := range l.zones {
		if d := z.Temp - targets[i]; d > band || d < -band {
			return false
		}
	}
	return true
}

func (l *line) values() ProcessValues {
	return ProcessValues{
		ExtruderRPM:      l.extruder.RPM,
		MotorCurrent:     l.extruder.Current,
		MotorLoad:        l.extruder.Load,
		MeltPressure:     l.melt.Pressure,
		MeltTemp:         l.melt.Temp,
		ZoneTemps:        [3]float64{l.zones[0].Temp, l.zones[1].Temp, l.zones[2].Temp},
		DieTemp:          l.zones[3].Temp,
		VacuumMagnitude:  l.vacuum.Magnitude,
		CoolingWaterTemp: l.cooling.WaterTemp,
		HaulOffSpeed:     l.pipe.Speed,
		Throughput:       l.extruder.Throughput,
		PowerConsumption: l.powerKW,
		PipeOD:           l.pipe.OD,
		WallThickness:    l.pipe.Wall,
	}
}

// heaterPercent is each heater's output in % of its installed power.
func (l *line) heaterPercent() [4]float64 {
	max := [4]float64{l.p.Zones[0].HeaterMax, l.p.Zones[1].HeaterMax, l.p.Zones[2].HeaterMax, l.p.Die.HeaterMax}
	var pct [4]float64
	for i, z := range l.zones {
		pct[i] = 100 * z.HeaterPower / max[i]
	}
	return pct
}

func (l *line) internals() Internals {
	var hp [4]float64
	for i, z := range l.zones {
		hp[i] = z.HeaterPower
	}
	return Internals{
		HeaterPower:       hp,
		ViscosityRatio:    l.melt.ViscosityRatio,
		MechPower:         l.extruder.MechPower,
		CoolingHeatLoad:   l.cooling.HeatLoad,
		CoolingEfficiency: l.cooling.Efficiency,
		ExitOD:            l.pipe.ExitOD,
		ExitWall:          l.pipe.ExitWall,
		HaulOffReference:  l.pipe.Reference,
	}
}

// NoiseConfig sets the measurement noise per sensor group. Magnitudes of
// the defaults are typical of industrial transmitters, not plant data.
type NoiseConfig struct {
	Temperature  NoiseSpec `yaml:"temperature"` // zones, die and melt
	ExtruderRPM  NoiseSpec `yaml:"extruder_rpm"`
	MotorCurrent NoiseSpec `yaml:"motor_current"`
	MotorLoad    NoiseSpec `yaml:"motor_load"`
	MeltPressure NoiseSpec `yaml:"melt_pressure"`
	Vacuum       NoiseSpec `yaml:"vacuum"`
	CoolingWater NoiseSpec `yaml:"cooling_water"`
	HaulOffSpeed NoiseSpec `yaml:"haul_off_speed"`
	Throughput   NoiseSpec `yaml:"throughput"`
	Power        NoiseSpec `yaml:"power"`
	PipeOD       NoiseSpec `yaml:"pipe_od"`
	Wall         NoiseSpec `yaml:"wall_thickness"`
}

// DefaultNoise returns the default sensor noise.
func DefaultNoise() NoiseConfig {
	return NoiseConfig{
		Temperature: tempNoise, ExtruderRPM: rpmNoise, MotorCurrent: currentNoise, MotorLoad: loadNoise,
		MeltPressure: pressureNoise, Vacuum: vacuumNoise, CoolingWater: waterNoise, HaulOffSpeed: speedNoise,
		Throughput: flowNoise, Power: powerNoise, PipeOD: odNoise, Wall: wallNoise,
	}
}

func (n NoiseConfig) validate() error {
	for name, s := range map[string]NoiseSpec{
		"temperature": n.Temperature, "extruder_rpm": n.ExtruderRPM, "motor_current": n.MotorCurrent,
		"motor_load": n.MotorLoad, "melt_pressure": n.MeltPressure, "vacuum": n.Vacuum,
		"cooling_water": n.CoolingWater, "haul_off_speed": n.HaulOffSpeed, "throughput": n.Throughput,
		"power": n.Power, "pipe_od": n.PipeOD, "wall_thickness": n.Wall,
	} {
		if s.Sigma < 0 || s.DriftStep < 0 || s.DriftMax < 0 {
			return fmt.Errorf("noise %s: sigma, drift_step and drift_max must be ≥ 0", name)
		}
	}
	return nil
}

// sensorSet measures ProcessValues; one independent stream per sensor.
type sensorSet struct {
	rpm, current, load, pressure, melt, die, vacuum, water, speed, flow, power, od, wall *Sensor
	zones                                                                                [3]*Sensor
}

var (
	tempNoise     = NoiseSpec{Sigma: 0.3, DriftStep: 0.01, DriftMax: 0.5}
	rpmNoise      = NoiseSpec{Sigma: 0.1, NonNegative: true, ZeroAtZero: true}
	currentNoise  = NoiseSpec{Sigma: 0.5, NonNegative: true, ZeroAtZero: true}
	loadNoise     = NoiseSpec{Sigma: 0.2, NonNegative: true, ZeroAtZero: true}
	pressureNoise = NoiseSpec{Sigma: 0.8, DriftStep: 0.02, DriftMax: 1.5, NonNegative: true, ZeroAtZero: true}
	vacuumNoise   = NoiseSpec{Sigma: 2, NonNegative: true, ZeroAtZero: true}
	waterNoise    = NoiseSpec{Sigma: 0.05}
	speedNoise    = NoiseSpec{Sigma: 0.005, NonNegative: true, ZeroAtZero: true}
	flowNoise     = NoiseSpec{Sigma: 0.8, DriftStep: 0.02, DriftMax: 1, NonNegative: true, ZeroAtZero: true}
	powerNoise    = NoiseSpec{Sigma: 0.2, NonNegative: true, ZeroAtZero: true}
	odNoise       = NoiseSpec{Sigma: 0.03, NonNegative: true, ZeroAtZero: true}
	wallNoise     = NoiseSpec{Sigma: 0.02, NonNegative: true, ZeroAtZero: true}
)

func newSensorSet(seed uint64, n NoiseConfig) sensorSet {
	s := func(name string, spec NoiseSpec) *Sensor { return newSensor(seed, name, spec) }
	return sensorSet{
		rpm:      s("extruder.rpm", n.ExtruderRPM),
		current:  s("extruder.motor_current", n.MotorCurrent),
		load:     s("extruder.motor_load", n.MotorLoad),
		pressure: s("melt.pressure", n.MeltPressure),
		melt:     s("melt.temp", n.Temperature),
		die:      s("die.temp", n.Temperature),
		vacuum:   s("vacuum.magnitude", n.Vacuum),
		water:    s("cooling.water_temp", n.CoolingWater),
		speed:    s("haul_off.speed", n.HaulOffSpeed),
		flow:     s("extruder.throughput", n.Throughput),
		power:    s("line.power", n.Power),
		od:       s("gauge.od", n.PipeOD),
		wall:     s("gauge.wall", n.Wall),
		zones:    [3]*Sensor{s("zone1.temp", n.Temperature), s("zone2.temp", n.Temperature), s("zone3.temp", n.Temperature)},
	}
}

func (s sensorSet) measure(v ProcessValues) ProcessValues {
	return ProcessValues{
		ExtruderRPM:      s.rpm.Measure(v.ExtruderRPM),
		MotorCurrent:     s.current.Measure(v.MotorCurrent),
		MotorLoad:        s.load.Measure(v.MotorLoad),
		MeltPressure:     s.pressure.Measure(v.MeltPressure),
		MeltTemp:         s.melt.Measure(v.MeltTemp),
		ZoneTemps:        [3]float64{s.zones[0].Measure(v.ZoneTemps[0]), s.zones[1].Measure(v.ZoneTemps[1]), s.zones[2].Measure(v.ZoneTemps[2])},
		DieTemp:          s.die.Measure(v.DieTemp),
		VacuumMagnitude:  s.vacuum.Measure(v.VacuumMagnitude),
		CoolingWaterTemp: s.water.Measure(v.CoolingWaterTemp),
		HaulOffSpeed:     s.speed.Measure(v.HaulOffSpeed),
		Throughput:       s.flow.Measure(v.Throughput),
		PowerConsumption: s.power.Measure(v.PowerConsumption),
		PipeOD:           s.od.Measure(v.PipeOD),
		WallThickness:    s.wall.Measure(v.WallThickness),
	}
}
