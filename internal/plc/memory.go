// Package plc implements the Virtual PLC Memory: the four Modbus data areas
// of one device, guarded by a single lock so that every request sees a
// consistent image. It implements modbus.Handler.
package plc

import (
	"fmt"
	"sync"

	"modbus-simulator/internal/modbus"
)

// Layout sets the size of each data area. Valid addresses in an area are
// 0..size-1; anything outside yields IllegalDataAddress.
type Layout struct {
	Coils            int
	DiscreteInputs   int
	HoldingRegisters int
	InputRegisters   int
}

// Range is an inclusive raw-value limit for a holding register.
type Range struct{ Min, Max uint16 }

// Config configures a Memory.
type Config struct {
	// UnitID is the Modbus unit (slave) id this memory answers to.
	UnitID uint8
	// AcceptAnyUnit makes the memory answer every unit id (some clients
	// send 0 or 255 to directly connected TCP devices).
	AcceptAnyUnit bool
	Layout        Layout
	// HoldingLimits restricts writable values per holding register address.
	// Writes outside the range are rejected with IllegalDataValue.
	HoldingLimits map[uint16]Range
}

// Memory is safe for concurrent use.
type Memory struct {
	unitID    uint8
	anyUnit   bool
	limits    map[uint16]Range
	mu        sync.RWMutex
	coils     []bool
	discretes []bool
	holding   []uint16
	input     []uint16
}

var _ modbus.Handler = (*Memory)(nil)

// New validates cfg and returns a zero-initialised Memory.
func New(cfg Config) (*Memory, error) {
	l := cfg.Layout
	for name, n := range map[string]int{
		"coils": l.Coils, "discrete inputs": l.DiscreteInputs,
		"holding registers": l.HoldingRegisters, "input registers": l.InputRegisters,
	} {
		if n < 0 || n > 0x10000 {
			return nil, fmt.Errorf("plc: %s size %d out of range 0..65536", name, n)
		}
	}
	limits := make(map[uint16]Range, len(cfg.HoldingLimits))
	for addr, r := range cfg.HoldingLimits {
		if int(addr) >= l.HoldingRegisters {
			return nil, fmt.Errorf("plc: limit for holding register %d outside layout (size %d)", addr, l.HoldingRegisters)
		}
		if r.Min > r.Max {
			return nil, fmt.Errorf("plc: limit for holding register %d has min %d > max %d", addr, r.Min, r.Max)
		}
		limits[addr] = r
	}
	return &Memory{
		unitID:    cfg.UnitID,
		anyUnit:   cfg.AcceptAnyUnit,
		limits:    limits,
		coils:     make([]bool, l.Coils),
		discretes: make([]bool, l.DiscreteInputs),
		holding:   make([]uint16, l.HoldingRegisters),
		input:     make([]uint16, l.InputRegisters),
	}, nil
}

// CheckUnit reports whether this memory answers requests for unit; the error
// wraps modbus.GatewayTargetFailed.
func (m *Memory) CheckUnit(unit uint8) error { return m.checkUnit(unit) }

// Layout returns the configured area sizes.
func (m *Memory) Layout() Layout {
	return Layout{Coils: len(m.coils), DiscreteInputs: len(m.discretes),
		HoldingRegisters: len(m.holding), InputRegisters: len(m.input)}
}

func (m *Memory) checkUnit(unit uint8) error {
	if m.anyUnit || unit == m.unitID {
		return nil
	}
	return fmt.Errorf("unit id %d not served: %w", unit, modbus.GatewayTargetFailed)
}

func checkRange(area string, addr uint16, qty, size int) error {
	if int(addr)+qty > size {
		return fmt.Errorf("%s %d..%d outside 0..%d: %w", area, addr, int(addr)+qty-1, size-1, modbus.IllegalDataAddress)
	}
	return nil
}

func readSlice[T any](m *Memory, unit uint8, area string, s []T, addr, qty uint16) ([]T, error) {
	if err := m.checkUnit(unit); err != nil {
		return nil, err
	}
	if err := checkRange(area, addr, int(qty), len(s)); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]T(nil), s[addr:int(addr)+int(qty)]...), nil
}

// ReadCoils implements modbus.Handler.
func (m *Memory) ReadCoils(unit uint8, addr, qty uint16) ([]bool, error) {
	return readSlice(m, unit, "coils", m.coils, addr, qty)
}

// ReadDiscreteInputs implements modbus.Handler.
func (m *Memory) ReadDiscreteInputs(unit uint8, addr, qty uint16) ([]bool, error) {
	return readSlice(m, unit, "discrete inputs", m.discretes, addr, qty)
}

// ReadHoldingRegisters implements modbus.Handler.
func (m *Memory) ReadHoldingRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	return readSlice(m, unit, "holding registers", m.holding, addr, qty)
}

// ReadInputRegisters implements modbus.Handler.
func (m *Memory) ReadInputRegisters(unit uint8, addr, qty uint16) ([]uint16, error) {
	return readSlice(m, unit, "input registers", m.input, addr, qty)
}

// WriteCoils implements modbus.Handler.
func (m *Memory) WriteCoils(unit uint8, addr uint16, values []bool) error {
	if err := m.checkUnit(unit); err != nil {
		return err
	}
	if err := checkRange("coils", addr, len(values), len(m.coils)); err != nil {
		return err
	}
	m.mu.Lock()
	copy(m.coils[addr:], values)
	m.mu.Unlock()
	return nil
}

// WriteHoldingRegisters implements modbus.Handler. All values are validated
// before any is written, so a rejected request leaves memory unchanged.
func (m *Memory) WriteHoldingRegisters(unit uint8, addr uint16, values []uint16) error {
	if err := m.checkUnit(unit); err != nil {
		return err
	}
	if err := checkRange("holding registers", addr, len(values), len(m.holding)); err != nil {
		return err
	}
	for i, v := range values {
		a := addr + uint16(i)
		if r, ok := m.limits[a]; ok && (v < r.Min || v > r.Max) {
			return fmt.Errorf("holding register %d value %d outside %d..%d: %w", a, v, r.Min, r.Max, modbus.IllegalDataValue)
		}
	}
	m.mu.Lock()
	copy(m.holding[addr:], values)
	m.mu.Unlock()
	return nil
}

// SetInputRegisters replaces a block of input registers atomically. It is
// the producer-side API (simulation / mapping); Modbus clients cannot write
// input registers.
func (m *Memory) SetInputRegisters(addr uint16, values []uint16) error {
	if err := checkRange("input registers", addr, len(values), len(m.input)); err != nil {
		return err
	}
	m.mu.Lock()
	copy(m.input[addr:], values)
	m.mu.Unlock()
	return nil
}

// SetDiscreteInputs replaces a block of discrete inputs atomically.
func (m *Memory) SetDiscreteInputs(addr uint16, values []bool) error {
	if err := checkRange("discrete inputs", addr, len(values), len(m.discretes)); err != nil {
		return err
	}
	m.mu.Lock()
	copy(m.discretes[addr:], values)
	m.mu.Unlock()
	return nil
}

// SetHoldingRegisters writes holding registers from the producer side
// (initial values), bypassing unit check and limits.
func (m *Memory) SetHoldingRegisters(addr uint16, values []uint16) error {
	if err := checkRange("holding registers", addr, len(values), len(m.holding)); err != nil {
		return err
	}
	m.mu.Lock()
	copy(m.holding[addr:], values)
	m.mu.Unlock()
	return nil
}

// Publish atomically replaces the complete input register and discrete
// input images and clears the given coils, all under one lock: a client
// request never sees values from two different simulation ticks.
func (m *Memory) Publish(inputs []uint16, discretes []bool, clearCoils []uint16) error {
	if len(inputs) != len(m.input) || len(discretes) != len(m.discretes) {
		return fmt.Errorf("plc: publish image sizes %d/%d, want %d/%d",
			len(inputs), len(discretes), len(m.input), len(m.discretes))
	}
	for _, c := range clearCoils {
		if int(c) >= len(m.coils) {
			return fmt.Errorf("plc: coil %d outside 0..%d", c, len(m.coils)-1)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.input, inputs)
	copy(m.discretes, discretes)
	for _, c := range clearCoils {
		m.coils[c] = false
	}
	return nil
}

// SetCoils writes coils from the producer side (e.g. reflecting a scripted
// emergency stop), bypassing the unit check.
func (m *Memory) SetCoils(addr uint16, values []bool) error {
	if err := checkRange("coils", addr, len(values), len(m.coils)); err != nil {
		return err
	}
	m.mu.Lock()
	copy(m.coils[addr:], values)
	m.mu.Unlock()
	return nil
}
