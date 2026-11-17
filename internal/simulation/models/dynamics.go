// Package models contains the simplified, deterministic process models of a
// pipe extrusion line and the dynamic primitives they are built from.
//
// Models are plain structs advanced by Step(…, dt). They hold no randomness
// (noise is applied to measurements by the simulation package) and never read
// the wall clock. Assumptions and coefficients: docs/spikes/005-process-model.md.
package models

import (
	"math"
	"time"
)

// FirstOrderLag models inertia: dy/dt = (u − y) / Tau.
//
// It uses the exact zero-order-hold discretisation
// y += (1 − e^(−dt/Tau)) · (u − y), which is stable for any dt and never
// overshoots the target. Tau <= 0 means no inertia (y follows u at once).
type FirstOrderLag struct {
	Tau   time.Duration
	Value float64
}

// Step advances the lag by dt towards target and returns the new value.
func (f *FirstOrderLag) Step(target float64, dt time.Duration) float64 {
	if f.Tau <= 0 {
		f.Value = target
		return f.Value
	}
	alpha := 1 - math.Exp(-dt.Seconds()/f.Tau.Seconds())
	f.Value += alpha * (target - f.Value)
	return f.Value
}

// RateLimiter moves Value towards a target by at most Rate units per second,
// modelling drive ramps (e.g. screw speed in rpm/s). Rate <= 0 means no limit.
type RateLimiter struct {
	Rate  float64
	Value float64
}

// Step advances the limiter by dt towards target and returns the new value.
func (r *RateLimiter) Step(target float64, dt time.Duration) float64 {
	if r.Rate <= 0 {
		r.Value = target
		return r.Value
	}
	maxDelta := r.Rate * dt.Seconds()
	r.Value += max(-maxDelta, min(maxDelta, target-r.Value))
	return r.Value
}

// DeadTime delays a signal by a fixed number of samples (transport delay).
type DeadTime struct {
	buf []float64
	pos int
}

// NewDeadTime returns a delay of d at sample period dt (rounded to whole
// samples), initially filled with initial. d < dt/2 means no delay.
func NewDeadTime(d, dt time.Duration, initial float64) *DeadTime {
	n := int((d + dt/2) / dt)
	buf := make([]float64, n)
	for i := range buf {
		buf[i] = initial
	}
	return &DeadTime{buf: buf}
}

// Step pushes x and returns the value from len(delay) samples ago.
func (d *DeadTime) Step(x float64) float64 {
	if len(d.buf) == 0 {
		return x
	}
	out := d.buf[d.pos]
	d.buf[d.pos] = x
	d.pos = (d.pos + 1) % len(d.buf)
	return out
}

// PI is a proportional-integral controller with output limits and
// conditional-integration anti-windup, as used by barrel temperature
// controllers. Output = Kp·e + I, clamped to [Min, Max].
type PI struct {
	Kp, Ti   float64 // gain (output units per error unit) and integral time in seconds
	Min, Max float64
	integral float64
}

// Step returns the controller output for error e over dt.
func (c *PI) Step(e float64, dt time.Duration) float64 {
	p := c.Kp * e
	if c.Ti > 0 {
		next := c.integral + c.Kp/c.Ti*e*dt.Seconds()
		u := p + next
		// Integrate only while the output is not saturated in the direction
		// the error pushes it (prevents windup during heat-up).
		if (u < c.Max || e < 0) && (u > c.Min || e > 0) {
			c.integral = max(c.Min, min(c.Max, next))
		}
	}
	return max(c.Min, min(c.Max, p+c.integral))
}

// Reset clears the integral term (controller switched off).
func (c *PI) Reset() { c.integral = 0 }
