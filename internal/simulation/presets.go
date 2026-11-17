package simulation

import (
	"fmt"
	"strings"
	"time"

	"modbus-simulator/internal/simulation/models"
)

// Preset is a named, ready-to-run line configuration. Coefficients are
// engineering-plausible values chosen so that every preset is consistent
// with its product's mass balance (docs/spikes/005-process-model.md,
// docs/reports/phase-6-report.md); they are not plant data.
type Preset struct {
	Name        string
	Description string
	NominalOD   float64 // mm
	NominalWall float64 // mm
	Config      func() Config
}

// Presets returns the built-in lines in their default order (Line 1..3).
func Presets() []Preset {
	return []Preset{
		{"hdpe-110", "HDPE PE100 pipe Ø110 × 10.0 mm (SDR 11), Ø75 single screw, 450 kg/h", 110, 10.0, PresetHDPE110},
		{"hdpe-63", "HDPE PE100 pipe Ø63 × 5.8 mm (SDR 11), Ø60 single screw, 240 kg/h", 63, 5.8, PresetHDPE63},
		{"pvc-110", "PVC-U pipe Ø110 × 3.2 mm, counter-rotating twin screw, 300 kg/h", 110, 3.2, PresetPVC110},
	}
}

// PresetByName returns the preset with the given name.
func PresetByName(name string) (Preset, error) {
	for _, p := range Presets() {
		if p.Name == name {
			return p, nil
		}
	}
	names := make([]string, 0, len(Presets()))
	for _, p := range Presets() {
		names = append(names, p.Name)
	}
	return Preset{}, fmt.Errorf("unknown line preset %q (available: %s)", name, strings.Join(names, ", "))
}

// PresetHDPE110 is the Phase 3 reference line (same as DefaultConfig).
func PresetHDPE110() Config { return DefaultConfig() }

// PresetHDPE63 is a smaller HDPE line: Ø60 extruder, 80 rpm × 3.0 kg/h/rpm =
// 240 kg/h; Ø63 × 5.8 mm weighs 0.995 kg/m → 4.02 m/min.
func PresetHDPE63() Config {
	c := DefaultConfig()
	zone := func(processHeat float64) models.ThermalZoneParams {
		return models.ThermalZoneParams{HeatCapacity: 28e3, LossCoeff: 24, HeaterMax: 8e3, Kp: 800, Ti: 300, ProcessHeatNominal: processHeat}
	}
	p := &c.Process
	p.Zones = [3]models.ThermalZoneParams{zone(-2100), zone(-1100), zone(800)}
	p.Die = models.ThermalZoneParams{HeatCapacity: 20e3, LossCoeff: 17, HeaterMax: 6e3, Kp: 550, Ti: 300, ProcessHeatNominal: 270}
	p.Extruder.NominalRPM, p.Extruder.OutputPerRPM = 80, 3.0
	p.Extruder.NoLoadPower, p.Extruder.RatedPower = 3, 90
	p.Melt.NominalPressure = 200
	p.Cooling.FlowRate = 10
	p.Pipe.CalibratorOD = 63
	p.Pipe.RefWaterTemp = 15.5 // 12 °C + 41 kW / (10 m³/h · 4.18)
	p.HaulOffDrive, p.NominalHaulOff = 1.1, 4.02
	c.Setpoints.ExtruderRPM = 80
	c.Setpoints.HaulOffSpeed = 4.02
	return c
}

// PresetPVC110 is a rigid PVC line: counter-rotating twin screw, 30 rpm ×
// 10 kg/h/rpm = 300 kg/h, lower processing temperatures, stronger shear
// thinning and temperature sensitivity, amorphous (no latent heat), density
// 1400 kg/m³; Ø110 × 3.2 mm weighs 1.503 kg/m → 3.33 m/min.
func PresetPVC110() Config {
	c := DefaultConfig()
	zone := func(processHeat float64) models.ThermalZoneParams {
		return models.ThermalZoneParams{HeatCapacity: 42e3, LossCoeff: 36, HeaterMax: 12e3, Kp: 1200, Ti: 300, ProcessHeatNominal: processHeat}
	}
	p := &c.Process
	p.Zones = [3]models.ThermalZoneParams{zone(-3000), zone(-1000), zone(1000)}
	p.Die.ProcessHeatNominal = 300
	p.Extruder = models.ExtruderParams{
		NominalRPM: 30, RampRate: 2, OutputPerRPM: 10, ThroughputTau: 12 * time.Second,
		SpecificEnergy: 0.15, NoLoadPower: 3, RatedPower: 75,
		Voltage: 400, PowerFactor: 0.88, Efficiency: 0.94,
	}
	p.Melt = models.MeltParams{
		RefTemp: 195, ViscosityTempCoef: 0.03, MinViscosityRatio: 0.05, MaxViscosityRatio: 20,
		ShearHeatPerRPM: 0.3, MeltTempTau: 20 * time.Second, NominalPressure: 250, FlowExponent: 0.3,
		PressureDeadTime: time.Second, PressureTau: 2 * time.Second,
	}
	p.Vacuum.Target = 500
	p.Cooling.FlowRate, p.Cooling.PolymerCp, p.Cooling.LatentHeat = 10, 1.1, 0
	p.Pipe.Density = 1400
	p.Pipe.NominalVacuum = 500
	p.Pipe.ShrinkPerDegC = 0.0002
	p.Pipe.RefWaterTemp = 13.2 // 12 °C + 14.3 kW / (10 m³/h · 4.18)
	p.HaulOffDrive, p.NominalHaulOff = 1.5, 3.33
	c.Setpoints = Setpoints{ExtruderRPM: 30, ZoneTemps: [3]float64{175, 180, 185}, DieTemp: 190, HaulOffSpeed: 3.33}
	c.MinRunTemp = 150
	return c
}
