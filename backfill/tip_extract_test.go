package backfill

import (
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/rpcpool/yellowstone-faithful/ipld/ipldbindcode"
	"github.com/rpcpool/yellowstone-faithful/third_party/solana_proto/confirmed_block"

	"github.com/Gealber/epochfill/landing"
)

// tipFixture builds a real serialized transaction whose account list contains
// the wallet, a tip destination and optionally a DEX program, plus the balance
// metadata showing the tip landing on the tip account.
func tipFixture(t *testing.T, wallet, tipAcct string, tip uint64, extra ...string) *ParsedTx {
	t.Helper()
	keys := []solana.PublicKey{
		solana.MustPublicKeyFromBase58(wallet),
		solana.MustPublicKeyFromBase58(tipAcct),
		solana.SystemProgramID,
	}
	for _, e := range extra {
		keys = append(keys, solana.MustPublicKeyFromBase58(e))
	}
	// The tip transfer itself, plus one instruction per extra program so the
	// programs column reflects them — programSet records what was INVOKED, not
	// what merely appears in the account list.
	ixs := []solana.CompiledInstruction{{ProgramIDIndex: 2, Accounts: []uint16{0, 1}}}
	for i := range extra {
		ixs = append(ixs, solana.CompiledInstruction{ProgramIDIndex: uint16(3 + i)})
	}
	tx := &solana.Transaction{Message: solana.Message{
		AccountKeys:  keys,
		Header:       solana.MessageHeader{NumRequiredSignatures: 1},
		Instructions: ixs,
	}}
	tx.Signatures = []solana.Signature{{1}}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	pre := []uint64{1_000_000_000, 0, 1, 1}
	post := []uint64{1_000_000_000 - tip - 5000, tip, 1, 1}
	meta := &confirmed_block.TransactionStatusMeta{
		Fee:          5000,
		PreBalances:  pre[:len(keys)],
		PostBalances: post[:len(keys)],
	}
	return &ParsedTx{Node: &ipldbindcode.Transaction{Data: ipldbindcode.DataFrame{Data: raw}}, Meta: meta}
}

// THE CALL SITE, not the helper. landing.TipAccounts being correct proves
// nothing if BuildLanding never consults it — which was the defect: it looked up
// the Jito-only set, so a Helius Sender tip was recorded as tip_lamports=0 and
// the transaction read as costing nothing beyond its 5,000 lamport base fee.
func TestBuildLandingCountsEveryTipLane(t *testing.T) {
	// Any address will do; the tracked wallet is a CLI flag, not a constant here.
	const wallet = "So11111111111111111111111111111111111111112"
	const dex = "LBUZKhRxPF3XUpBCjp4YzTKgLccjZhTSDM9YuVaPwxo"

	cases := []struct {
		name, acct, lane string
	}{
		{"jito", "96gYZGLnJYVFmbjzopPSU6QiEV5fGqZNyN9nmNhvrZU5", landing.LaneJito},
		{"helius", "wyvPkWjVZz1M8fHQnMMCDTQDbkManefNNhweYk5WkcF", landing.LaneHelius},
		{"bloxroute", "bLx7MvxGaKdKL7mEbpk9tC79z6MnBSJoJkuaEAPu6Nd", landing.LaneBloxroute},
		{"nozomi", "TEMPaMeCRFAS9EKF53Jd6KpHxgL47uWLcpFArU1Fanq", landing.LaneNozomi},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, err := BuildLanding(tipFixture(t, wallet, c.acct, 26_145_742), wallet, "leader", 1, 0)
			if err != nil {
				t.Fatalf("BuildLanding: %v", err)
			}
			if l.TipLamports != 26_145_742 {
				t.Errorf("TipLamports = %d, want 26145742", l.TipLamports)
			}
			if l.TipLane != c.lane {
				t.Errorf("TipLane = %q, want %q", l.TipLane, c.lane)
			}
			if !l.IsTipLeg {
				t.Error("a System-only transfer paying a tip is a bare tip leg")
			}
		})
	}

	// A tip paid alongside real work is still counted, but is NOT a tip leg —
	// excluding those rows would drop genuine transactions from any analysis.
	t.Run("tip inside real work", func(t *testing.T) {
		f := tipFixture(t, wallet, "wyvPkWjVZz1M8fHQnMMCDTQDbkManefNNhweYk5WkcF", 500_000, dex)
		l, err := BuildLanding(f, wallet, "leader", 1, 0)
		if err != nil {
			t.Fatalf("BuildLanding: %v", err)
		}
		if l.TipLamports != 500_000 {
			t.Errorf("TipLamports = %d, want 500000", l.TipLamports)
		}
		if l.IsTipLeg {
			t.Error("a tip paid alongside a DEX program is not a bare tip leg")
		}
	})
}
