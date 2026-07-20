package backfill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Endpoint is the Old Faithful static archive. Free, no auth, serves byte ranges.
const Endpoint = "https://files.old-faithful.net"

// downloadChunk is the unit of a parallel bulk download. Small enough that many
// are in flight at once (the transfer is latency-bound, not bandwidth-bound) and
// that an interrupted chunk is cheap to refetch.
const downloadChunk int64 = 32 << 20 // 32 MiB

// defaultDownloadConcurrency is how many chunks are fetched at once. A single
// stream measured 7.5-12 MB/s against the archive, so parallelism is what turns
// a 29 GB download from ~48 minutes into ~2.
const defaultDownloadConcurrency = 16

var errShortRead = errors.New("old faithful: short read")

// Client fetches Old Faithful epoch artifacts over HTTP. Plain struct with no
// service-framework lifecycle: this is used from a CLI, not a daemon.
type Client struct {
	http *http.Client

	// DownloadConcurrency is how many chunks a bulk download fetches at once.
	DownloadConcurrency int
}

// NewClient builds a Client sized for maxParallel concurrent requests.
//
// The transport bounds only connection setup and response headers — never the
// body, since a chunk can be tens of MB and a fixed deadline would abort a
// healthy long transfer.
//
// The idle-connection pool MUST be at least as large as the request concurrency.
// Below it, Go closes and reopens connections constantly and every request pays a
// fresh TLS handshake (~65ms measured against the archive), so a higher
// concurrency setting would run SLOWER than a lower one.
func NewClient(maxParallel int) *Client {
	if maxParallel < defaultDownloadConcurrency {
		maxParallel = defaultDownloadConcurrency
	}
	return &Client{
		DownloadConcurrency: defaultDownloadConcurrency,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          maxParallel * 2,
				MaxIdleConnsPerHost:   maxParallel,
				MaxConnsPerHost:       maxParallel,
			},
		},
	}
}

// CarURL is the epoch's CAR file. Never downloaded — read via byte ranges only.
func CarURL(epoch uint64) string {
	return fmt.Sprintf("%s/%d/epoch-%d.car", Endpoint, epoch, epoch)
}

// cidURL is the sidecar holding the CAR's root CID, which every index filename
// embeds.
func cidURL(epoch uint64) string {
	return fmt.Sprintf("%s/%d/epoch-%d.cid", Endpoint, epoch, epoch)
}

// IndexURL builds an index URL. The root CID is part of the filename, so it must
// be fetched first via RootCID.
func IndexURL(epoch uint64, rootCID, name string) string {
	return fmt.Sprintf("%s/%d/epoch-%d-%s-mainnet-%s.index",
		Endpoint, epoch, epoch, rootCID, name)
}

// RootCID fetches the epoch's CAR root CID.
func (c *Client) RootCID(ctx context.Context, epoch uint64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cidURL(epoch), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("root cid: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return "", err
	}
	cid := strings.TrimSpace(string(body))
	if cid == "" {
		return "", errors.New("root cid: empty response")
	}
	return cid, nil
}

// Size returns the object's byte length via HEAD.
func (c *Client) Size(ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("head %s: status %d", url, resp.StatusCode)
	}
	return strconv.ParseInt(resp.Header.Get("content-length"), 10, 64)
}

// ReadRange fetches [start, start+len(into)) into the caller's buffer. It is the
// single primitive every CAR read goes through.
func (c *Client) ReadRange(ctx context.Context, url string, start int64, into []byte) error {
	if len(into) == 0 {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	end := start + int64(len(into)) - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("range %s: status %d", url, resp.StatusCode)
	}
	if _, err := io.ReadFull(resp.Body, into); err != nil {
		return fmt.Errorf("%w: %v", errShortRead, err)
	}
	return nil
}

// DownloadResumable downloads url to path using parallel ranged requests,
// continuing from whatever a previous run finished.
//
// A single stream measured 7.5-12 MB/s against the archive, which would make the
// 11 GB offset index ~18 minutes and the 29 GB GSFA index ~48 minutes. The
// transfer is latency-bound per request, not bandwidth-bound, so chunks are
// fetched concurrently and written at their own offsets.
//
// Progress lives in a "<path>.parts" sidecar — one byte per chunk — because with
// out-of-order writes the file's SIZE no longer says how much is real.
// onProgress is called as chunks land, with (done, total) bytes.
func (c *Client) DownloadResumable(ctx context.Context, url, path string, onProgress func(done, total int64)) error {
	total, err := c.Size(ctx, url)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	nChunks := int((total + downloadChunk - 1) / downloadChunk)
	parts, err := openParts(path+partsSuffix, nChunks)
	if err != nil {
		return err
	}
	defer parts.Close()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// Size the file up front so every chunk can be written at its true offset.
	if fi, err := f.Stat(); err == nil && fi.Size() != total {
		if err := f.Truncate(total); err != nil {
			return err
		}
	}

	todo := parts.pending()
	var done atomic.Int64
	done.Store(int64(nChunks-len(todo)) * downloadChunk)
	if done.Load() > total {
		done.Store(total)
	}
	if len(todo) == 0 {
		if onProgress != nil {
			onProgress(total, total)
		}
		return nil
	}

	return c.runChunks(ctx, url, f, parts, todo, total, &done, onProgress)
}

// runChunks fetches the pending chunks concurrently. The first error wins and
// cancels the rest; already-written chunks stay recorded so a retry resumes.
func (c *Client) runChunks(
	parent context.Context, url string, f *os.File, parts *partsFile, todo []int,
	total int64, done *atomic.Int64, onProgress func(done, total int64),
) error {
	workers := c.DownloadConcurrency
	if workers <= 0 {
		workers = defaultDownloadConcurrency
	}
	if workers > len(todo) {
		workers = len(todo)
	}

	// Derived from the caller so Ctrl-C aborts a multi-GB transfer instead of
	// leaving the user waiting it out.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	jobs := make(chan int)
	errs := make(chan error, workers)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, downloadChunk)
			for idx := range jobs {
				n := downloadChunk
				if rem := total - int64(idx)*downloadChunk; rem < n {
					n = rem
				}
				if err := c.ReadRange(ctx, url, int64(idx)*downloadChunk, buf[:n]); err != nil {
					errs <- fmt.Errorf("chunk %d: %w", idx, err)
					cancel()
					return
				}
				if _, err := f.WriteAt(buf[:n], int64(idx)*downloadChunk); err != nil {
					errs <- fmt.Errorf("chunk %d write: %w", idx, err)
					cancel()
					return
				}
				parts.markDone(idx)
				if err := parts.maybeFlush(f); err != nil {
					errs <- err
					cancel()
					return
				}
				if onProgress != nil {
					onProgress(min(done.Add(n), total), total)
				}
			}
		}()
	}

	for _, idx := range todo {
		select {
		case jobs <- idx:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)

	// Flush the sidecar so an interrupted download resumes rather than restarts.
	if err := parts.flush(f); err != nil {
		return err
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return <-errs
}
