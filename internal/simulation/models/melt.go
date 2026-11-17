package models

import (
	"errors"
	"math"
	"time"
)

// MeltParams describe melt temperature, viscosity and die pressure.
type MeltParams struct {
	RefTemp           float64       `yaml:"ref_temp"`            // °C at which η_rel = 1
	ViscosityTempCoef float64       `yaml:"viscosity_temp_coef"` // 1/°C, η_rel = exp(−coef·(T − RefTemp))
	MinViscosityRatio float64       `yaml:"min_viscosity_ratio"` // clamp (avoid absurd values far from the window)
	MaxViscosityRatio float64       `yaml:"max_viscosity_ratio"` //
	ShearHeatPerRPM   float64       `yaml:"shear_heat_per_rpm"`  // °C of viscous heating per rpm
	MeltTempTau       time.Duration `yaml:"melt_temp_tau"`       // thermal inertia of the melt temperature
	NominalPressure   float64       `yaml:"nominal_pressure"`    // bar at nominal throughput and η_rel = 1
	FlowExponent      float64       `yaml:"flow_exponent"`       // shear thinning: P ∝ throughput^n
	PressureDeadTime  time.Duration `yaml:"pressure_dead_time"`  // transport/compression delay
	PressureTau       time.Duration `yaml:"pressure_tau"`
}

func (p MeltParams) Validate() error {
	switch {
	case p.ViscosityTempCoef <= 0, p.MinViscosityRatio <= 0, p.MaxViscosityRatio <= p.MinViscosityRatio:
		return errors.New("melt: viscosity parameters invalid")
	case p.NominalPressure <= 0, p.FlowExponent <= 0, p.FlowExponent > 1:
		return errors.New("melt: nominal pressure must be positive and flow exponent in (0, 1]")
	case p.PressureDeadTime < 0, p.PressureTau <= 0, p.ShearHeatPerRPM < 0, p.MeltTempTau <= 0:
		return errors.New("melt: delays must be non-negative and tau positive")
	}
	return nil
}

// Melt derives melt temperature and viscosity from the metering zone and die
// temperatures plus viscous heating, and the die head pressure from flow and
// viscosity:
//
//	T_melt   = lag((T_zone3 + T_die)/2 + ShearHeatPerRPM·rpm)
//	η_rel    = clamp(exp(−coef·(T_melt − T_ref)))
//	P_target = P_nom · (throughput/throughput_nom)^n · η_rel
//	pressure = lag(deadtime(P_target))
type Melt struct {
	p              MeltParams
	nominalFlow    float64
	dead           *DeadTime
	lag            FirstOrderLag
	temp           FirstOrderLag
	Temp           float64 // °C
	ViscosityRatio float64
	Pressure       float64 // bar

	// PressureFactor multiplies the die resistance (fault hook, e.g. a
	// clogging screen pack); 0 means 1.
	PressureFactor float64
}

func NewMelt(p MeltParams, nominalThroughput float64, dt time.Duration) *Melt {
	return &Melt{
		p: p, nominalFlow: nominalThroughput,
		dead: NewDeadTime(p.PressureDeadTime, dt, 0),
		lag:  FirstOrderLag{Tau: p.PressureTau},
		temp: FirstOrderLag{Tau: p.MeltTempTau},
	}
}

// SetTemp initialises the melt temperature (e.g. to ambient at start-up).
func (m *Melt) SetTemp(t float64) {
	m.temp.Value, m.Temp = t, t
	m.updateRatio()
}

// UpdateViscosity advances melt temperature and viscosity for the current
// barrel state. Call it before the extruder step (torque depends on it).
// The melt temperature lags: viscous heating from a speed change appears
// gradually, so pressure follows the (delayed) flow increase first.
func (m *Melt) UpdateViscosity(zone3, die, rpm float64, dt time.Duration) {
	m.Temp = m.temp.Step((zone3+die)/2+m.p.ShearHeatPerRPM*rpm, dt)
	m.updateRatio()
}

func (m *Melt) updateRatio() {
	r := math.Exp(-m.p.ViscosityTempCoef * (m.Temp - m.p.RefTemp))
	m.ViscosityRatio = max(m.p.MinViscosityRatio, min(m.p.MaxViscosityRatio, r))
}

// StepPressure advances the die pressure for the given throughput (kg/h).
func (m *Melt) StepPressure(throughput float64, dt time.Duration) {
	target := 0.0
	if throughput > 0 {
		target = m.p.NominalPressure * math.Pow(throughput/m.nominalFlow, m.p.FlowExponent) * m.ViscosityRatio
		if m.PressureFactor > 0 {
			target *= m.PressureFactor
		}
	}
	m.Pressure = m.lag.Step(m.dead.Step(target), dt)
	if target == 0 && m.Pressure < 1e-6 {
		m.lag.Value, m.Pressure = 0, 0
	}
}
