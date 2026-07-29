package backfill

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCopyBufSizeDefaultsToIoCopy(t *testing.T) {
	t.Setenv(copyBufEnv, "")
	if got := copyBufSize(); got != defaultCopyBuf {
		t.Fatalf("unset: %d, want the io.Copy default %d", got, defaultCopyBuf)
	}
	t.Setenv(copyBufEnv, "not a number")
	if got := copyBufSize(); got != defaultCopyBuf {
		t.Fatalf("garbage: %d, want the default %d", got, defaultCopyBuf)
	}
}

func TestCopyBufSizeIsClamped(t *testing.T) {
	// Below 4 KiB would make the syscall storm worse, not better; above 64 MiB is
	// only wasted memory. Both ends are clamped rather than rejected so a typo in
	// an env var on a remote box cannot fail a multi-hour run.
	cases := []struct{ set, want string }{
		{"1", "4096"},
		{"4194304", "4194304"},
		{"999999999", "67108864"},
		{"-5", "32768"},
	}
	for _, c := range cases {
		t.Setenv(copyBufEnv, c.set)
		got := copyBufSize()
		if want := atoi(t, c.want); got != want {
			t.Errorf("%s -> %d, want %d", c.set, got, want)
		}
	}
}

// The wrappers must DEFEAT the WriterTo/ReaderFrom fast paths. archive/tar's
// Reader implements WriteTo, so an unwrapped io.CopyBuffer would hand the whole
// transfer to it, ignore the buffer entirely, and record a single read — which
// would make the decode/write split meaningless.
func TestTimedWrappersForceABufferedLoop(t *testing.T) {
	const payload = 300 << 10
	src := &timedReader{r: strings.NewReader(strings.Repeat("x", payload))}
	dst := &timedWriter{w: io.Discard}

	buf := make([]byte, 64<<10)
	n, err := io.CopyBuffer(dst, src, buf)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if n != payload {
		t.Fatalf("copied %d, want %d", n, payload)
	}
	if dst.n != payload {
		t.Fatalf("writer saw %d bytes, want %d", dst.n, payload)
	}
	// A single 300 KiB write would mean the buffer was bypassed.
	if src.ns <= 0 || dst.ns <= 0 {
		t.Fatal("both sides must accumulate time; a zero means the fast path ran")
	}
}

// A WriterTo source must not smuggle itself past the wrapper.
func TestTimedReaderHidesWriteTo(t *testing.T) {
	var src io.Reader = bytes.NewReader([]byte("payload")) // implements WriteTo
	if _, ok := src.(io.WriterTo); !ok {
		t.Skip("bytes.Reader no longer implements WriteTo")
	}
	if _, ok := io.Reader(&timedReader{r: src}).(io.WriterTo); ok {
		t.Fatal("timedReader must NOT expose WriteTo, or the buffer is ignored")
	}
}

func TestThroughputIsZeroWithoutTime(t *testing.T) {
	if got := throughput(1<<20, 0); got != 0 {
		t.Fatalf("throughput with no elapsed time = %v, want 0", got)
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
