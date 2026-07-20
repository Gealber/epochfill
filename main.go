// Command epochfill backfills one wallet's Solana transaction history for one
// epoch from the free Old Faithful archive into <wallet_short_pk>.<epoch>.db.
//
// The epoch CAR is 600-900 GB and is NEVER downloaded: it is read by byte range.
// The two indexes needed to locate nodes (~11 GB) ARE downloaded, because served
// remotely a single lookup costs ~1.4s and a block needs hundreds. They are
// deleted when the run completes unless -keep-indexes is passed.
//
// No configuration is required by default. RPC_HTTP (via .env or the
// environment) is read ONLY when -rpc is passed, to list the wallet's
// signatures; otherwise every byte comes from the archive.
package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gagliardetto/solana-go"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/Gealber/epochfill/backfill"
)

func main() {
	_ = godotenv.Load()

	var (
		wallet      = flag.String("wallet", "", "wallet pubkey to collect (required)")
		epoch       = flag.Uint64("epoch", 0, "epoch to pull (required)")
		out         = flag.String("out", "", "output DB (default <wallet_short_pk>.<epoch>.db)")
		indexDir    = flag.String("index-dir", "indexes", "where the ~11 GB of indexes are stored")
		concurrency = flag.Int("concurrency", 32, "slots read in parallel")
		keepIndexes = flag.Bool("keep-indexes", false, "keep downloaded indexes after a successful run")
		dlConc      = flag.Int("download-concurrency", 16, "parallel chunks when downloading the indexes")
		useRPC      = flag.Bool("rpc", false, "discover slots via getSignaturesForAddress instead of the GSFA index (~50 calls, but depends on RPC history retention)")
	)
	flag.Parse()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out: os.Stdout, TimeFormat: zerolog.TimeFormatUnix, NoColor: true,
	}).With().Timestamp().Logger()
	setLogLevel()

	if *epoch == 0 {
		log.Fatal().Msg("-epoch is required")
	}
	if *wallet == "" {
		log.Fatal().Msg("-wallet is required")
	}
	pubkey, err := solana.PublicKeyFromBase58(*wallet)
	if err != nil {
		log.Fatal().Err(err).Str("wallet", *wallet).Msg("invalid wallet")
	}

	ctx, cancel := interruptContext()
	defer cancel()

	cfg := backfill.Config{
		Wallet:              pubkey,
		Epoch:               *epoch,
		OutPath:             *out,
		IndexDir:            *indexDir,
		Concurrency:         *concurrency,
		RPCEndpoint:         os.Getenv("RPC_HTTP"),
		KeepIndexes:         *keepIndexes,
		UseRPC:              *useRPC,
		DownloadConcurrency: *dlConc,
	}
	switch err := backfill.Run(ctx, cfg); {
	case err == nil:
		// done
	case errors.Is(err, context.Canceled):
		// A Ctrl-C is a normal outcome, not a failure: progress is committed per
		// slot and the downloads keep their .parts sidecars, so re-running resumes.
		log.Info().Msg("interrupted — re-run the same command to resume")
		os.Exit(130)
	default:
		log.Fatal().Err(err).Msg("backfill failed")
	}
}

// interruptContext cancels on the first SIGINT/SIGTERM and hard-exits on the
// second. Without the escape hatch a user who wants out NOW would be stuck: the
// first signal only unwinds once in-flight ranged reads return.
func interruptContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		log.Warn().Msg("interrupt: finishing in-flight reads, press Ctrl-C again to force quit")
		cancel()
		<-sig
		log.Warn().Msg("forced quit")
		os.Exit(130)
	}()
	return ctx, func() {
		signal.Stop(sig)
		cancel()
	}
}

// setLogLevel honors LOG_LEVEL (trace/debug/info), defaulting to info.
func setLogLevel() {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}
