package downloader

import (
	"context"
	"testing"
	"time"
)

func TestFlowGate(t *testing.T) {
	g := newFlowGate(8)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := g.acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// full: a 9th waits until the context ends
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := g.acquire(cctx); err == nil {
		t.Fatal("expected wait to end with ctx error")
	}
	g.release(false, true)
	if lim, fl := g.stats(); lim != 4 || fl != 1 {
		t.Fatalf("after flood: limit %d floods %d", lim, fl)
	}
	g.release(false, true) // same burst: no second cut
	if lim, _ := g.stats(); lim != 4 {
		t.Fatalf("burst cut twice: %d", lim)
	}
	for i := 0; i < 6; i++ {
		g.release(true, false)
	}
	// grows back after enough successes
	for i := 0; i < 40; i++ {
		_ = g.acquire(ctx)
		g.release(true, false)
	}
	if lim, _ := g.stats(); lim <= 4 {
		t.Fatalf("limit did not grow: %d", lim)
	}
}
