package simulation

import (
	"errors"
	"fmt"
	"time"
)

// Fault codes (Input Register 15). Zone-specific faults use one code per zone.
// The authoritative description is docs/faults/fault-table.md (kept in sync
// by a test).
const (
	FaultMotorOverload    FaultCode = 10
	FaultMeltPressureHigh FaultCode = 11
	FaultZone1TempHigh    FaultCode = 21
	FaultZone2TempHigh    FaultCode = 22
	FaultZone3TempHigh    FaultCode = 23
	FaultDieTempHigh      FaultCode = 24
	FaultZone1TempLow     FaultCode = 31
	FaultZone2TempLow     FaultCode = 32
	FaultZone3TempLow     FaultCode = 33
	FaultDieTempLow       FaultCode = 34
	FaultCoolingWaterHigh FaultCode = 40
	FaultVacuumLow        FaultCode = 41
	FaultHaulOffDeviation FaultCode = 50
	FaultSensorFailure    FaultCode = 60
)

// Severity classifies a fault.
type Severity uint8

const (
	SeverityTrip     Severity = 2 // machine to FAULT, drives stop
	SeverityCritical Severity = 3 // FAULT, everything off (emergency stop)
)

func (s Severity) String() string {
	switch s {
	case SeverityTrip:
		return "trip"
	case SeverityCritical:
		return "critical"
	}
	return fmt.Sprintf("Severity(%d)", uint8(s))
}

// Alarm is a bit in Snapshot.Alarms (alarm word, Input Register 26). A bit is
// set while the condition is beyond its warning limit (or, for the emergency
// stop, while it is pressed), independent of the machine state.
type Alarm uint8

const (
	AlarmMotorOverload Alarm = iota
	AlarmMeltPressureHigh
	AlarmZoneTempHigh
	AlarmZoneTempLow
	AlarmCoolingWaterHigh
	AlarmVacuumLow
	AlarmHaulOffDeviation
	AlarmSensorFailure
	AlarmEmergencyStop
	alarmEnd
)

var alarmNames = [...]string{
	"motor_overload", "melt_pressure_high", "zone_temp_high", "zone_temp_low", "cooling_water_high",
	"vacuum_low", "haul_off_deviation", "sensor_failure", "emergency_stop",
}

func (a Alarm) String() string {
	if a < alarmEnd {
		return alarmNames[a]
	}
	return fmt.Sprintf("Alarm(%d)", uint8(a))
}

// Alarms lists all alarm bits in bit order.
func Alarms() []Alarm {
	out := make([]Alarm, alarmEnd)
	for i := range out {
		out[i] = Alarm(i)
	}
	return out
}

// AlarmSet is a bit set of active alarms.
type AlarmSet uint16

// Has reports whether a is active.
func (s AlarmSet) Has(a Alarm) bool { return s&(1<<a) != 0 }

// FaultInfo documents one fault code.
type FaultInfo struct {
	Code       FaultCode
	Name       string
	Severity   Severity
	Alarm      Alarm
	Warning    string // warning (alarm bit) condition
	Trip       string // trip condition
	Recovery   string // what must be true before a reset is accepted
	HeatersOff bool   // equipment impact: heaters switched off in FAULT
}

// Reset requirement and drive impact are the same for all faults: a
// ResetFault command is required (FAULT → STOPPED, then Start), and drives
// (screw, haul-off) and the vacuum pump stop immediately.

// monitor watches one measured quantity.
type monitor struct {
	info     FaultInfo
	high     bool          // true: alarm above limits; false: below
	warn     float64       // warning limit
	trip     float64       // trip limit
	delay    time.Duration // trip limit must be exceeded continuously this long
	gate     func(MachineState, time.Duration) bool
	read     func(*monitorInput) (float64, bool)
	tripCond func(*monitorInput) bool // extra trip condition (nil = none)
	recovery bool                     // reset requires the value within the warning limit

	ticks     uint64 // consecutive ticks beyond the trip limit
	lastValue float64
	lastValid bool
}

type monitorInput struct {
	m         ProcessValues
	sp        Setpoints
	heaterPct [4]float64 // heater output, % of installed power (heater current monitoring)
	reference float64    // haul-off drive reference speed
}

func (m *monitor) beyond(v, limit float64) bool {
	if m.high {
		return v > limit
	}
	return v < limit
}

// Gates.
func always(MachineState, time.Duration) bool          { return true }
func whenRunning(s MachineState, _ time.Duration) bool { return s == Running }
func runningAfter(d time.Duration) func(MachineState, time.Duration) bool {
	return func(s MachineState, t time.Duration) bool { return s == Running && t >= d }
}

var zoneLabels = [4]string{"zone 1", "zone 2", "zone 3", "die"}

func zoneTemp(m ProcessValues, i int) float64 {
	if i == 3 {
		return m.DieTemp
	}
	return m.ZoneTemps[i]
}

func zoneSP(sp Setpoints, i int) float64 {
	if i == 3 {
		return sp.DieTemp
	}
	return sp.ZoneTemps[i]
}

// Limit is the warning/trip configuration of one monitored quantity.
type Limit struct {
	Warning float64       `yaml:"warning"`
	Trip    float64       `yaml:"trip"`
	Delay   time.Duration `yaml:"delay"` // trip limit must be exceeded continuously this long
}

// FaultLimits configures the fault monitors of a line. Zone limits are
// deviations from the setpoint in °C (both given as positive numbers).
type FaultLimits struct {
	MotorOverload    Limit         `yaml:"motor_overload"`     // % of rated motor power, high
	MeltPressureHigh Limit         `yaml:"melt_pressure_high"` // bar, high
	ZoneTempHigh     Limit         `yaml:"zone_temp_high"`     // °C above setpoint, high
	ZoneTempLow      Limit         `yaml:"zone_temp_low"`      // °C below setpoint, low
	CoolingWaterHigh Limit         `yaml:"cooling_water_high"` // °C, high
	VacuumLow        Limit         `yaml:"vacuum_low"`         // mbar, low
	HaulOffDeviation Limit         `yaml:"haul_off_deviation"` // % below the drive reference, high
	SensorMin        float64       `yaml:"sensor_min"`         // °C, plausible temperature range
	SensorMax        float64       `yaml:"sensor_max"`
	SensorDelay      time.Duration `yaml:"sensor_delay"`
}

// DefaultFaultLimits returns the limits documented in docs/faults/fault-table.md.
func DefaultFaultLimits() FaultLimits {
	return FaultLimits{
		MotorOverload:    Limit{95, 110, 5 * time.Second},
		MeltPressureHigh: Limit{300, 350, time.Second},
		ZoneTempHigh:     Limit{15, 30, time.Minute},
		ZoneTempLow:      Limit{10, 20, 10 * time.Second},
		CoolingWaterHigh: Limit{25, 30, 30 * time.Second},
		VacuumLow:        Limit{300, 200, 10 * time.Second},
		HaulOffDeviation: Limit{5, 10, 5 * time.Second},
		SensorMin:        -20, SensorMax: 500, SensorDelay: time.Second,
	}
}

func (l FaultLimits) validate() error {
	var errs []error
	check := func(name string, x Limit, high bool) {
		switch {
		case x.Delay < 0:
			errs = append(errs, fmt.Errorf("fault limit %s: delay must not be negative", name))
		case high && x.Trip < x.Warning:
			errs = append(errs, fmt.Errorf("fault limit %s: trip %g must be ≥ warning %g", name, x.Trip, x.Warning))
		case !high && x.Trip > x.Warning:
			errs = append(errs, fmt.Errorf("fault limit %s: trip %g must be ≤ warning %g", name, x.Trip, x.Warning))
		}
	}
	check("motor_overload", l.MotorOverload, true)
	check("melt_pressure_high", l.MeltPressureHigh, true)
	check("zone_temp_high", l.ZoneTempHigh, true)
	check("zone_temp_low", l.ZoneTempLow, true) // given as positive deviations
	check("cooling_water_high", l.CoolingWaterHigh, true)
	check("vacuum_low", l.VacuumLow, false)
	check("haul_off_deviation", l.HaulOffDeviation, true)
	if l.SensorMin >= l.SensorMax || l.SensorDelay < 0 {
		errs = append(errs, fmt.Errorf("fault limit sensor range: need sensor_min < sensor_max and sensor_delay ≥ 0"))
	}
	return errors.Join(errs...)
}

func secs(d time.Duration) string { return d.String() }

// newMonitors returns the fault monitors in priority order (first trip wins).
func newMonitors(l FaultLimits) []monitor {
	mo, mp, zh, zl, cw, vl, hd := l.MotorOverload, l.MeltPressureHigh, l.ZoneTempHigh, l.ZoneTempLow, l.CoolingWaterHigh, l.VacuumLow, l.HaulOffDeviation
	mons := []monitor{
		{
			info: FaultInfo{Code: FaultMotorOverload, Name: "extruder_motor_overload", Severity: SeverityTrip,
				Alarm: AlarmMotorOverload, Warning: fmt.Sprintf("motor load > %g %%", mo.Warning),
				Trip:     fmt.Sprintf("motor load > %g %% for %s", mo.Trip, secs(mo.Delay)),
				Recovery: fmt.Sprintf("motor load ≤ %g %% (immediate once the drive stopped)", mo.Warning)},
			high: true, warn: mo.Warning, trip: mo.Trip, delay: mo.Delay, gate: always, recovery: true,
			read: func(in *monitorInput) (float64, bool) { return in.m.MotorLoad, true },
		},
		{
			info: FaultInfo{Code: FaultMeltPressureHigh, Name: "melt_pressure_high", Severity: SeverityTrip,
				Alarm: AlarmMeltPressureHigh, Warning: fmt.Sprintf("melt pressure > %g bar", mp.Warning),
				Trip:     fmt.Sprintf("melt pressure > %g bar for %s", mp.Trip, secs(mp.Delay)),
				Recovery: fmt.Sprintf("melt pressure ≤ %g bar", mp.Warning)},
			high: true, warn: mp.Warning, trip: mp.Trip, delay: mp.Delay, gate: always, recovery: true,
			read: func(in *monitorInput) (float64, bool) { return in.m.MeltPressure, true },
		},
		{
			info: FaultInfo{Code: FaultSensorFailure, Name: "sensor_failure", Severity: SeverityTrip,
				Alarm: AlarmSensorFailure, HeatersOff: true,
				Warning:  fmt.Sprintf("any zone/die reading outside %g..%g °C", l.SensorMin, l.SensorMax),
				Trip:     fmt.Sprintf("same for %s", secs(l.SensorDelay)),
				Recovery: "all readings plausible (sensor repaired)"},
			high: true, warn: 0, trip: 0, delay: l.SensorDelay, gate: always, recovery: true,
			read: func(in *monitorInput) (float64, bool) {
				// Distance outside the plausible range; > 0 means implausible.
				worst := -1e9
				for i := range 4 {
					t := zoneTemp(in.m, i)
					worst = max(worst, t-l.SensorMax, l.SensorMin-t)
				}
				return worst, true
			},
		},
	}
	for i := range 4 {
		mons = append(mons, monitor{
			info: FaultInfo{Code: FaultZone1TempHigh + FaultCode(i), Name: fmt.Sprintf("%s_temp_high", zoneKey(i)),
				Severity: SeverityTrip, Alarm: AlarmZoneTempHigh, HeatersOff: true,
				Warning: fmt.Sprintf("%s > SP + %g °C", zoneLabels[i], zh.Warning),
				Trip: fmt.Sprintf("%s > SP + %g °C while its heater still draws current, for %s",
					zoneLabels[i], zh.Trip, secs(zh.Delay)),
				Recovery: fmt.Sprintf("%s ≤ SP + %g °C", zoneLabels[i], zh.Warning)},
			high: true, warn: zh.Warning, trip: zh.Trip, delay: zh.Delay, gate: always, recovery: true,
			read:     func(in *monitorInput) (float64, bool) { return zoneTemp(in.m, i) - zoneSP(in.sp, i), true },
			tripCond: func(in *monitorInput) bool { return in.heaterPct[i] > 0 },
		})
	}
	for i := range 4 {
		mons = append(mons, monitor{
			info: FaultInfo{Code: FaultZone1TempLow + FaultCode(i), Name: fmt.Sprintf("%s_temp_low", zoneKey(i)),
				Severity: SeverityTrip, Alarm: AlarmZoneTempLow,
				Warning: fmt.Sprintf("RUNNING and %s < SP − %g °C", zoneLabels[i], zl.Warning),
				Trip: fmt.Sprintf("RUNNING and %s < SP − %g °C while its heater delivers < 50 %%, for %s",
					zoneLabels[i], zl.Trip, secs(zl.Delay)),
				Recovery: fmt.Sprintf("%s ≥ SP − %g °C (heaters keep running in FAULT)", zoneLabels[i], zl.Warning)},
			high: false, warn: -zl.Warning, trip: -zl.Trip, delay: zl.Delay, gate: whenRunning, recovery: true,
			read:     func(in *monitorInput) (float64, bool) { return zoneTemp(in.m, i) - zoneSP(in.sp, i), true },
			tripCond: func(in *monitorInput) bool { return in.heaterPct[i] < 50 },
		})
	}
	mons = append(mons,
		monitor{
			info: FaultInfo{Code: FaultCoolingWaterHigh, Name: "cooling_water_temp_high", Severity: SeverityTrip,
				Alarm: AlarmCoolingWaterHigh, Warning: fmt.Sprintf("RUNNING and cooling water > %g °C", cw.Warning),
				Trip:     fmt.Sprintf("RUNNING and cooling water > %g °C for %s", cw.Trip, secs(cw.Delay)),
				Recovery: fmt.Sprintf("cooling water ≤ %g °C", cw.Warning)},
			high: true, warn: cw.Warning, trip: cw.Trip, delay: cw.Delay, gate: whenRunning, recovery: true,
			read: func(in *monitorInput) (float64, bool) { return in.m.CoolingWaterTemp, true },
		},
		monitor{
			info: FaultInfo{Code: FaultVacuumLow, Name: "vacuum_low", Severity: SeverityTrip,
				Alarm: AlarmVacuumLow, Warning: fmt.Sprintf("RUNNING ≥ 60 s and vacuum < %g mbar", vl.Warning),
				Trip:     fmt.Sprintf("RUNNING ≥ 60 s and vacuum < %g mbar for %s", vl.Trip, secs(vl.Delay)),
				Recovery: "none (pump is off in FAULT)"},
			high: false, warn: vl.Warning, trip: vl.Trip, delay: vl.Delay, gate: runningAfter(time.Minute),
			read: func(in *monitorInput) (float64, bool) { return in.m.VacuumMagnitude, true },
		},
		monitor{
			info: FaultInfo{Code: FaultHaulOffDeviation, Name: "haul_off_speed_deviation", Severity: SeverityTrip,
				Alarm:    AlarmHaulOffDeviation,
				Warning:  fmt.Sprintf("RUNNING and actual speed > %g %% below the drive reference", hd.Warning),
				Trip:     fmt.Sprintf("RUNNING and deviation > %g %% for %s", hd.Trip, secs(hd.Delay)),
				Recovery: "none (haul-off stopped in FAULT)"},
			high: true, warn: hd.Warning, trip: hd.Trip, delay: hd.Delay, gate: whenRunning,
			read: func(in *monitorInput) (float64, bool) {
				if in.reference < 0.2 {
					return 0, false
				}
				return (in.reference - in.m.HaulOffSpeed) / in.reference * 100, true
			},
		},
	)
	return mons
}

func zoneKey(i int) string {
	if i == 3 {
		return "die"
	}
	return fmt.Sprintf("zone%d", i+1)
}

// emergencyStopInfo documents the emergency stop (code 1), which is driven by
// the maintained E-Stop command rather than a measured value.
var emergencyStopInfo = FaultInfo{
	Code: FaultEmergencyStop, Name: "emergency_stop", Severity: SeverityCritical, Alarm: AlarmEmergencyStop,
	HeatersOff: true, Warning: "E-Stop pressed", Trip: "E-Stop pressed (immediate)",
	Recovery: "E-Stop released",
}

// FaultTable returns every fault in priority order, emergency stop first.
func FaultTable() []FaultInfo {
	out := []FaultInfo{emergencyStopInfo}
	for _, m := range newMonitors(DefaultFaultLimits()) {
		out = append(out, m.info)
	}
	return out
}

// LookupFault returns the description of a fault code.
func LookupFault(code FaultCode) (FaultInfo, bool) {
	for _, f := range FaultTable() {
		if f.Code == code {
			return f, true
		}
	}
	return FaultInfo{}, false
}

// evaluate updates all monitors with this tick's measurements and returns the
// active alarm bits and the first fault whose trip condition has persisted
// for its delay (FaultNone if none).
func evaluate(mons []monitor, in *monitorInput, state MachineState, stateTime, dt time.Duration) (AlarmSet, FaultCode) {
	var alarms AlarmSet
	trip := FaultNone
	for i := range mons {
		m := &mons[i]
		v, valid := m.read(in)
		m.lastValue, m.lastValid = v, valid
		active := valid && m.gate(state, stateTime)
		if active && m.beyond(v, m.warn) {
			alarms |= 1 << m.info.Alarm
		}
		if active && m.beyond(v, m.trip) && (m.tripCond == nil || m.tripCond(in)) {
			m.ticks++
		} else {
			m.ticks = 0
		}
		need := max(uint64(1), uint64((m.delay+dt-1)/dt))
		if trip == FaultNone && m.ticks >= need {
			trip = m.info.Code
		}
	}
	return alarms, trip
}

// blocksReset reports why the latched fault may not be reset yet ("" = ok).
func blocksReset(mons []monitor, code FaultCode) string {
	for i := range mons {
		m := &mons[i]
		if m.info.Code == code && m.recovery && m.lastValid && m.beyond(m.lastValue, m.warn) {
			return fmt.Sprintf("fault %d (%s) condition still present: %s", code, m.info.Name, m.info.Warning)
		}
	}
	return ""
}
