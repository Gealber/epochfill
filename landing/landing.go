// Package landing holds the observation record and its SQLite store, shared by
// the live Yellowstone daemon and the Old Faithful epoch backfill.
//
// It exists as its own package for a hard reason: the daemon links
// yellowstone-tritonone/proto and the backfill links yellowstone-faithful's
// confirmed_block, and BOTH register "solana-storage.proto" with the protobuf
// registry. A binary importing both panics at init. Keeping the shared schema
// here — free of any proto dependency — lets each side link only its own.
package landing

import (
	"database/sql"

	_ "github.com/glebarez/go-sqlite"
)

// BaseFeeLamports is the per-signature base fee. Priority fee = total − base·nsigs.
const BaseFeeLamports = 5000

// Landing is one observed transaction of the tracked wallet, fully resolved for
// analysis. All fields are plain scalars so the row is cheap to copy and store.
type Landing struct {
	// Sig is the base58 transaction signature (primary key).
	Sig string
	// Slot is the slot the tx landed in.
	Slot uint64
	// BlockIndex is the tx's position within the block (0 = first).
	BlockIndex uint64
	// BlockTxCount is the block's executed transaction count. NULL when it could
	// not be established: a 0 count is not a real observation, and storing it as 0
	// is indistinguishable from a genuine value.
	BlockTxCount sql.NullInt64
	// PositionPct is 100·BlockIndex/BlockTxCount; the early-vs-late signal. NULL
	// when BlockTxCount is unknown — storing 0 would collide with a genuine
	// first-in-block tx and silently bias every AVG(position_pct).
	PositionPct sql.NullFloat64
	// Leader is the base58 identity of the validator that built the block.
	Leader string
	// LeaderVersion is that identity's reported client version. The archive cannot
	// supply it (getClusterNodes is a live-only read), so backfilled rows leave it
	// empty; it is self-reported and unverifiable in any case.
	LeaderVersion string
	// Landed is true when the tx succeeded (empty error).
	Landed bool
	// FeeLamports is the total fee paid.
	FeeLamports uint64
	// PriorityFeeLamports is FeeLamports − base·numSignatures (guarded at 0).
	PriorityFeeLamports uint64
	// SolDeltaLamports is the wallet's own native lamport change (≈ −fee: the
	// signer only ever pays out natively).
	SolDeltaLamports int64
	// WsolDeltaLamports is the net WSOL change on wallet-owned token accounts.
	// WSOL is 1:1 SOL, so this is the arb's realized output; NET PROFIT is
	// WsolDeltaLamports + SolDeltaLamports (output minus the native fee).
	WsolDeltaLamports int64
	// TipLamports is the sum paid to ANY out-of-protocol tip account in this tx —
	// Jito, bloXroute, Helius Sender or Nozomi. Together with PriorityFeeLamports
	// this is what the sender actually spent to be placed; priority alone
	// understates a Helius or Nozomi transaction by orders of magnitude.
	TipLamports uint64
	// TipLane names the lane(s) paid, comma-joined when a tx pays more than one.
	// Empty when no tip was paid.
	TipLane string
	// IsTipLeg is true only for a BARE tip transaction: it paid a tip and ran
	// nothing but System and ComputeBudget. A tip carried inside real work
	// (which is how Helius Sender works) is NOT a tip leg — see IsBareTipLeg.
	IsTipLeg bool
	// NumIx is the top-level instruction count (tip legs are tiny).
	NumIx int
	// CuConsumed is the compute units the transaction consumed (from meta).
	CuConsumed uint64
	// CostUnits is the tx's block-cost-model units (from meta) — the cost charged
	// against the per-block/per-account cost limits, distinct from compute consumed.
	CostUnits uint64
	// Programs is the sorted set of program IDs touched (top-level + CPI inner).
	Programs string
	// Ts is the unix time the record was written.
	Ts int64
}

// OpenDB opens (creating if needed) a landing log at path and applies the schema
// plus idempotent migrations.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		return nil, err
	}
	// Idempotent migrations: add columns to a DB created before they existed. A
	// duplicate-column error means it's already there — ignore it.
	for _, col := range []string{
		`ALTER TABLE landing ADD COLUMN cu_consumed INTEGER`,
		`ALTER TABLE landing ADD COLUMN cost_units INTEGER`,
		`ALTER TABLE landing ADD COLUMN tip_lane TEXT`,
	} {
		_, _ = db.Exec(col)
	}
	return db, nil
}

// Args flattens a Landing into the bind arguments for InsertSQL, in column order.
// Callers that need the insert inside their own transaction use this directly.
func (l *Landing) Args() []any {
	return []any{
		l.Sig, l.Slot, l.BlockIndex, l.BlockTxCount, l.PositionPct,
		l.Leader, l.LeaderVersion, boolInt(l.Landed), l.FeeLamports,
		l.PriorityFeeLamports, l.SolDeltaLamports, l.WsolDeltaLamports,
		l.TipLamports, boolInt(l.IsTipLeg), l.NumIx, l.Programs, l.Ts,
		l.CuConsumed, l.CostUnits, l.TipLane,
	}
}

// Insert upserts one landing. Idempotent on Sig, so a re-run or a stream replay
// after reconnect never double-counts.
func Insert(db *sql.DB, l *Landing) error {
	_, err := db.Exec(InsertSQL, l.Args()...)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Schema is the landing table.
const Schema = `
CREATE TABLE IF NOT EXISTS landing (
  sig                    TEXT PRIMARY KEY,
  slot                   INTEGER,
  block_index            INTEGER,
  block_tx_count         INTEGER,
  position_pct           REAL,
  leader                 TEXT,
  leader_version         TEXT,
  landed                 INTEGER,
  fee_lamports           INTEGER,
  priority_fee_lamports  INTEGER,
  sol_delta_lamports     INTEGER,
  wsol_delta_lamports    INTEGER,
  tip_lamports           INTEGER,
  is_tip_leg             INTEGER,
  n_ix                   INTEGER,
  programs               TEXT,
  ts                     INTEGER,
  cu_consumed            INTEGER,
  cost_units             INTEGER,
  tip_lane               TEXT
);
CREATE INDEX IF NOT EXISTS idx_landing_leader ON landing(leader);
CREATE INDEX IF NOT EXISTS idx_landing_slot ON landing(slot);
`

// InsertSQL matches Args' column order.
const InsertSQL = `
INSERT INTO landing (
  sig, slot, block_index, block_tx_count, position_pct, leader, leader_version,
  landed, fee_lamports, priority_fee_lamports, sol_delta_lamports,
  wsol_delta_lamports, tip_lamports, is_tip_leg, n_ix, programs, ts, cu_consumed, cost_units,
  tip_lane
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(sig) DO NOTHING;
`
