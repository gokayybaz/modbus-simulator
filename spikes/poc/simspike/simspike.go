// Package simspike is a throwaway Spike 0.3 proof-of-concept: a fixed-timestep,
// wall-clock-independent, seeded simulation core. Not Phase 2 code.
package simspike

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// FirstOrderLag models thermal/mechanical inertia: dy/dt = (u - y)/tau.
// Exact zero-order-hold discretisation keeps it stable for any dt.
type FirstOrderLag struct {
	Tau time.Duration
	Y   float64
}

func (f *FirstOrderLag) Step(u float64, dt time.Duration) float64 {
	alpha := 1 - math.Exp(-dt.Seconds()/f.Tau.Seconds())
	f.Y += alpha * (u - f.Y)
	return f.Y
}

// Command is an input applied at the start of a tick (e.g. from a Modbus write).
type Command struct {
	AtTick uint64
	RPMSet float64
}

// Sim is a tiny two-variable process: screw RPM follows its setpoint with a
// lag; throughput follows RPM with a further lag; the reported value has
// seeded Gaussian sensor noise. State only advances in Step.
type Sim struct {
	Dt         time.Duration
	Tick       uint64
	SimTime    time.Duration
	rng        *rand.Rand
	rpmSet     float64
	rpm        FirstOrderLag
	throughput FirstOrderLag
	Produced   float64 // kg, integrates only true throughput
}

func New(seed uint64, dt time.Duration) *Sim {
	return &Sim{
		Dt:         dt,
		rng:        rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)),
		rpm:        FirstOrderLag{Tau: 3 * time.Second},
		throughput: FirstOrderLag{Tau: 8 * time.Second},
	}
}

type Snapshot struct {
	Tick           uint64
	RPM            float64
	ThroughputMeas float64 // kg/h, with sensor noise
	Produced       float64
}

func (s *Sim) Apply(c Command) { s.rpmSet = c.RPMSet }

func (s *Sim) Step() Snapshot {
	rpm := s.rpm.Step(s.rpmSet, s.Dt)
	tp := s.throughput.Step(rpm*2.5, s.Dt) // 2.5 kg/h per rpm (assumption)
	s.Produced += tp * s.Dt.Hours()
	s.Tick++
	s.SimTime += s.Dt
	meas := math.Max(0, tp+s.rng.NormFloat64()*0.5)
	return Snapshot{Tick: s.Tick, RPM: rpm, ThroughputMeas: meas, Produced: s.Produced}
}

// RunScripted advances n ticks with no wall clock involvement (tests, replay).
func (s *Sim) RunScripted(n uint64, cmds []Command) []Snapshot {
	out := make([]Snapshot, 0, n)
	ci := 0
	for s.Tick < n {
		for ci < len(cmds) && cmds[ci].AtTick == s.Tick {
			s.Apply(cmds[ci])
			ci++
		}
		out = append(out, s.Step())
	}
	return out
}

// RunRealtime paces Step with a ticker; the simulation itself never reads the
// clock, so pacing jitter cannot change results — only when they appear.
// Commands arrive on a channel and are applied at the next tick boundary.
func (s *Sim) RunRealtime(ctx context.Context, cmds <-chan Command, publish func(Snapshot)) {
	t := time.NewTicker(s.Dt)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-cmds:
			s.Apply(c)
		case <-t.C:
			publish(s.Step())
		}
	}
}
