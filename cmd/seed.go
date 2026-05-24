package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/seeder"
	"github.com/jdel/emission/internal/units"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func seedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "Seed a directory of torrents from the CLI (no API or auth)",
		Long: `Watches --storage.torrents and seeds every .torrent found, exactly
like serve but without the HTTP API, web UI, or authentication.

Speed values accept K/M/G suffixes ("500K", "2M"). All values are binary
(K=1024). Trailing "B" and "/s" are accepted.`,
		RunE: runSeed,
	}
}

func runSeed(_ *cobra.Command, _ []string) error {
	if err := initConfig(); err != nil {
		return err
	}
	if used := viper.ConfigFileUsed(); used != "" {
		log.Info().Str("file", used).Msg("config loaded")
	}

	torrentsDir, err := resolveTorrentsDir()
	if err != nil {
		return err
	}
	minSpeed, maxSpeed, err := parseSpeeds()
	if err != nil {
		return err
	}
	c, err := client.New(viper.GetString("client.name"))
	if err != nil {
		return err
	}
	if n := viper.GetInt("client.max-peers"); n > 0 {
		c.NumWant = n
	}
	info, err := os.Stat(torrentsDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("--storage.torrents must be a directory: %s", torrentsDir)
	}

	log.Info().Str("version", c.Version).Str("peer_id", c.PeerID).Msg("client")
	log.Info().Uint64("min", minSpeed).Uint64("max", maxSpeed).Msg("speed range")

	mgr := seeder.New(c, torrentsDir, viper.GetFloat64("client.max-ratio"))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go watchDir(ctx, mgr, torrentsDir, minSpeed, maxSpeed)
	go statsLoop(ctx, mgr)
	<-ctx.Done()

	log.Info().Msg("shutting down")
	mgr.Shutdown()
	return nil
}

func statsLoop(ctx context.Context, mgr *seeder.Manager) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			printStats(mgr)
		}
	}
}

// printStats emits one log line per loaded torrent so the output composes
// cleanly with the rest of the structured log stream — no tabwriter, no
// interleaved formatting.
func printStats(mgr *seeder.Manager) {
	for _, s := range mgr.List() {
		log.Info().
			Str("torrent", s.Name).
			Str("uploaded", units.FormatBytes(s.UploadedBytes)).
			Str("rate", units.FormatBytes(s.RateBytesPerSec)+"/s").
			Msg("stats")
	}
}
