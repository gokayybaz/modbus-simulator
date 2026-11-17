package rca

import (
	"math"
	"testing"
	"time"

	"modbus-dashboard/internal/catalog"
)

// steady returns a RUNNING sample with small deterministic noise.
func steady(t0 time.Time, i int) Sample {
	n := math.Sin(float64(i)*1.7) * 0.3
	v := map[string]float64{
		"extruder_rpm": 100 + n*0.2, "motor_current": 180 + n, "motor_load": 70 + n, "melt_pressure": 210 + n*2,
		"melt_temp": 205 + n, "zone1_temp": 180 + n, "zone2_temp": 190 + n, "zone3_temp": 200 + n, "die_temp": 205 + n,
		"vacuum": 400 + n*3, "cooling_water_temp": 15 + n*0.1, "haul_off_speed": 1.2, "pipe_outer_diameter": 110,
		"pipe_wall_thickness": 10, "throughput": 300 + n, "power_consumption": 150 + n,
		"extruder_rpm_sp": 100, "zone1_temp_sp": 180, "zone2_temp_sp": 190, "zone3_temp_sp": 200, "die_temp_sp": 205,
		"haul_off_speed_sp": 1.2,
	}
	return Sample{T: t0.Add(time.Duration(i) * time.Second), Sim: int64(i), State: 2, V: v}
}

func build(n, injectAt, trip int, mutate func(s *Sample, k int), scenario, code int) Input {
	t0 := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	var s []Sample
	for i := 0; i < n; i++ {
		x := steady(t0, i)
		if i >= injectAt {
			x.FaultScenario = scenario
			if i < trip {
				mutate(&x, i-injectAt)
			}
		}
		if i >= trip {
			x.State, x.ActiveFault = 3, code
			x.V["extruder_rpm"] = 0
		}
		s = append(s, x)
	}
	return Input{UnitID: 1, FaultCode: code, TripTime: s[trip].T, Samples: s}
}

func TestScreenPackClogging(t *testing.T) {
	in := build(120, 60, 100, func(s *Sample, k int) {
		s.V["melt_pressure"] += float64(k) * 4 // monotone pressure rise
		s.V["motor_load"] += float64(k) * 0.1
		if k > 25 {
			s.AlarmWord |= 1 << 1
		}
	}, 2, 11)
	r := Analyze(in)
	if len(r.Hypotheses) == 0 || r.Hypotheses[0].Cause.ID != "screen_pack_clogging" {
		t.Fatalf("top hypothesis = %+v", r.Hypotheses)
	}
	if r.GroundTruth == nil || !r.GroundTruth.Match {
		t.Fatalf("ground truth not matched: %+v", r.GroundTruth)
	}
	var kinds []string
	for _, c := range r.Chain {
		kinds = append(kinds, c.Kind)
	}
	if kinds[len(kinds)-2] != "trip" || kinds[len(kinds)-1] != "effect" {
		t.Fatalf("chain order: %v", kinds)
	}
	first := ""
	for _, c := range r.Chain {
		if c.Kind == "deviation" {
			first = c.Signal
			break
		}
	}
	if first != "melt_pressure" {
		t.Fatalf("first deviation = %q", first)
	}
}

func TestScrewBlockageBeatsClogging(t *testing.T) {
	in := build(120, 60, 100, func(s *Sample, k int) {
		s.V["motor_load"] += float64(k) * 1.0
		s.V["motor_current"] += float64(k) * 2.5
		s.V["power_consumption"] += float64(k) * 1.5
	}, 1, 10)
	r := Analyze(in)
	if r.Hypotheses[0].Cause.ID != "screw_blockage" {
		t.Fatalf("top = %s (%v)", r.Hypotheses[0].Cause.ID, r.Hypotheses[0].Fit)
	}
}

func TestThermocoupleOpen(t *testing.T) {
	in := build(80, 60, 62, func(s *Sample, k int) { s.V["zone2_temp"] = 999.9 }, 8, 60)
	r := Analyze(in)
	if r.Hypotheses[0].Cause.ID != "thermocouple_open" {
		t.Fatalf("top = %s", r.Hypotheses[0].Cause.ID)
	}
}

func TestSetpointChangeDetected(t *testing.T) {
	in := build(120, 70, 100, func(s *Sample, k int) {
		s.V["zone1_temp_sp"] = 220
		s.V["zone1_temp"] += float64(k) * 1.5
	}, 0, 21)
	r := Analyze(in)
	if r.Hypotheses[0].Cause.ID != "temp_setpoint_change" {
		t.Fatalf("top = %s", r.Hypotheses[0].Cause.ID)
	}
	if r.GroundTruth != nil {
		t.Fatal("no ground truth expected")
	}
}

func TestEveryFaultHasCauses(t *testing.T) {
	for _, f := range catalog.Faults {
		n := 0
		for _, c := range catalog.Causes {
			if contains(c.Faults, f.Code) {
				n++
			}
		}
		if n == 0 {
			t.Errorf("fault %d has no causes", f.Code)
		}
	}
	for _, sc := range catalog.Scenarios[1:] {
		found := false
		for _, c := range catalog.Causes {
			if c.ID == sc.Cause && contains(c.Faults, sc.FaultCode) {
				found = true
			}
		}
		if !found {
			t.Errorf("scenario %s: cause %s not linked to fault %d", sc.Name, sc.Cause, sc.FaultCode)
		}
	}
}

func TestEmptyAndShortInputs(t *testing.T) {
	_ = Analyze(Input{FaultCode: 11})
	in := build(3, 1, 0, func(*Sample, int) {}, 0, 11)
	_ = Analyze(in)
	in = build(3, 1, 2, func(*Sample, int) {}, 0, 11)
	_ = Analyze(in)
}

// Coarse polling: the cause starts and trips between two samples.
func TestCoarseSamplingInfersTripSignal(t *testing.T) {
	in := build(40, 30, 30, func(*Sample, int) {}, 7, 50)
	r := Analyze(in)
	if r.Hypotheses[0].Cause.ID != "haul_off_slip" || r.Hypotheses[0].Fit < 0.5 {
		t.Fatalf("top = %s fit %.2f", r.Hypotheses[0].Cause.ID, r.Hypotheses[0].Fit)
	}
	if !r.GroundTruth.Match {
		t.Fatal("ground truth should match")
	}
}
