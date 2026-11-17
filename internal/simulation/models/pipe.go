package models

import (
	"errors"
	"math"
	"time"
)

// PipeParams describe the product and the dimension gauge.
type PipeParams struct {
	CalibratorOD     float64 `yaml:"calibrator_od"`      // mm, sizing sleeve (nominal shrinkage included)
	Density          float64 `yaml:"density"`            // kg/m³ of the solid pipe
	HaulOffRampRate  float64 `yaml:"haul_off_ramp_rate"` // (m/min)/s
	MaxVacuumShrink  float64 `yaml:"max_vacuum_shrink"`  // fractional OD loss at zero vacuum (poor sleeve contact)
	NominalVacuum    float64 `yaml:"nominal_vacuum"`     // mbar giving full sleeve contact
	ShrinkPerDegC    float64 `yaml:"shrink_per_deg_c"`   // fractional OD loss per °C of cooling water above RefWaterTemp
	RefWaterTemp     float64 `yaml:"ref_water_temp"`     // °C
	GaugeDistance    float64 `yaml:"gauge_distance"`     // m from die to the ultrasonic/laser gauge
	SampleResolution float64 `yaml:"sample_resolution"`  // m between stored pipe sections (memory bound)
}

func (p PipeParams) Validate() error {
	switch {
	case p.CalibratorOD <= 0, p.Density <= 0, p.HaulOffRampRate <= 0, p.NominalVacuum <= 0:
		return errors.New("pipe: calibrator OD, density, haul-off ramp and nominal vacuum must be positive")
	case p.MaxVacuumShrink < 0 || p.MaxVacuumShrink >= 1, p.ShrinkPerDegC < 0:
		return errors.New("pipe: shrink factors out of range")
	case p.GaugeDistance < 0, p.SampleResolution <= 0:
		return errors.New("pipe: gauge distance must be ≥ 0 and sample resolution > 0")
	}
	return nil
}

// WallFromMassBalance returns the wall thickness (mm) of a pipe with outer
// diameter od (mm) carrying throughput (kg/h) at speed (m/min):
//
//	A = ṁ/(ρ·v) = π·s·(OD − s)  ⇒  s = (OD − √(OD² − 4A/π)) / 2
//
// The result is clamped to OD/2 (solid rod) when the pipe barely moves.
func WallFromMassBalance(throughput, speed, od, density float64) float64 {
	if throughput <= 0 || speed <= 0 || od <= 0 {
		return 0
	}
	areaMM2 := throughput / 60 / (density * speed) * 1e6
	disc := od*od - 4*areaMM2/math.Pi
	if disc <= 0 {
		return od / 2
	}
	return (od - math.Sqrt(disc)) / 2
}

type pipeSection struct {
	pos, od, wall float64
}

// Pipe models the haul-off and the pipe dimensions seen at the gauge.
//
// At the die exit the outer diameter is set by the calibrator, reduced by
// poor vacuum (sleeve contact) and by warm cooling water (post-shrinkage);
// the wall follows from mass conservation. Each section then travels
// GaugeDistance metres along the line before it is measured, so the delay is
// a distance, not a time: if the haul-off stops, the gauge reading freezes.
// Before the first section arrives, the gauge reads 0 (no pipe).
type Pipe struct {
	p         PipeParams
	speed     RateLimiter
	position  float64 // m of pipe pulled since start
	queue     []pipeSection
	Speed     float64 // m/min, actual pipe speed
	Reference float64 // m/min, speed the haul-off drive is commanded to (after ramp)
	ExitOD    float64 // mm at the die/calibrator (not measured)
	ExitWall  float64 // mm at the die/calibrator (not measured)
	OD        float64 // mm at the gauge
	Wall      float64 // mm at the gauge

	// Slip is the fraction by which the pipe moves slower than the drive
	// (fault hook: worn or wet caterpillar pads), 0..1.
	Slip float64
}

func NewPipe(p PipeParams) *Pipe { return &Pipe{p: p, speed: RateLimiter{Rate: p.HaulOffRampRate}} }

// StopNow halts the haul-off drive immediately (safe state).
func (pp *Pipe) StopNow() { pp.speed.Value, pp.Reference, pp.Speed = 0, 0, 0 }

// Step advances the haul-off and the pipe. stopNow halts the haul-off at once.
func (pp *Pipe) Step(targetSpeed, throughput, vacuum, waterTemp float64, stopNow bool, dt time.Duration) {
	if stopNow {
		pp.speed.Value, targetSpeed = 0, 0
	}
	pp.Reference = pp.speed.Step(targetSpeed, dt)
	pp.Speed = pp.Reference * (1 - max(0, min(1, pp.Slip)))

	vacDeficit := max(0, min(1, (pp.p.NominalVacuum-vacuum)/pp.p.NominalVacuum))
	shrink := pp.p.MaxVacuumShrink*vacDeficit + pp.p.ShrinkPerDegC*(waterTemp-pp.p.RefWaterTemp)
	pp.ExitOD = pp.p.CalibratorOD * (1 - max(-0.05, min(0.5, shrink)))
	pp.ExitWall = WallFromMassBalance(throughput, pp.Speed, pp.ExitOD, pp.p.Density)

	moved := pp.Speed / 60 * dt.Seconds()
	if moved <= 0 {
		return
	}
	pp.position += moved
	if n := len(pp.queue); n == 0 || pp.position-pp.queue[n-1].pos >= pp.p.SampleResolution {
		pp.queue = append(pp.queue, pipeSection{pos: pp.position, od: pp.ExitOD, wall: pp.ExitWall})
	}
	// Report the newest section that has reached the gauge.
	gaugePos := pp.position - pp.p.GaugeDistance
	i := 0
	for i < len(pp.queue) && pp.queue[i].pos <= gaugePos {
		pp.OD, pp.Wall = pp.queue[i].od, pp.queue[i].wall
		i++
	}
	if i > 0 {
		pp.queue = pp.queue[i:]
	}
}
