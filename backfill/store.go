package backfill

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/Gealber/epochfill/landing"
)

// backfillSchema adds the resume bookkeeping alongside the landing table.
const backfillSchema = `
CREATE TABLE IF NOT EXISTS backfill_slot (
  slot INTEGER PRIMARY KEY,
  done INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_backfill_slot_done ON backfill_slot(done);
CREATE TABLE IF NOT EXISTS backfill_meta (
  key   TEXT PRIMARY KEY,
  value TEXT
);
`

// EpochDB is the per-epoch output: the landing rows plus the work list that
// makes an interrupted run resumable.
type EpochDB struct {
	db *sql.DB
}

// OpenEpochDB opens (creating if needed) the epoch database at path.
func OpenEpochDB(path string) (*EpochDB, error) {
	db, err := landing.OpenDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(backfillSchema); err != nil {
		return nil, err
	}
	return &EpochDB{db: db}, nil
}

func (e *EpochDB) Close() error { return e.db.Close() }

// DBPath builds the output filename: <wallet_short_pk>.<epoch>.db.
func DBPath(wallet string, epoch uint64) string {
	short := wallet
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s.%d.db", short, epoch)
}

// Meta reads a bookkeeping value; missing keys return "".
func (e *EpochDB) Meta(key string) string {
	var v string
	if err := e.db.QueryRow(`SELECT value FROM backfill_meta WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetMeta records a bookkeeping value.
func (e *EpochDB) SetMeta(key, value string) error {
	_, err := e.db.Exec(
		`INSERT INTO backfill_meta (key, value) VALUES (?,?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SeedSlots records the slots to process. Idempotent: re-seeding never resets a
// slot already marked done, so discovery can be re-run safely.
func (e *EpochDB) SeedSlots(slots []uint64) error {
	if len(slots) == 0 {
		return nil
	}
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const batch = 500
	for start := 0; start < len(slots); start += batch {
		end := min(start+batch, len(slots))
		chunk := slots[start:end]

		values := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, s := range chunk {
			values[i] = "(?,0)"
			args[i] = int64(s)
		}
		stmt := `INSERT INTO backfill_slot (slot, done) VALUES ` +
			strings.Join(values, ",") + ` ON CONFLICT(slot) DO NOTHING`
		if _, err := tx.Exec(stmt, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PendingSlots returns the slots still to process, oldest first.
func (e *EpochDB) PendingSlots() ([]uint64, error) {
	rows, err := e.db.Query(`SELECT slot FROM backfill_slot WHERE done = 0 ORDER BY slot`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uint64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, uint64(s))
	}
	return out, rows.Err()
}

// Counts returns how many slots are done and how many are known in total.
func (e *EpochDB) Counts() (done, total int) {
	_ = e.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(done),0) FROM backfill_slot`).Scan(&total, &done)
	return done, total
}

// WriteSlot stores every landing found in one slot and marks that slot done, in
// a single transaction. The slot is only marked done if its rows committed, so a
// crash mid-write is retried rather than silently skipped.
func (e *EpochDB) WriteSlot(slot uint64, landings []*landing.Landing) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, l := range landings {
		if _, err := tx.Exec(landing.InsertSQL, l.Args()...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE backfill_slot SET done = 1 WHERE slot = ?`, int64(slot)); err != nil {
		return err
	}
	return tx.Commit()
}
