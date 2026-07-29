package backfill

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/klauspost/compress/zstd"
	"github.com/rpcpool/yellowstone-faithful/gsfa"
	"github.com/rs/zerolog/log"
)

// GsfaURL is the epoch's address index. Unlike the other indexes its filename
// carries no root CID.
func GsfaURL(epoch uint64) string {
	return fmt.Sprintf("%s/%d/epoch-%d-gsfa.index.tar.zstd", Endpoint, epoch, epoch)
}

// doneMarker is written once extraction finishes. Its absence means a previous
// run died mid-extract and the directory cannot be trusted.
const doneMarker = ".extracted"

// GsfaIndex is the local address index: pubkey -> the transactions touching it.
//
// This is what removes the RPC dependency. It is also the expensive artifact:
// ~29 GB compressed, ~54 GB extracted, and it MUST be local — the reader calls
// isDir() and yellowstone-faithful explicitly rejects a remote URI for it, so
// unlike every other index it cannot be range-queried.
type GsfaIndex struct {
	reader *gsfa.GsfaReader
	dir    string
}

// OpenGsfa downloads (resuming if partial) and extracts the epoch's GSFA index
// into dir, then opens it. The tarball is deleted once extracted.
func OpenGsfa(ctx context.Context, c *Client, epoch uint64, dir string) (*GsfaIndex, error) {
	root := filepath.Join(dir, fmt.Sprintf("gsfa-%d", epoch))

	indexDir, err := ensureExtracted(ctx, c, epoch, root)
	if err != nil {
		return nil, err
	}
	reader, err := gsfa.NewGsfaReader(indexDir)
	if err != nil {
		return nil, fmt.Errorf("open gsfa: %w", err)
	}
	reader.SetEpoch(epoch)
	log.Info().Str("dir", indexDir).Msg("gsfa index ready")
	return &GsfaIndex{reader: reader, dir: root}, nil
}

// ensureExtracted returns the indexdir path, downloading and unpacking first if
// it is not already present and complete.
func ensureExtracted(ctx context.Context, c *Client, epoch uint64, root string) (string, error) {
	if dir, ok := findIndexDir(root); ok {
		log.Info().Str("dir", dir).Msg("gsfa already extracted")
		return dir, nil
	}

	tarball := root + ".tar.zstd"
	url := GsfaURL(epoch)

	// Download to disk first rather than piping the HTTP body straight into the
	// extractor: a 29 GB transfer WILL be interrupted, and only a file on disk can
	// resume. Peak footprint is tarball + extracted, freed as soon as it unpacks.
	var bar *Bar
	err := c.DownloadResumable(ctx, url, tarball, func(done, total int64) {
		if bar == nil {
			bar = NewBar("gsfa index", total)
		}
		bar.Set(done)
	})
	if err != nil {
		return "", fmt.Errorf("download gsfa: %w", err)
	}
	if bar != nil {
		bar.Finish()
	}
	log.Info().Msg("extracting gsfa index (~47 GiB, several minutes)")

	if err := extractTarZstd(tarball, root); err != nil {
		return "", fmt.Errorf("extract gsfa: %w", err)
	}
	if err := os.Remove(tarball); err != nil {
		log.Warn().Err(err).Msg("remove gsfa tarball")
	}

	dir, ok := findIndexDir(root)
	if !ok {
		return "", fmt.Errorf("no *.indexdir found under %s after extraction", root)
	}
	return dir, nil
}

// findIndexDir locates the extracted *.indexdir, but only if extraction was
// marked complete.
func findIndexDir(root string) (string, bool) {
	if _, err := os.Stat(filepath.Join(root, doneMarker)); err != nil {
		return "", false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".indexdir") {
			return filepath.Join(root, e.Name()), true
		}
	}
	return "", false
}

// extractTarZstd unpacks a zstd-compressed tar into dst, streaming so the
// decompressed tar is never materialised.
func extractTarZstd(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, hdr.Size); err != nil {
				return err
			}
		}
	}
	return os.WriteFile(filepath.Join(dst, doneMarker), []byte("ok"), 0o644)
}

// safeJoin rejects tar entries that would escape the destination directory.
func safeJoin(dst, name string) (string, error) {
	target := filepath.Join(dst, filepath.Clean("/"+name))
	if !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) &&
		target != filepath.Clean(dst) {
		return "", fmt.Errorf("tar entry escapes destination: %q", name)
	}
	return target, nil
}

// barThreshold is the file size above which extraction draws a progress bar.
// The GSFA tarball is one ~47 GiB file plus two tiny ones, so without this the
// run prints "extracting" and then goes SILENT for minutes and looks hung.
const barThreshold = 64 << 20

func writeFile(path string, r io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var dst io.Writer = f
	var bar *Bar
	if size >= barThreshold {
		bar = NewBar("extract "+filepath.Base(path), size)
		dst = &countingWriter{w: f, onWrite: bar.Set}
	}

	// Decode and write are timed separately, and the wrappers keep this an
	// explicit buffered loop — see unpack.go for why that matters.
	tr := &timedReader{r: r}
	tw := &timedWriter{w: dst}
	buf := make([]byte, copyBufSize())
	n, err := io.CopyBuffer(tw, tr, buf)
	if err != nil {
		return err
	}
	if bar != nil {
		bar.Finish()
	}
	if n != size {
		return fmt.Errorf("%s: wrote %d of %d bytes", path, n, size)
	}
	if size >= barThreshold {
		log.Info().
			Str("file", filepath.Base(path)).
			Str("size", humanBytes(n)).
			Int("buf_bytes", len(buf)).
			Str("decode", time.Duration(tr.ns).Round(time.Second).String()).
			Str("write", time.Duration(tw.ns).Round(time.Second).String()).
			Float64("decode_mib_s", throughput(n, tr.ns)).
			Float64("write_mib_s", throughput(n, tw.ns)).
			Msg("unpack timing: decode and write are serialized, so these sum to wall clock")
	}
	log.Debug().Str("file", filepath.Base(path)).Str("size", humanBytes(n)).Msg("extracted")
	return nil
}

// countingWriter reports cumulative bytes written, so a long single-file copy can
// show progress.
type countingWriter struct {
	w       io.Writer
	written int64
	onWrite func(int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.written += int64(n)
	if c.onWrite != nil {
		c.onWrite(c.written)
	}
	return n, err
}

// Slots returns every slot in which the wallet had a transaction, newest first.
// The index is per-epoch, so this is already scoped to the epoch.
func (g *GsfaIndex) Slots(ctx context.Context, wallet solana.PublicKey) ([]uint64, error) {
	locs, err := g.reader.Get(ctx, wallet, math.MaxInt32)
	if err != nil {
		return nil, fmt.Errorf("gsfa lookup %s: %w", wallet, err)
	}
	seen := make(map[uint64]struct{}, len(locs))
	out := make([]uint64, 0, len(locs))
	for _, l := range locs {
		if _, ok := seen[l.Slot]; ok {
			continue
		}
		seen[l.Slot] = struct{}{}
		out = append(out, l.Slot)
	}
	log.Info().Int("transactions", len(locs)).Int("slots", len(out)).Msg("gsfa discovery")
	return out, nil
}

// Close releases the reader, leaving the extracted index on disk for resume.
func (g *GsfaIndex) Close() {
	if g.reader != nil {
		_ = g.reader.Close()
	}
}

// Cleanup closes the reader and deletes the extracted index (~54 GB).
func (g *GsfaIndex) Cleanup() {
	g.Close()
	if g.dir == "" {
		return
	}
	if err := os.RemoveAll(g.dir); err != nil {
		log.Warn().Err(err).Str("dir", g.dir).Msg("remove gsfa index")
		return
	}
	log.Info().Str("dir", g.dir).Msg("removed gsfa index")
}
