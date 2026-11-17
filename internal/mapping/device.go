package mapping

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"modbus-simulator/internal/modbus"
	"modbus-simulator/internal/plc"
	"modbus-simulator/internal/simulation"
)

// CommandSink accepts simulation commands (implemented by *simulation.Runner).
type CommandSink interface {
	Submit(simulation.Command) error
}

// SetpointLimiter reports a line's own setpoint limits (implemented by
// *simulation.Engine). Holding register writes must satisfy both the map's
// range and these limits.
type SetpointLimiter interface {
	SetpointLimits(id simulation.SetpointID) (lo, hi float64, ok bool)
}

// Device is the virtual PLC of one line as seen from Modbus. It implements
// modbus.Handler:
//
//   - reads are served from PLC memory (published every tick);
//   - holding register writes are validated against the map (whole fields
//     only, engineering limits), turned into SetSetpoint commands and stored;
//   - coil writes become Start/Stop/ResetFault (momentary, self-clearing) or
//     EmergencyStop (maintained) commands (ADR-0003).
//
// Publish is called from the simulation runner after every tick.
type Device struct {
	m    *Map
	mem  *plc.Memory
	sink CommandSink
	log  *slog.Logger

	mu      sync.Mutex                      // serialises writes and publishing
	pending map[simulation.CommandKind]bool // momentary commands awaiting the engine
	clamped map[string]bool                 // fields currently saturated (log on change)
	ranges  map[*HoldingField][2]float64    // effective writable range per field
	last    *simulation.Snapshot            // previous published snapshot (register sync)
}

var _ modbus.Handler = (*Device)(nil)

// NewDevice binds a map to PLC memory and a command sink. The memory layout
// must match m.Layout(). limits (may be nil) narrows setpoint writes to the
// line's own limits; a map range that does not overlap them is an error.
func NewDevice(m *Map, mem *plc.Memory, sink CommandSink, limits SetpointLimiter, log *slog.Logger) (*Device, error) {
	if mem.Layout() != m.Layout() {
		return nil, fmt.Errorf("mapping: memory layout %+v does not match map layout %+v", mem.Layout(), m.Layout())
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	d := &Device{m: m, mem: mem, sink: sink, log: log,
		pending: map[simulation.CommandKind]bool{}, clamped: map[string]bool{}, ranges: map[*HoldingField][2]float64{}}
	for i := range m.Holdings {
		h := &m.Holdings[i]
		lo, hi := h.Min, h.Max
		if limits != nil && !h.Reserved && !h.Scenario {
			if llo, lhi, ok := limits.SetpointLimits(h.Setpoint); ok {
				lo, hi = max(lo, llo), min(hi, lhi)
			}
		}
		if lo > hi {
			return nil, fmt.Errorf("mapping: %s range %g..%g does not overlap the line's %s limits", h.Name, h.Min, h.Max, h.Setpoint)
		}
		d.ranges[h] = [2]float64{lo, hi}
	}
	return d, nil
}

// Init fills holding registers from the engine's setpoints and publishes the
// initial snapshot. Call it once before serving clients.
func (d *Device) Init(s simulation.Snapshot) error {
	img := make([]uint16, d.m.layout.HoldingRegisters)
	for i, h := range d.m.Holdings {
		if h.Reserved {
			continue
		}
		v := holdingValue(h, s)
		r := d.ranges[&d.m.Holdings[i]]
		regs, clamped := Encode(v, h.Scale, h.Type, h.Order)
		if clamped || v < r[0] || v > r[1] {
			return fmt.Errorf("mapping: initial setpoint %s=%g outside the writable range %g..%g", h.Name, v, r[0], r[1])
		}
		copy(img[h.Address:], regs)
	}
	if err := d.mem.SetHoldingRegisters(0, img); err != nil {
		return err
	}
	d.Publish(s, nil)
	return nil
}

func holdingValue(h HoldingField, s simulation.Snapshot) float64 {
	if h.Scenario {
		return float64(s.Scenario)
	}
	v, _ := s.Setpoints.Get(h.Setpoint)
	return v
}

// syncWritables reflects changes made by other sources than Modbus (a
// scenario, the engine) into holding registers and the emergency stop coil.
// Only values that changed since the previous snapshot are written, so a
// client write that the engine has not applied yet is never overwritten.
func (d *Device) syncWritables(s simulation.Snapshot) {
	prev := d.last
	if prev == nil {
		return
	}
	for i := range d.m.Holdings {
		h := d.m.Holdings[i]
		if h.Reserved {
			continue
		}
		v := holdingValue(h, s)
		if v == holdingValue(h, *prev) {
			continue
		}
		regs, _ := Encode(v, h.Scale, h.Type, h.Order)
		if err := d.mem.SetHoldingRegisters(h.Address, regs); err != nil {
			d.log.Error("holding register sync failed", "field", h.Name, "err", err)
		}
	}
	if s.EmergencyStop != prev.EmergencyStop {
		if addr, ok := d.m.coilFor[simulation.CmdEmergencyStop]; ok {
			if err := d.mem.SetCoils(addr, []bool{s.EmergencyStop}); err != nil {
				d.log.Error("coil sync failed", "err", err)
			}
		}
	}
}

// Publish encodes the snapshot into the input register and discrete input
// images and acknowledges momentary command coils whose command the engine
// applied in this tick. Everything is written to PLC memory atomically.
func (d *Device) Publish(s simulation.Snapshot, applied []simulation.Command) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ir := make([]uint16, d.m.layout.InputRegisters)
	for _, f := range d.m.Inputs {
		v := f.get(s)
		regs, clamped := Encode(v, f.Scale, f.Type, f.Order)
		copy(ir[f.Address:], regs)
		if clamped != d.clamped[f.Name] {
			d.clamped[f.Name] = clamped
			if clamped {
				d.log.Warn("value saturated in register", "field", f.Name, "value", v, "type", f.Type, "scale", f.Scale)
			} else {
				d.log.Info("value back in register range", "field", f.Name, "value", v)
			}
		}
	}
	di := make([]bool, d.m.layout.DiscreteInputs)
	for _, f := range d.m.Discretes {
		di[f.Address] = f.get(s)
	}
	var clear []uint16
	for _, c := range applied {
		if d.pending[c.Kind] {
			delete(d.pending, c.Kind)
			clear = append(clear, d.m.coilFor[c.Kind])
		}
	}
	if err := d.mem.Publish(ir, di, clear); err != nil {
		// Sizes are fixed by the map, so this indicates a programming error.
		d.log.Error("publish failed", "err", err)
	}
	d.syncWritables(s)
	d.last = &s
}

// ReadCoils implements modbus.Handler.
func (d *Device) ReadCoils(unit uint8, addr, qty uint16) ([]bool, error) {
	return d.mem.ReadCoils(unit, addr, qty)
}

// ReadDiscreteInputs implements modbus.Handler.
func (d *Device) ReadDiscreteInputs(unit uint8, addr, qty uint16) ([]bool, error) {
	return d.mem.ReadDiscreteInputs(unit, addr, qty)
}

// ReadHoldingRegisters implements modbus.Handler.
func (d *Device) ReadHoldingRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	return d.mem.ReadHoldingRegisters(unit, addr, qty)
}

// ReadInputRegisters implements modbus.Handler.
func (d *Device) ReadInputRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	return d.mem.ReadInputRegisters(unit, addr, qty)
}

type holdingWrite struct {
	f     *HoldingField
	value float64
}

// WriteHoldingRegisters implements modbus.Handler. Every written address must
// belong to a mapped field (02 otherwise) and every field must be written
// completely, e.g. both words of a UINT32 in one FC16 request (03 otherwise).
// Values outside the field's engineering range are rejected with 03. The
// request is all-or-nothing.
func (d *Device) WriteHoldingRegisters(unit uint8, addr uint16, values []uint16) error {
	if err := d.mem.CheckUnit(unit); err != nil {
		return err
	}
	start, end := int(addr), int(addr)+len(values)
	var writes []holdingWrite
	for a := start; a < end; {
		f := d.m.holdingAt[uint16(a)]
		if f == nil {
			return fmt.Errorf("holding register %d is not writable: %w", a, modbus.IllegalDataAddress)
		}
		fs, fe := int(f.Address), int(f.Address)+f.Width()
		if fs < start || fe > end {
			return fmt.Errorf("%s spans registers %d..%d; write all of them in one request: %w",
				f.Name, fs, fe-1, modbus.IllegalDataValue)
		}
		v := Decode(values[fs-start:fe-start], f.Scale, f.Type, f.Order)
		r := d.ranges[f]
		if !(v >= r[0] && v <= r[1]) { // also rejects NaN
			return fmt.Errorf("%s=%g outside %g..%g: %w", f.Name, v, r[0], r[1], modbus.IllegalDataValue)
		}
		if f.Scenario && v != float64(int(v)) {
			return fmt.Errorf("%s=%g is not a scenario number: %w", f.Name, v, modbus.IllegalDataValue)
		}
		writes = append(writes, holdingWrite{f, v})
		a = fe
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, w := range writes {
		switch {
		case w.f.Reserved:
			continue
		case w.f.Scenario:
			if err := d.submit(simulation.SetScenario(simulation.Scenario(w.value))); err != nil {
				return err
			}
			d.log.Info("fault scenario selected", "scenario", simulation.Scenario(w.value).String())
		default:
			if err := d.submit(simulation.SetSetpoint(w.f.Setpoint, w.value)); err != nil {
				return err
			}
			d.log.Debug("setpoint written", "field", w.f.Name, "value", w.value)
		}
	}
	return d.mem.WriteHoldingRegisters(unit, addr, values)
}

// WriteCoils implements modbus.Handler (ADR-0003):
//   - start/stop/reset_fault: writing 1 queues the command once; the coil
//     reads 1 until the engine has applied it, then returns to 0. Writing 0
//     is accepted and has no effect.
//   - emergency_stop: maintained; the coil holds the written level.
func (d *Device) WriteCoils(unit uint8, addr uint16, values []bool) error {
	if err := d.mem.CheckUnit(unit); err != nil {
		return err
	}
	fields := make([]*CoilField, len(values))
	for i := range values {
		f := d.m.coilAt[addr+uint16(i)]
		if f == nil {
			return fmt.Errorf("coil %d is not mapped: %w", int(addr)+i, modbus.IllegalDataAddress)
		}
		fields[i] = f
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	store := make([]bool, len(values))
	for i, f := range fields {
		switch f.kind {
		case coilMomentary:
			if values[i] && !d.pending[f.cmdKind] {
				if err := d.submit(simulation.Command{Kind: f.cmdKind}); err != nil {
					return err
				}
				d.pending[f.cmdKind] = true
				d.log.Debug("command coil set", "coil", f.Name)
			}
			store[i] = d.pending[f.cmdKind]
		case coilLevel:
			if err := d.submit(simulation.EmergencyStop(values[i])); err != nil {
				return err
			}
			store[i] = values[i]
		}
	}
	return d.mem.WriteCoils(unit, addr, store)
}

func (d *Device) submit(c simulation.Command) error {
	if err := d.sink.Submit(c); err != nil {
		if errors.Is(err, simulation.ErrQueueFull) {
			return fmt.Errorf("%s: %v: %w", c, err, modbus.ServerDeviceBusy)
		}
		return fmt.Errorf("%s: %w", c, err)
	}
	return nil
}
