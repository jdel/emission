package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/jdel/emission/internal/auth"
	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/seeder"
	"github.com/jdel/emission/internal/units"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Seed every torrent in a watched directory",
		Long: `Recursively seeds every .torrent under --storage.torrents, picking a
client identity and announcing to each HTTP tracker on its own tracker-
reported interval. The directory tree is watched: files added later start
seeding, files removed stop.

With --http.api, an HTTP API is served on --http.port; with --http.ui the
web interface is served too (this implies --http.api). Seeding continues
in the background whether or not the API is on.

Each torrent reports an upload rate that scales with its leecher count, and the
sum across one user's torrents is capped by their --client.bandwidth (also each
new torrent's default max). Speed values accept K/M/G suffixes ("500K", "2M",
"1.5G"). All values are binary (K=1024). Trailing "B" and "/s" are accepted.`,
		RunE: runServe,
	}
	cmd.Flags().String("storage.auth", "", "passkey credential file (default: XDG data dir)")
	cmd.Flags().Bool("http.auth", false, "require passkey authentication for the HTTP API/UI")
	cmd.Flags().Bool("http.api", false, "serve the HTTP JSON API")
	cmd.Flags().Bool("http.ui", false, "serve the web UI (implies --http.api)")
	cmd.Flags().Int("http.port", 8080, "HTTP port for the API/UI when enabled")
	cmd.Flags().Bool("http.tls.enabled", false, "serve the API/UI over HTTPS")
	cmd.Flags().String("http.tls.cert", "", "TLS certificate file, PEM (required when TLS enabled)")
	cmd.Flags().String("http.tls.key", "", "TLS private key file, PEM (required when TLS enabled)")
	cmd.Flags().String("http.public-url", "", "externally reachable base URL, e.g. https://host:8443 (required when auth enabled)")
	cmd.Flags().StringSlice("http.trusted-proxies", nil, "CIDRs whose X-Forwarded-For is trusted for rate limiting (e.g. 172.16.0.0/12)")
	for _, name := range []string{
		"storage.auth", "http.auth",
		"http.api", "http.ui", "http.port",
		"http.tls.enabled", "http.tls.cert", "http.tls.key", "http.public-url",
		"http.trusted-proxies",
	} {
		_ = viper.BindPFlag(name, cmd.Flags().Lookup(name))
	}
	return cmd
}

func runServe(_ *cobra.Command, _ []string) error {
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
	bandwidth, err := parseBandwidth()
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
	log.Info().Uint64("userBandwidth", bandwidth).Msg("speed limit")

	mgr := seeder.New(c, torrentsDir, viper.GetFloat64("client.max-ratio"), viper.GetBool("client.autoremove"), bandwidth)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The web UI calls the API, so enabling it implies the API.
	apiEnabled := viper.GetBool("http.api")
	uiEnabled := viper.GetBool("http.ui")
	if uiEnabled && !apiEnabled {
		log.Info().Msg("--http.ui implies --http.api")
		apiEnabled = true
	}

	authSvc, err := setupAuth(apiEnabled)
	if err != nil {
		return err
	}

	var shutdownHTTP func()
	if apiEnabled {
		opts := httpOptions{
			addr:           fmt.Sprintf(":%d", viper.GetInt("http.port")),
			torrentsDir:    torrentsDir,
			withUI:         uiEnabled,
			auth:           authSvc,
			publicURL:      viper.GetString("http.public-url"),
			trustedProxies: viper.GetStringSlice("http.trusted-proxies"),
		}
		if viper.GetBool("http.tls.enabled") {
			cert := viper.GetString("http.tls.cert")
			key := viper.GetString("http.tls.key")
			if cert == "" || key == "" {
				return fmt.Errorf("--http.tls.enabled requires --http.tls.cert and --http.tls.key")
			}
			for _, f := range []string{cert, key} {
				if _, err := os.Stat(f); err != nil {
					return fmt.Errorf("tls: %w", err)
				}
			}
			opts.tlsCert, opts.tlsKey = cert, key
		}
		shutdownHTTP = startHTTP(cancel, mgr, opts)
	}

	// Watch the directory in the background; block until a signal arrives.
	go watchDir(ctx, mgr, torrentsDir)
	<-ctx.Done()

	log.Info().Msg("shutting down")
	if shutdownHTTP != nil {
		shutdownHTTP()
	}
	mgr.Shutdown()
	return nil
}

// setupAuth builds the auth service when --http.auth is on. Returns nil
// (auth disabled) when it is off. On a fresh credential store it logs a
// bootstrap invite link for registering the first device. The credential
// file path defaults to the XDG data dir when --storage.auth is empty.
func setupAuth(apiEnabled bool) (*auth.Service, error) {
	if !viper.GetBool("http.auth") {
		return nil, nil
	}
	if !apiEnabled {
		return nil, fmt.Errorf("--http.auth needs --http.api or --http.ui")
	}
	publicURL := viper.GetString("http.public-url")
	if publicURL == "" {
		return nil, fmt.Errorf("--http.auth requires --http.public-url")
	}
	keysPath := viper.GetString("storage.auth")
	if keysPath == "" {
		p, err := appScope.DataPath("auth.json")
		if err != nil {
			return nil, fmt.Errorf("resolve default auth file path: %w", err)
		}
		keysPath = p
	}
	if err := os.MkdirAll(filepath.Dir(keysPath), 0o755); err != nil {
		return nil, fmt.Errorf("create auth dir: %w", err)
	}
	creds, err := auth.LoadCredentials(keysPath)
	if err != nil {
		return nil, err
	}
	svc, err := auth.NewService(publicURL, creds)
	if err != nil {
		return nil, err
	}
	if n := svc.CredentialCount(); n == 0 {
		log.Info().Str("url", publicURL).Msg("no admin registered yet — open within 15 minutes to set one up")
	} else {
		log.Info().Int("devices", n).Msg("auth enabled")
	}
	return svc, nil
}

// parseBandwidth resolves --client.bandwidth (the default per-user upload
// ceiling) into bytes/sec. It must be positive — unlimited is not allowed.
func parseBandwidth() (uint64, error) {
	bw, err := units.ParseRate(viper.GetString("client.bandwidth"))
	if err != nil {
		return 0, fmt.Errorf("client.bandwidth: %w", err)
	}
	if bw == 0 {
		return 0, fmt.Errorf("client.bandwidth must be greater than zero")
	}
	return bw, nil
}

// watchDir recursively seeds every .torrent under root and watches the whole
// tree for changes until ctx ends. The Manager owns torrent/file bookkeeping;
// this loop only translates filesystem events into Manager calls.
func watchDir(ctx context.Context, mgr *seeder.Manager, root string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Error().Err(err).Str("path", root).Msg("cannot start watcher")
		return
	}
	defer watcher.Close()

	// watchTree registers a watch on dir and every subdirectory, and seeds any
	// .torrent files already present. fsnotify is not recursive, so each
	// directory needs its own watch. AddFile dedups, so re-walks are safe.
	watchTree := func(dir string) {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if err := watcher.Add(path); err != nil {
					log.Error().Err(err).Str("path", path).Msg("cannot watch directory")
				}
			} else if isTorrentFile(d.Name()) {
				_, _ = mgr.AddFile(path)
			}
			return nil
		})
	}

	watchTree(root)
	log.Info().Str("path", root).Msg("watching")

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			switch {
			case ev.Op&fsnotify.Create != 0:
				// A new subdirectory: start watching it (and catch any files
				// created inside it before the watch registered). A new file:
				// seed it if it's a torrent.
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					watchTree(ev.Name)
				} else if isTorrentFile(ev.Name) {
					_, _ = mgr.AddFile(ev.Name)
				}
			case ev.Op&fsnotify.Write != 0:
				if isTorrentFile(ev.Name) {
					_, _ = mgr.AddFile(ev.Name)
				}
			case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				if isTorrentFile(ev.Name) {
					mgr.RemoveByPath(ev.Name)
				} else {
					// Possibly a subdirectory: drop everything under it.
					mgr.RemoveUnder(ev.Name)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Error().Err(err).Msg("watcher error")
		}
	}
}

func isTorrentFile(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".torrent")
}

// resolveTorrentsDir returns the effective torrents directory: explicit flag
// value when set, otherwise the XDG data path. Creates the directory if needed.
func resolveTorrentsDir() (string, error) {
	dir := viper.GetString("storage.torrents")
	if dir == "" {
		p, err := appScope.DataPath("torrents")
		if err != nil {
			return "", fmt.Errorf("resolve default torrents dir: %w", err)
		}
		dir = p
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create torrents dir: %w", err)
	}
	return dir, nil
}
