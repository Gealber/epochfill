package backfill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// slowServer serves ranges at a trickle so a transfer is still in flight when the
// test cancels it.
func slowServer(t *testing.T, total int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("content-length", strconv.Itoa(total))
			w.WriteHeader(http.StatusOK)
			return
		}
		var start, end int
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		if end >= total {
			end = total - 1
		}
		w.Header().Set("content-range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)

		chunk := make([]byte, 4096)
		for sent := 0; sent <= end-start; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
}

// TestDownloadHonoursCancel is the Ctrl-C guarantee for the long phase of a run.
// The index downloads are 11 GB and 29 GB; if they ignore cancellation, an
// interrupt appears to hang and the user is stuck waiting out the transfer.
func TestDownloadHonoursCancel(t *testing.T) {
	srv := slowServer(t, int(downloadChunk)*4)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "out.bin")

	c := NewClient(8)
	c.DownloadConcurrency = 2

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := c.DownloadResumable(ctx, srv.URL, path, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download err = %v, want context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancel took %v; download did not abort promptly", elapsed)
	}
}

// TestReadRangeHonoursCancel covers the block-reading phase: a cancelled run must
// not sit waiting on an in-flight ranged GET.
func TestReadRangeHonoursCancel(t *testing.T) {
	srv := slowServer(t, 1<<20)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	c := NewClient(8)
	buf := make([]byte, 1<<20)

	start := time.Now()
	err := c.ReadRange(ctx, srv.URL, 0, buf)
	if err == nil {
		t.Fatal("ReadRange returned nil after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ReadRange took %v to notice cancellation", elapsed)
	}
}

// TestPartialDownloadStaysResumable checks the interaction that matters after a
// Ctrl-C: whatever landed before the interrupt must be recorded, so the retry
// does not start the multi-GB transfer over.
func TestPartialDownloadStaysResumable(t *testing.T) {
	srv := slowServer(t, int(downloadChunk)*3)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "out.bin")

	c := NewClient(8)
	c.DownloadConcurrency = 1

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	_ = c.DownloadResumable(ctx, srv.URL, path, nil)

	// The sidecar must survive so the next run resumes rather than restarts.
	if _, err := openParts(path+partsSuffix, 3); err != nil {
		t.Fatalf("sidecar unusable after interrupt: %v", err)
	}
}
