// Package plant assembles production lines: each Line is a fully isolated
// stack (simulation engine, real-time runner, PLC memory, register-map
// device), and a Router exposes several lines behind one Modbus endpoint by
// unit id. See docs/spikes/008-multi-line-isolation.md.
package plant

import (
	"context"
	"fmt"
	"log/slog"

	"modbus-simulator/internal/mapping"
	"modbus-simulator/internal/modbus"
	"modbus-simulator/internal/plc"
	"modbus-simulator/internal/simulation"
)

// LineSpec describes one line.
type LineSpec struct {
	ID            int    // 1-based line number
	Preset        string // informational (e.g. "pvc-110")
	UnitID        uint8
	AcceptAnyUnit bool   // only meaningful when the line has its own endpoint
	Addr          string // listen address of the line's endpoint
	Sim           simulation.Config
	Map           *mapping.Map
	Speed         float64                  // real-time acceleration factor
	Source        simulation.CommandSource // scripted commands (scenario), may be nil
	Name          string
}

// Line is one isolated production line. Nothing in it is shared with other
// lines.
type Line struct {
	Spec     LineSpec
	Engine   *simulation.Engine
	Runner   *simulation.Runner
	Memory   *plc.Memory
	Device   *mapping.Device
	Scenario string // name of the scenario played into the line ("" = none)
	log      *slog.Logger
}

// SetSource attaches a scripted command source (scenario) before Run.
func (l *Line) SetSource(src simulation.CommandSource) {
	l.Spec.Source = src
	l.Runner = l.newRunner()
}

// NewLine builds and initialises a line; Run starts its simulation.
func NewLine(spec LineSpec, log *slog.Logger) (*Line, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("line", spec.ID)
	if spec.Name != "" {
		log = log.With("line_name", spec.Name)
	}
	engine, err := simulation.New(spec.Sim)
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", spec.ID, err)
	}
	mem, err := plc.New(plc.Config{UnitID: spec.UnitID, AcceptAnyUnit: spec.AcceptAnyUnit, Layout: spec.Map.Layout()})
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", spec.ID, err)
	}
	l := &Line{Spec: spec, Engine: engine, Memory: mem, log: log}
	l.Runner = l.newRunner()
	// The device submits client commands through l (not a fixed runner) so a
	// runner replaced by SetSource keeps receiving them.
	if l.Device, err = mapping.NewDevice(spec.Map, mem, l, engine, log.With("component", "mapping")); err != nil {
		return nil, fmt.Errorf("line %d: %w", spec.ID, err)
	}
	if err := l.Device.Init(engine.Snapshot()); err != nil {
		return nil, fmt.Errorf("line %d: %w", spec.ID, err)
	}
	return l, nil
}

func (l *Line) newRunner() *simulation.Runner {
	return simulation.NewRunner(l.Engine, simulation.RunnerOptions{
		Logger: l.log.With("component", "simulation"),
		Speed:  l.Spec.Speed,
		Source: l.Spec.Source,
		OnTick: func(s simulation.Snapshot, applied []simulation.Command) { l.Device.Publish(s, applied) },
	})
}

// Submit forwards a client command to the line's runner (mapping.CommandSink).
func (l *Line) Submit(c simulation.Command) error { return l.Runner.Submit(c) }

// Run steps the line's simulation in real time until ctx is cancelled.
func (l *Line) Run(ctx context.Context) error { return l.Runner.Run(ctx) }

// Router dispatches Modbus requests to lines by unit id. It is configured
// before serving and read-only afterwards, so it needs no locking.
type Router struct {
	units map[uint8]modbus.Handler
	any   modbus.Handler
}

var _ modbus.Handler = (*Router)(nil)

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{units: map[uint8]modbus.Handler{}} }

// Add routes unit to h.
func (r *Router) Add(unit uint8, h modbus.Handler) error {
	if _, dup := r.units[unit]; dup {
		return fmt.Errorf("unit id %d is already assigned", unit)
	}
	if r.any != nil {
		return fmt.Errorf("endpoint already answers every unit id")
	}
	r.units[unit] = h
	return nil
}

// SetDefault makes h answer every unit id (single line on its own endpoint
// with accept-any-unit). It cannot be combined with Add.
func (r *Router) SetDefault(h modbus.Handler) error {
	if len(r.units) > 0 || r.any != nil {
		return fmt.Errorf("accept-any-unit needs an endpoint with exactly one line")
	}
	r.any = h
	return nil
}

// Units returns the number of routed unit ids (0 with a default handler).
func (r *Router) Units() int { return len(r.units) }

func (r *Router) pick(unit uint8) (modbus.Handler, error) {
	if r.any != nil {
		return r.any, nil
	}
	if h, ok := r.units[unit]; ok {
		return h, nil
	}
	return nil, fmt.Errorf("no line with unit id %d on this endpoint: %w", unit, modbus.GatewayTargetFailed)
}

func (r *Router) ReadCoils(unit uint8, addr, qty uint16) ([]bool, error) {
	h, err := r.pick(unit)
	if err != nil {
		return nil, err
	}
	return h.ReadCoils(unit, addr, qty)
}

func (r *Router) ReadDiscreteInputs(unit uint8, addr, qty uint16) ([]bool, error) {
	h, err := r.pick(unit)
	if err != nil {
		return nil, err
	}
	return h.ReadDiscreteInputs(unit, addr, qty)
}

func (r *Router) ReadHoldingRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	h, err := r.pick(unit)
	if err != nil {
		return nil, err
	}
	return h.ReadHoldingRegisters(unit, addr, qty)
}

func (r *Router) ReadInputRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	h, err := r.pick(unit)
	if err != nil {
		return nil, err
	}
	return h.ReadInputRegisters(unit, addr, qty)
}

func (r *Router) WriteCoils(unit uint8, addr uint16, values []bool) error {
	h, err := r.pick(unit)
	if err != nil {
		return err
	}
	return h.WriteCoils(unit, addr, values)
}

func (r *Router) WriteHoldingRegisters(unit uint8, addr uint16, values []uint16) error {
	h, err := r.pick(unit)
	if err != nil {
		return err
	}
	return h.WriteHoldingRegisters(unit, addr, values)
}
