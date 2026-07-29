package landing

import "testing"

// Every out-of-protocol lane must be recognised, not just Jito. A tip paid to
// Helius Sender, bloXroute or Nozomi is money spent to land the transaction
// exactly as a Jito tip is, and recognising only Jito reports the sender's cost
// as lower than it was.
func TestTipAccountsCoverEveryLane(t *testing.T) {
	want := map[string]string{
		"96gYZGLnJYVFmbjzopPSU6QiEV5fGqZNyN9nmNhvrZU5": LaneJito,
		"3AVi9Tg9Uo68tJfuvoKvqKNWKkC5wPdSSdeBnizKZ6jT": LaneJito,
		"wyvPkWjVZz1M8fHQnMMCDTQDbkManefNNhweYk5WkcF":  LaneHelius,
		"4ACfpUFoaSD9bfPdeu6DBt89gB6ENTeHBXCAi87NhDEE": LaneHelius,
		"bLx7MvxGaKdKL7mEbpk9tC79z6MnBSJoJkuaEAPu6Nd":  LaneBloxroute,
		"TEMPaMeCRFAS9EKF53Jd6KpHxgL47uWLcpFArU1Fanq":  LaneNozomi,
		"noz3jAjPiHuBPqiSPkkugaJDkJscPuRhYnSpbi8UvC4":  LaneNozomi,
	}
	for acct, lane := range want {
		got, ok := TipAccounts[acct]
		if !ok {
			t.Errorf("%s (%s) is not a known tip account", acct, lane)
			continue
		}
		if got != lane {
			t.Errorf("%s: lane = %q, want %q", acct, got, lane)
		}
	}
	lanes := map[string]int{}
	for _, l := range TipAccounts {
		lanes[l]++
	}
	if len(lanes) != 4 {
		t.Fatalf("lanes = %v, want all four", lanes)
	}
	// The Jito subset must still be exactly the eight it always was, so callers
	// that genuinely mean the bundle auction are unaffected by the widening.
	if len(JitoTips) != 8 {
		t.Fatalf("JitoTips has %d accounts, want 8", len(JitoTips))
	}
}

// IsTipLeg has to mean "this transaction is NOTHING BUT a tip", because its only
// use is excluding tip transactions from a population of real work. Defining it
// as "paid a tip" makes it true for genuine transactions that carry the tip
// inline, which is how Helius Sender works.
func TestBareTipLegVsTipInsideRealWork(t *testing.T) {
	const cb = "ComputeBudget111111111111111111111111111111"
	const system = "11111111111111111111111111111111"
	const dex = "LBUZKhRxPF3XUpBCjp4YzTKgLccjZhTSDM9YuVaPwxo"

	if !IsBareTipLeg(system+","+cb, 5000) {
		t.Error("a System+ComputeBudget transfer paying a tip is a bare tip leg")
	}
	if !IsBareTipLeg(system, 5000) {
		t.Error("a bare System transfer paying a tip is a bare tip leg")
	}
	if IsBareTipLeg(system+","+cb+","+dex, 5000) {
		t.Error("a tip paid alongside a DEX swap is NOT a bare tip leg")
	}
	if IsBareTipLeg(system+","+cb, 0) {
		t.Error("no tip paid means no tip leg, whatever the programs")
	}
}
