package backfill

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ipfs/go-cid"
	"github.com/rpcpool/yellowstone-faithful/indexes"
	"github.com/rs/zerolog/log"
)

// Index names as they appear in Old Faithful filenames.
const (
	idxSlotToCid   = "slot-to-cid"
	idxCidToOffset = "cid-to-offset-and-size"
)

// Indexes holds the two indexes the backfill needs, downloaded to local disk.
//
// They MUST be local. Served over HTTP range a single lookup costs ~1.4s (it is
// many small reads, each paying full network latency); one block has ~722
// entries, which works out to ~17 minutes per block. From local disk the same
// lookup is microseconds. Together they are ~11 GB — the CAR itself (600-900 GB)
// stays remote and is read by byte range.
type Indexes struct {
	// SlotToCid maps a slot to its Block node CID (~16 MB).
	SlotToCid *indexes.SlotToCid_Reader
	// CidToOffset maps any node CID to its byte location in the CAR (~11 GB).
	CidToOffset *indexes.CidToOffsetAndSize_Reader

	// paths are the downloaded files, retained so Cleanup can remove them.
	paths []string
}

// OpenIndexes downloads (resuming if partial) and opens the indexes for epoch
// into dir. Progress is reported per index because the big one is ~11 GB.
func OpenIndexes(ctx context.Context, c *Client, epoch uint64, rootCID, dir string) (*Indexes, error) {
	ix := &Indexes{}

	slotPath, err := ix.fetch(ctx, c, epoch, rootCID, dir, idxSlotToCid)
	if err != nil {
		return nil, err
	}
	offPath, err := ix.fetch(ctx, c, epoch, rootCID, dir, idxCidToOffset)
	if err != nil {
		return nil, err
	}

	if ix.SlotToCid, err = indexes.Open_SlotToCid(slotPath); err != nil {
		return nil, fmt.Errorf("open %s: %w", idxSlotToCid, err)
	}
	if ix.CidToOffset, err = indexes.Open_CidToOffsetAndSize(offPath); err != nil {
		return nil, fmt.Errorf("open %s: %w", idxCidToOffset, err)
	}
	return ix, nil
}

// fetch downloads one index, logging progress as a percentage.
func (ix *Indexes) fetch(ctx context.Context, c *Client, epoch uint64, rootCID, dir, name string) (string, error) {
	url := IndexURL(epoch, rootCID, name)
	path := filepath.Join(dir, fmt.Sprintf("epoch-%d-%s.index", epoch, name))

	var bar *Bar
	onProgress := func(done, total int64) {
		if bar == nil {
			bar = NewBar(name, total)
		}
		bar.Set(done)
	}

	if err := c.DownloadResumable(ctx, url, path, onProgress); err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	if bar != nil {
		bar.Finish()
	}
	ix.paths = append(ix.paths, path)
	return path, nil
}

// BlockCID resolves a slot to its Block node CID.
func (ix *Indexes) BlockCID(slot uint64) (cid.Cid, error) {
	return ix.SlotToCid.Get(slot)
}

// Locate resolves a node CID to its (offset, size) in the CAR.
func (ix *Indexes) Locate(c cid.Cid) (offset, size uint64, err error) {
	oas, err := ix.CidToOffset.Get(c)
	if err != nil {
		return 0, 0, err
	}
	return oas.Offset, oas.Size, nil
}

// Close releases the index readers but leaves the files on disk, so an
// interrupted run can resume without re-downloading ~11 GB.
func (ix *Indexes) Close() {
	closeQuiet(ix.SlotToCid)
	closeQuiet(ix.CidToOffset)
}

// Cleanup closes the readers and DELETES the downloaded index files. Called only
// on a fully successful run — the whole point of keeping them otherwise is
// resumability.
func (ix *Indexes) Cleanup() {
	ix.Close()
	for _, p := range ix.paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", p).Msg("remove index")
			continue
		}
		log.Info().Str("path", p).Msg("removed index")
	}
}

func closeQuiet(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}

// formatRate renders slots-per-second at a readable precision.
func formatRate(perSec float64) string {
	if perSec >= 10 {
		return fmt.Sprintf("%.0f slot/s", perSec)
	}
	return fmt.Sprintf("%.1f slot/s", perSec)
}

// humanBytes renders a byte count in the largest unit that keeps it readable.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
