# Code Review

Findings from a fresh-eyes pass over the codebase. Numbered for reference;
categorized by severity.

---

## Bugs

### 1. Stdlib `log` used in `cmd/emission/auth.go` — **DONE**

**File:** `cmd/emission/auth.go:7,277`

Imports `"log"` from the standard library and calls `log.Printf` once at
line 277. Every other file uses `github.com/rs/zerolog/log`. The result is
one unstructured line that breaks the JSON/console format the rest of the
process emits.

**Fix:** replace with `log.Error().Err(err).Str("path", dir).Msg("…")`.

---

### 2. Dead test `TestRealTorrentFile` — **DONE**

**File:** `pkg/torrent/torrent_test.go:46-61`

Calls `FromFile("/ratioup/tests/ubuntu-20.04.4-desktop-amd64.iso.torrent")`.
That path does not exist in the repo. The test always hits `t.Skipf` and
never exercises real code. Should be removed or replaced with a generated
fixture committed alongside the test.

---

### 3. `digitRangeToHex` skews single-value ranges — **DONE**

**File:** `pkg/client/algorithm.go:45-47`

```go
if hi <= lo {
    hi = lo + 1
}
```

When a profile specifies `lo == hi` (a fixed key value), the function
silently widens the range to `[lo, lo+1]` and picks one of two values
instead of the configured constant. Should accept `lo == hi` as a fixed
output.

---

### 4. `randomPoolWithChecksum` can split a UTF-8 codepoint — **DONE**

Renamed to `prefixedRandomPool` (no checksum involved; old name came from the
reference Rust crate). Reference removed from doc comment. Truncation now
detects multi-byte overshoot and substitutes a fitting rune from the pool
instead of slicing mid-codepoint.

**File:** `pkg/client/algorithm.go:62-78`

After appending random runes the function truncates the string at
`peerIDLength` bytes. If the pool contains multi-byte runes the cut can
land mid-codepoint, leaving an invalid UTF-8 sequence in `PeerID`. Every
shipped profile uses ASCII-only pools so the bug is latent, but the
algorithm name implies non-ASCII support.

**Fix:** count bytes when appending; never write a rune that would
overshoot.

---

### 5. `Manager.SetSpeed` does not accept `maxRatio` — **DONE**

Renamed to `SetClientOptions(id, minSpeed, maxSpeed, maxRatio)`. Sidecar JSON
gained a `maxRatio` field. Session stores `maxRatio` (under `m.mu`) and an
atomic `uploadCap` recomputed on each update. Status exposes `MaxRatio` in the
JSON payload. PATCH body, upload multipart (`max-ratio`), Swagger spec, and
the React speed-edit dialog all carry the new field. `maxRatio == 0` means
unlimited (seed indefinitely); negative values are rejected.

**File:** `internal/seeder/seeder.go:185-202`

The "add max-ratio input to the upload UI" feature from earlier shipped
the UI plumbing partially. `SetSpeed` still takes only `minSpeed,
maxSpeed`. Sidecar persistence of `maxRatio` was never added, so the
ratio cap can only be set globally via `--client.max-ratio`. Either
finish the feature or remove the partial UI work.

---

### 6. HTTP client recreated per announce — **DONE**

`tracker.Params` gained an `HTTPClient *http.Client` field; `Announce` falls
back to a package-level `defaultClient` (30s timeout) when unset. Manager
owns one `*http.Client` and threads it through `runTracker`. Isolates
tracker traffic from other HTTP work in the binary and gives tests a clean
injection point. Original 60s timeout dropped to 30s — tracker responses
are sub-second.

**File:** `pkg/tracker/announce.go:67`

```go
httpClient := &http.Client{Timeout: 60 * time.Second}
```

Built per call. No connection reuse, fresh TLS handshake every announce.
For a busy seeder with frequent intervals this is wasted CPU and visible
to tracker operators as a missing keep-alive header. Pass an
`*http.Client` in via `Params` or hang one on `Manager`.

---

### 7. `time.After` leaks timers on cancel — **DONE**

`runTracker` now uses a single `time.NewTimer` (deferred `Stop`, `Reset`
between iterations) instead of `time.After`. No more orphaned 30-minute
timers when a session is cancelled.

**File:** `internal/seeder/session.go:189`

```go
case <-time.After(interval):
```

When `s.ctx` cancels before `interval` fires the underlying timer is not
stopped — it lives until it fires. Intervals can be 30+ minutes. Use
`time.NewTimer(interval)` with `defer t.Stop()` and reset between
iterations.

---

## Test gaps

### 8. `internal/seeder` has effectively no coverage — **DONE**

Added three test files:

- `sidecar_test.go` — Save/Load round-trip including zero/negative ratio and
  min>max rejection.
- `helpers_test.go` — `uploadCapFor`, `pickRate`, `backoff`, `clampMin`,
  `pickInterval` (all 5 branches).
- `manager_test.go` — `AddFile` happy path / duplicate / min>max,
  sidecar-overrides-defaults, `SetClientOptions` happy path + 3 validation
  branches + sidecar persistence, `Remove`, `RemoveByPath`, `RemoveUnder`,
  `Page` filter/slice/offset-past-end, `Visible` admin/user/foreign scoping,
  `Subscribe` fires-on-add / coalesce / channel-closes-on-unsubscribe,
  `Owner` and `relPath`.

Coverage uses an `httptest.Server` as a stub tracker so sessions don't beat
on real hosts. Tests run in ~150 ms.

**File:** `internal/seeder/seeder_test.go`

Only `TestOwner` (4 lines). Manager, session, sidecar, subscription
machinery are all untested. Concrete missing tests:

- `Manager.AddFile` happy path + duplicate-hash rejection
- `Manager.Remove`, `RemoveByPath`, `RemoveUnder`
- `Manager.Page` filtering + paging + viewer scope
- `Manager.Visible` admin-vs-user scoping
- `Manager.Get`
- `Manager.SetSpeed` updates live `minSpeed`/`maxSpeed` atomics
- `Manager.Subscribe` / `notifyChanged` coalescing
- `Manager.Shutdown` honors `shutdownGrace`
- Session `accumulateLoop` guards: skip when `leechers == 0`,
  skip when `uploaded >= uploadCap`
- Sidecar `LoadSidecar` / `SaveSidecar` round-trip

---

### 9. `pkg/torrent` only covers the simplest path — **DONE**

Added a small `bstr` helper for readable bencode building, then nine focused
tests on top of the existing two: multi-file `totalLength`, private flag,
`announce-list` tier parsing + dedup against `announce`, `.local` host
filter, mixed UDP/`.local`/HTTPS announce-list keeping only HTTPS, missing
`info`, non-dict root, info with neither `length` nor `files`. UDP-only
rejection tightened to assert the error message mentions announce URLs.

**File:** `pkg/torrent/torrent_test.go`

Missing:

- multi-file torrent (the `files` branch of `totalLength`)
- `announce-list` tier parsing + dedup against `announce`
- `private` flag
- rejection when zero supported announce URLs after filtering
- `.local` host filtering

---

### 10. `pkg/bencode` only covers happy paths — **DONE**

Three new test functions: `TestDecodeErrors` (10 sub-cases — empty input,
unknown leading byte, unterminated int/list/dict, bad integer, bytes missing
colon, bytes truncated, non-string dict key, bad bytes length),
`TestDecodeIntEdgeCases` (zero, negative, max int64), `TestDecodeEmptyContainers`
(empty list/dict/byte-string).

**File:** `pkg/bencode/bencode_test.go`

No tests for error cases: unterminated int/list/dict, bad integer, bytes
length overflow, non-byte-string dict key, empty input.

---

### 11. `pkg/tracker` lacks end-to-end coverage — **DONE**

Added httptest-driven tests: `TestAnnounceHappy` (asserts both the parsed
response and that the server sees the expected info_hash placeholder
substituted in the URL), `TestAnnounceGzipResponse` (server sets
`Content-Encoding: gzip`, response decoded correctly), `TestAnnounceTrackerFailure`,
`TestAnnounceNetworkError`. Also `TestParseTrackerWarningAndID`,
`TestParseTrackerNotDict`, `TestParseTrackerInvalidBencode`.

**File:** `pkg/tracker/announce_test.go`

Only `BuildURL` and `parseResponse` are exercised directly. Missing:

- `Announce` against an `httptest.Server`
- gzip-encoded response decoding (path exists in code, never hit by tests)
- `warning message` field extraction
- `tracker id` field extraction
- non-dict body, invalid bencode body

---

### 12. HTTP handlers have zero tests — **PARTIAL**

Added `cmd/emission/api_test.go` covering the security-sensitive and
parse-heavy helpers:

- `TestSafeTargetPath` — 11 sub-cases including path traversal attempts,
  embedded directory components, missing suffix, bad usernames, and a
  trailing invariant check (resolved path must be a child of the storage
  root).
- `TestQueryInt` — explicit value, malformed → default, missing → default.
- `TestParseSpeedForm` — defaults, full override, and 5 validation
  branches (bad min/max/ratio, negative ratio, min>max).

Full integration tests of `uploadTorrent` / `updateTorrent` / `removeTorrent`
and the `requireAuth` middleware would need an injectable auth mock and a
constructed `*seeder.Manager`; deferred until further refactor.

**File:** `cmd/emission/`

Not a single handler test. `safeTargetPath` is security-sensitive (path
traversal) and untested. `parseSpeedForm`, `queryInt`, `requireAuth`
middleware, `viewer` scoping — all untested.

---

### 13. `pkg/client.TestRegenerate` only checks `PeerID` — **DONE**

Split into `TestRegeneratePeerID` and `TestRegenerateKey`; same 10-redraw
collision-avoidance loop applied to each.

**File:** `pkg/client/client_test.go:46-61`

Same drift could happen on `Key`. Cover both.

---

## Refactoring

### 14. Most `pkg/` packages should be `internal/` — **DONE**

First pass: `pkg/bencode`, `pkg/torrent`, `pkg/tracker`, `pkg/units` moved to
`internal/`.

Second pass: the remaining `pkg/client` and `pkg/docs` had no external
consumers either — moved to `internal/client` and `internal/docs`. The
public `pkg/client` framing was aspirational. `pkg/docs` is purely a
swag-generated artifact registered via blank import. `pkg/` is now empty
and removed; this is an application repo, not a library.

`internal/client/doc.go` trimmed: dropped the quick-start example and
redundant encoding/refresh prose (factual notes already live next to the
relevant types/methods). Package summary kept; refresh-policy note kept
since it warns callers about a non-obvious responsibility.

All imports, the Makefile (`swag init` output path, `sync-clients` script
path, `test` and `vet` targets), the sync script, and the README were
updated. Tests green.

**Files:** `pkg/bencode`, `pkg/torrent`, `pkg/tracker`, `pkg/units`

Only `pkg/client` is documented in the README as an external surface
(port of `fake-torrent-client`). The rest exist solely for emission and
are shaped to its needs:

- `bencode.Value.Start/End` exists specifically to compute info_hash from
  raw bytes — an internal concern of torrent parsing.
- `torrent` only handles HTTP trackers and only the fields the seeder
  reads.
- `tracker.Params`/`Response` mirror seeder's loop, not a general API.
- `units` is three trivial helpers.

**Recommendation:** move all four under `internal/`. Keep `pkg/client`
public.

---

### 15. HTTP doc-types scattered across files — **DONE**

New `cmd/emission/api_types.go` holds every JSON shape used by handlers and
the Swagger spec: `pagedTorrents`, `wsMessage`, `uploadResult`, `speedUpdate`,
`errorResponse`, `authStatusResponse`, `inviteBody`, `inviteRequest`,
`inviteResponse`, `deviceInfo`. `swagger.go` slimmed to just the handler
registration. Handlers swapped to use the named types instead of inline
anonymous structs (uploadTorrent response, updateTorrent body, authStatus
response, authRegisterBegin body, authInvite body+response, authUsers body
+ local `device` type). Schema and runtime now share one source of truth;
`make swagger` regenerated.

**Files:** `cmd/emission/api.go`, `auth.go`, `swagger.go`

`pagedTorrents`, `uploadResult`, `errorResponse`, `speedUpdate`,
`authStatusResponse`, `inviteBody`, `inviteRequest`, `inviteResponse`,
`deviceInfo`, anonymous request structs in handlers. Group in one
`api_types.go`.

---

### 16. Owner-or-admin gate duplicated — **DONE**

Extracted `server.authorizeTorrent(w, r, id) bool` in `auth.go`. Returns true
when auth is off, the caller is admin, or the caller owns the torrent;
writes a 404 or 403 response and returns false otherwise. `updateTorrent`
and `removeTorrent` now share the single call site.

**Files:** `cmd/emission/api.go:317-327, 374-385`

Same five-line block appears in `updateTorrent` and `removeTorrent`.
Extract `func (s *server) authorizeOwner(r *http.Request, id string) (ok bool, status int)`.

---

### 17. Swagger handler rebuilt per request — **DONE**

`swaggerHandlers` now caches one `httpSwagger.Handler` per `(scheme, host)`
pair in a `sync.Map`. First request for a key builds via
`LoadOrStore`; subsequent requests reuse the cached handler. The
UrlMutatorPlugin script extracted to a package-level constant.

**File:** `cmd/emission/swagger.go`

`httpSwagger.Handler(...)` is constructed for every `/docs/*` request to
inject the runtime `scheme` and `host`. Cache by `(scheme, host)` in a
`sync.Map` — handlers are immutable once built.

---

### 18. Two RNG calls per accumulate tick — **DONE**

Collapsed to a single signed draw in `[-span, +span]` where `span = rate/5+1`.
Result clamped to ≥ 1 byte so the accumulator never adds zero (preserves the
"swarm has leechers ⇒ uploads grow" invariant). One RNG call per second
instead of two.

**File:** `internal/seeder/session.go:148-153`

```go
jitter := rand.Uint64N(rate/5+1)
if rand.Uint32()&1 == 0 { ... } else if jitter < rate { ... }
```

Collapse to one signed draw:

```go
span := int64(rate/5 + 1)
delta := rand.Int64N(2*span+1) - span
```

---

### 19. `tracker.Announce` mixes formatter styles — **DONE**

Dropped the trivial `u16str` / `u64str` helpers and the inline
`fmt.Sprintf("%d", ...)`. Now uses `strconv.FormatUint` / `strconv.Itoa`
consistently across all numeric substitutions.

**File:** `pkg/tracker/announce.go:101-111`

Uses helper `u16str`, `u64str` for most fields and inline `fmt.Sprintf("%d", numwant)`
for `numwant`. Pick one (either drop the helpers and use `strconv` everywhere,
or route numwant through `u64str`).

---

### 20. Sidecar round-trip relies on `ParseRate` tolerance — **DONE**

Switched the sidecar layout from human-readable strings (`"1.0 KB"`) to raw
`uint64` byte counts. Round-trip is now lossless by construction —
`LoadSidecar` returns exactly what `SaveSidecar` wrote, no `ParseRate`
hop in between. The `units` package is no longer imported here. Test
`TestSidecarRoundtrip` re-tightened with `min=2000` which the old format
would have rounded to 2048.

**File:** `internal/seeder/sidecar.go:46-55`

`SaveSidecar` writes `units.FormatBytes(...)` ("1.0 KB"); `LoadSidecar`
parses with `units.ParseRate`. `ParseRate` happens to accept the
formatted form because the space-stripping order works out. Lock the
invariant with a round-trip test.

---

### 21. `seed` stats table mingles with logs — **DONE**

Dropped the `tabwriter`. `printStats` now emits one structured `log.Info()`
entry per torrent (`stats` message with `torrent` / `uploaded` / `rate`
fields). Composes cleanly with the rest of the zerolog stream — no
interleaved table rows, machine-parseable in JSON mode.

**File:** `cmd/emission/seed.go` (`printStats`)

Tabwriter table writes to `os.Stderr`; zerolog also writes there. Rows
interleave with structured log lines, especially with `--log-level
debug`. Either render via zerolog as a structured event, or send the
table to `os.Stdout`.

---

### 22. `Manager.Page` / `Visible` use a fragile reslice idiom — **DONE**

Replaced both `all[:0:0]` reslices with explicit `make([]Status, 0, len(all))`.
Removes the implicit dependency on `List`'s return making a fresh copy.

**File:** `internal/seeder/seeder.go:298-304, 327-334`

```go
out := all[:0:0]
```

Allocates a fresh backing array because `Visible` already returned a
copy. Safe today; breaks silently if `List` ever returns a shared slice.
Add a comment or use `make([]Status, 0, len(all))`.

---

## Nice to have

### 23. `runTracker` sends `EventStarted` even when ctx is already cancelled — **DONE**

`runTracker` now checks `s.ctx.Err()` before issuing the synchronous
`EventStarted`. A cancel-before-tick exits cleanly without firing a doomed
`started` followed by an equally doomed `stopped`.

**File:** `internal/seeder/session.go:174`

If a session is cancelled before its first tick the loop still fires an
`EventStarted` request (it will fail fast on the cancelled context), then
the very next iteration sends `EventStopped` for a session the tracker
never registered. Cosmetic: an extra failed request per shutdown race.

---

### 24. `Manager.numwant` lives on the manager, not the client — **DONE**

`Manager.numwant` field deleted. `seeder.New` no longer takes a numwant arg.
The `--client.max-peers` override is now applied at the CLI layer
(`if n := viper.GetInt("client.max-peers"); n > 0 { client.NumWant = n }`)
before passing the client to `seeder.New`. `tracker.BuildURL` already falls
back to `c.NumWant` when `p.NumWant == 0`, so the substitution path is
unchanged. Test signature updated.

**File:** `internal/seeder/seeder.go:58-59`

It is a property of the impersonation profile, not of the seeder. Could
live on `client.Client` (already has `NumWant` and `NumWantOnStop`) with
the CLI flag merely overriding the default. Lower priority.

---

### 25. `requireAuth` allows `/docs` without auth — **DONE**

Added a paragraph on `requireAuth` documenting that the `/docs/*` carve-out
is intentional: the OpenAPI spec leaks endpoint shape but no secrets, and
API consumers integrating with an auth-protected deployment need the docs
UI reachable.

**File:** `cmd/emission/auth.go:374-388`

By design: the middleware only gates `/api/*`. `/docs/*` is reachable on
auth-enabled deployments. Worth a one-line comment confirming this is
intentional (the spec leaks endpoint shape but no secrets).

---

### 26. WebSocket origin policy is wildcard — **DONE**

`handleWS` now reads `OriginPatterns` from a precomputed
`server.wsOriginPatterns` slice. Computed once via `wsOrigins(publicURL)`:

- `publicURL == ""` (local-network / no public URL configured) → wildcard,
  preserving the legacy convenience.
- `publicURL` set → only that host plus `localhost:*`, `127.0.0.1:*`,
  `[::1]:*` (dev access still works through the vite proxy).

Closes the read-only stats leak on auth-disabled deployments fronted by a
public reverse proxy. Auth-enabled deployments were already safe via
`SameSite=Strict` + `requireAuth`. Unit test added.

**File:** `cmd/emission/api.go:447-449`

```go
OriginPatterns: []string{"*"}
```

Documented as needed for the vite dev proxy. For a deployment exposed
publicly this loosens CSRF protection on the WS. Tighten when
`--http.public-url` is set: accept only that origin (and `localhost` for
dev).

---

## Summary

| Category   | Count | Done |
|------------|-------|------|
| Bugs       | 7     | 7    |
| Test gaps  | 6     | 5    |
| Refactoring| 9     | 9    |
| Nice to have | 4   | 4    |
| **Total**  | 26    | 25   |

**Resolved:** #1, #2, #3, #4, #5, #6, #7, #8, #9, #10, #11, #13, #14, #15, #16, #17, #18, #19, #20, #21, #22, #23, #24, #25, #26.
**Partial:** #12 (helpers covered; full handler integration deferred).

Only #12 follow-up (full HTTP handler integration tests) remains.

**Suggested priority order:**

1. #14 (move pkg→internal — easiest blast radius to bound now, harder later)
2. #1 (stdlib log) and #2 (dead test) — trivial cleanups
3. #3, #4 (client algorithm correctness)
4. #5 (decide: finish maxRatio feature or rip out half-wired UI)
5. #8, #9, #10, #11, #12 (test coverage)
6. Remaining refactoring items
