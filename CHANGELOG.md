# Changelog

All notable changes to emission are documented here. The format is loosely
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project uses [Conventional Commits](https://www.conventionalcommits.org/).

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
