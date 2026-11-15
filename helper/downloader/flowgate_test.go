package downloader

import (
	"context"
	"testing"
	"time"
)

func TestFlowGateHyper(t *testing.T) {
	g := newFlowGate(16, 64)
	ctx := context.Background()
	for i := 0; i < 16; i++ {
		if err := g.acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := g.acquire(cctx); err == nil {
		t.Fatal("window full: expected to wait")
	}
	// two short floods: no cut yet; the third cuts a quarter
	g.release(false, true, time.Second)
	g.release(false, true, time.Second)
	if lim, _ := g.stats(); lim != 16 {
		t.Fatalf("cut too early: %d", lim)
	}
	g.release(false, true, time.Second)
	if lim, _ := g.stats(); lim != 12 {
		t.Fatalf("after 3 floods: %d", lim)
	}
	for i := 0; i < 13; i++ { // the rest of the window ends (neither ok nor flood)
		g.release(false, false, 0)
	}
	// a long flood cuts at once, never below 4
	for i := 0; i < 20; i++ {
		_ = g.acquire(ctx)
		g.release(false, true, 30*time.Second)
	}
	if lim, _ := g.stats(); lim != 4 {
		t.Fatalf("min window: %d", lim)
	}
	// grows by 2 after a full window of successes
	for i := 0; i < 4; i++ {
		_ = g.acquire(ctx)
		g.release(true, false, 0)
	}
	if lim, _ := g.stats(); lim != 6 {
		t.Fatalf("grow: %d", lim)
	}
	for i := 0; i < 2000; i++ {
		_ = g.acquire(ctx)
		g.release(true, false, 0)
	}
	if lim, _ := g.stats(); lim != 64 {
		t.Fatalf("max: %d", lim)
	}
}
