package plant

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"

	"modbus-simulator/configs"
	"modbus-simulator/internal/config"
	"modbus-simulator/internal/mapping"
	"modbus-simulator/internal/scenarios"
	"modbus-simulator/internal/simulation"
)

// Build turns a resolved configuration into ready (not yet running) lines:
// preset + overrides, register map, scenario. Everything is validated here,
// before any listener or simulation starts, and all problems are reported
// together with the line they belong to.
func Build(cfg config.Config, log *slog.Logger) ([]*Line, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var lines []*Line
	var errs []error
	maps := map[string][]byte{}
	for _, lc := range cfg.Lines {
		l, err := buildLine(cfg, lc, maps, log)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s (%s): %w", lc.Source, lc.Name, err))
			continue
		}
		lines = append(lines, l)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return lines, nil
}

func buildLine(cfg config.Config, lc config.Line, maps map[string][]byte, log *slog.Logger) (*Line, error) {
	var errs []error
	fail := func(err error) { errs = append(errs, err) }

	preset, err := simulation.PresetByName(lc.Preset)
	if err != nil {
		return nil, err
	}
	sim := preset.Config()
	sim.Seed, sim.Period = lc.Seed, cfg.SimPeriod
	if lc.MinRunTemp != nil {
		sim.MinRunTemp = *lc.MinRunTemp
	}
	for section, dst := range map[string]struct {
		node *yaml.Node
		into any
	}{
		"process":      {lc.Process, &sim.Process},
		"noise":        {lc.Noise, &sim.Noise},
		"fault_limits": {lc.FaultLimits, &sim.FaultLimits},
	} {
		if err := decodeOverride(dst.node, dst.into); err != nil {
			fail(fmt.Errorf("%s: %w", section, err))
		}
	}
	for name, v := range lc.Setpoints {
		id, ok := simulation.SetpointByName(name)
		if !ok {
			fail(fmt.Errorf("setpoints: unknown setpoint %q", name))
			continue
		}
		sim.Setpoints.Assign(id, v)
	}
	for name, r := range lc.SetpointLimits {
		id, ok := simulation.SetpointByName(name)
		if !ok {
			fail(fmt.Errorf("setpoint_limits: unknown setpoint %q", name))
			continue
		}
		if sim.SetpointLimits == nil {
			sim.SetpointLimits = map[simulation.SetpointID]simulation.Range{}
		}
		sim.SetpointLimits[id] = simulation.Range{Min: r.Min, Max: r.Max}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	// Register map: each line parses its own instance.
	src, ok := maps[lc.RegisterMap]
	if !ok {
		src = configs.DefaultRegisterMap
		if lc.RegisterMap != "" {
			if src, err = os.ReadFile(lc.RegisterMap); err != nil {
				return nil, fmt.Errorf("register map: %w", err)
			}
		}
		maps[lc.RegisterMap] = src
	}
	regMap, err := mapping.Parse(src)
	if err != nil {
		name := lc.RegisterMap
		if name == "" {
			name = "embedded register map"
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	spec := LineSpec{
		ID: lc.ID, Name: lc.Name, Preset: lc.Preset, UnitID: lc.UnitID, AcceptAnyUnit: cfg.AcceptAnyUnit,
		Addr: lc.Addr, Sim: sim, Map: regMap, Speed: cfg.TimeScale,
	}
	var scenario *scenarios.Scenario
	if lc.Scenario != "" {
		if scenario, err = scenarios.Load(lc.Scenario); err != nil {
			return nil, err
		}
	}
	line, err := NewLine(spec, log)
	if err != nil {
		return nil, err
	}
	if scenario != nil {
		if err := checkScenario(scenario, line.Engine); err != nil {
			return nil, err
		}
		line.SetSource(scenarios.NewPlayer(scenario, sim.Period, log.With("line", lc.ID)))
		line.Scenario = scenario.Name
	}
	return line, nil
}

// checkScenario verifies scripted setpoints against the line's own limits
// (the scenario parser only knows the global limits).
func checkScenario(s *scenarios.Scenario, e *simulation.Engine) error {
	var errs []error
	for i, ev := range s.Events {
		if ev.Command == nil || ev.Command.Kind != simulation.CmdSetSetpoint {
			continue
		}
		lo, hi, _ := e.SetpointLimits(ev.Command.Setpoint)
		if v := ev.Command.Value; v < lo || v > hi {
			errs = append(errs, fmt.Errorf("scenario %q event %d (%s at %v): %s=%g outside this line's limits %g..%g",
				s.Name, i, ev.Action, ev.At, ev.Command.Setpoint, v, lo, hi))
		}
	}
	return errors.Join(errs...)
}

// decodeOverride decodes an optional YAML section strictly onto dst, keeping
// every field the section does not mention.
func decodeOverride(n *yaml.Node, dst any) error {
	if n == nil {
		return nil
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(dst); err != nil {
		// Line numbers refer to the re-encoded section; map them back to the
		// configuration file (the section's first key is its line 1).
		return errors.New(relLine.ReplaceAllStringFunc(err.Error(), func(m string) string {
			k, _ := strconv.Atoi(relLine.FindStringSubmatch(m)[1])
			return fmt.Sprintf("line %d:", n.Line+k-1)
		}))
	}
	return nil
}

var relLine = regexp.MustCompile(`line (\d+):`)
