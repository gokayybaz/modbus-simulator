package plant

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modbus-simulator/internal/config"
	"modbus-simulator/internal/simulation"
)

func parse(t *testing.T, yaml string, args ...string) config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sim.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(append([]string{"-config", p}, args...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDefaultPresetNamesExist(t *testing.T) {
	for _, name := range config.DefaultPresets {
		if _, err := simulation.PresetByName(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildExampleConfig(t *testing.T) {
	cfg, err := config.Parse([]string{"-config", "../../configs/simulator.example.yaml"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0].Scenario != "normal-production" || lines[2].Spec.Name != "pvc-line" {
		t.Fatalf("%d lines", len(lines))
	}
	pvc := lines[2].Spec.Sim
	if pvc.Process.Cooling.FlowRate != 12 || pvc.Process.Melt.NominalPressure != 260 ||
		pvc.Process.Melt.FlowExponent != 0.3 || pvc.Setpoints.ExtruderRPM != 28 || pvc.MinRunTemp != 150 ||
		pvc.FaultLimits.MeltPressureHigh.Trip != 330 || pvc.FaultLimits.MotorOverload.Trip != 110 {
		t.Fatalf("pvc overrides: %+v", pvc)
	}
	if lo, hi, _ := lines[2].Engine.SetpointLimits(simulation.SPZone1Temp); lo != 0 || hi != 200 {
		t.Fatalf("pvc limits %v..%v", lo, hi)
	}
	if lines[1].Spec.Sim.Noise.Temperature.Sigma != 0.6 || lines[0].Spec.Sim.Noise.Temperature.Sigma != 0.3 {
		t.Fatal("noise overrides")
	}
	// Line-specific setpoint limits reach the Modbus write path.
	if err := lines[2].Device.WriteHoldingRegisters(3, 1, []uint16{2050}); err == nil {
		t.Fatal("205 °C accepted on the PVC zone 1 (limit 200)")
	}
	if err := lines[0].Device.WriteHoldingRegisters(1, 1, []uint16{2050}); err != nil {
		t.Fatalf("205 °C on the HDPE line: %v", err)
	}
}

// Overrides change only what they mention, and change the physics.
func TestOverridesAffectSimulation(t *testing.T) {
	cfg := parse(t, `version: 1
lines:
  - preset: hdpe-110
    min_run_temp: 100
    setpoints: {extruder_rpm: 80, haul_off_speed: 2.0}
    process:
      extruder: {output_per_rpm: 5}
    noise:
      temperature: {sigma: 0, drift_step: 0, drift_max: 0}
    fault_limits:
      motor_overload: {warning: 50, trip: 60, delay: 2s}
`)
	lines, err := Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := lines[0].Engine
	sim := lines[0].Spec.Sim
	if sim.Process.Extruder.NominalRPM != 100 || sim.Process.Extruder.OutputPerRPM != 5 || sim.MinRunTemp != 100 {
		t.Fatalf("process: %+v", sim.Process.Extruder)
	}
	e.Step([]simulation.Command{simulation.Start()})
	var s simulation.Snapshot
	for s = e.Snapshot(); s.State != simulation.Fault; {
		s, _ = e.Step(nil)
		if s.Tick > 8000 {
			t.Fatal("no trip")
		}
		if s.State == simulation.Heating && s.Measured.ZoneTemps[0] != s.True.ZoneTemps[0] {
			t.Fatal("temperature noise not disabled")
		}
	}
	// 80 rpm × 5 kg/h/rpm = 400 kg/h → ~63 % load: trips the lowered 60 % limit.
	if s.FaultCode != simulation.FaultMotorOverload {
		t.Fatalf("fault %d", s.FaultCode)
	}
}

func TestBuildErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown preset", "version: 1\nlines: [{name: x, preset: abs-50}]",
			[]string{"lines[0] (x)", `unknown line preset "abs-50" (available: hdpe-110, hdpe-63, pvc-110)`}},
		{"process typo with file line", "version: 1\nlines:\n  - preset: hdpe-110\n    process:\n      extruder:\n        output_per_rmp: 3\n",
			[]string{"process:", "line 6: field output_per_rmp not found"}},
		{"noise type", "version: 1\nlines:\n  - preset: hdpe-110\n    noise: {temperature: {sigma: high}}\n",
			[]string{"noise:", "cannot unmarshal"}},
		{"fault limit order", "version: 1\nlines:\n  - preset: hdpe-110\n    fault_limits: {vacuum_low: {warning: 100, trip: 200}}\n",
			[]string{"fault limit vacuum_low: trip 200 must be ≤ warning 100"}},
		{"invalid process value", "version: 1\nlines:\n  - preset: hdpe-110\n    process: {extruder: {efficiency: 2}}\n",
			[]string{"power factor and efficiency must be in (0, 1]"}},
		{"unknown setpoint", "version: 1\nlines: [{preset: hdpe-110, setpoints: {speed: 1}}]",
			[]string{`setpoints: unknown setpoint "speed"`}},
		{"unknown limit", "version: 1\nlines: [{preset: hdpe-110, setpoint_limits: {speed: {min: 0, max: 1}}}]",
			[]string{`setpoint_limits: unknown setpoint "speed"`}},
		{"limit wider than global", "version: 1\nlines: [{preset: hdpe-110, setpoint_limits: {zone1_temp: {min: 0, max: 400}}}]",
			[]string{"zone1_temp: 0..400 must satisfy min ≤ max within 0..260"}},
		{"setpoint outside line limit", "version: 1\nlines: [{preset: hdpe-110, setpoint_limits: {extruder_rpm: {min: 0, max: 50}}}]",
			[]string{"initial setpoint extruder_rpm=100 outside 0..50"}},
		{"missing map", "version: 1\nregister_map: nope.yaml\nlines: [{preset: hdpe-110}]",
			[]string{"register map:", "no such file"}},
		{"bad map", "version: 1\nlines: [{preset: hdpe-110, register_map: bad-map.yaml}]",
			[]string{`unknown command "fly"`}},
		{"unknown scenario", "version: 1\nlines: [{preset: hdpe-110, scenario: nope}]",
			[]string{`unknown built-in scenario "nope"`}},
		{"scenario beyond line limits", "version: 1\nlines: [{preset: pvc-110, scenario: cold-startup, setpoint_limits: {zone2_temp: {min: 0, max: 185}}}]",
			[]string{`scenario "cold-startup"`, "zone2_temp=190 outside this line's limits 0..185"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parse(t, tt.yaml)
			if cfg.File != "" { // relative map paths point next to the file
				_ = os.WriteFile(filepath.Join(filepath.Dir(cfg.File), "bad-map.yaml"),
					[]byte("version: 1\ncoils: [{address: 0, name: a, command: fly}]\n"), 0o600)
			}
			_, err := Build(cfg, nil)
			for _, w := range tt.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Fatalf("err = %v\nwant mention of %q", err, w)
				}
			}
		})
	}
	// Problems of several lines are reported together.
	cfg := parse(t, "version: 1\nlines: [{name: a, preset: nope}, {name: b, preset: hdpe-110, setpoints: {x: 1}}]")
	_, err := Build(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "(a)") || !strings.Contains(err.Error(), "(b)") {
		t.Fatalf("multi-line errors: %v", err)
	}
}

// A line built with a scenario plays it through its runner.
func TestBuildScenarioIsPlayed(t *testing.T) {
	cfg := parse(t, "version: 1\nsimulation: {time_scale: 1000}\nlines: [{preset: hdpe-110, scenario: pressure-test}]")
	lines, err := Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	l := lines[0]
	// Drive the runner's step path without real time: the source is consulted.
	snaps := simulation.Replay(l.Engine, l.Spec.Source, 300)
	last := snaps[len(snaps)-1]
	if last.State != simulation.Fault || last.FaultCode != simulation.FaultMeltPressureHigh {
		t.Fatalf("after pressure-test: %v %d", last.State, last.FaultCode)
	}
}
