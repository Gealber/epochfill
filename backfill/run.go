package backfill

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/rs/zerolog/log"

	"github.com/Gealber/epochfill/landing"
)

// Config parameterises one backfill run.
type Config struct {
	// Wallet is the address to collect.
	Wallet solana.PublicKey
	// Epoch is the epoch to pull.
	Epoch uint64
	// OutPath is the SQLite output; defaults to <wallet_short_pk>.<epoch>.db.
	OutPath string
	// IndexDir is where the ~11 GB of indexes land.
	IndexDir string
	// Concurrency is how many slots are read in parallel.
	Concurrency int
	// RPCEndpoint serves signature discovery when UseRPC is set.
	RPCEndpoint string
	// UseRPC discovers slots via getSignaturesForAddress instead of the GSFA
	// index. Far cheaper (~50 calls vs a ~29 GB download) but reintroduces the
	// dependency GSFA exists to remove, and is bounded by the provider's history
	// retention.
	UseRPC bool
	// KeepIndexes leaves the downloaded indexes on disk after a successful run.
	KeepIndexes bool
	// DownloadConcurrency is how many chunks are pulled at once when fetching the
	// multi-GB indexes. A single stream tops out around 10 MB/s against the
	// archive, so this is what keeps the download from dominating the run.
	DownloadConcurrency int
}

// metaDiscovered marks that signature discovery already completed, so a resumed
// run skips straight to block reading.
const metaDiscovered = "discovered"

// Run executes the backfill end to end: discover the wallet's slots, read each
// block out of the remote CAR, and store the landings.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 32
	}
	if cfg.OutPath == "" {
		cfg.OutPath = DBPath(cfg.Wallet.String(), cfg.Epoch)
	}

	db, err := OpenEpochDB(cfg.OutPath)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info().Str("db", cfg.OutPath).Uint64("epoch", cfg.Epoch).
		Str("wallet", cfg.Wallet.String()).Msg("epoch backfill")

	// Size the connection pool for whichever phase runs hotter: parallel slot
	// reads or parallel download chunks.
	client := NewClient(max(cfg.Concurrency, cfg.DownloadConcurrency))
	if cfg.DownloadConcurrency > 0 {
		client.DownloadConcurrency = cfg.DownloadConcurrency
	}
	if err := discover(ctx, client, db, cfg); err != nil {
		return err
	}

	pending, err := db.PendingSlots()
	if err != nil {
		return err
	}
	done, total := db.Counts()
	log.Info().Int("pending", len(pending)).Int("done", done).Int("total", total).Msg("work list")
	if len(pending) == 0 {
		log.Info().Msg("nothing to do")
		return nil
	}

	rootCID, err := client.RootCID(ctx, cfg.Epoch)
	if err != nil {
		return err
	}
	log.Info().Str("root_cid", rootCID).Msg("epoch root")

	ix, err := OpenIndexes(ctx, client, cfg.Epoch, rootCID, cfg.IndexDir)
	if err != nil {
		return err
	}
	defer func() {
		if cfg.KeepIndexes {
			ix.Close()
			return
		}
		ix.Cleanup()
	}()

	return process(ctx, db, ix, client, cfg, pending)
}

// discover fills the work list, unless a previous run already did. Default is
// the GSFA index (no RPC at all); -rpc swaps in getSignaturesForAddress.
func discover(ctx context.Context, c *Client, db *EpochDB, cfg Config) error {
	if db.Meta(metaDiscovered) == "1" {
		log.Info().Msg("discovery already complete; resuming")
		return nil
	}
	slots, err := discoverSlots(ctx, c, cfg)
	if err != nil {
		return err
	}
	log.Info().Int("slots", len(slots)).Msg("discovery complete")

	if err := db.SeedSlots(slots); err != nil {
		return err
	}
	return db.SetMeta(metaDiscovered, "1")
}

// discoverSlots returns every slot the wallet appears in during the epoch.
func discoverSlots(ctx context.Context, c *Client, cfg Config) ([]uint64, error) {
	if !cfg.UseRPC {
		g, err := OpenGsfa(ctx, c, cfg.Epoch, cfg.IndexDir)
		if err != nil {
			return nil, err
		}
		defer func() {
			if cfg.KeepIndexes {
				g.Close()
				return
			}
			// The GSFA index is only needed for discovery; free its ~54 GB before
			// the block-reading phase rather than holding it for the whole run.
			g.Cleanup()
		}()
		return g.Slots(ctx, cfg.Wallet)
	}

	if cfg.RPCEndpoint == "" {
		return nil, errors.New("RPC_HTTP unset (needed for -rpc discovery)")
	}
	refs, err := DiscoverSignatures(ctx, rpc.New(cfg.RPCEndpoint), cfg.Wallet, cfg.Epoch)
	if err != nil {
		return nil, err
	}
	return distinctSlots(refs), nil
}

// distinctSlots collapses the signature list to the slots that must be read. One
// block read yields every transaction the wallet had in that slot.
func distinctSlots(refs []SigRef) []uint64 {
	seen := make(map[uint64]struct{}, len(refs))
	out := make([]uint64, 0, len(refs))
	for _, r := range refs {
		if _, ok := seen[r.Slot]; ok {
			continue
		}
		seen[r.Slot] = struct{}{}
		out = append(out, r.Slot)
	}
	return out
}

// slotResult carries one finished slot to the single writer goroutine.
type slotResult struct {
	slot     uint64
	landings []*landing.Landing
}

// process reads the pending slots concurrently and serialises the writes.
// SQLite takes a single writer, so workers never touch the DB directly.
func process(ctx context.Context, db *EpochDB, ix *Indexes, client *Client, cfg Config, pending []uint64) error {
	reader := NewBlockReader(client, ix, cfg.Epoch)

	var (
		slotsDone, rowsOut, bytesRead, failures atomic.Int64
		work                                    = make(chan uint64)
		results                                 = make(chan slotResult, cfg.Concurrency)
		wg                                      sync.WaitGroup
	)

	for range cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slot := range work {
				block, err := reader.ReadBlock(ctx, slot)
				if err != nil {
					failures.Add(1)
					log.Warn().Err(err).Uint64("slot", slot).Msg("read block")
					continue
				}
				bytesRead.Add(block.BytesRead)
				select {
				case results <- slotResult{slot: slot, landings: collect(block, cfg.Wallet.String())}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(work)
		for _, s := range pending {
			select {
			case work <- s:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	stop := startProgress(&slotsDone, &rowsOut, &bytesRead, &failures, len(pending))
	defer stop()

	for res := range results {
		if err := db.WriteSlot(res.slot, res.landings); err != nil {
			log.Error().Err(err).Uint64("slot", res.slot).Msg("write slot")
			failures.Add(1)
			continue
		}
		slotsDone.Add(1)
		rowsOut.Add(int64(len(res.landings)))
	}

	log.Info().Int64("slots", slotsDone.Load()).Int64("rows", rowsOut.Load()).
		Str("read", humanBytes(bytesRead.Load())).Int64("failures", failures.Load()).
		Msg("backfill finished")
	return ctx.Err()
}

// collect turns one parsed block into the landings that concern the wallet.
func collect(block *ParsedBlock, wallet string) []*landing.Landing {
	var out []*landing.Landing
	now := time.Now().Unix()
	for _, pt := range block.Txs {
		// Most transactions in the block belong to other wallets; BuildLanding
		// filters them out as it decodes, so each tx is only parsed once.
		l, err := BuildLanding(pt, wallet, block.Leader, block.Slot, block.TxCount)
		if err != nil {
			continue
		}
		l.Ts = now
		out = append(out, l)
	}
	return out
}

// startProgress logs throughput until the returned stop function is called.
func startProgress(slots, rows, bytesRead, failures *atomic.Int64, total int) func() {
	ticker := time.NewTicker(10 * time.Second)
	done := make(chan struct{})
	start := time.Now()

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				n := slots.Load()
				elapsed := time.Since(start).Seconds()
				rate := float64(n) / elapsed
				var eta time.Duration
				if rate > 0 {
					eta = time.Duration(float64(int64(total)-n)/rate) * time.Second
				}
				log.Info().Int64("slots", n).Int("total", total).
					Int64("rows", rows.Load()).Str("read", humanBytes(bytesRead.Load())).
					Int64("failed", failures.Load()).Str("rate", formatRate(rate)).
					Str("eta", eta.Round(time.Second).String()).Msg("progress")
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
	}
}
