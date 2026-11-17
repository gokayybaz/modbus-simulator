package simulation

import "fmt"

// MachineState is the line's operating state. Values match Input Register 0
// in docs/registers/convention.md.
type MachineState uint8

const (
	Stopped MachineState = 0
	Heating MachineState = 1
	Running MachineState = 2
	Fault   MachineState = 3
)

func (s MachineState) String() string {
	switch s {
	case Stopped:
		return "STOPPED"
	case Heating:
		return "HEATING"
	case Running:
		return "RUNNING"
	case Fault:
		return "FAULT"
	default:
		return fmt.Sprintf("MachineState(%d)", uint8(s))
	}
}

// FaultCode identifies the active fault; 0 means none. The full fault table
// is defined in Phase 5; Phase 2 only knows the emergency stop and manually
// injected codes.
type FaultCode uint16

const (
	FaultNone          FaultCode = 0
	FaultEmergencyStop FaultCode = 1 // see faults.go for the full table
)

// CommandKind enumerates the commands the engine accepts.
type CommandKind uint8

const (
	CmdStart CommandKind = iota + 1
	CmdStop
	CmdResetFault
	CmdEmergencyStop
	CmdSetSetpoint
	CmdTriggerFault
	CmdSetScenario
)

func (k CommandKind) String() string {
	switch k {
	case CmdStart:
		return "Start"
	case CmdStop:
		return "Stop"
	case CmdResetFault:
		return "ResetFault"
	case CmdEmergencyStop:
		return "EmergencyStop"
	case CmdSetSetpoint:
		return "SetSetpoint"
	case CmdTriggerFault:
		return "TriggerFault"
	case CmdSetScenario:
		return "SetScenario"
	default:
		return fmt.Sprintf("CommandKind(%d)", uint8(k))
	}
}

// Command is an input to the engine. It is a plain comparable value so
// command scripts can be recorded and replayed. Use the constructors.
type Command struct {
	Kind     CommandKind
	Setpoint SetpointID // CmdSetSetpoint
	Value    float64    // CmdSetSetpoint, engineering units
	On       bool       // CmdEmergencyStop: true = pressed (maintained)
	Fault    FaultCode  // CmdTriggerFault
	Scenario Scenario   // CmdSetScenario
}

func Start() Command                   { return Command{Kind: CmdStart} }
func Stop() Command                    { return Command{Kind: CmdStop} }
func ResetFault() Command              { return Command{Kind: CmdResetFault} }
func EmergencyStop(on bool) Command    { return Command{Kind: CmdEmergencyStop, On: on} }
func TriggerFault(c FaultCode) Command { return Command{Kind: CmdTriggerFault, Fault: c} }
func SetSetpoint(id SetpointID, v float64) Command {
	return Command{Kind: CmdSetSetpoint, Setpoint: id, Value: v}
}

func (c Command) String() string {
	switch c.Kind {
	case CmdEmergencyStop:
		return fmt.Sprintf("EmergencyStop(%t)", c.On)
	case CmdSetSetpoint:
		return fmt.Sprintf("SetSetpoint(%s=%g)", c.Setpoint, c.Value)
	case CmdTriggerFault:
		return fmt.Sprintf("TriggerFault(%d)", c.Fault)
	case CmdSetScenario:
		return fmt.Sprintf("SetScenario(%s)", c.Scenario)
	default:
		return c.Kind.String()
	}
}

// SetpointID identifies an operator setpoint.
type SetpointID uint8

const (
	SPExtruderRPM SetpointID = iota + 1
	SPZone1Temp
	SPZone2Temp
	SPZone3Temp
	SPDieTemp
	SPHaulOffSpeed
	SPProductionTarget
)

type setpointInfo struct {
	name     string
	min, max float64
}

// setpointTable holds names and engineering limits (docs/registers/convention.md).
var setpointTable = map[SetpointID]setpointInfo{
	SPExtruderRPM:  {"extruder_rpm", 0, 150},
	SPZone1Temp:    {"zone1_temp", 0, 260},
	SPZone2Temp:    {"zone2_temp", 0, 260},
	SPZone3Temp:    {"zone3_temp", 0, 260},
	SPDieTemp:      {"die_temp", 0, 260},
	SPHaulOffSpeed: {"haul_off_speed", 0, 30},
	// 0 disables the target.
	SPProductionTarget: {"production_target_length", 0, 1e7},
}

// SetpointByName returns the setpoint with the given name (see String).
func SetpointByName(name string) (SetpointID, bool) {
	for id, info := range setpointTable {
		if info.name == name {
			return id, true
		}
	}
	return 0, false
}

// Limits returns the engineering limits of the setpoint.
func (id SetpointID) Limits() (lo, hi float64, ok bool) {
	info, ok := setpointTable[id]
	return info.min, info.max, ok
}

// Assign stores v without validation (the engine validates its initial
// setpoints); it reports false for an unknown id.
func (s *Setpoints) Assign(id SetpointID, v float64) bool {
	p := s.ptr(id)
	if p == nil {
		return false
	}
	*p = v
	return true
}

// Get returns the value of setpoint id.
func (s Setpoints) Get(id SetpointID) (float64, bool) {
	if p := s.ptr(id); p != nil {
		return *p, true
	}
	return 0, false
}

func (id SetpointID) String() string {
	if info, ok := setpointTable[id]; ok {
		return info.name
	}
	return fmt.Sprintf("SetpointID(%d)", uint8(id))
}

// Setpoints are the operator targets in engineering units.
type Setpoints struct {
	ExtruderRPM  float64    // rpm
	ZoneTemps    [3]float64 // °C, barrel zones 1..3
	DieTemp      float64    // °C
	HaulOffSpeed float64    // m/min
	// ProductionTarget is a produced-length target in m; 0 = none. Reaching it
	// only raises Snapshot.TargetReached, it does not stop the line.
	ProductionTarget float64
}

// ptr returns the field addressed by id.
func (s *Setpoints) ptr(id SetpointID) *float64 {
	switch id {
	case SPExtruderRPM:
		return &s.ExtruderRPM
	case SPZone1Temp:
		return &s.ZoneTemps[0]
	case SPZone2Temp:
		return &s.ZoneTemps[1]
	case SPZone3Temp:
		return &s.ZoneTemps[2]
	case SPDieTemp:
		return &s.DieTemp
	case SPHaulOffSpeed:
		return &s.HaulOffSpeed
	case SPProductionTarget:
		return &s.ProductionTarget
	}
	return nil
}

// Range is an inclusive engineering-unit range.
type Range struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// effectiveLimits overlays per-line limits on the global table. Overrides
// may only narrow the global limits.
func effectiveLimits(over map[SetpointID]Range) (map[SetpointID]Range, error) {
	lim := make(map[SetpointID]Range, len(setpointTable))
	for id, info := range setpointTable {
		lim[id] = Range{info.min, info.max}
	}
	for id, r := range over {
		info, ok := setpointTable[id]
		switch {
		case !ok:
			return nil, fmt.Errorf("setpoint limits: unknown setpoint %d", id)
		case !(r.Min <= r.Max) || r.Min < info.min || r.Max > info.max:
			return nil, fmt.Errorf("setpoint limits %s: %g..%g must satisfy min ≤ max within %g..%g",
				info.name, r.Min, r.Max, info.min, info.max)
		}
		lim[id] = r
	}
	return lim, nil
}

// set validates v against lim and stores it.
func (s *Setpoints) set(id SetpointID, v float64, lim map[SetpointID]Range) error {
	r, ok := lim[id]
	p := s.ptr(id)
	if !ok || p == nil {
		return fmt.Errorf("unknown setpoint %d", id)
	}
	if v != v || v < r.Min || v > r.Max { // v != v: NaN
		return fmt.Errorf("setpoint %s=%g outside %g..%g", id, v, r.Min, r.Max)
	}
	*p = v
	return nil
}

// validate checks every setpoint against lim.
func (s Setpoints) validate(lim map[SetpointID]Range) error {
	for id := range setpointTable {
		if err := s.set(id, *s.ptr(id), lim); err != nil {
			return err
		}
	}
	return nil
}
