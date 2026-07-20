package backfill

import (
	"encoding/binary"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"

	"github.com/Gealber/epochfill/landing"
	"github.com/rpcpool/yellowstone-faithful/ipld/ipldbindcode"
)

func TestEpochBounds(t *testing.T) {
	first, last := EpochBounds(1001)
	if first != 432432000 {
		t.Fatalf("first = %d, want 432432000", first)
	}
	if last != 432863999 {
		t.Fatalf("last = %d, want 432863999", last)
	}
	if last-first+1 != SlotsPerEpoch {
		t.Fatalf("epoch spans %d slots, want %d", last-first+1, SlotsPerEpoch)
	}
}

func TestDBPath(t *testing.T) {
	got := DBPath("So11111111111111111111111111111111111111112", 1001)
	if want := "So111111.1001.db"; got != want {
		t.Fatalf("DBPath = %q, want %q", got, want)
	}
}

func TestDistinctSlots(t *testing.T) {
	refs := []SigRef{
		{Sig: "a", Slot: 10},
		{Sig: "b", Slot: 10},
		{Sig: "c", Slot: 11},
		{Sig: "d", Slot: 10},
	}
	got := distinctSlots(refs)
	if len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("distinctSlots = %v, want [10 11]", got)
	}
}

// TestWalkSectionsRoundTrip feeds hand-built CAR sections through the walker.
// The walker is what replaced ~2,200 index lookups per block with one sequential
// pass, so a framing mistake here silently truncates every block.
func TestWalkSectionsRoundTrip(t *testing.T) {
	payloads := [][]byte{
		{0x82, kindTransaction, 0xaa},
		{0x82, kindEntry, 0xbb, 0xcc},
		{0x82, kindBlock},
	}
	var buf []byte
	var wantCIDs []cid.Cid
	for _, p := range payloads {
		c := cid.NewCidV1(cid.DagCBOR, mustHash(p))
		wantCIDs = append(wantCIDs, c)
		buf = append(buf, encodeSection(c, p)...)
	}

	var gotCIDs []cid.Cid
	var gotKinds []byte
	err := walkSections(buf, func(s section) error {
		gotCIDs = append(gotCIDs, s.cid)
		gotKinds = append(gotKinds, s.data[1])
		return nil
	})
	if err != nil {
		t.Fatalf("walkSections: %v", err)
	}
	if len(gotCIDs) != len(payloads) {
		t.Fatalf("walked %d sections, want %d", len(gotCIDs), len(payloads))
	}
	for i := range wantCIDs {
		if !gotCIDs[i].Equals(wantCIDs[i]) {
			t.Errorf("section %d cid = %s, want %s", i, gotCIDs[i], wantCIDs[i])
		}
	}
	wantKinds := []byte{kindTransaction, kindEntry, kindBlock}
	for i, k := range wantKinds {
		if gotKinds[i] != k {
			t.Errorf("section %d kind = %d, want %d", i, gotKinds[i], k)
		}
	}
}

// TestWalkSectionsRejectsTruncated makes sure a short region errors instead of
// silently returning a partial block, which would under-count block_tx_count.
func TestWalkSectionsRejectsTruncated(t *testing.T) {
	payload := []byte{0x82, kindTransaction, 0xaa}
	c := cid.NewCidV1(cid.DagCBOR, mustHash(payload))
	full := encodeSection(c, payload)

	err := walkSections(full[:len(full)-1], func(section) error { return nil })
	if err == nil {
		t.Fatal("truncated region must error, got nil")
	}
}

// encodeSection frames a node the way a CAR file does: uvarint(len) || cid || data.
func encodeSection(c cid.Cid, data []byte) []byte {
	body := append(append([]byte{}, c.Bytes()...), data...)
	hdr := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(hdr, uint64(len(body)))
	return append(hdr[:n], body...)
}

func mustHash(b []byte) multihash.Multihash {
	h, err := multihash.Sum(b, multihash.SHA2_256, -1)
	if err != nil {
		panic(err)
	}
	return h
}

// txWithIndex builds a Transaction node carrying an in-block index. The field is
// **int in the IPLD binding, so it needs two levels of pointer.
func txWithIndex(idx int) *ipldbindcode.Transaction {
	p := &idx
	return &ipldbindcode.Transaction{Index: &p}
}

// TestApplyPositionNullsAreNotZero is the important one. A missing block tx count
// or a missing index must leave position NULL, never 0 — a stored 0 is
// indistinguishable from a genuine first-in-block transaction and would bias
// every AVG(position_pct) downwards.
func TestApplyPositionNullsAreNotZero(t *testing.T) {
	t.Run("unknown tx count leaves position NULL", func(t *testing.T) {
		var l landing.Landing
		applyPosition(&l, txWithIndex(7), 0)

		if l.BlockIndex != 7 {
			t.Fatalf("BlockIndex = %d, want 7", l.BlockIndex)
		}
		if l.BlockTxCount.Valid {
			t.Fatal("BlockTxCount must be NULL when the count is unknown")
		}
		if l.PositionPct.Valid {
			t.Fatal("PositionPct must be NULL when the count is unknown")
		}
	})

	t.Run("missing index leaves position NULL", func(t *testing.T) {
		var l landing.Landing
		applyPosition(&l, &ipldbindcode.Transaction{}, 1500)

		if l.BlockTxCount.Valid || l.PositionPct.Valid {
			t.Fatal("position must be NULL when the tx carries no index")
		}
	})

	t.Run("first in block is a real observation", func(t *testing.T) {
		var l landing.Landing
		applyPosition(&l, txWithIndex(0), 1000)

		if !l.PositionPct.Valid {
			t.Fatal("a genuine index 0 must be stored, not treated as unknown")
		}
		if l.PositionPct.Float64 != 0 {
			t.Fatalf("PositionPct = %v, want 0", l.PositionPct.Float64)
		}
		if l.BlockTxCount.Int64 != 1000 {
			t.Fatalf("BlockTxCount = %d, want 1000", l.BlockTxCount.Int64)
		}
	})

	t.Run("percentage math", func(t *testing.T) {
		var l landing.Landing
		applyPosition(&l, txWithIndex(250), 1000)

		if l.PositionPct.Float64 != 25 {
			t.Fatalf("PositionPct = %v, want 25", l.PositionPct.Float64)
		}
	})
}

func TestSubFloor(t *testing.T) {
	if got := subFloor(5000, 10000); got != 0 {
		t.Fatalf("subFloor underflowed to %d, want 0", got)
	}
	if got := subFloor(15000, 10000); got != 5000 {
		t.Fatalf("subFloor = %d, want 5000", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:           "512 B",
		1024:          "1.0 KiB",
		11 * 1 << 30:  "11.0 GiB",
		779 * 1 << 30: "779.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
