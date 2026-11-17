// Package scenarios loads time-based scenario files and plays them into a
// line's simulation as commands at exact tick boundaries, so a scenario run
// is reproducible (same seed + same scenario ⇒ same data).
//
// File format (docs/scenarios.md):
//
//	scenario:
//	  name: pressure-test
//	  description: optional text
//	  loop: false            # restart after the last event
//	  events:
//	    - at_seconds: 10
//	      action: start_machine
//	    - at_seconds: 60
//	      action: set_extruder_rpm
//	      value: 80
//	    - at_seconds: 120
//	      action: inject_fault
//	      fault: melt_pressure_high
package scenarios

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"modbus-simulator/configs"
	"modbus-simulator/internal/simulation"
)

type fileSpec struct {
	Scenario struct {
		Name        string      `yaml:"name"`
		Description string      `yaml:"description"`
		Loop        bool        `yaml:"loop"`
		Events      []eventSpec `yaml:"events"`
	} `yaml:"scenario"`
}

type eventSpec struct {
	AtSeconds *float64 `yaml:"at_seconds"`
	Action    string   `yaml:"action"`
	Value     *float64 `yaml:"value"`
	Setpoint  string   `yaml:"setpoint"`
	Zone      string   `yaml:"zone"`
	Fault     string   `yaml:"fault"`
	Cause     string   `yaml:"cause"`
	Message   string   `yaml:"message"`
}

// Event is one validated scenario step.
type Event struct {
	At      time.Duration
	Action  string
	Command *simulation.Command // nil for notes
	Message string
}

// Scenario is a validated, immutable scenario.
type Scenario struct {
	Name        string
	Description string
	Loop        bool
	Events      []Event // sorted by At (stable)
}

// Actions lists the supported actions and their parameters.
var Actions = []string{
	"start_machine", "stop_machine", "reset_fault", "emergency_stop", "release_emergency_stop",
	"set_setpoint (setpoint, value)", "set_extruder_rpm (value)", "set_haul_off_speed (value)",
	"set_zone_temp (zone: 1|2|3|die, value)", "set_production_target (value, m)",
	"inject_fault (fault: fault name)", "inject_cause (cause: cause name)", "clear_cause", "note (message)",
}

// Parse reads and validates a scenario document. Unknown keys are errors.
func Parse(b []byte) (*Scenario, error) {
	var spec fileSpec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("scenario: empty document")
		}
		return nil, fmt.Errorf("scenario: %w", err)
	}
	s := &Scenario{Name: spec.Scenario.Name, Description: spec.Scenario.Description, Loop: spec.Scenario.Loop}
	var errs []error
	if s.Name == "" {
		errs = append(errs, errors.New("scenario.name is required"))
	}
	if len(spec.Scenario.Events) == 0 {
		errs = append(errs, errors.New("scenario.events must contain at least one event"))
	}
	for i, e := range spec.Scenario.Events {
		ev, err := compileEvent(e)
		if err != nil {
			errs = append(errs, fmt.Errorf("events[%d] (%s): %w", i, e.Action, err))
			continue
		}
		s.Events = append(s.Events, ev)
	}
	if err := errors.Join(errs...); err != nil {
		name := s.Name
		if name == "" {
			name = "?"
		}
		return nil, fmt.Errorf("scenario %q invalid:\n%w", name, err)
	}
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].At < s.Events[j].At })
	return s, nil
}

func compileEvent(e eventSpec) (Event, error) {
	if e.AtSeconds == nil {
		return Event{}, errors.New("at_seconds is required")
	}
	if *e.AtSeconds < 0 || math.IsNaN(*e.AtSeconds) || *e.AtSeconds > 1e7 {
		return Event{}, fmt.Errorf("at_seconds %v must be in 0..1e7", *e.AtSeconds)
	}
	ev := Event{At: time.Duration(*e.AtSeconds * float64(time.Second)), Action: e.Action}

	// Parameters each action accepts; anything else given is an error.
	used := map[string]bool{}
	value := func() (float64, error) {
		used["value"] = true
		if e.Value == nil {
			return 0, errors.New("value is required")
		}
		return *e.Value, nil
	}
	setpoint := func(id simulation.SetpointID) error {
		v, err := value()
		if err != nil {
			return err
		}
		if lo, hi, _ := id.Limits(); !(v >= lo && v <= hi) {
			return fmt.Errorf("value %g outside %s limits %g..%g", v, id, lo, hi)
		}
		c := simulation.SetSetpoint(id, v)
		ev.Command = &c
		return nil
	}
	plain := func(c simulation.Command) error { ev.Command = &c; return nil }

	var err error
	switch e.Action {
	case "start_machine":
		err = plain(simulation.Start())
	case "stop_machine":
		err = plain(simulation.Stop())
	case "reset_fault":
		err = plain(simulation.ResetFault())
	case "emergency_stop":
		err = plain(simulation.EmergencyStop(true))
	case "release_emergency_stop":
		err = plain(simulation.EmergencyStop(false))
	case "set_extruder_rpm":
		err = setpoint(simulation.SPExtruderRPM)
	case "set_haul_off_speed":
		err = setpoint(simulation.SPHaulOffSpeed)
	case "set_production_target":
		err = setpoint(simulation.SPProductionTarget)
	case "set_zone_temp":
		used["zone"] = true
		zones := map[string]simulation.SetpointID{"1": simulation.SPZone1Temp, "2": simulation.SPZone2Temp,
			"3": simulation.SPZone3Temp, "die": simulation.SPDieTemp}
		id, ok := zones[e.Zone]
		if !ok {
			err = fmt.Errorf("zone %q must be 1, 2, 3 or die", e.Zone)
			break
		}
		err = setpoint(id)
	case "set_setpoint":
		used["setpoint"] = true
		id, ok := simulation.SetpointByName(e.Setpoint)
		if !ok {
			err = fmt.Errorf("unknown setpoint %q", e.Setpoint)
			break
		}
		err = setpoint(id)
	case "inject_fault":
		used["fault"] = true
		code, ok := faultByName(e.Fault)
		if !ok {
			err = fmt.Errorf("unknown fault %q (see docs/faults/fault-table.md)", e.Fault)
			break
		}
		err = plain(simulation.TriggerFault(code))
	case "inject_cause":
		used["cause"] = true
		sc, ok := causeByName(e.Cause)
		if !ok || sc == simulation.ScenarioNone {
			err = fmt.Errorf("unknown cause %q", e.Cause)
			break
		}
		err = plain(simulation.SetScenario(sc))
	case "clear_cause":
		err = plain(simulation.SetScenario(simulation.ScenarioNone))
	case "note":
		used["message"] = true
		if e.Message == "" {
			err = errors.New("message is required")
		}
		ev.Message = e.Message
	case "":
		err = errors.New("action is required")
	default:
		err = fmt.Errorf("unknown action; supported: %s", strings.Join(Actions, ", "))
	}
	if err != nil {
		return Event{}, err
	}
	for name, set := range map[string]bool{
		"value": e.Value != nil, "setpoint": e.Setpoint != "", "zone": e.Zone != "",
		"fault": e.Fault != "", "cause": e.Cause != "", "message": e.Message != "",
	} {
		if set && !used[name] {
			return Event{}, fmt.Errorf("%s is not valid for this action", name)
		}
	}
	return ev, nil
}

func faultByName(name string) (simulation.FaultCode, bool) {
	for _, f := range simulation.FaultTable() {
		if f.Name == name {
			return f.Code, true
		}
	}
	return 0, false
}

func causeByName(name string) (simulation.Scenario, bool) {
	for s := simulation.Scenario(0); s <= simulation.MaxScenario; s++ {
		if s.String() == name {
			return s, true
		}
	}
	return 0, false
}

// Builtin returns the names of the embedded scenarios.
func Builtin() []string {
	entries, _ := configs.Scenarios.ReadDir("scenarios")
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
			names = append(names, n)
		}
	}
	return names
}

// Load reads a scenario from a file path, or an embedded scenario by name
// when ref has no path separator and no ".yaml" suffix.
func Load(ref string) (*Scenario, error) {
	var b []byte
	var err error
	if !strings.ContainsAny(ref, `/\`) && !strings.HasSuffix(ref, ".yaml") && !strings.HasSuffix(ref, ".yml") {
		b, err = configs.Scenarios.ReadFile("scenarios/" + ref + ".yaml")
		if err != nil {
			return nil, fmt.Errorf("unknown built-in scenario %q (available: %s)", ref, strings.Join(Builtin(), ", "))
		}
	} else if b, err = os.ReadFile(ref); err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return s, nil
}

// Player feeds a scenario into a runner (simulation.CommandSource). It is
// used only from the runner goroutine.
type Player struct {
	s      *Scenario
	period time.Duration
	log    *slog.Logger
	ticks  []uint64 // tick (relative to a pass) of each event
	length uint64   // ticks per pass (loop length)
	offset uint64   // tick at which the current pass started (pass tick 1 = offset + 1)
	next   int
	done   bool
}

// NewPlayer prepares s for a line stepping with period. Events at time t are
// applied at the start of the step that begins at t (tick round(t/period)+1).
func NewPlayer(s *Scenario, period time.Duration, log *slog.Logger) *Player {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	p := &Player{s: s, period: period, log: log.With("scenario", s.Name)}
	for _, e := range s.Events {
		p.ticks = append(p.ticks, uint64(math.Round(float64(e.At)/float64(period)))+1)
	}
	p.length = p.ticks[len(p.ticks)-1]
	return p
}

// Commands implements simulation.CommandSource.
func (p *Player) Commands(tick uint64) []simulation.Command {
	if p.done {
		return nil
	}
	var out []simulation.Command
	for {
		if p.next == len(p.ticks) {
			if !p.s.Loop {
				p.done = true
				p.log.Info("scenario finished", "tick", tick)
				return out
			}
			p.offset += p.length
			p.next = 0
			p.log.Info("scenario restarted", "tick", tick)
		}
		if p.offset+p.ticks[p.next] > tick {
			return out
		}
		e := p.s.Events[p.next]
		p.next++
		if e.Command != nil {
			out = append(out, *e.Command)
		}
		p.log.Info("scenario event", "at", e.At, "tick", tick, "action", e.Action, "message", e.Message)
	}
}

// Done reports whether a non-looping scenario has played all events.
func (p *Player) Done() bool { return p.done }
