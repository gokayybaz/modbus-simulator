package simspike

import (
	"context"
	"math"
	"runtime"
	"testing"
	"time"
)

const dt = 500 * time.Millisecond

func TestDeterministicReplay(t *testing.T) {
	cmds := []Command{{AtTick: 2, RPMSet: 60}, {AtTick: 100, RPMSet: 80}}
	a := New(42, dt).RunScripted(400, cmds)
	b := New(42, dt).RunScripted(400, cmds)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
	c := New(43, dt).RunScripted(400, cmds)
	if a[399].ThroughputMeas == c[399].ThroughputMeas {
		t.Fatal("different seed should give different noise")
	}
	if a[399].Produced != c[399].Produced {
		t.Fatal("noise must not leak into the true process state")
	}
}

func TestLagNoJumpAndConverges(t *testing.T) {
	f := FirstOrderLag{Tau: 3 * time.Second}
	y1 := f.Step(100, dt)
	if y1 > 20 {
		t.Fatalf("first step jumped to %v", y1)
	}
	// Analytical: after one tau, 63.2% of the step.
	g := FirstOrderLag{Tau: 3 * time.Second}
	for i := 0; i < 6; i++ {
		g.Step(100, dt)
	}
	if math.Abs(g.Y-63.21) > 0.05 {
		t.Fatalf("after 1 tau got %v want ~63.21", g.Y)
	}
}

func TestStoppedProducesNothing(t *testing.T) {
	s := New(1, dt)
	snaps := s.RunScripted(100, nil)
	if snaps[99].Produced != 0 {
		t.Fatalf("produced %v with rpm setpoint 0", snaps[99].Produced)
	}
}

func TestRealtimeRunnerStopsCleanly(t *testing.T) {
	before := runtime.NumGoroutine()
	s := New(7, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	n := 0
	go func() { s.RunRealtime(ctx, make(chan Command), func(Snapshot) { n++ }); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if n == 0 {
		t.Fatal("no ticks executed")
	}
	time.Sleep(10 * time.Millisecond)
	if runtime.NumGoroutine() > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
	}
}
