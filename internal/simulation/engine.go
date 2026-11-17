// Package simulation is the protocol-independent production line simulator:
// a fixed-timestep, deterministic engine plus a real-time runner.
//
// The engine never reads the wall clock. Simulation time is tick × Period and
// commands are applied only at tick boundaries, so the same seed and the same
// (tick, command) sequence always produce identical snapshots (ADR-0004).
// This package must not import any protocol package (ADR-0005).
package simulation

import (
	"errors"
	"fmt"
	"time"
)

// Stepper is the contract between a line model and the runner. *Engine
// implements it; other line types (Phase 6) or test fakes can too.
type Stepper interface {
	// Step applies cmds at the start of the next tick, advances one Period
	// and returns the new snapshot. A non-nil error lists rejected commands;
	// the step itself always happens.
	Step(cmds []Command) (Snapshot, error)
	// Snapshot returns the latest snapshot without advancing.
	Snapshot() Snapshot
	// Period is the fixed simulation timestep.
	Period() time.Duration
}

// Config configures an Engine. Start from DefaultConfig.
type Config struct {
	Seed   uint64
	Period time.Duration // fixed timestep, default 500 ms
	// HeatingBand and HeatingHold define "at temperature": all zones within
	// ±HeatingBand °C of their setpoints continuously for HeatingHold
	// switches HEATING to RUNNING.
	HeatingBand float64
	HeatingHold time.Duration
	// MinRunTemp is the cold-start interlock: the screw may only start
	// (HEATING → RUNNING) once every zone and the die are at least this hot,
	// whatever the setpoints (e.g. during a low-temperature soak).
	MinRunTemp  float64
	Setpoints   Setpoints // initial setpoints
	Process     ProcessParams
	Noise       NoiseConfig
	FaultLimits FaultLimits
	// SetpointLimits narrows the global setpoint limits for this line
	// (e.g. lower temperatures for PVC). Missing entries keep the defaults.
	SetpointLimits map[SetpointID]Range
}

// DefaultConfig returns the reference line: PE100 pipe 110 × 10 mm (SDR 11)
// at 450 kg/h. Coefficients are plausible, not plant data
// (docs/spikes/005-process-model.md).
func DefaultConfig() Config {
	return Config{
		Seed:        1,
		Period:      500 * time.Millisecond,
		HeatingBand: 5,
		HeatingHold: 30 * time.Second,
		MinRunTemp:  160,
		Setpoints: Setpoints{
			ExtruderRPM:  100,
			ZoneTemps:    [3]float64{180, 190, 200},
			DieTemp:      205,
			HaulOffSpeed: 2.5,
		},
		Process:     DefaultProcessParams(),
		Noise:       DefaultNoise(),
		FaultLimits: DefaultFaultLimits(),
	}
}

func (c Config) validate() error {
	var errs []error
	if c.Period <= 0 {
		errs = append(errs, errors.New("period must be positive"))
	}
	if c.HeatingBand <= 0 {
		errs = append(errs, errors.New("heating band must be positive"))
	}
	if c.HeatingHold < 0 {
		errs = append(errs, errors.New("heating hold must not be negative"))
	}
	lim, err := effectiveLimits(c.SetpointLimits)
	if err != nil {
		errs = append(errs, err)
	} else if err := c.Setpoints.validate(lim); err != nil {
		errs = append(errs, fmt.Errorf("initial %w", err))
	}
	if err := c.Noise.validate(); err != nil {
		errs = append(errs, err)
	}
	if err := c.FaultLimits.validate(); err != nil {
		errs = append(errs, err)
	}
	if err := c.Process.validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Snapshot is an immutable, comparable view of the line after a tick.
type Snapshot struct {
	Tick    uint64
	SimTime time.Duration
	State   MachineState
	// StateTime is the time spent in the current state. Commanded transitions
	// take effect at the start of a tick (StateTime = Period right after);
	// automatic ones at its end (StateTime = 0 right after).
	StateTime     time.Duration
	FaultCode     FaultCode
	EmergencyStop bool
	Setpoints     Setpoints
	True          ProcessValues // model state, for tests and diagnostics
	Measured      ProcessValues // what sensors report (with noise)
	Internals     Internals     // model diagnostics (not sensor readings)
	TotalLength   float64       // m, produced in RUNNING
	TotalWeight   float64       // kg, produced in RUNNING
	TargetReached bool          // ProductionTarget > 0 and TotalLength ≥ it
	Scenario      Scenario      // active injected fault cause (0 = none)
	Alarms        AlarmSet      // conditions beyond their warning limits
	LastFaultCode FaultCode     // most recent fault, kept after reset
}

// Engine simulates one extrusion line. It is not safe for concurrent use;
// a single Runner (or test) owns it.
type Engine struct {
	cfg         Config
	tick        uint64
	state       MachineState
	stateSince  uint64 // tick at which the current state was entered
	fault       FaultCode
	estop       bool
	sp          Setpoints
	heatOKTicks uint64
	holdTicks   uint64
	line        *line
	sensors     sensorSet
	last        Snapshot

	scenario   Scenario
	dist       *disturbance
	monitors   []monitor
	alarms     AlarmSet
	lastFault  FaultCode
	heatersOff bool // equipment impact of the latched fault
	lim        map[SetpointID]Range
}

var _ Stepper = (*Engine)(nil)

// New validates cfg and returns an engine in STOPPED at ambient temperature.
func New(cfg Config) (*Engine, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("simulation config: %w", err)
	}
	e := &Engine{
		cfg:       cfg,
		state:     Stopped,
		sp:        cfg.Setpoints,
		holdTicks: uint64((cfg.HeatingHold + cfg.Period - 1) / cfg.Period),
		line:      newLine(cfg.Process, cfg.Period),
		sensors:   newSensorSet(cfg.Seed, cfg.Noise),
		dist:      newDisturbance(),
		monitors:  newMonitors(cfg.FaultLimits),
	}
	e.lim, _ = effectiveLimits(cfg.SetpointLimits) // validated above
	e.last = e.snapshot(e.measure())
	return e, nil
}

// SetpointLimits returns this engine's limits for a setpoint.
func (e *Engine) SetpointLimits(id SetpointID) (lo, hi float64, ok bool) {
	r, ok := e.lim[id]
	return r.Min, r.Max, ok
}

// Period implements Stepper.
func (e *Engine) Period() time.Duration { return e.cfg.Period }

// Snapshot implements Stepper.
func (e *Engine) Snapshot() Snapshot { return e.last }

// Step implements Stepper. Order within a tick: commands → disturbance →
// process models → measurement → fault monitors (may trip to FAULT) →
// HEATING→RUNNING check → snapshot.
func (e *Engine) Step(cmds []Command) (Snapshot, error) {
	err := e.apply(cmds)
	e.tick++
	e.dist.step(e.scenario, e.cfg.Period)
	inFault := e.state == Fault
	heatersOn := e.state == Heating || e.state == Running || (inFault && !e.heatersOff)
	e.line.step(e.state, e.sp, heatersOn, !(inFault && e.heatersOff), e.dist, e.cfg.Period)

	measured := e.measure()
	in := monitorInput{m: measured, sp: e.sp, heaterPct: e.line.heaterPercent(), reference: e.line.pipe.Reference}
	alarms, trip := evaluate(e.monitors, &in, e.state, e.stateTime(), e.cfg.Period)
	if e.estop {
		alarms |= 1 << AlarmEmergencyStop
	}
	e.alarms = alarms
	if trip != FaultNone && e.state != Fault {
		e.enterFault(trip)
		e.line.safeStop()
	}
	e.autoTransitions()
	e.last = e.snapshot(measured)
	return e.last, err
}

// measure reads all sensors once and applies sensor fault overrides.
func (e *Engine) measure() ProcessValues {
	m := e.sensors.measure(e.line.values())
	for i, open := range e.dist.sensorFailed {
		if !open {
			continue
		}
		if i == 3 {
			m.DieTemp = openThermocoupleReading
		} else {
			m.ZoneTemps[i] = openThermocoupleReading
		}
	}
	return m
}

func (e *Engine) stateTime() time.Duration {
	return time.Duration(e.tick-e.stateSince) * e.cfg.Period
}

// apply processes one tick's command batch in a fixed priority order,
// independent of arrival order within the batch:
//
//  1. setpoints and scenario (in order), 2. emergency stop level (last one
//     wins), 3. injected faults, 4. fault reset, 5. stop/start — Stop wins
//     (ADR-0003).
//
// Reset is handled before Start, so "Reset + Start" in one tick restarts a
// line whose fault has cleared.
func (e *Engine) apply(cmds []Command) error {
	var errs []error
	reject := func(c Command, why string) { errs = append(errs, fmt.Errorf("%s rejected: %s", c, why)) }

	var start, stop, reset bool
	var estopSeen bool
	var estopLevel bool
	var faults []FaultCode
	for _, c := range cmds {
		switch c.Kind {
		case CmdSetSetpoint:
			if err := e.sp.set(c.Setpoint, c.Value, e.lim); err != nil {
				reject(c, err.Error())
			}
		case CmdSetScenario:
			if c.Scenario > MaxScenario {
				reject(c, fmt.Sprintf("unknown scenario (valid 0..%d)", MaxScenario))
				continue
			}
			e.scenario = c.Scenario
		case CmdEmergencyStop:
			estopSeen, estopLevel = true, c.On
		case CmdTriggerFault:
			if c.Fault == FaultNone {
				reject(c, "fault code 0 is reserved for no fault")
				continue
			}
			faults = append(faults, c.Fault)
		case CmdResetFault:
			reset = true
		case CmdStart:
			start = true
		case CmdStop:
			stop = true
		default:
			reject(c, "unknown command")
		}
	}

	if estopSeen {
		e.estop = estopLevel
	}
	if e.estop {
		e.enterFault(FaultEmergencyStop)
	}
	for _, f := range faults {
		e.enterFault(f)
	}
	if reset {
		switch {
		case e.state != Fault:
			// Nothing to reset; harmless.
		case e.estop:
			reject(ResetFault(), "emergency stop still active")
		case blocksReset(e.monitors, e.fault) != "":
			reject(ResetFault(), blocksReset(e.monitors, e.fault))
		default:
			e.fault = FaultNone
			e.enter(Stopped)
		}
	}
	switch {
	case stop:
		if e.state == Heating || e.state == Running {
			e.enter(Stopped)
		}
	case start:
		switch e.state {
		case Stopped:
			e.enter(Heating)
		case Fault:
			reject(Start(), fmt.Sprintf("fault %d active, reset first", e.fault))
		}
	}
	return errors.Join(errs...)
}

// enterFault latches the first fault; later faults do not overwrite the code.
func (e *Engine) enterFault(code FaultCode) {
	if e.state == Fault {
		return
	}
	e.fault, e.lastFault = code, code
	info, _ := LookupFault(code)
	e.heatersOff = info.HeatersOff // unknown (manually injected) codes keep heaters on
	e.enter(Fault)
}

func (e *Engine) enter(s MachineState) {
	if e.state != s {
		e.state = s
		e.stateSince = e.tick
		e.heatOKTicks = 0
	}
}

func (e *Engine) autoTransitions() {
	if e.state != Heating {
		return
	}
	if e.line.zonesWithin(e.sp, e.cfg.HeatingBand) && e.line.coldestZone() >= e.cfg.MinRunTemp {
		e.heatOKTicks++
	} else {
		e.heatOKTicks = 0
	}
	if e.heatOKTicks > 0 && e.heatOKTicks >= e.holdTicks {
		e.enter(Running)
	}
}

func (e *Engine) snapshot(measured ProcessValues) Snapshot {
	tv := e.line.values()
	return Snapshot{
		Tick:          e.tick,
		SimTime:       time.Duration(e.tick) * e.cfg.Period,
		State:         e.state,
		StateTime:     e.stateTime(),
		FaultCode:     e.fault,
		EmergencyStop: e.estop,
		Setpoints:     e.sp,
		True:          tv,
		Measured:      measured,
		Internals:     e.line.internals(),
		TotalLength:   e.line.length,
		TotalWeight:   e.line.weight,
		TargetReached: e.sp.ProductionTarget > 0 && e.line.length >= e.sp.ProductionTarget,
		Scenario:      e.scenario,
		Alarms:        e.alarms,
		LastFaultCode: e.lastFault,
	}
}
