package models

import (
	"errors"
	"time"
)

// VacuumParams describe the vacuum calibration tank pump.
type VacuumParams struct {
	Target   float64       `yaml:"target"`    // mbar below atmosphere when the pump runs
	PullTau  time.Duration `yaml:"pull_tau"`  // evacuation time constant
	DecayTau time.Duration `yaml:"decay_tau"` // leak-down when the pump stops
}

func (p VacuumParams) Validate() error {
	if p.Target <= 0 || p.PullTau <= 0 || p.DecayTau <= 0 {
		return errors.New("vacuum: target and time constants must be positive")
	}
	return nil
}

// Vacuum is the tank vacuum magnitude (mbar below atmosphere, ≥ 0).
type Vacuum struct {
	p         VacuumParams
	lag       FirstOrderLag
	Magnitude float64
}

func NewVacuum(p VacuumParams) *Vacuum { return &Vacuum{p: p} }

// Step advances the vacuum; capacity scales the achievable vacuum (1 =
// healthy pump; reduced by fault injection in Phase 5).
func (v *Vacuum) Step(pumpOn bool, capacity float64, dt time.Duration) {
	target, tau := 0.0, v.p.DecayTau
	if pumpOn {
		target, tau = v.p.Target*max(0, capacity), v.p.PullTau
	}
	v.lag.Tau = tau
	v.Magnitude = v.lag.Step(target, dt)
}

// CoolingParams describe the spray/immersion cooling water circuit.
type CoolingParams struct {
	SupplyTemp   float64       `yaml:"supply_temp"`    // °C chilled water supply
	FlowRate     float64       `yaml:"flow_rate"`      // m³/h
	TankTau      time.Duration `yaml:"tank_tau"`       // mixing time constant of the tank
	PolymerCp    float64       `yaml:"polymer_cp"`     // kJ/(kg·K)
	LatentHeat   float64       `yaml:"latent_heat"`    // kJ/kg (crystallisation, PE ≈ 200–230)
	PipeExitTemp float64       `yaml:"pipe_exit_temp"` // °C pipe temperature leaving the cooling line
}

func (p CoolingParams) Validate() error {
	if p.FlowRate <= 0 || p.TankTau <= 0 || p.PolymerCp <= 0 || p.LatentHeat < 0 {
		return errors.New("cooling: flow, tank tau and polymer cp must be positive")
	}
	return nil
}

// Cooling models the cooling water temperature from the heat the pipe
// releases:
//
//	Q [kW]  = throughput[kg/s] · (cp·(T_melt − T_exit) + latent)
//	T_water = lag(T_supply + Q / (flow[kg/s] · 4.18))
//	efficiency = (T_melt − T_water) / (T_melt − T_supply)   (1 = ideal)
type Cooling struct {
	p          CoolingParams
	lag        FirstOrderLag
	WaterTemp  float64 // °C
	HeatLoad   float64 // kW
	Efficiency float64 // 0..1
}

func NewCooling(p CoolingParams) *Cooling {
	return &Cooling{p: p, lag: FirstOrderLag{Tau: p.TankTau, Value: p.SupplyTemp}, WaterTemp: p.SupplyTemp, Efficiency: 1}
}

// Step advances the water temperature. flowFactor scales the water flow
// (1 = healthy; reduced by a cooling failure in Phase 5).
func (c *Cooling) Step(throughput, meltTemp, flowFactor float64, dt time.Duration) {
	dT := max(0, meltTemp-c.p.PipeExitTemp)
	c.HeatLoad = throughput / 3600 * (c.p.PolymerCp*dT + c.p.LatentHeat)
	waterKgPerS := c.p.FlowRate * max(0.01, flowFactor) * 1000 / 3600
	c.WaterTemp = c.lag.Step(c.p.SupplyTemp+c.HeatLoad/(waterKgPerS*4.18), dt)
	if span := meltTemp - c.p.SupplyTemp; span > 0 {
		c.Efficiency = max(0, min(1, (meltTemp-c.WaterTemp)/span))
	} else {
		c.Efficiency = 1
	}
}
