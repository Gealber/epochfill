package landing

import "strings"

// Out-of-protocol tip lanes. A transfer to one of these accounts buys bundle or
// block-builder placement, so it is money the sender spent to land the
// transaction, exactly as a priority fee is. Recognising only Jito reports the
// cost of a Helius Sender, bloXroute or Nozomi transaction as far lower than it
// was: those tips can be thousands of times the base fee while the transaction's
// declared priority fee stays nominal.
//
// One map rather than four so a lookup is a single probe on the decode path,
// with the lane as the value so callers can split by it.
const (
	LaneJito      = "jito"
	LaneBloxroute = "bloxroute"
	LaneHelius    = "helius"
	LaneNozomi    = "nozomi"
)

// TipAccounts maps a tip destination to the lane that owns it. These are the
// published tip accounts of each service.
var TipAccounts = map[string]string{
	// jito
	"ADuUkR4vqLUMWXxW9gh6D6L8pMSawimctcNZ5pGwDcEt": LaneJito,
	"DttWaMuVvTiduZRnguLF7jNxTgiMBZ1hyAumKUiL2KRL": LaneJito,
	"ADaUMid9yfUytqMBgopwjb2DTLSokTSzL1zt6iGPaS49": LaneJito,
	"96gYZGLnJYVFmbjzopPSU6QiEV5fGqZNyN9nmNhvrZU5": LaneJito,
	"3AVi9Tg9Uo68tJfuvoKvqKNWKkC5wPdSSdeBnizKZ6jT": LaneJito,
	"DfXygSm4jCyNCybVYYK6DwvWqjKee8pbDmJGcLWNDXjh": LaneJito,
	"Cw8CFyM9FkoMi7K7Crf6HNQqf4uEMzpKw6QNghXLvLkY": LaneJito,
	"HFqU5x63VTqvQss8hp11i4wVV8bD44PvwucfZ2bU7gRe": LaneJito,

	// bloxroute
	"3UQUKjhMKaY2S6bjcQD6yHB7utcZt5bfarRCmctpRtUd": LaneBloxroute,
	"FogxVNs6Mm2w9rnGL1vkARSwJxvLE8mujTv3LK8RnUhF": LaneBloxroute,
	"bLx7MvxGaKdKL7mEbpk9tC79z6MnBSJoJkuaEAPu6Nd":  LaneBloxroute,
	"bLx7XBqSg3LUPVf1bRgCnkJmgVZR8QEgDJBPqcRLHvp":  LaneBloxroute,
	"bLx8KeZxinPwy6kkUgyzMLeqb2ARNsWjADG1dhSsVba":  LaneBloxroute,
	"bLxADBknoNj8WAGw2W6GBYeq848Xx6ajhaymV1YvrHm":  LaneBloxroute,
	"bLxAc88vRBwvcUQJEgcxNfBLvHPikY4csNsUmPeWea2":  LaneBloxroute,
	"bLxQ88oCiTsL8Xj4YWekKi1hjrgmbE3J3FFZ2xZHR3h":  LaneBloxroute,
	"bLxS7NoLuynNRJ4mCnEE2YbtwJFttYsEyp2ME7rp2yt":  LaneBloxroute,
	"bLxW6mCov7VEbrKc3S9tcBRcfSzRnLCbNp3Dfn3SJG5":  LaneBloxroute,
	"bLxXSGXs4mYPTC5okZXed1qzvjNwNJ48QJ82hT2V7w7":  LaneBloxroute,
	"bLxYi3vojbbB7hVzVDVTdBLVPhp7GJ3ZB3BwdK5sFXi":  LaneBloxroute,
	"bLxhLPgBXtUpX4b1bH3HatuMGMSKT9GnwtuCGiMSAqe":  LaneBloxroute,
	"bLxpY1mniuFW4PgkNA4JiNxoeKHFszryi6tNgyZAiAA":  LaneBloxroute,
	"bLxuETxd2tgWxBALNwPzAfHhsik4BzD3nrEBCiPNZQD":  LaneBloxroute,
	"bLxuL2gK5FW7xfahvwLrxLyW76vcCpNsKQY2CmnE6kV":  LaneBloxroute,
	"bLxv4Hnub7nDJWHs8s17o9bGU65Bnx6Yqp2fqtMgHmm":  LaneBloxroute,

	// helius
	"4ACfpUFoaSD9bfPdeu6DBt89gB6ENTeHBXCAi87NhDEE": LaneHelius,
	"D2L6yPZ2FmmmTKPgzaMKdhu6EWZcTpLy1Vhx8uvZe7NZ": LaneHelius,
	"9bnz4RShgq1hAnLnZbP8kbgBg1kEmcJBYQq3gQbmnSta": LaneHelius,
	"5VY91ws6B2hMmBFRsXkoAAdsPHBJwRfBht4DXox3xkwn": LaneHelius,
	"2nyhqdwKcJZR2vcqCyrYsaPVdAnFoJjiksCXJ7hfEYgD": LaneHelius,
	"2q5pghRs6arqVjRvT5gfgWfWcHWmw1ZuCzphgd5KfWGJ": LaneHelius,
	"wyvPkWjVZz1M8fHQnMMCDTQDbkManefNNhweYk5WkcF":  LaneHelius,
	"3KCKozbAaF75qEU33jtzozcJ29yJuaLJTy2jFdzUY8bT": LaneHelius,
	"4vieeGHPYPG2MmyPRcYjdiDmmhN3ww7hsFNap8pVN3Ey": LaneHelius,
	"4TQLFNWK8AovT1gFvda5jfw2oJeRMKEmw7aH6MGBJ3or": LaneHelius,

	// nozomi
	"TEMPaMeCRFAS9EKF53Jd6KpHxgL47uWLcpFArU1Fanq": LaneNozomi,
	"noz3jAjPiHuBPqiSPkkugaJDkJscPuRhYnSpbi8UvC4": LaneNozomi,
	"noz3str9KXfpKknefHji8L1mPgimezaiUyCHYMDv1GE": LaneNozomi,
	"noz6uoYCDijhu1V7cutCpwxNiSovEwLdRHPwmgCGDNo": LaneNozomi,
	"noz9EPNcT7WH6Sou3sr3GGjHQYVkN3DNirpbvDkv9YJ": LaneNozomi,
	"nozc5yT15LazbLTFVZzoNZCwjh3yUtW86LoUyqsBu4L": LaneNozomi,
	"nozFrhfnNGoyqwVuwPAW4aaGqempx4PU6g6D9CJMv7Z": LaneNozomi,
	"nozievPk7HyK1Rqy1MPJwVQ7qQg2QoJGyP71oeDwbsu": LaneNozomi,
	"noznbgwYnBLDHu8wcQVCEw6kDrXkPdKkydGJGNXGvL7": LaneNozomi,
	"nozNVWs5N8mgzuD3qigrCG2UoKxZttxzZ85pvAQVrbP": LaneNozomi,
	"nozpEGbwx4BcGp6pvEdAh1JoC2CQGZdU6HbNP1v2p6P": LaneNozomi,
	"nozrhjhkCr3zXT3BiT4WCodYCUFeQvcdUkM7MqhKqge": LaneNozomi,
	"nozrwQtWhEdrA6W8dkbt9gnUaMs52PdAv5byipnadq3": LaneNozomi,
	"nozUacTVWub3cL4mJmGCYjKZTnE9RbdY5AP46iQgbPJ": LaneNozomi,
	"nozWCyTPppJjRuw2fpzDhhWbW355fzosWSzrrMYB1Qk": LaneNozomi,
	"nozWNju6dY353eMkMqURqwQEoM3SFgEKC6psLCSfUne": LaneNozomi,
	"nozxNBgWohjR75vdspfxR5H9ceC7XXH99xpxhVGt3Bb": LaneNozomi,
}

// JitoTips is the Jito subset, retained for callers that specifically mean the
// Jito bundle auction. Anything measuring COST wants TipAccounts.
var JitoTips = func() map[string]struct{} {
	m := make(map[string]struct{})
	for a, lane := range TipAccounts {
		if lane == LaneJito {
			m[a] = struct{}{}
		}
	}
	return m
}()

// IsBareTipLeg reports whether a transaction is NOTHING BUT a tip payment: it
// moved a tip and ran no program beyond System and ComputeBudget.
//
// The distinction matters because the two shapes mean different things. A Jito
// bundle conventionally pays its tip in a separate tiny transaction, which is
// not a trade and should be excluded when analysing what a wallet does. Helius
// Sender instead requires the tip INSIDE the transaction being landed, so that
// transaction is real work that also tipped. Treating "paid a tip" as "is a tip
// leg" silently reclassifies those real transactions as overhead.
func IsBareTipLeg(programs string, tipLamports uint64) bool {
	if tipLamports == 0 {
		return false
	}
	for _, p := range strings.Split(programs, ",") {
		switch strings.TrimSpace(p) {
		case "", systemProgram, computeBudgetProgram:
		default:
			return false
		}
	}
	return true
}

const (
	systemProgram        = "11111111111111111111111111111111"
	computeBudgetProgram = "ComputeBudget111111111111111111111111111111"
)
