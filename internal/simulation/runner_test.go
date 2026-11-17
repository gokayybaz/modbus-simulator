package simulation

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeStepper records command batches; it optionally sleeps in Step.
type fakeStepper struct {
	period  time.Duration
	mu      sync.Mutex
	batches [][]Command
	tick    uint64
	sleep   map[uint64]time.Duration
}

func (f *fakeStepper) Period() time.Duration { return f.period }
func (f *fakeStepper) Snapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Snapshot{Tick: f.tick}
}
func (f *fakeStepper) Step(cmds []Command) (Snapshot, error) {
	f.mu.Lock()
	f.tick++
	f.batches = append(f.batches, cmds)
	d := f.sleep[f.tick]
	s := Snapshot{Tick: f.tick}
	f.mu.Unlock()
	time.Sleep(d)
	return s, nil
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func startRunner(t *testing.T, r *Runner) (cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			stop()
			if err := <-done; err != nil {
				t.Errorf("Run returned %v", err)
			}
		})
	}
	t.Cleanup(cancel)
	return cancel
}

func TestRunnerDrivesEngine(t *testing.T) {
	before := runtime.NumGoroutine()
	e := newEngine(t, func(c *Config) { c.Period = 5 * time.Millisecond })
	var mu sync.Mutex
	var ticks []uint64
	r := NewRunner(e, RunnerOptions{OnTick: func(s Snapshot, _ []Command) {
		mu.Lock()
		ticks = append(ticks, s.Tick)
		mu.Unlock()
	}})
	if r.Snapshot().Tick != 0 || r.Snapshot().State != Stopped {
		t.Fatal("initial snapshot not published")
	}
	cancel := startRunner(t, r)

	if err := r.Submit(Start()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return r.Snapshot().State == Heating })

	// Concurrent readers while running (exercised under -race).
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				_ = r.Snapshot()
				_ = r.Stats()
			}
		})
	}
	wg.Wait()
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(ticks) == 0 || !slices.IsSorted(ticks) || ticks[0] != 1 {
		t.Fatalf("OnTick ticks %v", ticks)
	}
	if st := r.Stats(); st.Ticks != uint64(len(ticks)) {
		t.Fatalf("stats %+v vs %d OnTick calls", st, len(ticks))
	}
	waitUntil(t, func() bool { return runtime.NumGoroutine() <= before })
}

func TestRunnerBatchesCommandsPerTick(t *testing.T) {
	f := &fakeStepper{period: time.Hour} // never ticks on its own
	r := NewRunner(f, RunnerOptions{})
	for _, c := range []Command{Start(), SetSetpoint(SPExtruderRPM, 60), Stop()} {
		if err := r.Submit(c); err != nil {
			t.Fatal(err)
		}
	}
	r.step()
	r.step()
	if len(f.batches) != 2 {
		t.Fatalf("batches %v", f.batches)
	}
	if !slices.Equal(f.batches[0], []Command{Start(), SetSetpoint(SPExtruderRPM, 60), Stop()}) {
		t.Fatalf("first batch %v", f.batches[0])
	}
	if len(f.batches[1]) != 0 {
		t.Fatalf("queue not drained: %v", f.batches[1])
	}
}

func TestRunnerReportsAppliedCommands(t *testing.T) {
	f := &fakeStepper{period: time.Hour}
	var got [][]Command
	r := NewRunner(f, RunnerOptions{OnTick: func(_ Snapshot, applied []Command) { got = append(got, applied) }})
	_ = r.Submit(Start())
	r.step()
	r.step()
	if len(got) != 2 || !slices.Equal(got[0], []Command{Start()}) || len(got[1]) != 0 {
		t.Fatalf("applied %v", got)
	}
}

func TestRunnerQueueFull(t *testing.T) {
	r := NewRunner(&fakeStepper{period: time.Hour}, RunnerOptions{QueueSize: 2})
	_ = r.Submit(Start())
	_ = r.Submit(Stop())
	if err := r.Submit(Start()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v", err)
	}
	r.step()
	if err := r.Submit(Start()); err != nil {
		t.Fatalf("after drain: %v", err)
	}
}

func TestRunnerRejectsConcurrentRun(t *testing.T) {
	r := NewRunner(&fakeStepper{period: time.Millisecond}, RunnerOptions{})
	startRunner(t, r)
	waitUntil(t, func() bool { return r.Stats().Ticks > 0 })
	if err := r.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v", err)
	}
}

// A slow step loses ticks instead of bursting to catch up (Spike 004).
func TestRunnerCountsDroppedTicks(t *testing.T) {
	f := &fakeStepper{period: 5 * time.Millisecond, sleep: map[uint64]time.Duration{3: 40 * time.Millisecond}}
	r := NewRunner(f, RunnerOptions{})
	cancel := startRunner(t, r)
	waitUntil(t, func() bool { return r.Stats().Ticks >= 8 })
	cancel()
	if st := r.Stats(); st.DroppedTicks == 0 {
		t.Fatalf("no dropped ticks reported: %+v", st)
	}
}

// Speed changes only the wall-clock pacing: 100× runs 100 steps of 100 ms
// (10 s of simulated time) in about 0.1 s.
func TestRunnerSpeed(t *testing.T) {
	f := &fakeStepper{period: 100 * time.Millisecond}
	r := NewRunner(f, RunnerOptions{Speed: 100})
	start := time.Now()
	cancel := startRunner(t, r)
	waitUntil(t, func() bool { return r.Stats().Ticks >= 100 })
	cancel()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("100 ticks at 100× took %v", d)
	}
}

func TestRunnerCoalescesLevelCommands(t *testing.T) {
	f := &fakeStepper{period: time.Hour}
	r := NewRunner(f, RunnerOptions{QueueSize: 3})
	for i := range 1000 {
		if err := r.Submit(SetSetpoint(SPExtruderRPM, float64(i%150))); err != nil {
			t.Fatalf("setpoint %d: %v", i, err)
		}
	}
	_ = r.Submit(EmergencyStop(true))
	_ = r.Submit(SetSetpoint(SPZone1Temp, 200))
	_ = r.Submit(EmergencyStop(false))
	if err := r.Submit(Start()); !errors.Is(err, ErrQueueFull) { // queue holds 3 distinct commands
		t.Fatalf("err = %v", err)
	}
	r.step()
	want := []Command{SetSetpoint(SPExtruderRPM, 999%150), EmergencyStop(false), SetSetpoint(SPZone1Temp, 200)}
	if !slices.Equal(f.batches[0], want) {
		t.Fatalf("batch %v, want %v", f.batches[0], want)
	}
}

// Coalescing gives the same engine result as applying every command.
func TestCoalescingPreservesResult(t *testing.T) {
	cmds := []Command{SetSetpoint(SPExtruderRPM, 60), EmergencyStop(true), SetSetpoint(SPExtruderRPM, 80), EmergencyStop(false)}
	full, _ := New(DefaultConfig())
	a, _ := full.Step(cmds)
	coalesced, _ := New(DefaultConfig())
	r := NewRunner(coalesced, RunnerOptions{})
	for _, c := range cmds {
		_ = r.Submit(c)
	}
	r.step()
	if b := r.Snapshot(); a != b {
		t.Fatalf("\n%+v\n%+v", a, b)
	}
}
