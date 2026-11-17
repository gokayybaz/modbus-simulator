package simulation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrQueueFull is returned by Submit when the command queue is full.
var ErrQueueFull = errors.New("simulation: command queue full")

// ErrAlreadyRunning is returned by Run when the runner is already running.
var ErrAlreadyRunning = errors.New("simulation: runner already running")

// CommandSource supplies scripted commands (e.g. a scenario player). The
// runner asks it once per tick, before stepping, for the commands to apply
// at the start of tick (the number the coming step will have). They are
// applied before commands submitted by clients in the same tick.
type CommandSource interface {
	Commands(tick uint64) []Command
}

// RunnerOptions configure a Runner.
type RunnerOptions struct {
	// Source, if set, contributes scripted commands every tick.
	Source CommandSource
	// OnTick is called from the runner goroutine after every step with the
	// new snapshot and the commands applied in that step (e.g. to publish the
	// snapshot to PLC memory and acknowledge command coils). It must not
	// block long.
	OnTick func(s Snapshot, applied []Command)
	// Logger receives command rejections and state transitions.
	Logger *slog.Logger
	// QueueSize bounds commands waiting for the next tick. Default 256.
	QueueSize int
	// Speed is the time-acceleration factor: the runner ticks every
	// Period/Speed of wall time while each step still advances Period of
	// simulated time, so results are identical at any speed. Default 1.
	Speed float64
}

// Stats describe runner pacing (see docs/spikes/004-runner-pacing.md).
type Stats struct {
	Ticks        uint64        // steps executed
	DroppedTicks uint64        // ticks lost because a step ran late (no catch-up)
	LastStep     time.Duration // wall time of the most recent Step + OnTick
}

// Runner paces a Stepper in real time with a ticker. Commands submitted from
// any goroutine are applied at the next tick boundary, in submission order.
type Runner struct {
	sim  Stepper
	opts RunnerOptions
	log  *slog.Logger

	mu    sync.Mutex
	queue []Command

	latest   atomic.Pointer[Snapshot]
	running  atomic.Bool
	ticks    atomic.Uint64
	dropped  atomic.Uint64
	lastStep atomic.Int64
}

// NewRunner returns a runner for sim. The initial snapshot is available
// immediately via Snapshot.
func NewRunner(sim Stepper, opts RunnerOptions) *Runner {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 256
	}
	if opts.Speed <= 0 {
		opts.Speed = 1
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	r := &Runner{sim: sim, opts: opts, log: log}
	s := sim.Snapshot()
	r.latest.Store(&s)
	return r
}

// Submit queues a command for the next tick. It is safe for concurrent use.
//
// Level-type commands are coalesced while queued: a SetSetpoint replaces a
// queued SetSetpoint for the same setpoint, an EmergencyStop replaces a
// queued EmergencyStop. The engine applies these as "last value wins" within
// a tick anyway, so the result is identical, and frequent setpoint writes can
// never fill the queue.
func (r *Runner) Submit(c Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, q := range r.queue {
		if q.Kind == c.Kind && (c.Kind == CmdEmergencyStop || c.Kind == CmdSetScenario ||
			(c.Kind == CmdSetSetpoint && q.Setpoint == c.Setpoint)) {
			r.queue[i] = c
			return nil
		}
	}
	if len(r.queue) >= r.opts.QueueSize {
		return ErrQueueFull
	}
	r.queue = append(r.queue, c)
	return nil
}

// Snapshot returns the latest published snapshot. Safe for concurrent use.
func (r *Runner) Snapshot() Snapshot { return *r.latest.Load() }

// Stats returns pacing statistics. Safe for concurrent use.
func (r *Runner) Stats() Stats {
	return Stats{
		Ticks:        r.ticks.Load(),
		DroppedTicks: r.dropped.Load(),
		LastStep:     time.Duration(r.lastStep.Load()),
	}
}

// Run steps the simulation every Period until ctx is cancelled, then returns
// nil. It starts no other goroutines, so nothing outlives the call.
func (r *Runner) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer r.running.Store(false)

	period := max(time.Duration(float64(r.sim.Period())/r.opts.Speed), 50*time.Microsecond)
	t := time.NewTicker(period)
	defer t.Stop()
	start := time.Now()
	var ticks uint64
	r.log.Info("simulation started", "step", r.sim.Period(), "speed", r.opts.Speed, "tick_every", period, "state", r.Snapshot().State)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("simulation stopped", "ticks", ticks, "sim_time", r.Snapshot().SimTime)
			return nil
		case now := <-t.C:
			r.step()
			ticks++
			r.ticks.Store(ticks)
			r.lastStep.Store(int64(time.Since(now)))
			if expected := uint64(now.Sub(start) / period); expected > ticks {
				r.dropped.Store(expected - ticks)
			}
		}
	}
}

func (r *Runner) step() {
	prev := r.Snapshot()
	var cmds []Command
	if r.opts.Source != nil {
		cmds = append(cmds, r.opts.Source.Commands(prev.Tick+1)...)
	}
	r.mu.Lock()
	cmds = append(cmds, r.queue...)
	r.queue = nil
	r.mu.Unlock()

	snap, err := r.sim.Step(cmds)
	r.latest.Store(&snap)

	if err != nil {
		r.log.Warn("command rejected", "tick", snap.Tick, "err", err)
	}
	if snap.State != prev.State {
		r.log.Info("state changed", "tick", snap.Tick, "sim_time", snap.SimTime,
			"from", prev.State, "to", snap.State, "fault_code", snap.FaultCode)
	}
	if r.opts.OnTick != nil {
		r.opts.OnTick(snap, cmds)
	}
}

// Replay advances e by ticks steps without wall-clock pacing, applying the
// commands of src (if any), and returns every snapshot. It is the offline,
// deterministic counterpart of Runner.Run (tests, scenario validation).
func Replay(e *Engine, src CommandSource, ticks int) []Snapshot {
	out := make([]Snapshot, 0, ticks)
	for range ticks {
		var cmds []Command
		if src != nil {
			cmds = src.Commands(e.Snapshot().Tick + 1)
		}
		s, _ := e.Step(cmds)
		out = append(out, s)
	}
	return out
}
