package mapping

import (
	"strings"
	"testing"

	"modbus-simulator/configs"
	"modbus-simulator/internal/plc"
	"modbus-simulator/internal/simulation"
)

func TestDefaultMap(t *testing.T) {
	m, err := Parse(configs.DefaultRegisterMap)
	if err != nil {
		t.Fatal(err)
	}
	want := plc.Layout{Coils: 4, DiscreteInputs: 16, HoldingRegisters: 9, InputRegisters: 29}
	if m.Layout() != want {
		t.Fatalf("layout %+v", m.Layout())
	}
	// Spot-check the registers required by the project brief.
	byName := map[string]InputField{}
	for _, f := range m.Inputs {
		byName[f.Name] = f
	}
	for name, want := range map[string]struct {
		addr  uint16
		typ   DataType
		scale float64
	}{
		"machine_state": {0, Uint16, 1}, "extruder_rpm": {1, Uint16, 10}, "melt_pressure": {3, Uint16, 10},
		"haul_off_speed": {10, Uint16, 100}, "pipe_outer_diameter": {13, Uint16, 100},
		"active_fault_code": {15, Uint16, 1}, "total_produced_length": {16, Uint32, 10},
		"total_produced_weight": {18, Uint32, 10}, "melt_pressure_f32": {22, Float32, 1},
	} {
		f, ok := byName[name]
		if !ok || f.Address != want.addr || f.Type != want.typ || f.Scale != want.scale {
			t.Errorf("%s: %+v", name, f.Field)
		}
	}
	if m.coilFor[simulation.CmdStart] != 0 || m.coilFor[simulation.CmdEmergencyStop] != 3 {
		t.Fatal("coil commands")
	}
	if h := m.holdingAt[7]; h == nil || h.Name != "production_target" || h.Max != 1e7 {
		t.Fatalf("holding 7: %+v", h)
	}
}

func TestMapValidation(t *testing.T) {
	const ok = "version: 1\n"
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"empty", "", "empty document"},
		{"no fields", ok, "no fields defined"},
		{"bad version", "version: 2\ncoils: [{address: 0, name: s, command: start}]", "unsupported version 2"},
		{"unknown key", ok + "input_registers: [{address: 0, name: a, source: vacuum, colour: red}]", "field colour not found"},
		{"address too big for yaml type", ok + "input_registers: [{address: 70000, name: a, source: vacuum}]", "out of range"},
		{"missing address", ok + "input_registers: [{name: a, source: vacuum}]", "address is required"},
		{"missing name", ok + "input_registers: [{address: 0, source: vacuum}]", "name is required"},
		{"duplicate name", ok + "input_registers: [{address: 0, name: a, source: vacuum}, {address: 1, name: a, source: vacuum}]", "duplicate name"},
		{"unknown source", ok + "input_registers: [{address: 0, name: a, source: humidity}]", `unknown source "humidity"`},
		{"unknown type", ok + "input_registers: [{address: 0, name: a, source: vacuum, type: double}]", `unknown data type "double"`},
		{"bool register", ok + "input_registers: [{address: 0, name: a, source: vacuum, type: bool}]", "only valid for coils"},
		{"zero scale", ok + "input_registers: [{address: 0, name: a, source: vacuum, scale: 0}]", "scale must be a positive"},
		{"negative scale", ok + "input_registers: [{address: 0, name: a, source: vacuum, scale: -10}]", "scale must be a positive"},
		{"word order on 16 bit", ok + "input_registers: [{address: 0, name: a, source: vacuum, word_order: cdab}]", "only applies to 32-bit"},
		{"bad word order", ok + "input_registers: [{address: 0, name: a, source: vacuum, type: uint32, word_order: badc}]", "unknown word order"},
		{"overlap 32-bit", ok + "input_registers: [{address: 0, name: a, source: total_length, type: uint32}, {address: 1, name: b, source: vacuum}]", "overlaps"},
		{"beyond 65535", ok + "input_registers: [{address: 65535, name: a, source: total_length, type: uint32}]", "exceeds 65535"},
		{"setpoint on input", ok + "input_registers: [{address: 0, name: a, source: vacuum, setpoint: extruder_rpm}]", "setpoint is not valid"},
		{"di non-bool", ok + "discrete_inputs: [{address: 0, name: a, source: running, type: uint16}]", "type must be bool"},
		{"di scale", ok + "discrete_inputs: [{address: 0, name: a, source: running, scale: 2}]", "scale is not valid"},
		{"di unknown source", ok + "discrete_inputs: [{address: 0, name: a, source: vacuum}]", `unknown source "vacuum"`},
		{"hr neither", ok + "holding_registers: [{address: 0, name: a}]", "one of setpoint, scenario: true or reserved: true is required"},
		{"hr both", ok + "holding_registers: [{address: 0, name: a, setpoint: extruder_rpm, reserved: true}]", "mutually exclusive"},
		{"hr scenario and setpoint", ok + "holding_registers: [{address: 0, name: a, setpoint: extruder_rpm, scenario: true}]", "mutually exclusive"},
		{"hr scenario max", ok + "holding_registers: [{address: 0, name: a, scenario: true, max: 99}]", "scenario range 0..8"},
		{"di unknown alarm", ok + "discrete_inputs: [{address: 0, name: a, source: alarm_fire}]", `unknown source "alarm_fire"`},
		{"hr unknown setpoint", ok + "holding_registers: [{address: 0, name: a, setpoint: speed}]", `unknown setpoint "speed"`},
		{"hr max above limit", ok + "holding_registers: [{address: 0, name: a, setpoint: extruder_rpm, scale: 10, max: 200}]", "within the setpoint limits 0..150"},
		{"hr min > max", ok + "holding_registers: [{address: 0, name: a, setpoint: extruder_rpm, min: 100, max: 50}]", "min 100 > max 50"},
		{"hr range does not fit", ok + "holding_registers: [{address: 0, name: a, setpoint: production_target_length, scale: 10, max: 100000}]", "does not fit uint16"},
		{"hr source", ok + "holding_registers: [{address: 0, name: a, setpoint: extruder_rpm, source: vacuum}]", "source is not valid"},
		{"coil unknown command", ok + "coils: [{address: 0, name: a, command: jump}]", `unknown command "jump"`},
		{"coil duplicate command", ok + "coils: [{address: 0, name: a, command: start}, {address: 1, name: b, command: start}]", "already mapped"},
		{"coil overlap", ok + "coils: [{address: 0, name: a, command: start}, {address: 0, name: b, command: stop}]", "overlaps"},
		{"size too small", ok + "sizes: {input_registers: 1}\ninput_registers: [{address: 3, name: a, source: vacuum}]", "smaller than mapped"},
		{"size too large", ok + "sizes: {coils: 70000}\ncoils: [{address: 0, name: a, command: start}]", "out of range 0..65536"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant mention of %q", err, tt.want)
			}
		})
	}
}

// All problems are reported at once, each with its location.
func TestMapValidationReportsAllErrors(t *testing.T) {
	_, err := Parse([]byte(`version: 1
input_registers:
  - {address: 0, name: a, source: nope}
  - {address: 1, name: b, source: vacuum, scale: 0}
coils:
  - {address: 0, name: c, command: jump}
`))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"input_registers[0] (a)", "input_registers[1] (b)", "coils[0] (c)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

// Without explicit limits a narrow type narrows the writable range.
func TestMapRangeNarrowedByType(t *testing.T) {
	m, err := Parse([]byte("version: 1\nholding_registers: [{address: 0, name: a, setpoint: production_target_length, scale: 10}]"))
	if err != nil {
		t.Fatal(err)
	}
	if h := m.Holdings[0]; h.Min != 0 || h.Max != 6553.5 {
		t.Fatalf("range %g..%g", h.Min, h.Max)
	}
}

func TestMapSizesAndReserved(t *testing.T) {
	m, err := Parse([]byte(`version: 1
sizes: {holding_registers: 20, coils: 8}
holding_registers:
  - {address: 4, name: spare, reserved: true, type: int16, scale: 10}
coils:
  - {address: 2, name: go, command: start}
`))
	if err != nil {
		t.Fatal(err)
	}
	if l := m.Layout(); l.HoldingRegisters != 20 || l.Coils != 8 || l.InputRegisters != 0 {
		t.Fatalf("layout %+v", l)
	}
	h := m.Holdings[0]
	if !h.Reserved || h.Min != -3276.8 || h.Max != 3276.7 {
		t.Fatalf("reserved range %+v", h)
	}
}
