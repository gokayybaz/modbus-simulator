package mapping

import (
	"math"
	"slices"
	"testing"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		scale   float64
		typ     DataType
		order   WordOrder
		want    []uint16
		clamped bool
	}{
		{"uint16 scaled", 45.5, 10, Uint16, ABCD, []uint16{455}, false},
		{"uint16 rounds half away", 45.55, 10, Uint16, ABCD, []uint16{456}, false},
		{"uint16 rounds down", 45.54, 10, Uint16, ABCD, []uint16{455}, false},
		{"uint16 negative clamps", -1, 10, Uint16, ABCD, []uint16{0}, true},
		{"uint16 overflow clamps", 7000, 10, Uint16, ABCD, []uint16{65535}, true},
		{"uint16 NaN", math.NaN(), 10, Uint16, ABCD, []uint16{0}, true},
		{"int16 negative", -12.5, 10, Int16, ABCD, []uint16{0xFF83}, false},
		{"int16 min clamps", -4000, 10, Int16, ABCD, []uint16{0x8000}, true},
		{"int16 max clamps", 4000, 10, Int16, ABCD, []uint16{0x7FFF}, true},
		{"uint32 abcd", 12345.6, 10, Uint32, ABCD, []uint16{0x0001, 0xE240}, false},
		{"uint32 cdab", 12345.6, 10, Uint32, CDAB, []uint16{0xE240, 0x0001}, false},
		{"uint32 overflow", 1e12, 1, Uint32, ABCD, []uint16{0xFFFF, 0xFFFF}, true},
		{"int32 negative abcd", -2, 1, Int32, ABCD, []uint16{0xFFFF, 0xFFFE}, false},
		{"int32 negative cdab", -2, 1, Int32, CDAB, []uint16{0xFFFE, 0xFFFF}, false},
		{"float32 abcd", 3.14159, 1, Float32, ABCD, []uint16{0x4049, 0x0FD0}, false},
		{"float32 cdab", 3.14159, 1, Float32, CDAB, []uint16{0x0FD0, 0x4049}, false},
		{"float32 scaled", 1.5, 2, Float32, ABCD, []uint16{0x4040, 0x0000}, false},
		{"float32 NaN", math.NaN(), 1, Float32, ABCD, []uint16{0, 0}, true},
		{"float32 overflow", 1e39, 1, Float32, ABCD, []uint16{0x7F7F, 0xFFFF}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, clamped := Encode(tt.value, tt.scale, tt.typ, tt.order)
			if !slices.Equal(got, tt.want) || clamped != tt.clamped {
				t.Fatalf("got %04X clamped=%v, want %04X clamped=%v", got, clamped, tt.want, tt.clamped)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		v     float64
		scale float64
		typ   DataType
	}{
		{45.5, 10, Uint16}, {-12.5, 10, Int16}, {12345.6, 10, Uint32},
		{-123456.7, 10, Int32}, {3.25, 1, Float32}, {1e7, 10, Uint32},
	}
	for _, c := range cases {
		for _, o := range []WordOrder{ABCD, CDAB} {
			regs, clamped := Encode(c.v, c.scale, c.typ, o)
			if clamped || len(regs) != c.typ.Registers() {
				t.Fatalf("%v %v: %v clamped=%v", c, o, regs, clamped)
			}
			if got := Decode(regs, c.scale, c.typ, o); math.Abs(got-c.v) > 1e-6*math.Max(1, math.Abs(c.v)) {
				t.Fatalf("%v %v: decoded %v", c, o, got)
			}
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]DataType{"bool": Bool, "UINT16": Uint16, "Int32": Int32, "float32": Float32} {
		if got, err := ParseDataType(in); err != nil || got != want {
			t.Fatalf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseDataType("double"); err == nil {
		t.Fatal("double accepted")
	}
	for in, want := range map[string]WordOrder{"": ABCD, "ABCD": ABCD, "cdab": CDAB} {
		if got, err := ParseWordOrder(in); err != nil || got != want {
			t.Fatalf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseWordOrder("badc"); err == nil {
		t.Fatal("badc accepted")
	}
	if Float32.String() != "float32" || DataType(99).String() != "DataType(99)" || CDAB.String() != "cdab" || ABCD.String() != "abcd" {
		t.Fatal("stringers")
	}
}
