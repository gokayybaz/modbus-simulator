package models

import (
	"math"
	"testing"
	"time"
)

func TestFirstOrderLag(t *testing.T) {
	const dt = 500 * time.Millisecond
	tests := []struct {
		name    string
		tau     time.Duration
		from    float64
		target  float64
		steps   int
		want    float64
		epsilon float64
	}{
		// After one time constant a step response reaches 1 − 1/e ≈ 63.21 %.
		{"1 tau heating", 3 * time.Second, 0, 100, 6, 100 * (1 - math.Exp(-1)), 1e-9},
		{"3 tau heating", 3 * time.Second, 0, 100, 18, 100 * (1 - math.Exp(-3)), 1e-9},
		{"1 tau cooling", 120 * time.Second, 200, 20, 240, 20 + 180*math.Exp(-1), 1e-9},
		{"no inertia", 0, 5, 42, 1, 42, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := FirstOrderLag{Tau: tt.tau, Value: tt.from}
			for range tt.steps {
				f.Step(tt.target, dt)
			}
			if math.Abs(f.Value-tt.want) > tt.epsilon {
				t.Fatalf("got %.12f want %.12f", f.Value, tt.want)
			}
		})
	}
}

// The exact discretisation must be independent of the step size: 10 steps of
// 50 ms equal one step of 500 ms.
func TestFirstOrderLagStepSizeInvariant(t *testing.T) {
	a := FirstOrderLag{Tau: 2 * time.Second}
	b := FirstOrderLag{Tau: 2 * time.Second}
	for range 10 {
		a.Step(100, 50*time.Millisecond)
	}
	b.Step(100, 500*time.Millisecond)
	if math.Abs(a.Value-b.Value) > 1e-9 {
		t.Fatalf("%v vs %v", a.Value, b.Value)
	}
}

func TestFirstOrderLagNeverOvershoots(t *testing.T) {
	f := FirstOrderLag{Tau: 100 * time.Millisecond}
	for range 1000 {
		if v := f.Step(50, time.Second); v > 50 {
			t.Fatalf("overshoot %v", v)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	r := RateLimiter{Rate: 5} // units per second
	for i := 1; i <= 18; i++ {
		v := r.Step(45, 500*time.Millisecond)
		if want := math.Min(45, float64(i)*2.5); v != want {
			t.Fatalf("step %d: %v want %v", i, v, want)
		}
	}
	if r.Step(45, time.Second) != 45 {
		t.Fatal("did not hold target")
	}
	r.Step(0, 2*time.Second)
	if r.Value != 35 {
		t.Fatalf("ramp down: %v", r.Value)
	}
	unlimited := RateLimiter{}
	if unlimited.Step(99, time.Millisecond) != 99 {
		t.Fatal("rate 0 should not limit")
	}
}
