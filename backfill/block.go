package backfill

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/rpcpool/yellowstone-faithful/ipld/ipldbindcode"
	"github.com/rpcpool/yellowstone-faithful/iplddecoders"
	blockrewards "github.com/rpcpool/yellowstone-faithful/solana-block-rewards"
	"github.com/rpcpool/yellowstone-faithful/third_party/solana_proto/confirmed_block"
	"github.com/rpcpool/yellowstone-faithful/tooling"
)

// Old Faithful node kinds, encoded as byte[1] of every dag-cbor node.
const (
	kindTransaction byte = 0
	kindEntry       byte = 1
	kindBlock       byte = 2
	kindRewards     byte = 5
)

// maxRegionBytes caps a single block region read. A block measured ~2.3 MiB; the
// cap only bites when many consecutive slots were skipped and the region spans
// several blocks.
const maxRegionBytes = 128 << 20

// maxSkipBack is how many slots to walk back looking for the previous block node
// when slots were skipped. Skipped slots are common; long runs are not.
const maxSkipBack = 200

var errNoAnchor = errors.New("no previous block node found to anchor the region")

// BlockReader pulls whole blocks out of the remote CAR using the local indexes.
//
// MEASURED LAYOUT (epoch 1001, slot 432433000): a block's nodes are written
// children-first and INTERLEAVED — TX x1 | ENTRY x1 | TX x9 | ENTRY x1 | ... —
// with the Rewards and Block nodes last. 1466 transactions and 722 entries span
// 2.31 MiB, and the entries are scattered right through it.
//
// So there is no cheap subset to fetch: everything for slot N lives between block
// node N-1 and block node N. That is read as ONE range and walked forward. CAR
// sections are self-describing (uvarint length || CID || data), so past the
// anchor no further index lookups are needed — 2 lookups per slot, not ~2,200.
type BlockReader struct {
	client *Client
	carURL string
	ix     *Indexes
}

// NewBlockReader binds a reader to one epoch's CAR.
func NewBlockReader(c *Client, ix *Indexes, epoch uint64) *BlockReader {
	return &BlockReader{client: c, carURL: CarURL(epoch), ix: ix}
}

// ParsedBlock is one fully-resolved block: every transaction it executed, plus
// the leader that built it.
type ParsedBlock struct {
	// Slot is the block's slot.
	Slot uint64
	// Leader is the validator identity that produced the block, taken from the
	// Fee reward. Empty when rewards are missing or unparseable.
	Leader string
	// TxCount is the block's executed transaction count, summed over the entries
	// the Block node actually references. The Block node does NOT store this.
	TxCount uint64
	// Txs holds the block's transactions; each carries its own in-block Index.
	Txs []*ParsedTx
	// BytesRead is how many CAR bytes this block cost, for progress accounting.
	BytesRead int64
}

// ParsedTx is one transaction node with its metadata decoded.
type ParsedTx struct {
	// Node is the raw IPLD transaction (holds wire bytes + in-block index).
	Node *ipldbindcode.Transaction
	// Meta is the decoded status metadata (fees, balances, CU, error).
	Meta *confirmed_block.TransactionStatusMeta
}

// ReadBlock resolves one slot into a fully parsed block using ONE ranged GET,
// regardless of how many transactions the block holds.
func (r *BlockReader) ReadBlock(ctx context.Context, slot uint64) (*ParsedBlock, error) {
	blockCID, err := r.ix.BlockCID(slot)
	if err != nil {
		return nil, fmt.Errorf("slot %d -> cid: %w", slot, err)
	}
	blockOff, blockSize, err := r.ix.Locate(blockCID)
	if err != nil {
		return nil, fmt.Errorf("locate block %s: %w", blockCID, err)
	}

	start, err := r.anchor(slot)
	if err != nil {
		return nil, err
	}
	end := blockOff + blockSize
	if end <= start {
		return nil, fmt.Errorf("slot %d: empty region [%d,%d)", slot, start, end)
	}
	if n := end - start; n > maxRegionBytes {
		return nil, fmt.Errorf("slot %d: region %s exceeds cap %s",
			slot, humanBytes(int64(n)), humanBytes(maxRegionBytes))
	}

	buf := make([]byte, end-start)
	if err := r.client.ReadRange(ctx, r.carURL, int64(start), buf); err != nil {
		return nil, fmt.Errorf("read region: %w", err)
	}
	out, err := parseRegion(buf, blockCID)
	if err != nil {
		return nil, fmt.Errorf("slot %d: %w", slot, err)
	}
	out.Slot = slot
	out.BytesRead = int64(len(buf))
	return out, nil
}

// anchor returns the byte offset just past the previous block node, which is
// where this slot's nodes begin. Skipped slots have no block, so it walks back
// until one resolves.
func (r *BlockReader) anchor(slot uint64) (uint64, error) {
	for back := uint64(1); back <= maxSkipBack && back <= slot; back++ {
		prevCID, err := r.ix.BlockCID(slot - back)
		if err != nil {
			continue // slot was skipped
		}
		off, size, err := r.ix.Locate(prevCID)
		if err != nil {
			continue
		}
		return off + size, nil
	}
	return 0, fmt.Errorf("slot %d: %w", slot, errNoAnchor)
}

// cidOf unwraps an IPLD link to its CID.
func cidOf(l datamodel.Link) cid.Cid { return l.(cidlink.Link).Cid }

// section is one decoded CAR section: the node's CID and its dag-cbor bytes.
type section struct {
	cid  cid.Cid
	data []byte
}

// walkSections steps through every CAR section in buf. It relies on buf starting
// exactly on a section boundary — guaranteed by anchoring on a block node's end.
func walkSections(buf []byte, visit func(section) error) error {
	for pos := 0; pos < len(buf); {
		secLen, n := binary.Uvarint(buf[pos:])
		if n <= 0 || secLen == 0 {
			return fmt.Errorf("bad section length at %d", pos)
		}
		start := pos + n
		end := start + int(secLen)
		if end > len(buf) {
			return fmt.Errorf("section at %d runs past the region", pos)
		}
		cidLen, c, err := cid.CidFromBytes(buf[start:end])
		if err != nil {
			return fmt.Errorf("bad cid at %d: %w", start, err)
		}
		if err := visit(section{cid: c, data: buf[start+cidLen : end]}); err != nil {
			return err
		}
		pos = end
	}
	return nil
}

// parseRegion decodes every node in the region and assembles the block.
//
// The region can span MORE than one block when slots were skipped, so membership
// is resolved through the Block node's own entry links rather than by counting
// whatever happens to sit in the buffer.
func parseRegion(buf []byte, wantBlock cid.Cid) (*ParsedBlock, error) {
	var (
		block      *ipldbindcode.Block
		entries    = map[cid.Cid]*ipldbindcode.Entry{}
		txs        = map[cid.Cid]*ipldbindcode.Transaction{}
		rewardsRaw = map[cid.Cid][]byte{}
	)

	err := walkSections(buf, func(s section) error {
		if len(s.data) < 2 {
			return nil
		}
		switch s.data[1] {
		case kindTransaction:
			if tx, err := iplddecoders.DecodeTransaction(s.data); err == nil {
				txs[s.cid] = tx
			}
		case kindEntry:
			if e, err := iplddecoders.DecodeEntry(s.data); err == nil {
				entries[s.cid] = e
			}
		case kindRewards:
			rewardsRaw[s.cid] = s.data
		case kindBlock:
			if !s.cid.Equals(wantBlock) {
				return nil // a neighbouring block caught in a multi-slot region
			}
			b, err := iplddecoders.DecodeBlock(s.data)
			if err != nil {
				return fmt.Errorf("decode block: %w", err)
			}
			block = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, errors.New("block node not found in region")
	}

	out := &ParsedBlock{}
	if rc, ok := block.GetRewards(); ok {
		if leader, ok := leaderFromRewards(rewardsRaw[rc]); ok {
			out.Leader = leader
		}
	}

	// Count and collect strictly through the block's own entries, so a region
	// covering several slots never inflates TxCount.
	for _, el := range block.Entries {
		entry, ok := entries[cidOf(el)]
		if !ok {
			continue
		}
		out.TxCount += uint64(len(entry.Transactions))
		for _, tl := range entry.Transactions {
			node, ok := txs[cidOf(tl)]
			if !ok {
				continue
			}
			out.Txs = append(out.Txs, &ParsedTx{Node: node, Meta: decodeMeta(node)})
		}
	}
	return out, nil
}

// decodeMeta returns the transaction's status metadata, or nil when it is absent
// or split across data frames outside this region.
func decodeMeta(tx *ipldbindcode.Transaction) *confirmed_block.TransactionStatusMeta {
	container, err := tx.GetMetadata()
	if err != nil || container == nil || !container.IsProtobuf() {
		return nil
	}
	return container.GetProtobuf()
}

// leaderFromRewards extracts the block producer: the reward whose type is Fee.
// Verified against mainnet — getSlotLeaders and the Fee reward agree. Older
// epochs use a legacy encoding that leaves RewardType unset, so a lone reward is
// accepted as the fee reward.
func leaderFromRewards(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	frame, err := iplddecoders.DecodeRewards(raw)
	if err != nil {
		return "", false
	}
	payload, err := tooling.DecompressZstd(frame.Data.Bytes())
	if err != nil {
		return "", false
	}
	rewards, err := blockrewards.ParseRewards(payload)
	if err != nil || rewards == nil {
		return "", false
	}
	for _, rw := range rewards.Rewards {
		if rw.GetRewardType() == confirmed_block.RewardType_Fee {
			return rw.GetPubkey(), true
		}
	}
	if len(rewards.Rewards) == 1 {
		return rewards.Rewards[0].GetPubkey(), true
	}
	return "", false
}
