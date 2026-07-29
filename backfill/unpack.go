package backfill

import (
	"io"
	"os"
	"strconv"
	"time"
)

// Unpacking a ~47 GiB index is the slowest phase of a run, and it is NOT bound by
// cores or RAM. A single zstd frame decodes sequentially — every block
// back-references the sliding window, so block N+1 cannot start before N — which
// is why the reference zstd CLI has no multithreaded decompression either.
// Decoder concurrency only splits work across FRAMES, and the archive is one.
//
// That leaves two costs we control, and this file measures them separately so we
// stop guessing which one to attack:
//
//   - DECODE: time spent inside the zstd reader, one core, unavoidable.
//   - WRITE:  time spent in write syscalls, plus the syscall count itself.
//
// io.Copy alternates the two in a single goroutine, so while a write is in flight
// the CPU idles and vice versa. If the split comes back write-heavy, overlapping
// them (and/or a bigger buffer) is worth doing; if it comes back decode-heavy,
// neither is, and the only real lever left is not making two passes over the
// archive at all.

// defaultCopyBuf is io.Copy's own buffer size. Kept as the default so an
// instrumented run measures the EXISTING behaviour rather than a changed one.
const defaultCopyBuf = 32 << 10

// copyBufEnv overrides it. An env knob rather than a rebuild because the machines
// this runs on carry no Go toolchain, so one cross-built binary has to be able to
// test both sizes in a single session.
const copyBufEnv = "EPOCHFILL_COPY_BUF"

// copyBufSize returns the unpack buffer size, clamped to something sane. A value
// under 4 KiB would make the syscall storm worse, and past 64 MiB it is only
// wasting memory.
func copyBufSize() int {
	n, err := strconv.Atoi(os.Getenv(copyBufEnv))
	if err != nil || n <= 0 {
		return defaultCopyBuf
	}
	return min(max(n, 4<<10), 64<<20)
}

// timedReader accumulates time spent producing bytes. Wrapping also DEFEATS
// io.CopyBuffer's WriterTo fast path on purpose: archive/tar's Reader implements
// WriteTo, which would silently ignore our buffer and hide the split we are
// trying to measure.
type timedReader struct {
	r  io.Reader
	ns int64
}

func (t *timedReader) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := t.r.Read(p)
	t.ns += int64(time.Since(start))
	return n, err
}

// timedWriter accumulates time spent in writes, and likewise hides *os.File's
// ReaderFrom so the copy stays an explicit buffered loop.
type timedWriter struct {
	w  io.Writer
	ns int64
	n  int64
}

func (t *timedWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := t.w.Write(p)
	t.ns += int64(time.Since(start))
	t.n += int64(n)
	return n, err
}

// throughput is bytes per second as MiB/s, 0 when no time was spent.
func throughput(bytes, ns int64) float64 {
	if ns <= 0 {
		return 0
	}
	return float64(bytes) / (1 << 20) / (float64(ns) / float64(time.Second))
}
