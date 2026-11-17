package mapping

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"

	"go.yaml.in/yaml/v3"

	"modbus-simulator/internal/plc"
	"modbus-simulator/internal/simulation"
)

// --- YAML schema -----------------------------------------------------------

type fileSpec struct {
	Version          int         `yaml:"version"`
	Sizes            sizesSpec   `yaml:"sizes"`
	InputRegisters   []fieldSpec `yaml:"input_registers"`
	DiscreteInputs   []fieldSpec `yaml:"discrete_inputs"`
	HoldingRegisters []fieldSpec `yaml:"holding_registers"`
	Coils            []fieldSpec `yaml:"coils"`
}

// sizesSpec reserves area sizes beyond the mapped addresses (reads of
// reserved addresses return 0; writes are rejected).
type sizesSpec struct {
	Coils            int `yaml:"coils"`
	DiscreteInputs   int `yaml:"discrete_inputs"`
	HoldingRegisters int `yaml:"holding_registers"`
	InputRegisters   int `yaml:"input_registers"`
}

type fieldSpec struct {
	Address     *int     `yaml:"address"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Unit        string   `yaml:"unit"`
	Type        string   `yaml:"type"`
	Scale       *float64 `yaml:"scale"`
	WordOrder   string   `yaml:"word_order"`
	Source      string   `yaml:"source"`   // input registers, discrete inputs
	Setpoint    string   `yaml:"setpoint"` // holding registers
	Reserved    bool     `yaml:"reserved"` // holding registers: store only
	Scenario    bool     `yaml:"scenario"` // holding registers: fault scenario selector
	Min         *float64 `yaml:"min"`      // holding registers, engineering units
	Max         *float64 `yaml:"max"`
	Command     string   `yaml:"command"` // coils
}

// --- compiled map ----------------------------------------------------------

// Field is the common part of every mapped item.
type Field struct {
	Name        string
	Description string
	Unit        string
	Address     uint16
	Type        DataType
	Scale       float64
	Order       WordOrder
}

// Width is the number of addresses the field occupies.
func (f Field) Width() int { return f.Type.Registers() }

// InputField publishes a snapshot value into input registers.
type InputField struct {
	Field
	Source string
	get    func(simulation.Snapshot) float64
}

// DiscreteField publishes a snapshot flag into a discrete input.
type DiscreteField struct {
	Field
	Source string
	get    func(simulation.Snapshot) bool
}

// HoldingField is a writable setpoint, the fault scenario selector, or a
// reserved store-only register.
type HoldingField struct {
	Field
	Setpoint simulation.SetpointID // 0 unless a setpoint
	Reserved bool
	Scenario bool
	Min, Max float64 // engineering units
}

// Coil kinds (ADR-0003).
const (
	coilMomentary = iota + 1 // write 1 to trigger, cleared when the engine applied it
	coilLevel                // maintained (emergency stop)
)

// CoilField is a command coil.
type CoilField struct {
	Field
	Command string
	kind    int
	cmdKind simulation.CommandKind
}

// Map is a validated register map. It is immutable after Parse.
type Map struct {
	Inputs    []InputField
	Discretes []DiscreteField
	Holdings  []HoldingField
	Coils     []CoilField
	layout    plc.Layout

	holdingAt map[uint16]*HoldingField // every occupied address → field
	coilAt    map[uint16]*CoilField
	coilFor   map[simulation.CommandKind]uint16
}

// Layout returns the PLC memory layout required by the map.
func (m *Map) Layout() plc.Layout { return m.layout }

// --- sources and commands --------------------------------------------------

var analogSources = map[string]func(simulation.Snapshot) float64{
	"machine_state":      func(s simulation.Snapshot) float64 { return float64(s.State) },
	"fault_code":         func(s simulation.Snapshot) float64 { return float64(s.FaultCode) },
	"extruder_rpm":       func(s simulation.Snapshot) float64 { return s.Measured.ExtruderRPM },
	"motor_current":      func(s simulation.Snapshot) float64 { return s.Measured.MotorCurrent },
	"motor_load":         func(s simulation.Snapshot) float64 { return s.Measured.MotorLoad },
	"melt_pressure":      func(s simulation.Snapshot) float64 { return s.Measured.MeltPressure },
	"melt_temp":          func(s simulation.Snapshot) float64 { return s.Measured.MeltTemp },
	"zone1_temp":         func(s simulation.Snapshot) float64 { return s.Measured.ZoneTemps[0] },
	"zone2_temp":         func(s simulation.Snapshot) float64 { return s.Measured.ZoneTemps[1] },
	"zone3_temp":         func(s simulation.Snapshot) float64 { return s.Measured.ZoneTemps[2] },
	"die_temp":           func(s simulation.Snapshot) float64 { return s.Measured.DieTemp },
	"vacuum":             func(s simulation.Snapshot) float64 { return s.Measured.VacuumMagnitude },
	"cooling_water_temp": func(s simulation.Snapshot) float64 { return s.Measured.CoolingWaterTemp },
	"haul_off_speed":     func(s simulation.Snapshot) float64 { return s.Measured.HaulOffSpeed },
	"throughput":         func(s simulation.Snapshot) float64 { return s.Measured.Throughput },
	"power":              func(s simulation.Snapshot) float64 { return s.Measured.PowerConsumption },
	"pipe_od":            func(s simulation.Snapshot) float64 { return s.Measured.PipeOD },
	"wall_thickness":     func(s simulation.Snapshot) float64 { return s.Measured.WallThickness },
	"total_length":       func(s simulation.Snapshot) float64 { return s.TotalLength },
	"total_weight":       func(s simulation.Snapshot) float64 { return s.TotalWeight },
	"sim_time":           func(s simulation.Snapshot) float64 { return s.SimTime.Seconds() },
	"alarm_word":         func(s simulation.Snapshot) float64 { return float64(s.Alarms) },
	"scenario":           func(s simulation.Snapshot) float64 { return float64(s.Scenario) },
	"last_fault_code":    func(s simulation.Snapshot) float64 { return float64(s.LastFaultCode) },
	"state_time":         func(s simulation.Snapshot) float64 { return s.StateTime.Seconds() },
}

var bitSources = map[string]func(simulation.Snapshot) bool{
	"stopped":        func(s simulation.Snapshot) bool { return s.State == simulation.Stopped },
	"heating":        func(s simulation.Snapshot) bool { return s.State == simulation.Heating },
	"running":        func(s simulation.Snapshot) bool { return s.State == simulation.Running },
	"fault":          func(s simulation.Snapshot) bool { return s.State == simulation.Fault },
	"emergency_stop": func(s simulation.Snapshot) bool { return s.EmergencyStop },
	"target_reached": func(s simulation.Snapshot) bool { return s.TargetReached },
}

func init() {
	for _, a := range simulation.Alarms() {
		bitSources["alarm_"+a.String()] = func(s simulation.Snapshot) bool { return s.Alarms.Has(a) }
	}
}

var coilCommands = map[string]struct {
	kind    int
	cmdKind simulation.CommandKind
}{
	"start":          {coilMomentary, simulation.CmdStart},
	"stop":           {coilMomentary, simulation.CmdStop},
	"reset_fault":    {coilMomentary, simulation.CmdResetFault},
	"emergency_stop": {coilLevel, simulation.CmdEmergencyStop},
}

// --- parsing and validation ------------------------------------------------

// Load reads and validates a YAML register map. Unknown keys are errors.
func Load(r io.Reader) (*Map, error) {
	var spec fileSpec
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("register map: empty document")
		}
		return nil, fmt.Errorf("register map: %w", err)
	}
	m, err := compile(spec)
	if err != nil {
		return nil, fmt.Errorf("register map invalid:\n%w", err)
	}
	return m, nil
}

// Parse is Load for an in-memory document.
func Parse(b []byte) (*Map, error) { return Load(bytes.NewReader(b)) }

type validator struct {
	errs  []error
	names map[string]string
}

func (v *validator) errorf(where, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", where, fmt.Sprintf(format, args...)))
}

// area tracks address occupancy of one Modbus area.
type area struct {
	name string
	used map[int]string
	end  int // highest occupied address + 1
}

func newArea(name string) *area { return &area{name: name, used: map[int]string{}} }

func (a *area) claim(v *validator, where, field string, addr, width int) {
	if addr+width > 0x10000 {
		v.errorf(where, "address %d + width %d exceeds 65535", addr, width)
		return
	}
	for i := addr; i < addr+width; i++ {
		if other, ok := a.used[i]; ok {
			v.errorf(where, "address %d overlaps %q in %s", i, other, a.name)
			return
		}
	}
	for i := addr; i < addr+width; i++ {
		a.used[i] = field
	}
	a.end = max(a.end, addr+width)
}

// common validates name, address, type, scale and word order.
func (v *validator) common(section string, i int, s fieldSpec, defType DataType, bitArea bool) (Field, string, bool) {
	where := fmt.Sprintf("%s[%d]", section, i)
	if s.Name != "" {
		where = fmt.Sprintf("%s[%d] (%s)", section, i, s.Name)
	}
	ok := true
	f := Field{Name: s.Name, Description: s.Description, Unit: s.Unit, Scale: 1}
	if s.Name == "" {
		v.errorf(where, "name is required")
		ok = false
	} else if prev, dup := v.names[s.Name]; dup {
		v.errorf(where, "duplicate name, already used in %s", prev)
		ok = false
	} else {
		v.names[s.Name] = where
	}
	if s.Address == nil {
		v.errorf(where, "address is required")
		ok = false
	} else if *s.Address < 0 || *s.Address > 0xFFFF {
		v.errorf(where, "address %d out of range 0..65535", *s.Address)
		ok = false
	} else {
		f.Address = uint16(*s.Address)
	}
	f.Type = defType
	if s.Type != "" {
		t, err := ParseDataType(s.Type)
		if err != nil {
			v.errorf(where, "%v", err)
			ok = false
		} else {
			f.Type = t
		}
	}
	if bitArea && f.Type != Bool {
		v.errorf(where, "type must be bool in %s", section)
		ok = false
	}
	if !bitArea && f.Type == Bool {
		v.errorf(where, "type bool is only valid for coils and discrete inputs")
		ok = false
	}
	if s.Scale != nil {
		if bitArea {
			v.errorf(where, "scale is not valid for bits")
			ok = false
		} else if *s.Scale <= 0 || math.IsInf(*s.Scale, 0) || math.IsNaN(*s.Scale) {
			v.errorf(where, "scale must be a positive number, got %v", *s.Scale)
			ok = false
		} else {
			f.Scale = *s.Scale
		}
	}
	if s.WordOrder != "" {
		o, err := ParseWordOrder(s.WordOrder)
		switch {
		case err != nil:
			v.errorf(where, "%v", err)
			ok = false
		case f.Type.Registers() != 2:
			v.errorf(where, "word_order only applies to 32-bit types, not %s", f.Type)
			ok = false
		default:
			f.Order = o
		}
	}
	return f, where, ok
}

func (v *validator) forbid(where, key string, set bool) bool {
	if set {
		v.errorf(where, "%s is not valid here", key)
	}
	return !set
}

func compile(spec fileSpec) (*Map, error) {
	v := &validator{names: map[string]string{}}
	if spec.Version != 1 {
		v.errorf("version", "unsupported version %d (want 1)", spec.Version)
	}
	m := &Map{
		holdingAt: map[uint16]*HoldingField{},
		coilAt:    map[uint16]*CoilField{},
		coilFor:   map[simulation.CommandKind]uint16{},
	}
	ir, di, hr, co := newArea("input_registers"), newArea("discrete_inputs"), newArea("holding_registers"), newArea("coils")

	for i, s := range spec.InputRegisters {
		f, where, ok := v.common("input_registers", i, s, Uint16, false)
		ok = v.forbid(where, "setpoint", s.Setpoint != "") && ok
		ok = v.forbid(where, "command", s.Command != "") && ok
		ok = v.forbid(where, "reserved", s.Reserved) && ok
		ok = v.forbid(where, "min/max", s.Min != nil || s.Max != nil) && ok
		get, known := analogSources[s.Source]
		if !known {
			v.errorf(where, "unknown source %q", s.Source)
			ok = false
		}
		if ok {
			ir.claim(v, where, f.Name, int(f.Address), f.Width())
			m.Inputs = append(m.Inputs, InputField{Field: f, Source: s.Source, get: get})
		}
	}

	for i, s := range spec.DiscreteInputs {
		f, where, ok := v.common("discrete_inputs", i, s, Bool, true)
		ok = v.forbid(where, "setpoint", s.Setpoint != "") && ok
		ok = v.forbid(where, "command", s.Command != "") && ok
		ok = v.forbid(where, "reserved", s.Reserved) && ok
		ok = v.forbid(where, "min/max", s.Min != nil || s.Max != nil) && ok
		get, known := bitSources[s.Source]
		if !known {
			v.errorf(where, "unknown source %q", s.Source)
			ok = false
		}
		if ok {
			di.claim(v, where, f.Name, int(f.Address), 1)
			m.Discretes = append(m.Discretes, DiscreteField{Field: f, Source: s.Source, get: get})
		}
	}

	for i, s := range spec.HoldingRegisters {
		f, where, ok := v.common("holding_registers", i, s, Uint16, false)
		ok = v.forbid(where, "source", s.Source != "") && ok
		ok = v.forbid(where, "command", s.Command != "") && ok
		h := HoldingField{Field: f, Reserved: s.Reserved}
		lo, hi := f.Type.rawRange()
		h.Min, h.Max = lo/f.Scale, hi/f.Scale
		h.Scenario = s.Scenario
		kinds := 0
		for _, set := range []bool{s.Setpoint != "", s.Reserved, s.Scenario} {
			if set {
				kinds++
			}
		}
		switch {
		case kinds > 1:
			v.errorf(where, "setpoint, scenario and reserved are mutually exclusive")
			ok = false
		case kinds == 0:
			v.errorf(where, "one of setpoint, scenario: true or reserved: true is required")
			ok = false
		case s.Scenario:
			h.Min, h.Max = 0, min(h.Max, float64(simulation.MaxScenario))
			if (s.Min != nil && *s.Min < 0) || (s.Max != nil && *s.Max > float64(simulation.MaxScenario)) {
				v.errorf(where, "min/max must stay within the scenario range 0..%d", simulation.MaxScenario)
				ok = false
			}
		case s.Setpoint != "":
			id, known := simulation.SetpointByName(s.Setpoint)
			if !known {
				v.errorf(where, "unknown setpoint %q", s.Setpoint)
				ok = false
				break
			}
			h.Setpoint = id
			spLo, spHi, _ := id.Limits()
			h.Min, h.Max = max(h.Min, spLo), min(h.Max, spHi)
			if (s.Min != nil && *s.Min < spLo) || (s.Max != nil && *s.Max > spHi) {
				v.errorf(where, "min/max must stay within the setpoint limits %g..%g", spLo, spHi)
				ok = false
			}
		}
		if s.Min != nil {
			h.Min = *s.Min
		}
		if s.Max != nil {
			h.Max = *s.Max
		}
		if h.Min > h.Max {
			v.errorf(where, "min %g > max %g", h.Min, h.Max)
			ok = false
		}
		if _, c := Encode(h.Min, f.Scale, f.Type, f.Order); c {
			v.errorf(where, "min %g × scale %g does not fit %s", h.Min, f.Scale, f.Type)
			ok = false
		}
		if _, c := Encode(h.Max, f.Scale, f.Type, f.Order); c {
			v.errorf(where, "max %g × scale %g does not fit %s", h.Max, f.Scale, f.Type)
			ok = false
		}
		if ok {
			hr.claim(v, where, f.Name, int(f.Address), f.Width())
			m.Holdings = append(m.Holdings, h)
		}
	}

	for i, s := range spec.Coils {
		f, where, ok := v.common("coils", i, s, Bool, true)
		ok = v.forbid(where, "source", s.Source != "") && ok
		ok = v.forbid(where, "setpoint", s.Setpoint != "") && ok
		ok = v.forbid(where, "reserved", s.Reserved) && ok
		ok = v.forbid(where, "min/max", s.Min != nil || s.Max != nil) && ok
		cmd, known := coilCommands[s.Command]
		if !known {
			v.errorf(where, "unknown command %q (want start, stop, reset_fault or emergency_stop)", s.Command)
			ok = false
		} else if _, dup := m.coilFor[cmd.cmdKind]; dup {
			v.errorf(where, "command %q is already mapped to another coil", s.Command)
			ok = false
		}
		if ok {
			co.claim(v, where, f.Name, int(f.Address), 1)
			m.coilFor[cmd.cmdKind] = f.Address
			m.Coils = append(m.Coils, CoilField{Field: f, Command: s.Command, kind: cmd.kind, cmdKind: cmd.cmdKind})
		}
	}

	size := func(a *area, declared int) int {
		if declared < 0 || declared > 0x10000 {
			v.errorf("sizes."+a.name, "size %d out of range 0..65536", declared)
		}
		if declared != 0 && declared < a.end {
			v.errorf("sizes."+a.name, "size %d smaller than mapped addresses (need %d)", declared, a.end)
		}
		return max(a.end, declared)
	}
	m.layout = plc.Layout{
		Coils:            size(co, spec.Sizes.Coils),
		DiscreteInputs:   size(di, spec.Sizes.DiscreteInputs),
		HoldingRegisters: size(hr, spec.Sizes.HoldingRegisters),
		InputRegisters:   size(ir, spec.Sizes.InputRegisters),
	}
	if len(m.Inputs)+len(m.Discretes)+len(m.Holdings)+len(m.Coils) == 0 && len(v.errs) == 0 {
		v.errorf("map", "no fields defined")
	}
	if err := errors.Join(v.errs...); err != nil {
		return nil, err
	}

	for i := range m.Holdings {
		h := &m.Holdings[i]
		for a := 0; a < h.Width(); a++ {
			m.holdingAt[h.Address+uint16(a)] = h
		}
	}
	for i := range m.Coils {
		m.coilAt[m.Coils[i].Address] = &m.Coils[i]
	}
	return m, nil
}
