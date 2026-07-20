package backfill

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/rpcpool/yellowstone-faithful/ipld/ipldbindcode"
	"github.com/rpcpool/yellowstone-faithful/third_party/solana_proto/confirmed_block"

	"github.com/Gealber/epochfill/landing"
)

// wsolMint is native SOL wrapped as an SPL token (1:1). An arb cycles through a
// WSOL account it owns; the net change there is the realized output.
const wsolMint = "So11111111111111111111111111111111111111112"

var (
	errSplitTxData = errors.New("transaction data split across frames")
	errNoMeta      = errors.New("transaction has no status metadata")
	// errNotOurs marks a transaction that simply belongs to another wallet — the
	// overwhelmingly common case, since a whole block is parsed to find ours.
	errNotOurs = errors.New("transaction does not touch wallet")
)

// containsKey reports whether the account list holds the wallet.
func containsKey(keys []solana.PublicKey, wallet string) bool {
	for _, k := range keys {
		if k.String() == wallet {
			return true
		}
	}
	return false
}

// BuildLanding converts one archived transaction into a Landing row.
//
// wallet is the tracked address; leader and txCount come from the enclosing
// block. txCount of 0 means unknown and leaves position NULL rather than
// claiming first-in-block.
func BuildLanding(pt *ParsedTx, wallet, leader string, slot, txCount uint64) (*landing.Landing, error) {
	if total, ok := pt.Node.Data.GetTotal(); ok && total > 1 {
		return nil, errSplitTxData
	}
	tx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(pt.Node.Data.Bytes()))
	if err != nil {
		return nil, err
	}
	if len(tx.Signatures) == 0 {
		return nil, errors.New("transaction has no signature")
	}
	// Without metadata there are no fees, balances or error status: the row would
	// be mostly empty AND would claim Landed=true (a nil Err reads as success).
	// Skip it and let the caller count it rather than store a lie.
	if pt.Meta == nil {
		return nil, errNoMeta
	}
	meta := pt.Meta
	keys := resolveKeys(tx, meta)
	// Membership is "the wallet appears in the transaction's accounts", the same
	// set a Geyser account_include filter yields. Checked here because the keys
	// are only known after decoding.
	if !containsKey(keys, wallet) {
		return nil, errNotOurs
	}
	nsig := uint64(tx.Message.Header.NumRequiredSignatures)
	fee := meta.GetFee()

	l := &landing.Landing{
		Sig:                 tx.Signatures[0].String(),
		Slot:                slot,
		Leader:              leader,
		LeaderVersion:       "", // not recoverable historically, and self-reported anyway
		Landed:              meta.GetErr() == nil,
		FeeLamports:         fee,
		PriorityFeeLamports: subFloor(fee, landing.BaseFeeLamports*nsig),
		SolDeltaLamports:    walletDelta(keys, meta, wallet),
		WsolDeltaLamports:   wsolProfit(meta, wallet),
		NumIx:               len(tx.Message.Instructions),
		CuConsumed:          meta.GetComputeUnitsConsumed(),
		CostUnits:           meta.GetCostUnits(),
		Programs:            programSet(tx, meta, keys),
	}
	l.TipLamports = tipPaid(keys, meta)
	l.IsTipLeg = l.TipLamports > 0
	applyPosition(l, pt.Node, txCount)
	return l, nil
}

// applyPosition attaches the block-relative position. Both the in-block index and
// the block's tx count can be unknown; either one missing leaves position NULL,
// because storing 0 is indistinguishable from a genuine first-in-block tx and
// would silently bias every AVG(position_pct).
func applyPosition(l *landing.Landing, node *ipldbindcode.Transaction, txCount uint64) {
	idx, ok := node.GetPositionIndex()
	if !ok || idx < 0 {
		return
	}
	l.BlockIndex = uint64(idx)
	if txCount == 0 {
		return
	}
	l.BlockTxCount = sql.NullInt64{Int64: int64(txCount), Valid: true}
	l.PositionPct = sql.NullFloat64{
		Float64: 100 * float64(idx) / float64(txCount),
		Valid:   true,
	}
}

// wsolProfit is the net WSOL change (lamports) across wallet-owned token
// accounts, matched by account index. An account present pre but not post was
// closed (unwrapped to native) — subtracted so profit stays value-conserving.
func wsolProfit(meta *confirmed_block.TransactionStatusMeta, wallet string) int64 {
	pre := make(map[uint32]int64)
	for _, b := range meta.GetPreTokenBalances() {
		if b.GetMint() == wsolMint && b.GetOwner() == wallet {
			pre[b.GetAccountIndex()] = lamports(b.GetUiTokenAmount().GetAmount())
		}
	}
	var delta int64
	for _, b := range meta.GetPostTokenBalances() {
		if b.GetMint() != wsolMint || b.GetOwner() != wallet {
			continue
		}
		idx := b.GetAccountIndex()
		delta += lamports(b.GetUiTokenAmount().GetAmount()) - pre[idx]
		delete(pre, idx)
	}
	for _, amt := range pre {
		delta -= amt
	}
	return delta
}

// walletDelta is the wallet's own lamport change (post − pre).
func walletDelta(keys []solana.PublicKey, meta *confirmed_block.TransactionStatusMeta, wallet string) int64 {
	pre, post := meta.GetPreBalances(), meta.GetPostBalances()
	for i, k := range keys {
		if k.String() == wallet && i < len(pre) && i < len(post) {
			return int64(post[i]) - int64(pre[i])
		}
	}
	return 0
}

// tipPaid sums positive lamport deltas landing on any Jito tip account.
func tipPaid(keys []solana.PublicKey, meta *confirmed_block.TransactionStatusMeta) uint64 {
	pre, post := meta.GetPreBalances(), meta.GetPostBalances()
	var tip uint64
	for i, k := range keys {
		if _, ok := landing.JitoTips[k.String()]; !ok {
			continue
		}
		if i < len(pre) && i < len(post) && post[i] > pre[i] {
			tip += post[i] - pre[i]
		}
	}
	return tip
}

// programSet is the sorted, comma-joined set of program IDs (top-level + inner).
func programSet(tx *solana.Transaction, meta *confirmed_block.TransactionStatusMeta, keys []solana.PublicKey) string {
	seen := make(map[string]struct{})
	add := func(idx uint32) {
		if int(idx) < len(keys) {
			seen[keys[idx].String()] = struct{}{}
		}
	}
	for _, ci := range tx.Message.Instructions {
		add(uint32(ci.ProgramIDIndex))
	}
	for _, inner := range meta.GetInnerInstructions() {
		for _, ii := range inner.GetInstructions() {
			add(ii.GetProgramIdIndex())
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// resolveKeys returns static account keys plus ALT-loaded writable/readonly, in
// the order the balances arrays are indexed.
func resolveKeys(tx *solana.Transaction, meta *confirmed_block.TransactionStatusMeta) []solana.PublicKey {
	keys := make([]solana.PublicKey, 0, len(tx.Message.AccountKeys)+8)
	keys = append(keys, tx.Message.AccountKeys...)
	for _, b := range meta.GetLoadedWritableAddresses() {
		keys = append(keys, solana.PublicKeyFromBytes(b))
	}
	for _, b := range meta.GetLoadedReadonlyAddresses() {
		keys = append(keys, solana.PublicKeyFromBytes(b))
	}
	return keys
}

func lamports(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func subFloor(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
