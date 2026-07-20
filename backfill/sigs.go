package backfill

import (
	"context"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/rs/zerolog/log"
)

// SlotsPerEpoch is the mainnet epoch length.
const SlotsPerEpoch uint64 = 432000

// sigPageSize is the getSignaturesForAddress cap.
const sigPageSize = 1000

// EpochBounds returns the inclusive slot range of an epoch.
func EpochBounds(epoch uint64) (first, last uint64) {
	first = epoch * SlotsPerEpoch
	return first, first + SlotsPerEpoch - 1
}

// SigRef is one discovered transaction: its signature and the slot it landed in.
type SigRef struct {
	Sig  string
	Slot uint64
}

// DiscoverSignatures lists every transaction of wallet within the epoch.
//
// Old Faithful's only address index is GSFA, which is 29 GB AND its reader
// requires a local directory, so it cannot be range-queried. RPC does the same
// job in ~40-50 paged calls, which is why the archive is used for transaction
// BODIES only.
//
// Paging runs newest-first. It seeks straight to the epoch's end rather than
// walking back from the chain tip, so cost is proportional to the epoch's own
// traffic and not to how long ago the epoch was.
func DiscoverSignatures(ctx context.Context, client *rpc.Client, wallet solana.PublicKey, epoch uint64) ([]SigRef, error) {
	first, last := EpochBounds(epoch)

	before, err := seekMarker(ctx, client, last)
	if err != nil {
		log.Warn().Err(err).Msg("no epoch marker; paging from chain tip (slower)")
	}

	var (
		out  []SigRef
		page int
	)
	for {
		opts := &rpc.GetSignaturesForAddressOpts{Limit: ptr(sigPageSize)}
		if !before.IsZero() {
			opts.Before = before
		}
		sigs, err := client.GetSignaturesForAddressWithOpts(ctx, wallet, opts)
		if err != nil {
			return nil, fmt.Errorf("getSignaturesForAddress page %d: %w", page, err)
		}
		if len(sigs) == 0 {
			break
		}
		page++

		var reachedBefore bool
		for _, s := range sigs {
			switch {
			case s.Slot > last:
				continue // still newer than the epoch
			case s.Slot < first:
				reachedBefore = true // paged past the epoch's start
			default:
				out = append(out, SigRef{Sig: s.Signature.String(), Slot: s.Slot})
			}
		}
		before = sigs[len(sigs)-1].Signature

		log.Info().Int("page", page).Int("found", len(out)).
			Uint64("oldest_slot", sigs[len(sigs)-1].Slot).Msg("discovering signatures")

		if reachedBefore {
			break
		}
	}
	return out, nil
}

// seekMarker finds any signature just past the epoch's last slot, to be used as
// the `before` cursor. Without it, paging would start at the chain tip and walk
// through every epoch since — fine for a recent epoch, ruinous for an old one.
func seekMarker(ctx context.Context, client *rpc.Client, lastSlot uint64) (solana.Signature, error) {
	details := rpc.TransactionDetailsSignatures
	rewards := false
	// Walk forward a little: not every slot is skipped, but some are.
	for slot := lastSlot + 1; slot <= lastSlot+64; slot++ {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		block, err := client.GetBlockWithOpts(cctx, slot, &rpc.GetBlockOpts{
			TransactionDetails:             details,
			Rewards:                        &rewards,
			MaxSupportedTransactionVersion: ptr(uint64(0)),
		})
		cancel()
		if err != nil || block == nil || len(block.Signatures) == 0 {
			continue
		}
		return block.Signatures[0], nil
	}
	return solana.Signature{}, fmt.Errorf("no block with signatures after slot %d", lastSlot)
}

func ptr[T any](v T) *T { return &v }
