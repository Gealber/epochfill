package backfill

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// rangeServer serves body with byte-range support, counting requests. It stands
// in for files.old-faithful.net so the download logic is testable offline.
func rangeServer(t *testing.T, body []byte, reqs *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqs != nil {
			reqs.Add(1)
		}
		w.Header().Set("accept-ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("content-length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Write(body)
			return
		}
		var start, end int
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
			t.Errorf("bad range header %q", rng)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if end >= len(body) {
			end = len(body) - 1
		}
		w.Header().Set("content-range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start : end+1])
	}))
}

// randomBody builds a payload big enough to span several download chunks.
func randomBody(t *testing.T, chunks float64) []byte {
	t.Helper()
	b := make([]byte, int(float64(downloadChunk)*chunks))
	rng := rand.New(rand.NewSource(1))
	rng.Read(b)
	return b
}

// TestDownloadParallelMatchesSource is the core guarantee: chunks land out of
// order at their own offsets, so the assembled file must still be byte-identical.
func TestDownloadParallelMatchesSource(t *testing.T) {
	body := randomBody(t, 3.5)
	srv := rangeServer(t, body, nil)
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "out.bin")
	c := NewClient(8)
	c.DownloadConcurrency = 8

	if err := c.DownloadResumable(context.Background(), srv.URL, path, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded %d bytes, want %d, content differs", len(got), len(body))
	}
	// A complete download drops its sidecar.
	if _, err := os.Stat(path + partsSuffix); !os.IsNotExist(err) {
		t.Errorf("parts sidecar should be removed after a complete download")
	}
}

// TestDownloadResumesFromSidecar checks that an interrupted transfer refetches
// only the missing chunks. Without the sidecar, out-of-order writes would make
// the file's size meaningless and force a full restart.
func TestDownloadResumesFromSidecar(t *testing.T) {
	body := randomBody(t, 4)
	nChunks := len(body) / int(downloadChunk)

	var reqs atomic.Int64
	srv := rangeServer(t, body, &reqs)
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")

	// Simulate a run that completed all but the last chunk.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(len(body))); err != nil {
		t.Fatal(err)
	}
	done := int(downloadChunk) * (nChunks - 1)
	if _, err := f.WriteAt(body[:done], 0); err != nil {
		t.Fatal(err)
	}
	f.Close()

	state := bytes.Repeat([]byte{1}, nChunks)
	state[nChunks-1] = 0
	if err := os.WriteFile(path+partsSuffix, state, 0o644); err != nil {
		t.Fatal(err)
	}

	reqs.Store(0)
	c := NewClient(8)
	c.DownloadConcurrency = 4
	if err := c.DownloadResumable(context.Background(), srv.URL, path, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("resumed file does not match source")
	}
	// One HEAD for the size plus exactly one GET for the single missing chunk.
	if n := reqs.Load(); n > 2 {
		t.Errorf("resume made %d requests, want <=2 (should refetch only 1 chunk)", n)
	}
}

// TestPartsRejectsStaleSidecar guards against a sidecar left by a DIFFERENT
// object. Trusting it would mark chunks complete that were never written, and the
// index reader would be handed a file full of holes.
func TestPartsRejectsStaleSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin.parts")

	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 99), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := openParts(path, 5) // disagrees with the 99 recorded chunks
	if err != nil {
		t.Fatal(err)
	}
	if got := len(p.pending()); got != 5 {
		t.Fatalf("stale sidecar was trusted: %d chunks pending, want 5", got)
	}
}

// TestPartsKeepsSidecarWhenIncomplete ensures a partial download stays resumable.
func TestPartsKeepsSidecarWhenIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.parts")
	p, err := openParts(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	p.markDone(0)
	p.markDone(1)
	if err := p.flush(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("sidecar must survive an incomplete download so it can resume")
	}

	reopened, err := openParts(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	pending := reopened.pending()
	if len(pending) != 2 || pending[0] != 2 || pending[1] != 3 {
		t.Fatalf("pending = %v, want [2 3]", pending)
	}
}

// TestSafeJoinRejectsTraversal covers the tar extraction path: a crafted archive
// must not be able to write outside the destination directory.
func TestSafeJoinRejectsTraversal(t *testing.T) {
	dst := "/tmp/dest"
	for _, name := range []string{"../evil", "../../etc/passwd", "/etc/passwd"} {
		got, err := safeJoin(dst, name)
		if err == nil && !strings.HasPrefix(got, dst) {
			t.Errorf("safeJoin(%q) escaped to %q", name, got)
		}
	}
	if _, err := safeJoin(dst, "good/file.index"); err != nil {
		t.Errorf("safeJoin rejected a legitimate entry: %v", err)
	}
}
