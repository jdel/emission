# Changelog

All notable changes to emission are documented here. The format is loosely
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project uses [Conventional Commits](https://www.conventionalcommits.org/).

## [0.4.0] - 2026-07-08

### Fixed
- The mobile menu's "My bandwidth" control is now shown to every signed-in user, not just when auth is off — it had been accidentally hidden whenever authentication was enabled.
- Uptime display no longer confuses months/years with minutes (e.g. `1M` for one month vs `1m` for one minute).
- Fixed a data race where a torrent's ratio cap could be read from a background goroutine while being written concurrently by an API request.

## [0.3.3] - 2026-07-01

### Changed
- Internal: the storage layer is now extracted behind repository interfaces, decoupling application logic from the on-disk file format.

### Fixed
- Restored the SSRF guard on direct tracker announces, closing a regression that let a torrent's tracker reach loopback/internal addresses.
- Tracker announce replies are capped at 1 MiB, preventing a malicious or compromised tracker from exhausting memory with an oversized (or gzip-bomb) response.
- Expired sessions, invites, and WebAuthn ceremonies are swept on each new entry, bounding memory on a long-running server.
- Rejected non-finite and overflowing rate values instead of silently wrapping.
- Rejected an overflowing bencode byte-string length instead of risking a panic on the untrusted length field.
- Re-adding a torrent whose file path was overwritten now replaces the old session instead of leaving a stale one running.

## [0.3.2] - 2026-06-05

### Added
- Per-tracker **private/public badge** on torrent cards, reflecting the torrent's `private` flag (now exposed in the API).
- The reported upload rate jitters ±20% per second, so the live rate and the stats graph fluctuate like a real client instead of showing a flat line.

### Changed
- Stats history is capped to ~1 day, in memory and on disk — the `.stats` file is compacted instead of growing without bound.
- The torrent chart is windowed to the last day and downsampled to a fixed draw budget, keeping long-lived torrents readable.
- Announce queries omit unfilled parameters (e.g. `ipv6=`) instead of sending them blank; per-client parameter order is preserved.
- Internal: per-user rate caps recompute each torrent's natural rate once per tick, and the torrent-list snapshot is cached across WebSocket clients.

### Fixed
- State files are written atomically (unique temp + rename), preventing corruption when multiple trackers persist a torrent concurrently.
- Idle rate-limiter buckets are evicted, bounding memory on a long-running server.
- The proxy reachability probe is capped at 5s and the proxy endpoints are rate-limited, removing a way to tie up server goroutines.

### Security
- Updated the Go toolchain to 1.26.4, resolving two standard-library advisories (GO-2026-5037 `crypto/x509`, GO-2026-5039 `net/textproto`).

## [0.3.1] - 2026-06-01

### Added
- Desktop app: native builds (powered by Tauri) that wrap the seeder and web UI in a single window, shipped as `.dmg` (macOS), `.exe` (Windows), and `.AppImage` (Linux) installers on the releases page. New satellite-dish app icon and favicon across the UI.
- The per-user bandwidth control is now available in the web UI when authentication is disabled — previously it only appeared with auth on.

### Changed
- With authentication disabled the implicit user is now `admin`: uploads go to `admin/` and bandwidth is keyed to that account, instead of the unnamed root bucket.
- The default `--storage.torrents` directory is now the XDG data directory itself (`~/.local/share/emission/`) rather than a nested `torrents/` subdirectory.
- Renamed the per-user settings file from `.emission-users.json` to `client-settings.json`.
- Torrent cards show the "added … ago" time using the same precise duration as the status tooltip.

## [0.3.0] - 2026-05-31

### Added
- Tracker proxy: a server-wide `--client.proxy` default (http/https/socks5) that every user inherits, plus per-user overrides set from the UI. User-supplied proxies are SSRF-guarded (rejected if they point at a loopback/private/link-local address, and dialed through the same guard so a hostname resolving to an internal address fails too); the admin-set CLI default is trusted. Each proxy is probed for reachability and its status surfaced.
- Admin can filter the main torrent list by owner (server-side, so it works across pagination), via a username dropdown mirroring the users list.
- `emission where` command — prints the resolved data and config directories and the config file viper loaded.
- Torrent cards show how long each torrent has been up, as a relative duration in the status tooltip.

### Changed
- **Breaking:** the seeding profile is now a numeric half-saturation curve instead of a fixed enum; `stealth` / `normal` / `aggressive` are display labels for half-saturation presets (10 / 4 / 1) and any value is accepted.
- Reworked the admin invite graph as a tidy top-down tree (d3-hierarchy) with pending invites shown as blue-outlined nodes and a finer zoom step.
- Extracted the HTTP API/UI server out of the `cmd` package into `internal/api`, split by concern (server, middleware, auth, ratelimit, torrents, bandwidth, proxy, ws) — internal refactor, no behavior change.

### Fixed
- `/start/` (with a trailing slash) now redirects to `/start` so the admin-bootstrap screen loads instead of falling through to the login screen.

## [0.2.0] - 2026-05-30

### Added
- Per-user upload bandwidth ceiling and seeding profile (stealth / normal / aggressive). Replaces the old `--client.min-speed` / `--client.max-speed` flags; each user's torrents share their bandwidth proportionally by leecher share, and a new torrent's max defaults to its owner's bandwidth.
- Per-user BitTorrent identity: each user (and the auth-off root bucket) gets a stable peer\_id, key, and advertised port — trackers see one identity per user rather than one shared across all of them.
- Admin-bootstrap registration moved behind a dedicated `/start` route. On a fresh install (no users yet) the server transparently redirects `/` to `/start`; once any user exists, `/` shows the normal login screen even during the bootstrap window, so a regular user hitting the UI during an admin reset no longer lands on the bootstrap page.
- Echoed tracker IDs on follow-up announces, per BEP-3.
- User-management polish in the UI (admin device/user/invite management).

### Changed
- `--client.bandwidth` (default `1M`) replaces both `--client.min-speed` and `--client.max-speed`. The single value is the per-user ceiling and the default max for every new torrent the user uploads.
- `/api/auth/status` no longer publishes `authEnabled` or `bootstrapAvailable`. The client derives whether auth is configured from `authenticated` and `username` alone (`isAuthEnabled(s) = !s.authenticated || !!s.username`), and the bootstrap surface is exposed through the `/start` route rather than a status field.
- "No admin registered yet" log line now points at `<publicURL>/start` so the operator's first click lands on the right page.

### Fixed
- Emit raw peer\_id and key bytes as Latin-1 rather than UTF-8 — matches the BEP-3 wire format and prevents tracker rejection.
- Omit the `event` query parameter on regular (non-start/stop/complete) announces.
- Send `numwant=0` on stop announces so trackers don't return a peer list we'll never use.
- Tightened the rate limiter: shared per-IP and global token buckets across all auth routes, configurable trusted-proxy CIDRs for honest XFF handling.
- Restored jitter on the simulated upload rate so the UI graph reads as organic activity rather than a flat line.

### Removed
- `--client.min-speed` and `--client.max-speed` flags — superseded by `--client.bandwidth` and per-torrent max overrides.
- Unused `torrent.FromFile` helper.

## [0.1.1] - prior

See `git log v0.1.0..v0.1.1` for the v0.1.1 release notes (security fixes
for SSRF, bencode stack overflow, unbounded announce-list fan-out, and
auth-endpoint rate limiting; `/docs` now requires auth; trusted-proxies
flag added).

## [0.1.0] - prior

Initial release.
