package backfill

import (
	"testing"
	"time"
)

// TestETAForUsesSeconds pins the unit conversion. The first implementation fed
// a seconds-valued float straight into time.Duration, which reads it as
// NANOSECONDS — so a multi-hour download reported "ETA 0s" the whole way.
func TestETAForUsesSeconds(t *testing.T) {
	cases := []struct {
		name      string
		remaining int64
		rate      float64 // bytes/sec
		want      time.Duration
	}{
		{"10GiB at 21MB/s", 10 << 30, 21 << 20, 8*time.Minute + 8*time.Second},
		{"1GiB at 10MB/s", 1 << 30, 10 << 20, 102 * time.Second},
		{"nothing left", 0, 21 << 20, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := etaFor(tc.remaining, tc.rate).Round(time.Second)
			if diff := got - tc.want; diff > 2*time.Second || diff < -2*time.Second {
				t.Fatalf("etaFor = %v, want ~%v", got, tc.want)
			}
		})
	}
}

// TestETAForHandlesNoProgress guards the divide-by-zero at startup, before any
// bytes have landed.
func TestETAForHandlesNoProgress(t *testing.T) {
	if got := etaFor(1<<30, 0); got != 0 {
		t.Fatalf("etaFor with zero rate = %v, want 0", got)
	}
	if got := etaFor(-5, 1000); got != 0 {
		t.Fatalf("etaFor with negative remaining = %v, want 0", got)
	}
}

// TestBarETAIsNonZeroWhileRunning is the regression the user actually hit: a bar
// with real progress and real elapsed time must not report 0s.
func TestBarETAIsNonZeroWhileRunning(t *testing.T) {
	b := NewBar("test", 10<<30)
	b.start = time.Now().Add(-60 * time.Second) // 60s elapsed
	b.Set(1 << 30)                              // 1 GiB of 10 GiB done

	b.mu.Lock()
	eta := b.etaLocked()
	b.mu.Unlock()

	if eta == "0s" || eta == "--" {
		t.Fatalf("eta = %q, want a real estimate (9 GiB left at ~17 MiB/s)", eta)
	}
}
