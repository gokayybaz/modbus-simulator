package simulation

import (
	"math"
	"testing"
)

func TestStreamDeterminism(t *testing.T) {
	seq := func(seed uint64, name string) [5]float64 {
		r := newStream(seed, name)
		var out [5]float64
		for i := range out {
			out[i] = r.Float64()
		}
		return out
	}
	if seq(7, "a") != seq(7, "a") {
		t.Fatal("same seed and name must repeat")
	}
	if seq(7, "a") == seq(7, "b") {
		t.Fatal("different names must give different streams")
	}
	if seq(7, "a") == seq(8, "a") {
		t.Fatal("different seeds must give different streams")
	}
}

func TestSensorStatistics(t *testing.T) {
	s := newSensor(1, "test", NoiseSpec{Sigma: 2})
	const n = 50000
	var sum, sq float64
	for range n {
		e := s.Measure(100) - 100
		sum += e
		sq += e * e
	}
	mean := sum / n
	sd := math.Sqrt(sq/n - mean*mean)
	if math.Abs(mean) > 0.05 || math.Abs(sd-2) > 0.05 {
		t.Fatalf("mean %.4f sd %.4f, want ~0 and ~2", mean, sd)
	}
}

func TestSensorDriftBounded(t *testing.T) {
	s := newSensor(3, "drift", NoiseSpec{DriftStep: 0.2, DriftMax: 0.5})
	sawDrift := false
	for range 10000 {
		d := s.Measure(10) - 10
		if math.Abs(d) > 0.5+1e-12 {
			t.Fatalf("drift %v exceeds bound", d)
		}
		sawDrift = sawDrift || math.Abs(d) > 0.4
	}
	if !sawDrift {
		t.Fatal("drift never approached its bound")
	}
}

func TestSensorClamps(t *testing.T) {
	nn := newSensor(1, "nn", NoiseSpec{Sigma: 5, NonNegative: true})
	zz := newSensor(1, "zz", NoiseSpec{Sigma: 5, DriftStep: 1, DriftMax: 3, ZeroAtZero: true})
	for range 5000 {
		if v := nn.Measure(0.1); v < 0 {
			t.Fatalf("negative reading %v", v)
		}
		if v := zz.Measure(0); v != 0 {
			t.Fatalf("standstill reading %v", v)
		}
	}
	if zz.Measure(10) == 10 {
		t.Fatal("ZeroAtZero must not suppress noise on non-zero values")
	}
}

// Measure consumes the same random numbers whatever the value, so a sensor's
// sequence does not depend on the process trajectory.
func TestSensorStreamAlignment(t *testing.T) {
	spec := NoiseSpec{Sigma: 1, ZeroAtZero: true}
	a, b := newSensor(9, "x", spec), newSensor(9, "x", spec)
	for range 100 {
		a.Measure(0) // suppressed readings
		b.Measure(5)
	}
	if a.Measure(5) != b.Measure(5) {
		t.Fatal("streams diverged after suppressed readings")
	}
}
