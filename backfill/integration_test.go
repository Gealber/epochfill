package backfill

import (
	"context"
	"testing"

	"github.com/rpcpool/yellowstone-faithful/indexes"
	splitcarfetcher "github.com/rpcpool/yellowstone-faithful/split-car-fetcher"
)

// The block measured with the layout walker on 2026-07-20. Its shape is the
// ground truth this test pins:
//
//	region 2,425,256 bytes  |  1466 transactions  |  722 entries  |  1 rewards
const (
	itEpoch    uint64 = 1001
	itSlot     uint64 = 432433000
	itTxCount  uint64 = 1466
	itRegionSz        = 2425256
)

// TestParseRegionAgainstRealCAR fetches one real block region from Old Faithful
// and checks the single-read walk reproduces the measured shape: the right
// transaction count, a leader from the Fee reward, and in-block indexes present.
//
// It hits the network (a few remote index lookups plus a ~2.3 MB ranged read) and
// is skipped under `go test -short`. Remote index lookups cost ~1.4s each, which
// is exactly why the CLI itself uses LOCAL indexes.
func TestParseRegionAgainstRealCAR(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: reads from files.old-faithful.net")
	}
	ctx := context.Background()
	client := NewClient(8)

	rootCID, err := client.RootCID(ctx, itEpoch)
	if err != nil {
		t.Fatalf("root cid: %v", err)
	}

	s2cR, _, err := splitcarfetcher.NewRemoteHTTPFileAsIoReaderAt(ctx, IndexURL(itEpoch, rootCID, idxSlotToCid))
	if err != nil {
		t.Fatalf("open slot-to-cid: %v", err)
	}
	s2c, err := indexes.OpenWithReader_SlotToCid(s2cR)
	if err != nil {
		t.Fatalf("read slot-to-cid: %v", err)
	}
	c2oR, _, err := splitcarfetcher.NewRemoteHTTPFileAsIoReaderAt(ctx, IndexURL(itEpoch, rootCID, idxCidToOffset))
	if err != nil {
		t.Fatalf("open cid-to-offset: %v", err)
	}
	c2o, err := indexes.OpenWithReader_CidToOffsetAndSize(c2oR)
	if err != nil {
		t.Fatalf("read cid-to-offset: %v", err)
	}

	locate := func(slot uint64) (uint64, uint64) {
		t.Helper()
		c, err := s2c.Get(slot)
		if err != nil {
			t.Fatalf("slot %d -> cid: %v", slot, err)
		}
		oas, err := c2o.Get(c)
		if err != nil {
			t.Fatalf("locate %s: %v", c, err)
		}
		return oas.Offset, oas.Size
	}

	prevOff, prevSize := locate(itSlot - 1)
	curOff, curSize := locate(itSlot)
	start, end := prevOff+prevSize, curOff+curSize

	if got := int(end - start); got != itRegionSz {
		t.Errorf("region = %d bytes, want %d (archive layout changed?)", got, itRegionSz)
	}

	buf := make([]byte, end-start)
	if err := client.ReadRange(ctx, CarURL(itEpoch), int64(start), buf); err != nil {
		t.Fatalf("read region: %v", err)
	}

	blockCID, err := s2c.Get(itSlot)
	if err != nil {
		t.Fatalf("block cid: %v", err)
	}
	block, err := parseRegion(buf, blockCID)
	if err != nil {
		t.Fatalf("parseRegion: %v", err)
	}

	if block.TxCount != itTxCount {
		t.Errorf("TxCount = %d, want %d", block.TxCount, itTxCount)
	}
	if len(block.Txs) == 0 {
		t.Fatal("no transactions decoded from the region")
	}
	if block.Leader == "" {
		t.Error("leader not recovered from the Fee reward")
	}

	// Every transaction should carry an in-block index, and it must be < TxCount.
	var withIndex int
	for _, pt := range block.Txs {
		if idx, ok := pt.Node.GetPositionIndex(); ok {
			withIndex++
			if uint64(idx) >= block.TxCount {
				t.Errorf("in-block index %d >= TxCount %d", idx, block.TxCount)
			}
		}
	}
	if withIndex == 0 {
		t.Error("no transaction carried an in-block index")
	}
	t.Logf("slot %d: leader=%s tx_count=%d decoded=%d indexed=%d",
		itSlot, block.Leader, block.TxCount, len(block.Txs), withIndex)
}
