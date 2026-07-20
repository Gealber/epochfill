package backfill

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// barWidth is the drawn width of the filled/empty track.
const barWidth = 32

// redrawEvery throttles repaints; chunks land far faster than a terminal needs.
const redrawEvery = 100 * time.Millisecond

// ewmaWeight is how much the newest sample moves the rate estimate. High enough
// to follow a link that stalls or recovers, low enough not to jitter per chunk.
const ewmaWeight = 0.3

// Bar is a single-line terminal progress bar for the multi-GB index downloads.
//
// It writes to STDERR so it never interleaves with the zerolog stream on stdout,
// and it degrades to periodic log lines when stdout is not a terminal — a piped
// or nohup'd run would otherwise fill the log with thousands of carriage returns.
type Bar struct {
	label string
	total int64
	start time.Time
	tty   bool

	mu         sync.Mutex
	done       int64
	lastDraw   time.Time
	lastPct    int64
	finished   bool
	recent     float64   // EWMA of recent bytes/sec
	lastSample time.Time // when recent was last folded
	lastBytes  int64     // b.done at that sample
}

// NewBar creates a bar for a transfer of total bytes.
func NewBar(label string, total int64) *Bar {
	return &Bar{
		label: label,
		total: total,
		start: time.Now(),
		tty:   isTerminal(),
	}
}

// Set records absolute progress and repaints if enough time has passed.
func (b *Bar) Set(done int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.done = done
	b.sampleLocked()
	if b.finished {
		return
	}
	if !b.tty {
		b.logLocked()
		return
	}
	if time.Since(b.lastDraw) < redrawEvery && done < b.total {
		return
	}
	b.lastDraw = time.Now()
	b.drawLocked()
}

// Finish paints the completed bar and ends the line.
func (b *Bar) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finished {
		return
	}
	b.finished = true
	if !b.tty {
		return
	}
	b.done = b.total
	b.drawLocked()
	fmt.Fprintln(os.Stderr)
}

func (b *Bar) drawLocked() {
	pct := b.pctLocked()
	filled := int(int64(barWidth) * pct / 100)
	track := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	fmt.Fprintf(os.Stderr, "\r%-22s %s %3d%%  %9s / %-9s  %8s  ETA %-8s",
		truncate(b.label, 22), track, pct,
		humanBytes(b.done), humanBytes(b.total),
		b.rateLocked(), b.etaLocked())
}

// logLocked emits one line per whole percent when there is no terminal.
func (b *Bar) logLocked() {
	pct := b.pctLocked()
	if pct == b.lastPct {
		return
	}
	b.lastPct = pct
	fmt.Fprintf(os.Stderr, "%s %d%% (%s / %s) %s ETA %s\n",
		b.label, pct, humanBytes(b.done), humanBytes(b.total),
		b.rateLocked(), b.etaLocked())
}

func (b *Bar) pctLocked() int64 {
	if b.total <= 0 {
		return 0
	}
	pct := 100 * b.done / b.total
	if pct > 100 {
		return 100
	}
	return pct
}

// bytesPerSecLocked is the transfer rate. It blends the cumulative average with
// the rate since the last sample (EWMA), so the estimate tracks a link that
// speeds up or stalls instead of staying anchored to a slow start.
func (b *Bar) bytesPerSecLocked() float64 {
	elapsed := time.Since(b.start).Seconds()
	if elapsed <= 0 || b.done <= 0 {
		return 0
	}
	overall := float64(b.done) / elapsed
	if b.recent <= 0 {
		return overall
	}
	return ewmaWeight*b.recent + (1-ewmaWeight)*overall
}

// sampleLocked folds the bytes since the previous sample into the recent rate.
func (b *Bar) sampleLocked() {
	now := time.Now()
	if b.lastSample.IsZero() {
		b.lastSample, b.lastBytes = now, b.done
		return
	}
	dt := now.Sub(b.lastSample).Seconds()
	if dt < 0.5 {
		return // too short a window to be meaningful
	}
	rate := float64(b.done-b.lastBytes) / dt
	if b.recent <= 0 {
		b.recent = rate
	} else {
		b.recent = ewmaWeight*rate + (1-ewmaWeight)*b.recent
	}
	b.lastSample, b.lastBytes = now, b.done
}

func (b *Bar) rateLocked() string {
	rate := b.bytesPerSecLocked()
	if rate <= 0 {
		return "--"
	}
	return humanBytes(int64(rate)) + "/s"
}

func (b *Bar) etaLocked() string {
	rate := b.bytesPerSecLocked()
	if rate <= 0 {
		return "--"
	}
	return etaFor(b.total-b.done, rate).Round(time.Second).String()
}

// etaFor converts a byte count and a bytes-per-second rate into a duration.
//
// It is a named function because the inline version got this wrong: a
// seconds-valued float handed to time.Duration is read as NANOSECONDS, so every
// download reported "ETA 0s" from start to finish.
func etaFor(remaining int64, bytesPerSec float64) time.Duration {
	if remaining <= 0 || bytesPerSec <= 0 {
		return 0
	}
	seconds := float64(remaining) / bytesPerSec
	return time.Duration(seconds * float64(time.Second))
}

// isTerminal reports whether stderr is attached to a character device.
func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
