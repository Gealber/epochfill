# epochfill

Backfill one Solana wallet's complete transaction history for one epoch from the
[Old Faithful](https://docs.old-faithful.net/) archive into a SQLite database.

Old Faithful is a free, public, unauthenticated archive of the entire Solana
ledger. `epochfill` reads it directly — **the epoch CAR file is 600–900 GB and is
never downloaded**, only byte-ranged.

```
epochfill -epoch 1001 -wallet <pubkey>
```

Output is `<wallet_short_pk>.<epoch>.db`, one row per transaction, holding block
position, the leader that built the block, landed/reverted, fees, SOL and WSOL
deltas, Jito tip, compute units and the set of programs touched.

## Install

```bash
go install github.com/Gealber/epochfill@latest
```

## Usage

```
-epoch                  epoch to pull (required)
-wallet                 wallet pubkey to collect (required)
-out                    output DB (default <wallet_short_pk>.<epoch>.db)
-index-dir              where downloaded indexes are stored (default "indexes")
-concurrency            slots read in parallel (default 32)
-download-concurrency   parallel chunks when downloading indexes (default 16)
-rpc                    discover slots via getSignaturesForAddress instead of GSFA
-keep-indexes           keep downloaded indexes after a successful run
```

No configuration is needed. `RPC_HTTP` (env or `.env`) is read **only** with
`-rpc`.

Interrupt-safe: press Ctrl-C and re-run the same command to resume. Twice to
force-quit.

## How it works

Two facts about the archive shape the whole design.

**A remote index lookup costs about 1.4 seconds.** The indexes are many small
reads, each paying full network latency. A block references ~700 entries, so
resolving them one at a time over HTTP would take ~17 minutes *per block*. The
node-location index is therefore downloaded once (~11 GB) and queried locally,
where the same lookup is microseconds. Design around **request count**, not bytes.

**A block's nodes are interleaved.** Measured on epoch 1001, slot 432433000:

```
TX x1 | ENTRY x1 | TX x9 | ENTRY x1 | TX x8 | ENTRY x1 | ...   (1318 runs)

1466 transactions   2.2 MiB
 722 entries        113 KiB
   1 rewards        124 B
   1 block          32 KiB
                    2.31 MiB total
```

Entries are scattered through the transactions, so there is no cheap subset to
fetch. But nodes are written children-first, which means **everything for slot N
lives between block node N-1 and block node N**. `epochfill` anchors on the
previous block node, pulls that region in **one ranged GET**, and walks the CAR
sections forward. Sections are self-describing, so past the anchor no further
index lookups are needed — 2 lookups per slot instead of ~2,200.

### Fields the archive does not hand you

- **Transaction count per block** is not stored. `Block` holds only entry links,
  so the count is summed across the entries the block actually references.
  (Resolving it that way matters: when slots are skipped the region spans several
  blocks, and counting whatever sits in the buffer would inflate it.)
- **Leader identity** is not stored either, but it is recoverable: the block's
  `Rewards` node contains a reward with `rewardType == Fee`, and its pubkey is the
  block producer. Verified to agree with `getSlotLeaders`. Very old epochs use a
  legacy encoding that leaves `RewardType` unset, so a lone reward is accepted.
- **In-block index** *is* stored on the transaction, but it is nullable. A missing
  index or an unknown transaction count stores NULL, never 0 — a stored 0 is
  indistinguishable from a genuine first-in-block transaction and would quietly
  bias every `AVG(position_pct)`.

## Discovery: GSFA vs RPC

Finding which slots a wallet appears in needs an address index.

By default `epochfill` uses the archive's **GSFA** index, so the run touches no
RPC at all. It is expensive: ~41 GB compressed for a recent epoch, expanding
about 1.15x, and it **must** be local — its reader requires a directory and
cannot be range-queried. It is downloaded resumably and freed as soon as
discovery finishes, before the node-location index is fetched, so the two never
overlap on disk.

`-rpc` swaps in `getSignaturesForAddress` — about 50 paged calls instead of a
41 GB download, at the cost of depending on your provider's history retention.

## Disk

| Phase | Peak |
|---|---|
| GSFA discovery (default) | ~90 GB |
| GSFA discovery (`-rpc`) | — |
| Block reading | ~11 GB |

Indexes are deleted on a successful run unless `-keep-indexes`, and kept on
failure so a re-run resumes instead of restarting.

## Tuning

A slot read is ~2.3 MiB and takes ~0.65 s, so one worker sustains ~3.5 MB/s:

```
-concurrency  ≈  your_bandwidth_MB/s ÷ 3.5      # ~36 for 1 Gbps
```

Start around 64 and watch the `read` rate in the progress line; if it plateaus
you are bandwidth-bound and can lower it. The HTTP connection pool is sized from
the concurrency automatically — undersized, every request would pay a fresh TLS
handshake and more workers would run *slower*.

## Schema

```sql
CREATE TABLE landing (
  sig                    TEXT PRIMARY KEY,
  slot                   INTEGER,
  block_index            INTEGER,
  block_tx_count         INTEGER,   -- NULL when unknown
  position_pct           REAL,      -- NULL when unknown
  leader                 TEXT,
  leader_version         TEXT,      -- always empty for backfilled rows
  landed                 INTEGER,
  fee_lamports           INTEGER,
  priority_fee_lamports  INTEGER,   -- total fee minus 5000 x signatures
  sol_delta_lamports     INTEGER,
  wsol_delta_lamports    INTEGER,
  tip_lamports           INTEGER,
  is_tip_leg             INTEGER,
  n_ix                   INTEGER,
  programs               TEXT,      -- sorted, comma-joined
  ts                     INTEGER,   -- when the row was written, NOT block time
  cu_consumed            INTEGER,
  cost_units             INTEGER
);
```

Two `backfill_slot` / `backfill_meta` tables carry the resume state.

### Interpreting the data

`sol_delta_lamports + wsol_delta_lamports` is a **balance delta, not profit**. A
plain transfer in or out of the wallet shows up here at full size and will
dominate any aggregate. Filter on `programs` and `n_ix` before treating it as
trading P&L.

## Notes

- Archive publication lags roughly 3 epochs.
- `ts` is when the row was written, not block time. If you need block time, the
  `slot-to-blocktime` index is only 1.7 MB.

## License

MIT
