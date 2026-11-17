package simulation

import (
	"hash/fnv"
	"math"
	"math/rand/v2"
)

// newStream returns a deterministic random stream for (seed, name). Each
// sensor gets its own stream, so adding or removing a sensor never shifts the
// noise sequence of another one (ADR-0004).
func newStream(seed uint64, name string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(name))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}

// NoiseSpec describes the measurement error of one sensor.
type NoiseSpec struct {
	// Sigma is the standard deviation of white Gaussian noise.
	Sigma float64 `yaml:"sigma"`
	// DriftStep is the per-measurement standard deviation of a slow random
	// walk added to the reading; DriftMax bounds its magnitude.
	DriftStep float64 `yaml:"drift_step"`
	DriftMax  float64 `yaml:"drift_max"`
	// NonNegative clamps readings at zero (physical magnitudes).
	NonNegative bool `yaml:"non_negative"`
	// ZeroAtZero reports exactly 0 when the true value is 0, like a speed
	// encoder or flow meter at standstill.
	ZeroAtZero bool `yaml:"zero_at_zero"`
}

// Sensor turns true process values into measurements. Noise is applied to
// the reading only; it never feeds back into the process state.
type Sensor struct {
	spec  NoiseSpec
	rng   *rand.Rand
	drift float64
}

func newSensor(seed uint64, name string, spec NoiseSpec) *Sensor {
	return &Sensor{spec: spec, rng: newStream(seed, name)}
}

// Measure returns one reading of trueValue. It draws the same number of
// random numbers on every call, whatever the value, so the sequence stays
// aligned across process states.
func (s *Sensor) Measure(trueValue float64) float64 {
	walk := s.rng.NormFloat64()
	white := s.rng.NormFloat64()
	if s.spec.DriftMax > 0 {
		s.drift = max(-s.spec.DriftMax, min(s.spec.DriftMax, s.drift+walk*s.spec.DriftStep))
	}
	if s.spec.ZeroAtZero && trueValue == 0 {
		return 0
	}
	v := trueValue + s.drift + white*s.spec.Sigma
	if s.spec.NonNegative {
		v = math.Max(0, v)
	}
	return v
}
