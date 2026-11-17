package models

import (
	"errors"
	"time"
)

// ThermalZoneParams describe one heated barrel zone or the die.
type ThermalZoneParams struct {
	HeatCapacity float64 `yaml:"heat_capacity"` // J/K, steel mass × specific heat
	LossCoeff    float64 `yaml:"loss_coeff"`    // W/K, losses to ambient
	HeaterMax    float64 `yaml:"heater_max"`    // W, installed heater power
	Kp           float64 `yaml:"kp"`            // W/K, controller gain
	Ti           float64 `yaml:"ti"`            // s, controller integral time
	// ProcessHeatNominal is the heat the melt delivers to (+) or draws from
	// (−) the zone at nominal throughput, in W. It scales linearly with
	// throughput: feed zones heat cold pellets, metering zones and the die
	// receive viscous dissipation.
	ProcessHeatNominal float64 `yaml:"process_heat_nominal"`
}

func (p ThermalZoneParams) Validate() error {
	if p.HeatCapacity <= 0 || p.LossCoeff <= 0 || p.HeaterMax <= 0 || p.Kp <= 0 || p.Ti <= 0 {
		return errors.New("thermal zone: capacity, loss, heater power and controller gains must be positive")
	}
	return nil
}

// ThermalZone is a lumped energy balance:
//
//	C·dT/dt = P_heater + P_process − h·(T − T_ambient)
//
// with P_heater from a PI temperature controller (0..HeaterMax). Heating is
// fast (full heater power) while cooling is slow (losses only), so the
// asymmetry follows from the physics rather than a tuned time constant.
type ThermalZone struct {
	p           ThermalZoneParams
	ctl         PI
	Temp        float64 // °C
	HeaterPower float64 // W actually delivered by the heater

	// Fault hooks (zero values = healthy), set before Step.
	SupplyOff    bool // main heater contactor open: no power at all
	Stuck        bool // SSR welded: full power whenever supplied
	Failed       bool // heater element open: no power
	SensorFailed bool // thermocouple open (upscale burnout): controller output 0
}

// NewThermalZone returns a zone at temperature t0.
func NewThermalZone(p ThermalZoneParams, t0 float64) *ThermalZone {
	return &ThermalZone{p: p, Temp: t0, ctl: PI{Kp: p.Kp, Ti: p.Ti, Min: 0, Max: p.HeaterMax}}
}

// Step advances the zone. heatersOn enables the controller; flowRatio is
// throughput / nominal throughput (scales the process heat).
func (z *ThermalZone) Step(heatersOn bool, setpoint, ambient, flowRatio float64, dt time.Duration) {
	switch {
	case z.SupplyOff || z.Failed:
		z.ctl.Reset()
		z.HeaterPower = 0
	case z.Stuck:
		z.HeaterPower = z.p.HeaterMax
	case heatersOn && !z.SensorFailed:
		z.HeaterPower = z.ctl.Step(setpoint-z.Temp, dt)
	default:
		z.ctl.Reset()
		z.HeaterPower = 0
	}
	q := z.HeaterPower + z.p.ProcessHeatNominal*flowRatio - z.p.LossCoeff*(z.Temp-ambient)
	// Explicit Euler is stable here: C/h ≈ 1000 s ≫ dt.
	z.Temp += q / z.p.HeatCapacity * dt.Seconds()
}
