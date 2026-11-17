package simulation

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFaultTableConsistency(t *testing.T) {
	codes, names := map[FaultCode]bool{}, map[string]bool{}
	alarms := map[Alarm]bool{}
	for _, f := range FaultTable() {
		if f.Code == FaultNone || codes[f.Code] || names[f.Name] {
			t.Fatalf("duplicate or zero entry: %+v", f)
		}
		codes[f.Code], names[f.Name] = true, true
		alarms[f.Alarm] = true
		if f.Name == "" || f.Warning == "" || f.Trip == "" || f.Recovery == "" || f.Severity.String() == "" {
			t.Fatalf("incomplete entry: %+v", f)
		}
		if got, ok := LookupFault(f.Code); !ok || got != f {
			t.Fatalf("lookup %d", f.Code)
		}
	}
	if len(codes) != 15 {
		t.Fatalf("%d fault codes", len(codes))
	}
	for _, a := range Alarms() {
		if !alarms[a] {
			t.Fatalf("alarm %v has no fault", a)
		}
	}
	if _, ok := LookupFault(99); ok {
		t.Fatal("unknown code found")
	}
	if Alarm(99).String() != "Alarm(99)" || Severity(9).String() != "Severity(9)" || Scenario(42).String() != "Scenario(42)" {
		t.Fatal("stringers")
	}
}

// The documented fault table lists every code, name and scenario.
func TestFaultTableDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/faults/fault-table.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	for _, f := range FaultTable() {
		if !strings.Contains(text, fmt.Sprintf("| %d | %s |", f.Code, f.Name)) {
			t.Errorf("fault %d %s missing from the fault table document", f.Code, f.Name)
		}
	}
	for s := Scenario(0); s <= MaxScenario; s++ {
		if !strings.Contains(text, fmt.Sprintf("| %d | %s |", s, s)) {
			t.Errorf("scenario %d %s missing from the fault table document", s, s)
		}
	}
}

// Each scenario, from steady production: warning before (or with) the trip,
// the expected fault code within its documented time, safe state, reset
// rules, repair, reset, restart, and counters that resume where they froze.
func TestScenarioChains(t *testing.T) {
	tests := []struct {
		sc         Scenario
		code       FaultCode
		tripAt     time.Duration // documented trip time (±25 %)
		heatersOff bool
		blocks     bool // reset refused right after the trip while the cause persists
	}{
		{ScenarioScrewBlockage, FaultMotorOverload, 40500 * time.Millisecond, false, true},
		{ScenarioScreenClogging, FaultMeltPressureHigh, 59500 * time.Millisecond, false, true},
		{ScenarioHeaterStuckOn, FaultZone2TempHigh, 441500 * time.Millisecond, true, true},
		{ScenarioHeaterFailure, FaultZone1TempLow, 99500 * time.Millisecond, false, true},
		{ScenarioCoolingFailure, FaultCoolingWaterHigh, 193500 * time.Millisecond, false, true},
		{ScenarioVacuumLeak, FaultVacuumLow, 28500 * time.Millisecond, false, false},
		{ScenarioHaulOffSlip, FaultHaulOffDeviation, 10500 * time.Millisecond, false, false},
		{ScenarioThermocoupleOpen, FaultSensorFailure, time.Second, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.sc.String(), func(t *testing.T) {
			e, _ := steady(t)
			if s, err := e.Step([]Command{SetScenario(tt.sc)}); err != nil || s.Scenario != tt.sc {
				t.Fatalf("set scenario: %v %v", s.Scenario, err)
			}
			start := e.Snapshot().SimTime - e.Period()
			var warnAt time.Duration
			s := e.Snapshot()
			for s.State != Fault {
				s, _ = e.Step(nil)
				if warnAt == 0 && s.Alarms.Has(mustInfo(t, tt.code).Alarm) {
					warnAt = s.SimTime - start
				}
				if s.SimTime-start > 20*time.Minute {
					t.Fatal("no trip within 20 min")
				}
			}
			tripAt := s.SimTime - start
			if s.FaultCode != tt.code || s.LastFaultCode != tt.code {
				t.Fatalf("fault %d, want %d", s.FaultCode, tt.code)
			}
			if warnAt == 0 || warnAt > tripAt {
				t.Fatalf("warning at %v, trip at %v", warnAt, tripAt)
			}
			lo, hi := tt.tripAt*3/4, tt.tripAt*5/4
			if tripAt < lo || tripAt > hi {
				t.Fatalf("trip after %v, documented %v", tripAt, tt.tripAt)
			}

			// Detection and output cut happen in the same tick (PLC scan).
			if s.True.ExtruderRPM != 0 || s.True.HaulOffSpeed != 0 {
				t.Fatalf("drives running in the trip tick: %+v", s.True)
			}
			frozenLen, frozenW := s.TotalLength, s.TotalWeight
			s, err := e.Step([]Command{ResetFault()})
			if tt.blocks != (err != nil) || (tt.blocks && s.State != Fault) {
				t.Fatalf("reset right after trip: err=%v state=%v, want blocked=%v", err, s.State, tt.blocks)
			}
			if !tt.blocks {
				e.Step([]Command{Start()}) // fresh fault cycle from STOPPED for uniform checks below
				e.Step([]Command{Stop()})
				e.Step([]Command{TriggerFault(tt.code)})
			}
			heated := e.Snapshot().Internals.HeaterPower
			if off := heated == [4]float64{}; off != tt.heatersOff {
				t.Fatalf("heater powers %v in FAULT, want off=%v", heated, tt.heatersOff)
			}

			// Repair, then reset is accepted once the recovery condition holds.
			e.Step([]Command{SetScenario(ScenarioNone)})
			for i := 0; ; i++ {
				if s, _ = e.Step([]Command{ResetFault()}); s.State == Stopped {
					break
				}
				if i > 7200 {
					t.Fatalf("reset never accepted after repair: %+v", s.Measured)
				}
			}
			if s.FaultCode != FaultNone || s.LastFaultCode != tt.code {
				t.Fatalf("after reset: code %d last %d", s.FaultCode, s.LastFaultCode)
			}
			if s.TotalLength != frozenLen || s.TotalWeight != frozenW {
				t.Fatal("counters changed during FAULT or by the reset")
			}

			// Restart: back to RUNNING, counters continue from the frozen values.
			e.Step([]Command{Start()})
			stepUntil(t, e, 8000, func(s Snapshot) bool { return s.State == Running })
			s = stepN(e, 120)
			if s.State != Running || s.TotalLength <= frozenLen {
				t.Fatalf("restart: state %v length %.2f (frozen %.2f), fault %d", s.State, s.TotalLength, frozenLen, s.FaultCode)
			}
		})
	}
}

func mustInfo(t *testing.T, c FaultCode) FaultInfo {
	t.Helper()
	f, ok := LookupFault(c)
	if !ok {
		t.Fatalf("unknown fault %d", c)
	}
	return f
}

// A cause that is not repaired trips the line again after reset + restart.
func TestUnrepairedCauseTripsAgain(t *testing.T) {
	e, _ := steady(t)
	e.Step([]Command{SetScenario(ScenarioScrewBlockage)})
	stepUntil(t, e, 2000, func(s Snapshot) bool { return s.State == Fault })
	// Reset is refused until the decaying throughput no longer overloads the
	// motor, then accepted although the cause persists.
	for i := 0; ; i++ {
		if s, _ := e.Step([]Command{ResetFault()}); s.State == Stopped {
			break
		}
		if i > 200 {
			t.Fatal("reset never accepted")
		}
	}
	if s, err := e.Step([]Command{Start()}); err != nil || s.State != Heating {
		t.Fatalf("start: %v %v", s.State, err)
	}
	s := stepUntil(t, e, 8000, func(s Snapshot) bool { return s.State == Fault })
	if s.FaultCode != FaultMotorOverload {
		t.Fatalf("second trip %d", s.FaultCode)
	}
}

// Normal operation with legitimate operator changes never trips (2 h).
func TestNoFalseTrips(t *testing.T) {
	e := newEngine(t)
	e.Step([]Command{Start()})
	sched := map[int][]Command{
		3000:  {SetSetpoint(SPExtruderRPM, 150)},
		6000:  {SetSetpoint(SPExtruderRPM, 60), SetSetpoint(SPHaulOffSpeed, 1.5)},
		9000:  {SetSetpoint(SPZone3Temp, 230)}, // raise 30 °C while running
		11000: {SetSetpoint(SPZone3Temp, 170)}, // lower 60 °C while running
		12500: {Stop()},
		12600: {Start()},
	}
	var seen AlarmSet
	for i := range 14400 {
		s, _ := e.Step(sched[i])
		seen |= s.Alarms
		if s.State == Fault {
			t.Fatalf("false trip %d at %v", s.FaultCode, s.SimTime)
		}
	}
	// Expected warnings only: overload pre-alarm at 150 rpm, deviation alarms
	// during the setpoint changes.
	if seen != 1<<AlarmMotorOverload|1<<AlarmZoneTempHigh|1<<AlarmZoneTempLow {
		t.Fatalf("alarms seen %09b", seen)
	}
}

func TestScenarioValidation(t *testing.T) {
	e := newEngine(t)
	if _, err := e.Step([]Command{SetScenario(MaxScenario + 1)}); err == nil || e.Snapshot().Scenario != ScenarioNone {
		t.Fatal("invalid scenario accepted")
	}
	if SetScenario(ScenarioVacuumLeak).String() != "SetScenario(vacuum_leak)" {
		t.Fatal("stringer")
	}
}

// Fault scenarios replay deterministically, including alarm and trip timing.
func TestFaultReplayDeterministic(t *testing.T) {
	sched := map[int][]Command{
		1:    {Start()},
		2400: {SetScenario(ScenarioCoolingFailure)},
		3200: {SetScenario(ScenarioNone)},
		3500: {ResetFault()},
		3501: {Start()},
		5800: {SetScenario(ScenarioScreenClogging)},
		6200: {EmergencyStop(true)},
		6300: {EmergencyStop(false), SetScenario(ScenarioNone)},
		6400: {ResetFault()},
	}
	run := func(seed uint64) []Snapshot {
		e := newEngine(t, func(c *Config) { c.Seed = seed })
		out := make([]Snapshot, 0, 7000)
		for i := 1; i <= 7000; i++ {
			s, _ := e.Step(sched[i])
			out = append(out, s)
		}
		return out
	}
	a, b := run(5), run(5)
	faults := map[FaultCode]bool{}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs", i+1)
		}
		faults[a[i].FaultCode] = true
	}
	if !faults[FaultCoolingWaterHigh] || !faults[FaultMeltPressureHigh] {
		t.Fatalf("script did not trip as intended: %v", faults)
	}
}

// Monitors only look at measured values: the open thermocouple trips even
// though the true zone temperature is normal.
func TestSensorFailureUsesMeasurement(t *testing.T) {
	e, _ := steady(t)
	s, _ := e.Step([]Command{SetScenario(ScenarioThermocoupleOpen)})
	if s.Measured.ZoneTemps[1] != openThermocoupleReading || s.True.ZoneTemps[1] > 200 {
		t.Fatalf("measured %.1f true %.1f", s.Measured.ZoneTemps[1], s.True.ZoneTemps[1])
	}
	s, _ = e.Step(nil)
	if s.State != Fault || s.FaultCode != FaultSensorFailure {
		t.Fatalf("state %v code %d", s.State, s.FaultCode)
	}
}
