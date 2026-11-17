package plc

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"modbus-simulator/internal/modbus"
)

func newMem(t *testing.T, cfg Config) *Memory {
	t.Helper()
	if cfg.Layout == (Layout{}) {
		cfg.Layout = Layout{Coils: 4, DiscreteInputs: 16, HoldingRegisters: 9, InputRegisters: 20}
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"negative size", Config{Layout: Layout{Coils: -1}}},
		{"too large", Config{Layout: Layout{InputRegisters: 0x10001}}},
		{"limit outside layout", Config{Layout: Layout{HoldingRegisters: 2}, HoldingLimits: map[uint16]Range{2: {0, 1}}}},
		{"min > max", Config{Layout: Layout{HoldingRegisters: 2}, HoldingLimits: map[uint16]Range{0: {5, 1}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if _, err := New(Config{Layout: Layout{HoldingRegisters: 0x10000}}); err != nil {
		t.Fatalf("full 65536 area: %v", err)
	}
}

func TestAddressing(t *testing.T) {
	m := newMem(t, Config{UnitID: 1})
	if err := m.SetInputRegisters(16, []uint16{0x0001, 0xE240}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		addr     uint16
		qty      uint16
		wantCode modbus.ExceptionCode // 0 = success
	}{
		{"first", 0, 1, 0},
		{"whole area", 0, 20, 0},
		{"last", 19, 1, 0},
		{"one past end", 20, 1, modbus.IllegalDataAddress},
		{"partial overlap", 18, 3, modbus.IllegalDataAddress},
		{"far", 1000, 1, modbus.IllegalDataAddress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.ReadInputRegisters(1, tt.addr, tt.qty)
			assertCode(t, err, tt.wantCode)
			if tt.wantCode == 0 && len(got) != int(tt.qty) {
				t.Fatalf("got %d values", len(got))
			}
		})
	}
	got, _ := m.ReadInputRegisters(1, 16, 2)
	if !slices.Equal(got, []uint16{0x0001, 0xE240}) {
		t.Fatalf("UINT32 words %X", got)
	}
}

func TestUnitID(t *testing.T) {
	m := newMem(t, Config{UnitID: 5})
	_, err := m.ReadHoldingRegisters(5, 0, 1)
	assertCode(t, err, 0)
	_, err = m.ReadHoldingRegisters(1, 0, 1)
	assertCode(t, err, modbus.GatewayTargetFailed)
	assertCode(t, m.WriteCoils(0, 0, []bool{true}), modbus.GatewayTargetFailed)

	anyUnit := newMem(t, Config{UnitID: 5, AcceptAnyUnit: true})
	for _, u := range []uint8{0, 1, 5, 255} {
		_, err := anyUnit.ReadCoils(u, 0, 1)
		assertCode(t, err, 0)
	}
}

func TestHoldingLimitsAreAtomic(t *testing.T) {
	m := newMem(t, Config{UnitID: 1, HoldingLimits: map[uint16]Range{1: {10, 20}}})
	assertCode(t, m.WriteHoldingRegisters(1, 1, []uint16{10}), 0)
	assertCode(t, m.WriteHoldingRegisters(1, 1, []uint16{20}), 0)
	assertCode(t, m.WriteHoldingRegisters(1, 1, []uint16{21}), modbus.IllegalDataValue)
	assertCode(t, m.WriteHoldingRegisters(1, 1, []uint16{9}), modbus.IllegalDataValue)
	// Multi-register write with one bad value changes nothing.
	assertCode(t, m.WriteHoldingRegisters(1, 0, []uint16{7, 99, 7}), modbus.IllegalDataValue)
	got, _ := m.ReadHoldingRegisters(1, 0, 3)
	if !slices.Equal(got, []uint16{0, 20, 0}) {
		t.Fatalf("memory changed by rejected write: %v", got)
	}
	// Unlimited registers accept anything.
	assertCode(t, m.WriteHoldingRegisters(1, 8, []uint16{0xFFFF}), 0)
}

func TestCoilsAndDiscretes(t *testing.T) {
	m := newMem(t, Config{UnitID: 1})
	assertCode(t, m.WriteCoils(1, 3, []bool{true}), 0)
	assertCode(t, m.WriteCoils(1, 4, []bool{true}), modbus.IllegalDataAddress)
	got, _ := m.ReadCoils(1, 0, 4)
	if !slices.Equal(got, []bool{false, false, false, true}) {
		t.Fatalf("coils %v", got)
	}
	if err := m.SetDiscreteInputs(15, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDiscreteInputs(16, []bool{true}); err == nil {
		t.Fatal("SetDiscreteInputs out of range: want error")
	}
	d, _ := m.ReadDiscreteInputs(1, 15, 1)
	if !d[0] {
		t.Fatal("discrete 15 not set")
	}
}

// Readers must never observe half of a multi-register update.
func TestConcurrentConsistency(t *testing.T) {
	m := newMem(t, Config{UnitID: 1})
	stop := make(chan struct{})
	var writer, wg sync.WaitGroup
	writer.Go(func() {
		for n := uint16(0); ; n++ {
			select {
			case <-stop:
				return
			default:
				_ = m.SetInputRegisters(16, []uint16{n, n, n, n})
			}
		}
	})
	for range 8 {
		wg.Go(func() {
			for range 2000 {
				v, err := m.ReadInputRegisters(1, 16, 4)
				if err != nil || v[0] != v[1] || v[1] != v[2] || v[2] != v[3] {
					t.Errorf("torn read %v %v", v, err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		for i := range 2000 {
			_ = m.WriteHoldingRegisters(1, 0, []uint16{uint16(i)})
			_, _ = m.ReadHoldingRegisters(1, 0, 9)
		}
	})
	wg.Wait() // readers finish while the writer is still running
	close(stop)
	writer.Wait()
}

func assertCode(t *testing.T, err error, want modbus.ExceptionCode) {
	t.Helper()
	if want == 0 {
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		return
	}
	var got modbus.ExceptionCode
	if !errors.As(err, &got) || got != want {
		t.Fatalf("err = %v, want exception %#x", err, uint8(want))
	}
}

func TestPublish(t *testing.T) {
	m := newMem(t, Config{UnitID: 1})
	_ = m.WriteCoils(1, 0, []bool{true, true, true, true})
	in := make([]uint16, 20)
	in[19] = 7
	di := make([]bool, 16)
	di[4] = true
	if err := m.Publish(in, di, []uint16{0, 2}); err != nil {
		t.Fatal(err)
	}
	r, _ := m.ReadInputRegisters(1, 19, 1)
	d, _ := m.ReadDiscreteInputs(1, 4, 1)
	c, _ := m.ReadCoils(1, 0, 4)
	if r[0] != 7 || !d[0] || !slices.Equal(c, []bool{false, true, false, true}) {
		t.Fatalf("%v %v %v", r, d, c)
	}
	if m.Publish(in[:5], di, nil) == nil || m.Publish(in, di, []uint16{9}) == nil {
		t.Fatal("bad publish accepted")
	}
	if err := m.SetHoldingRegisters(7, []uint16{1, 2}); err != nil {
		t.Fatal(err)
	}
	if m.SetHoldingRegisters(8, []uint16{1, 2}) == nil {
		t.Fatal("out of range accepted")
	}
	if m.CheckUnit(2) == nil || m.CheckUnit(1) != nil {
		t.Fatal("CheckUnit")
	}
	if m.Layout() != (Layout{Coils: 4, DiscreteInputs: 16, HoldingRegisters: 9, InputRegisters: 20}) {
		t.Fatalf("layout %+v", m.Layout())
	}
}

func TestSetCoils(t *testing.T) {
	m := newMem(t, Config{UnitID: 1})
	if err := m.SetCoils(2, []bool{true, true}); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.ReadCoils(1, 0, 4); !slices.Equal(c, []bool{false, false, true, true}) {
		t.Fatalf("coils %v", c)
	}
	if m.SetCoils(3, []bool{true, true}) == nil {
		t.Fatal("out of range accepted")
	}
}
