package backfill

import (
	"os"
	"sync"
)

// partsSuffix names the sidecar tracking which chunks of a download landed.
const partsSuffix = ".parts"

// syncEvery is how many freshly-completed chunks may accumulate before the data
// file is fsynced and the sidecar rewritten. A crash loses at most this much
// progress; syncing per chunk would cost far more than re-fetching a few.
const syncEvery = 16

// partsFile records, one byte per chunk, which chunks of a parallel download are
// complete.
//
// It exists because parallel ranged writes land OUT OF ORDER, so the target
// file's size stops being a progress marker — a 29 GB download that dies at 80%
// would otherwise have to start over.
type partsFile struct {
	path string

	mu      sync.Mutex
	state   []byte // one byte per chunk: 0 pending, 1 done
	dirty   int    // completed chunks not yet flushed
	syncErr error
}

// openParts loads or creates the sidecar for a download of nChunks chunks. A
// sidecar whose length disagrees with nChunks describes a different file, so it
// is discarded and the download restarts.
func openParts(path string, nChunks int) (*partsFile, error) {
	p := &partsFile{path: path, state: make([]byte, nChunks)}

	existing, err := os.ReadFile(path)
	if err == nil && len(existing) == nChunks {
		copy(p.state, existing)
	} else if err == nil {
		// Length mismatch: stale sidecar from a different object.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return p, nil
}

// pending returns the indexes of chunks still to fetch, in order.
func (p *partsFile) pending() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []int
	for i, b := range p.state {
		if b == 0 {
			out = append(out, i)
		}
	}
	return out
}

// markDone records a completed chunk. The sidecar is only persisted every
// syncEvery chunks — see flush for the ordering guarantee.
func (p *partsFile) markDone(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if idx < 0 || idx >= len(p.state) || p.state[idx] == 1 {
		return
	}
	p.state[idx] = 1
	p.dirty++
}

// flush fsyncs the data file, THEN writes the sidecar. That order matters: the
// sidecar must never claim a chunk the data file has not durably stored, or a
// resumed download would skip a hole and hand corrupt bytes to the index reader.
func (p *partsFile) flush(data *os.File) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flushLocked(data)
}

func (p *partsFile) flushLocked(data *os.File) error {
	if data != nil {
		if err := data.Sync(); err != nil {
			return err
		}
	}
	if err := os.WriteFile(p.path, p.state, 0o644); err != nil {
		return err
	}
	p.dirty = 0
	return nil
}

// maybeFlush persists progress once enough chunks have accumulated.
func (p *partsFile) maybeFlush(data *os.File) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dirty < syncEvery {
		return nil
	}
	return p.flushLocked(data)
}

// Close drops the sidecar. Called only once the download is verified complete —
// on any other exit it must survive so the next run can resume.
func (p *partsFile) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, b := range p.state {
		if b == 0 {
			return nil // incomplete: keep the sidecar for resume
		}
	}
	if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
