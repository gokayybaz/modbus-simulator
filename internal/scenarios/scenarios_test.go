package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"modbus-simulator/internal/simulation"
)

func TestParseValidation(t *testing.T) {
	const head = "scenario:\n  name: x\n  events:\n"
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"empty", "", "empty document"},
		{"unknown key", head + "    - {at_seconds: 1, action: start_machine, colour: red}", "field colour not found"},
		{"no name", "scenario:\n  events: [{at_seconds: 1, action: start_machine}]", "scenario.name is required"},
		{"no events", "scenario:\n  name: x\n", "at least one event"},
		{"no time", head + "    - {action: start_machine}", "at_seconds is required"},
		{"negative time", head + "    - {at_seconds: -1, action: start_machine}", "must be in 0..1e7"},
		{"no action", head + "    - {at_seconds: 1}", "action is required"},
		{"unknown action", head + "    - {at_seconds: 1, action: fly}", "unknown action; supported:"},
		{"missing value", head + "    - {at_seconds: 1, action: set_extruder_rpm}", "value is required"},
		{"value out of range", head + "    - {at_seconds: 1, action: set_extruder_rpm, value: 400}", "outside extruder_rpm limits 0..150"},
		{"extra parameter", head + "    - {at_seconds: 1, action: start_machine, value: 3}", "value is not valid for this action"},
		{"bad zone", head + "    - {at_seconds: 1, action: set_zone_temp, zone: '4', value: 100}", `zone "4" must be`},
		{"unknown setpoint", head + "    - {at_seconds: 1, action: set_setpoint, setpoint: speed, value: 1}", `unknown setpoint "speed"`},
		{"unknown fault", head + "    - {at_seconds: 1, action: inject_fault, fault: fire}", `unknown fault "fire"`},
		{"unknown cause", head + "    - {at_seconds: 1, action: inject_cause, cause: none}", `unknown cause "none"`},
		{"note without message", head + "    - {at_seconds: 1, action: note}", "message is required"},
		{"type error", head + "    - {at_seconds: soon, action: start_machine}", "cannot unmarshal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant mention of %q", err, tt.want)
			}
		})
	}
	// All problems at once, with their position.
	_, err := Parse([]byte(head + "    - {at_seconds: 1, action: fly}\n    - {at_seconds: 2, action: set_extruder_rpm}"))
	if err == nil || !strings.Contains(err.Error(), "events[0] (fly)") || !strings.Contains(err.Error(), "events[1] (set_extruder_rpm)") {
		t.Fatalf("multi-error: %v", err)
	}
}

func TestEveryActionCompiles(t *testing.T) {
	s, err := Parse([]byte(`scenario:
  name: all
  events:
    - {at_seconds: 9, action: note, message: last}
    - {at_seconds: 0, action: start_machine}
    - {at_seconds: 1, action: stop_machine}
    - {at_seconds: 1, action: reset_fault}
    - {at_seconds: 2, action: emergency_stop}
    - {at_seconds: 2, action: release_emergency_stop}
    - {at_seconds: 3, action: set_setpoint, setpoint: die_temp, value: 200}
    - {at_seconds: 3, action: set_extruder_rpm, value: 90}
    - {at_seconds: 3, action: set_haul_off_speed, value: 2}
    - {at_seconds: 3, action: set_zone_temp, zone: die, value: 199}
    - {at_seconds: 3, action: set_production_target, value: 50}
    - {at_seconds: 4, action: inject_fault, fault: vacuum_low}
    - {at_seconds: 5, action: inject_cause, cause: vacuum_leak}
    - {at_seconds: 6, action: clear_cause}
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []simulation.Command
	for _, e := range s.Events {
		if e.Command != nil {
			got = append(got, *e.Command)
		}
	}
	want := []simulation.Command{
		simulation.Start(), simulation.Stop(), simulation.ResetFault(), simulation.EmergencyStop(true),
		simulation.EmergencyStop(false), simulation.SetSetpoint(simulation.SPDieTemp, 200),
		simulation.SetSetpoint(simulation.SPExtruderRPM, 90), simulation.SetSetpoint(simulation.SPHaulOffSpeed, 2),
		simulation.SetSetpoint(simulation.SPDieTemp, 199), simulation.SetSetpoint(simulation.SPProductionTarget, 50),
		simulation.TriggerFault(simulation.FaultVacuumLow), simulation.SetScenario(simulation.ScenarioVacuumLeak),
		simulation.SetScenario(simulation.ScenarioNone),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("commands\n got %v\nwant %v", got, want)
	}
	if s.Events[len(s.Events)-1].Message != "last" {
		t.Fatal("events not sorted by time")
	}
}

// Events at t are applied at the start of tick round(t/dt)+1; looping
// restarts after the last event's tick.
func TestPlayerTicks(t *testing.T) {
	s, _ := Parse([]byte(`scenario:
  name: p
  loop: true
  events:
    - {at_seconds: 0, action: start_machine}
    - {at_seconds: 1, action: stop_machine}
    - {at_seconds: 1, action: reset_fault}
`))
	p := NewPlayer(s, 500*time.Millisecond, nil)
	got := map[uint64][]simulation.Command{}
	for tick := uint64(1); tick <= 9; tick++ {
		if c := p.Commands(tick); c != nil {
			got[tick] = c
		}
	}
	want := map[uint64][]simulation.Command{
		1: {simulation.Start()}, 3: {simulation.Stop(), simulation.ResetFault()},
		4: {simulation.Start()}, 6: {simulation.Stop(), simulation.ResetFault()},
		7: {simulation.Start()}, 9: {simulation.Stop(), simulation.ResetFault()},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	once, _ := Parse([]byte("scenario:\n  name: o\n  events: [{at_seconds: 0, action: start_machine}]"))
	po := NewPlayer(once, time.Second, nil)
	if len(po.Commands(1)) != 1 || po.Commands(2) != nil || !po.Done() {
		t.Fatal("non-looping player must finish")
	}
}

type transition struct {
	state simulation.MachineState
	fault simulation.FaultCode
}

func run(t *testing.T, name string, ticks int) []simulation.Snapshot {
	t.Helper()
	s, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	e, err := simulation.New(simulation.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return simulation.Replay(e, NewPlayer(s, e.Period(), nil), ticks)
}

func transitions(snaps []simulation.Snapshot) []transition {
	out := []transition{}
	prev := simulation.Stopped
	for _, s := range snaps {
		if s.State != prev {
			out = append(out, transition{s.State, s.FaultCode})
			prev = s.State
		}
	}
	return out
}

// Every built-in scenario does what it says on the reference line (1 h).
func TestBuiltinScenarios(t *testing.T) {
	H, R, F, S := simulation.Heating, simulation.Running, simulation.Fault, simulation.Stopped
	restart := []transition{{S, 0}, {H, 0}, {R, 0}}
	tests := map[string][]transition{
		"normal-production": {{H, 0}, {R, 0}},
		"cold-startup":      {{H, 0}, {R, 0}},
		"heating-delay":     {{H, 0}, {R, 0}},
		"pressure-test":     {{H, 0}, {F, simulation.FaultMeltPressureHigh}},
		"pressure-increase": append([]transition{{H, 0}, {R, 0}, {F, simulation.FaultMeltPressureHigh}}, restart...),
		"motor-overload":    append([]transition{{H, 0}, {R, 0}, {F, simulation.FaultMotorOverload}}, restart...),
		"cooling-failure":   append([]transition{{H, 0}, {R, 0}, {F, simulation.FaultCoolingWaterHigh}}, restart...),
		"emergency-stop":    append([]transition{{H, 0}, {R, 0}, {F, simulation.FaultEmergencyStop}}, restart...),
		"stop-restart":      {{H, 0}, {R, 0}, {S, 0}, {H, 0}, {R, 0}},
	}
	if names := Builtin(); len(names) != len(tests) {
		t.Fatalf("built-in scenarios %v, tested %d", names, len(tests))
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			snaps := run(t, name, 7200)
			if got := transitions(snaps); !slices.Equal(got, want) {
				t.Fatalf("transitions\n got %v\nwant %v", got, want)
			}
		})
	}

	// Scenario-specific checks.
	snaps := run(t, "cold-startup", 7200)
	for _, s := range snaps {
		if s.State == R && s.SimTime < 20*time.Minute {
			t.Fatalf("cold-startup: screw started during the 120 °C soak at %v", s.SimTime)
		}
	}
	snaps = run(t, "heating-delay", 7200)
	for _, s := range snaps {
		if s.State == R && s.SimTime < 30*time.Minute {
			t.Fatalf("heating-delay: RUNNING at %v despite the failed heater", s.SimTime)
		}
	}
	snaps = run(t, "normal-production", 7200)
	if !snaps[len(snaps)-1].TargetReached {
		t.Fatal("normal-production: production target not reached")
	}
	snaps = run(t, "emergency-stop", 3600)
	if s := snaps[1530*2+1]; s.State != F { // reset at 1530 s refused (E-Stop still pressed)
		t.Fatalf("emergency-stop: state %v right after the refused reset", s.State)
	}
}

// Re-running a scenario reproduces it exactly; another seed changes only noise.
func TestScenarioRerunIsDeterministic(t *testing.T) {
	a, b := run(t, "cooling-failure", 7200), run(t, "cooling-failure", 7200)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs between runs", i+1)
		}
	}
}

func TestLoad(t *testing.T) {
	if _, err := Load("no-such-scenario"); err == nil || !strings.Contains(err.Error(), "available: cold-startup") {
		t.Fatalf("unknown built-in: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mine.yaml")
	if err := os.WriteFile(path, []byte("scenario:\n  name: mine\n  events: [{at_seconds: 0, action: start_machine}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.Name != "mine" {
		t.Fatalf("file: %v %v", s, err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("scenario:\n  name: bad\n  events: [{at_seconds: 0, action: fly}]\n"), 0o600)
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Fatalf("bad file: %v", err)
	}
}
