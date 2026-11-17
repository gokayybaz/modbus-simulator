// Package mapping connects the simulation to the virtual PLC: it encodes
// snapshot values into Modbus registers and bits, and turns client writes
// into simulation commands, according to a YAML register map.
//
// Conventions (ADR-0002): zero-based PDU addresses, big-endian bytes inside a
// register, 32-bit word order ABCD by default (CDAB optional per field),
// raw = round(value × scale) with saturation.
package mapping

import (
	"fmt"
	"math"
	"strings"
)

// DataType is the register encoding of a field.
type DataType uint8

const (
	Bool DataType = iota + 1
	Uint16
	Int16
	Uint32
	Int32
	Float32
)

var typeNames = map[DataType]string{
	Bool: "bool", Uint16: "uint16", Int16: "int16",
	Uint32: "uint32", Int32: "int32", Float32: "float32",
}

func (t DataType) String() string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("DataType(%d)", uint8(t))
}

// ParseDataType parses a type name (case-insensitive).
func ParseDataType(s string) (DataType, error) {
	for t, n := range typeNames {
		if strings.EqualFold(s, n) {
			return t, nil
		}
	}
	return 0, fmt.Errorf("unknown data type %q (want bool, uint16, int16, uint32, int32 or float32)", s)
}

// Registers is the number of 16-bit registers the type occupies (bool: 1 bit).
func (t DataType) Registers() int {
	switch t {
	case Uint32, Int32, Float32:
		return 2
	default:
		return 1
	}
}

// rawRange is the representable raw integer range (float32: ±MaxFloat32).
func (t DataType) rawRange() (lo, hi float64) {
	switch t {
	case Uint16:
		return 0, math.MaxUint16
	case Int16:
		return math.MinInt16, math.MaxInt16
	case Uint32:
		return 0, math.MaxUint32
	case Int32:
		return math.MinInt32, math.MaxInt32
	case Float32:
		return -math.MaxFloat32, math.MaxFloat32
	default:
		return 0, 1
	}
}

// WordOrder is the register order of 32-bit values.
type WordOrder uint8

const (
	ABCD WordOrder = iota // high word at the lower address (default)
	CDAB                  // low word at the lower address (word-swapped)
)

// ParseWordOrder parses "abcd" (or "") and "cdab".
func ParseWordOrder(s string) (WordOrder, error) {
	switch strings.ToLower(s) {
	case "", "abcd":
		return ABCD, nil
	case "cdab":
		return CDAB, nil
	}
	return 0, fmt.Errorf("unknown word order %q (want abcd or cdab)", s)
}

func (o WordOrder) String() string {
	if o == CDAB {
		return "cdab"
	}
	return "abcd"
}

// Encode converts an engineering value to registers. clamped reports that
// the scaled value did not fit the type and was saturated (NaN encodes as 0).
func Encode(value, scale float64, t DataType, o WordOrder) (regs []uint16, clamped bool) {
	scaled := value * scale
	var bits uint32
	switch t {
	case Float32:
		if math.IsNaN(scaled) {
			scaled, clamped = 0, true
		}
		lo, hi := t.rawRange()
		if scaled < lo || scaled > hi {
			scaled, clamped = max(lo, min(hi, scaled)), true
		}
		bits = math.Float32bits(float32(scaled))
	default:
		r := math.Round(scaled)
		if math.IsNaN(r) {
			r, clamped = 0, true
		}
		lo, hi := t.rawRange()
		if r < lo || r > hi {
			r, clamped = max(lo, min(hi, r)), true
		}
		if r < 0 {
			bits = uint32(int32(r)) // two's complement
		} else {
			bits = uint32(r)
		}
	}
	if t.Registers() == 1 {
		return []uint16{uint16(bits)}, clamped
	}
	hiW, loW := uint16(bits>>16), uint16(bits)
	if o == CDAB {
		return []uint16{loW, hiW}, clamped
	}
	return []uint16{hiW, loW}, clamped
}

// Decode converts registers back to an engineering value (raw / scale).
func Decode(regs []uint16, scale float64, t DataType, o WordOrder) float64 {
	var raw float64
	switch t {
	case Uint16:
		raw = float64(regs[0])
	case Int16:
		raw = float64(int16(regs[0]))
	default:
		hiW, loW := regs[0], regs[1]
		if o == CDAB {
			hiW, loW = loW, hiW
		}
		bits := uint32(hiW)<<16 | uint32(loW)
		switch t {
		case Uint32:
			raw = float64(bits)
		case Int32:
			raw = float64(int32(bits))
		case Float32:
			raw = float64(math.Float32frombits(bits))
		}
	}
	return raw / scale
}
