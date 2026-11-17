// Package main is a throwaway Phase 0 proof-of-concept (Spike 0.1 / 0.2).
//
// It is NOT the Phase 1 implementation. It exists to validate, against real
// TCP traffic, how github.com/simonvetter/modbus behaves as a Modbus TCP
// server: handler dispatch, unit-id handling, exception mapping, read-only
// enforcement and shutdown. Findings are recorded in docs/spikes/.
package main

import (
	"math"
	"sync"

	"github.com/simonvetter/modbus"
)

// Memory is a minimal fixed-size virtual PLC memory with four Modbus areas.
// All addresses are zero-based PDU addresses.
type Memory struct {
	mu        sync.RWMutex
	unitID    uint8
	coils     []bool
	discretes []bool
	holding   []uint16
	input     []uint16
}

func NewMemory(unitID uint8, nCoils, nDiscretes, nHolding, nInput int) *Memory {
	return &Memory{
		unitID:    unitID,
		coils:     make([]bool, nCoils),
		discretes: make([]bool, nDiscretes),
		holding:   make([]uint16, nHolding),
		input:     make([]uint16, nInput),
	}
}

// inRange reports whether [addr, addr+qty) fits in an area of length n.
func inRange(addr, qty uint16, n int) bool {
	return int(addr)+int(qty) <= n
}

// checkUnit returns the sentinel the library maps to exception 0x0B.
// NOTE: the library compares errors with ==, so sentinels must NOT be wrapped.
func (m *Memory) checkUnit(id uint8) error {
	if id != m.unitID {
		return modbus.ErrGWTargetFailedToRespond
	}
	return nil
}

func (m *Memory) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	if err := m.checkUnit(req.UnitId); err != nil {
		return nil, err
	}
	if !inRange(req.Addr, req.Quantity, len(m.coils)) {
		return nil, modbus.ErrIllegalDataAddress
	}
	if req.IsWrite {
		m.mu.Lock()
		copy(m.coils[req.Addr:], req.Args)
		m.mu.Unlock()
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]bool(nil), m.coils[req.Addr:req.Addr+req.Quantity]...), nil
}

func (m *Memory) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	if err := m.checkUnit(req.UnitId); err != nil {
		return nil, err
	}
	if !inRange(req.Addr, req.Quantity, len(m.discretes)) {
		return nil, modbus.ErrIllegalDataAddress
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]bool(nil), m.discretes[req.Addr:req.Addr+req.Quantity]...), nil
}

func (m *Memory) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if err := m.checkUnit(req.UnitId); err != nil {
		return nil, err
	}
	if !inRange(req.Addr, req.Quantity, len(m.holding)) {
		return nil, modbus.ErrIllegalDataAddress
	}
	if req.IsWrite {
		// PoC of value validation: register 0 accepts only 0..1000.
		for i, v := range req.Args {
			if int(req.Addr)+i == 0 && v > 1000 {
				return nil, modbus.ErrIllegalDataValue
			}
		}
		m.mu.Lock()
		copy(m.holding[req.Addr:], req.Args)
		m.mu.Unlock()
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]uint16(nil), m.holding[req.Addr:req.Addr+req.Quantity]...), nil
}

func (m *Memory) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	if err := m.checkUnit(req.UnitId); err != nil {
		return nil, err
	}
	if !inRange(req.Addr, req.Quantity, len(m.input)) {
		return nil, modbus.ErrIllegalDataAddress
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]uint16(nil), m.input[req.Addr:req.Addr+req.Quantity]...), nil
}

// SetInputs atomically replaces a block of input registers (simulates a
// "snapshot publish" from the simulation side).
func (m *Memory) SetInputs(addr int, vals []uint16) {
	m.mu.Lock()
	copy(m.input[addr:], vals)
	m.mu.Unlock()
}

func (m *Memory) SetDiscrete(addr int, v bool) {
	m.mu.Lock()
	m.discretes[addr] = v
	m.mu.Unlock()
}

// --- encoding helpers (big-endian bytes, high word first = "ABCD") ---

func U32ToRegs(v uint32) []uint16 { return []uint16{uint16(v >> 16), uint16(v)} }

func F32ToRegs(f float32) []uint16 { return U32ToRegs(math.Float32bits(f)) }

func I16ToReg(v int16) uint16 { return uint16(v) }

// Scale converts an engineering value to a scaled UINT16 with rounding and
// saturation. Returns ok=false when the value was clamped.
func ScaleU16(v, scale float64) (uint16, bool) {
	r := math.Round(v * scale)
	switch {
	case math.IsNaN(r) || r < 0:
		return 0, false
	case r > math.MaxUint16:
		return math.MaxUint16, false
	}
	return uint16(r), true
}
