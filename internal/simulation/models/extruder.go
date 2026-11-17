package models

import (
	"errors"
	"math"
	"time"
)

// ExtruderParams describe the screw drive and motor.
type ExtruderParams struct {
	NominalRPM     float64       `yaml:"nominal_rpm"`     // rpm, reference operating point
	RampRate       float64       `yaml:"ramp_rate"`       // rpm/s
	OutputPerRPM   float64       `yaml:"output_per_rpm"`  // kg/h per rpm (ideal conveying)
	ThroughputTau  time.Duration `yaml:"throughput_tau"`  // melt flow delay behind screw speed
	SpecificEnergy float64       `yaml:"specific_energy"` // kWh/kg mechanical energy at η_rel = 1
	NoLoadPower    float64       `yaml:"no_load_power"`   // kW at nominal rpm (friction, gearbox)
	RatedPower     float64       `yaml:"rated_power"`     // kW motor rating (100 % load)
	Voltage        float64       `yaml:"voltage"`         // V line-to-line
	PowerFactor    float64       `yaml:"power_factor"`    // cos φ
	Efficiency     float64       `yaml:"efficiency"`      // motor efficiency 0..1
}

func (p ExtruderParams) Validate() error {
	switch {
	case p.NominalRPM <= 0, p.RampRate <= 0, p.OutputPerRPM <= 0, p.ThroughputTau <= 0:
		return errors.New("extruder: nominal rpm, ramp rate, output per rpm and throughput tau must be positive")
	case p.SpecificEnergy <= 0, p.NoLoadPower < 0, p.RatedPower <= 0, p.Voltage <= 0:
		return errors.New("extruder: specific energy, rated power and voltage must be positive")
	case p.PowerFactor <= 0 || p.PowerFactor > 1, p.Efficiency <= 0 || p.Efficiency > 1:
		return errors.New("extruder: power factor and efficiency must be in (0, 1]")
	}
	return nil
}

// NominalThroughput is the throughput at nominal rpm in kg/h.
func (p ExtruderParams) NominalThroughput() float64 { return p.NominalRPM * p.OutputPerRPM }

// Extruder models the screw speed, melt output and drive power.
//
//	rpm        ramps towards the target (immediate stop on emergency)
//	throughput = OutputPerRPM·rpm, delayed by a first-order lag
//	P_mech     = NoLoad·rpm/rpm_nom + SEC·throughput·√η_rel  (higher viscosity → more torque)
//	P_elec     = P_mech / efficiency ; I = P_elec / (√3·U·cos φ)
type Extruder struct {
	p          ExtruderParams
	rpm        RateLimiter
	flow       FirstOrderLag
	RPM        float64 // rpm
	Throughput float64 // kg/h
	MechPower  float64 // kW
	ElecPower  float64 // kW
	Current    float64 // A
	Load       float64 // % of rated power

	// TorqueFactor multiplies the mechanical load (fault hook, e.g. screw
	// blockage); 0 means 1.
	TorqueFactor float64
}

func NewExtruder(p ExtruderParams) *Extruder {
	return &Extruder{p: p, rpm: RateLimiter{Rate: p.RampRate}, flow: FirstOrderLag{Tau: p.ThroughputTau}}
}

// StopNow cuts the screw speed to zero immediately (safe state); throughput
// keeps decaying through its lag.
func (x *Extruder) StopNow() { x.rpm.Value, x.RPM = 0, 0 }

// Step advances the drive. stopNow cuts speed to zero at once (safe state).
func (x *Extruder) Step(targetRPM, viscosityRatio float64, stopNow bool, dt time.Duration) {
	if stopNow {
		x.rpm.Value, targetRPM = 0, 0
	}
	x.RPM = x.rpm.Step(targetRPM, dt)
	x.Throughput = x.flow.Step(x.RPM*x.p.OutputPerRPM, dt)
	if x.RPM == 0 && x.Throughput < 1e-6 {
		x.flow.Value, x.Throughput = 0, 0 // settle exactly
	}
	tf := x.TorqueFactor
	if tf <= 0 {
		tf = 1
	}
	x.MechPower = tf * (x.p.NoLoadPower*x.RPM/x.p.NominalRPM + x.p.SpecificEnergy*x.Throughput*math.Sqrt(viscosityRatio))
	x.ElecPower = x.MechPower / x.p.Efficiency
	x.Current = x.ElecPower * 1000 / (math.Sqrt(3) * x.p.Voltage * x.p.PowerFactor)
	x.Load = 100 * x.MechPower / x.p.RatedPower
}
