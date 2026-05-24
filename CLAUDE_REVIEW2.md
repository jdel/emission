# Last Review

Fresh-eyes pass over the codebase after CLAUDE_REVIEW1.md cleanup. Sections:

1. **Verification** — every CLAUDE_REVIEW1.md finding re-checked against current code.
2. **New findings** — issues spotted on this pass.
3. **Test coverage** — what's covered, what isn't, whether it's meaningful.
4. **Recommendations** — only changes that improve performance or readability.

Build/tests/vet: all green at the time of writing.

---

## 1. Verification of CLAUDE_REVIEW1.md fixes

Each previous finding traced to the current code; no regressions observed.

| # | Finding | Current state | Notes |
|---|---------|---------------|-------|
| 1 | stdlib `log` in auth.go | Fixed — zerolog throughout | `auth.go:269` uses `log.Error().Err(err).Str("path", dir).Msg(...)` |
| 2 | dead `TestRealTorrentFile` | Removed | `pkg/torrent/torrent_test.go` no longer references `/ratioup/tests/...` |
| 3 | `digitRangeToHex` skews `lo==hi` | Fixed | `if hi < lo { hi = lo }` plus `+1` outside `Uint64N` argument |
| 4 | `randomPoolWithChecksum` UTF-8 split | Fixed and renamed → `prefixedRandomPool` | utf8.RuneLen guard + `pickFittingRune` fallback |
| 5 | `SetSpeed` missing `maxRatio` | Fixed — renamed `SetClientOptions(id, min, max, ratio)` | Sidecar JSON gained `maxRatio` field; UI dialog wired |
| 6 | HTTP client recreated per announce | Fixed | `Params.HTTPClient` + package singleton; `Manager.httpClient` reused |
| 7 | `time.After` timer leak | Fixed | `runTracker` uses `time.NewTimer` + `defer Stop()` + `Reset` |
| 8 | `internal/seeder` no coverage | Fixed | 86% — three test files (manager/sidecar/helpers) |
| 9 | `pkg/torrent` thin coverage | Fixed | Multi-file, announce-list, private flag, `.local` filter, missing-info all covered |
| 10 | `pkg/bencode` happy-path only | Fixed | 10-case `TestDecodeErrors`, int edge cases, empty containers |
| 11 | `pkg/tracker` no e2e | Fixed | httptest covers happy/gzip/failure/network-error + parseResponse edge cases |
| 12 | HTTP handlers untested | **PARTIAL** | helpers covered (`safeTargetPath`, `queryInt`, `parseSpeedForm`, `wsOrigins`); handlers + middleware still untested |
| 13 | `TestRegenerate` PeerID-only | Fixed | Split into `TestRegeneratePeerID` and `TestRegenerateKey` |
| 14 | `pkg/*` should be internal | Fixed | `pkg/` directory gone; `bencode/torrent/tracker/units/client/docs` all under `internal/` |
| 15 | doc-types scattered | Fixed | `cmd/emission/api_types.go` consolidates them |
| 16 | owner-or-admin gate duplicated | Fixed | `server.authorizeTorrent(w, r, id)` extracted in `auth.go` |
| 17 | swagger handler rebuilt per request | Fixed | `sync.Map` keyed by `scheme\|host` |
| 18 | two RNG calls per accumulate tick | Fixed | Single signed draw, clamped to ≥1 |
| 19 | tracker formatter inconsistency | Fixed | `strconv.FormatUint`/`Itoa` throughout |
| 20 | sidecar lossy `ParseRate` round-trip | Fixed | Switched to `uint64` byte counts on disk |
| 21 | seed stats table mingles with logs | Fixed | Per-torrent structured zerolog entries |
| 22 | `all[:0:0]` reslice idiom | Fixed | Explicit `make([]Status, 0, len(all))` |
| 23 | `EventStarted` on cancelled ctx | Fixed | `if s.ctx.Err() != nil { return }` before first call |
| 24 | `Manager.numwant` placement | Fixed | Field gone; `client.NumWant` mutated at construction |
| 25 | `/docs` auth-free carve-out undocumented | Fixed | Comment block on `requireAuth` confirms intent |
| 26 | WebSocket wildcard origin | Fixed | `wsOrigins(publicURL)` restricts when public URL is set |

### Side effects checked

- `Manager.New` signature change (drop `numwant`) — caller sites in `cmd/emission/seed.go`, `cmd/emission/serve.go`, and `internal/seeder/manager_test.go` all updated. Confirmed no dangling references.
- Sidecar format change (string → uint64) intentionally has no backward-compat shim. Old sidecars fail to parse (`LoadSidecar` returns `ok=false`) and the defaults take over. Acceptable per project policy.
- `pkg/` removal scrubbed all imports across `cmd/`, `internal/`, tests, Makefile, sync-clients script, and README. No stragglers.
- Renames `SetSpeed`→`SetClientOptions` and `setTorrentSpeed`→`setClientOptions` propagated through both Go and TypeScript code. Frontend type `Torrent` gained `maxRatio` field; speed-edit dialog gained ratio input with non-negative validation.
- swag spec regenerated after the api_types.go consolidation. `internal/docs/swagger.json` references the consolidated types.

---

## 2. New findings (this pass)

### 2.1 `writeError` uses an anonymous `map[string]string` — minor inconsistency

**File:** `cmd/emission/api.go:491-493`

```go
func writeError(w http.ResponseWriter, status int, msg string) {
    writeJSON(w, status, map[string]string{"error": msg})
}
```

#15 consolidated every shape into `api_types.go` and migrated handlers to
named types. `writeError` was missed — it still emits an anonymous map.
Wire-shape identical to `errorResponse`, so swagger and runtime agree, but
the internal inconsistency is jarring. Trivial fix.

### 2.2 Variable shadowing of `client` package — readability footgun

**Files:** `cmd/emission/seed.go:48-54`, `cmd/emission/serve.go:76-82`

```go
client, err := client.New(viper.GetString("client.name"))
if err != nil {
    return err
}
if n := viper.GetInt("client.max-peers"); n > 0 {
    client.NumWant = n
}
```

The local variable `client` shadows the package import `client` for the
rest of the function body. Currently nothing inside these functions needs
the package after this point, so it compiles and works. Adding any later
reference to `client.New`, `client.Versions`, etc. would produce a
confusing error. Rename to `c` (matches the convention already used in
tests).

### 2.3 Two more `map[string]any` responses missed by #15 — **DONE**

Named in `api_types.go`: `registerChallenge` (ceremonyId + options +
username), `loginChallenge` (ceremonyId + options), `authResult`
(authenticated bool). Swagger annotations updated, spec regenerated.
WebAuthn `*protocol.CredentialCreation` / `*protocol.CredentialAssertion`
now appear in the spec with their library-defined schema.

### 2.4 `SaveSidecar` non-atomic — minor reliability gap

**File:** `internal/seeder/sidecar.go:38-48`

```go
return os.WriteFile(sidecarPath(torrentPath), data, 0o644)
```

`os.WriteFile` truncates then writes. A crash mid-write leaves a truncated
JSON file. `LoadSidecar` is defensive (returns `ok=false` on parse error,
defaults apply), so the impact is bounded: per-torrent overrides revert to
manager-level defaults until the user edits them again.

`internal/auth/credentials.go` already uses the atomic tmp+rename pattern
for the equivalent on-disk state. Aligning sidecar with that pattern would
remove the asymmetry. Small change, small win.

### 2.5 `uploadTorrent` TOCTOU on existence check — benign

**File:** `cmd/emission/api.go:222-238, 259`

Between `s.mgr.Exists(id)` (line 223), `os.Stat(target)` (line 236), and
`os.WriteFile(target, ...)` (line 259), concurrent uploads can race.
Worst-case outcome: two HTTP `202 Accepted` responses, but only one
session is created — the watcher's `AddFile` is the authoritative dedup
(holds `m.mu` for the duplicate check). Not a correctness bug; only
client-visible surprise is that one of two concurrent uploads "succeeded"
without becoming a new torrent.

Not worth fixing unless concurrent uploads become a real scenario.

### 2.6 `RemoveByPath` race with concurrent `AddFile`

**File:** `internal/seeder/seeder.go:214-230`

If the directory watcher fires a Remove event while an HTTP `AddFile` is
in flight for the same path:

1. AddFile reads file → parses → acquires `m.mu` → registers session
2. Watcher's Remove event has already deleted the file → calls `RemoveByPath`
3. RemoveByPath acquires `m.mu` after AddFile releases → cancels session

End state is correct (no session, no file). The window where the session
briefly exists with no backing file is harmless because nothing
re-derives the file from disk while running. Logging may show a spurious
"seeding" → "stopped" pair for the same torrent. Documented edge case;
not a bug.

### 2.7 `session.maxRatio` placement next to atomics — minor confusion

**File:** `internal/seeder/session.go:49-55`

```go
minSpeed  atomic.Uint64
maxSpeed  atomic.Uint64
uploaded  atomic.Uint64
rate      atomic.Uint64
uploadCap atomic.Uint64
maxRatio  float64       // original ratio cap; read/written under Manager.mu
```

The single non-atomic field sits inside a block of atomics. The trailing
comment documents the difference, but a reader skimming the struct could
miss it. Consider grouping non-atomic fields in a separate stanza, or
hoisting `maxRatio` above the atomic block.

### 2.8 Test helper noise: `newTrackerServer` requires manual `defer srv.Close()`

**File:** `internal/seeder/manager_test.go`

Every test repeats:
```go
srv := newTrackerServer(t)
defer srv.Close()
```

The helper takes `*testing.T` already; it could register the close via
`t.Cleanup(srv.Close)` and drop the `defer` from each test. Saves a line
per test and removes a footgun (forgetting the defer would leak the
httptest server).

Also: `defer` runs before `t.Cleanup`, so the tracker server is closed
before `Manager.Shutdown`. Shutdown's stopped-announce calls then fail
with "connection refused" and log errors. The errors are harmless but
spam the test output. Reversing the order (use Cleanup for both, or close
server after shutdown) would silence the noise.

### 2.9 `accumulateLoop` `int64` conversion — narrow upper bound — **DONE**

Added `if rate > math.MaxInt64 { rate = math.MaxInt64 }` at the top of
the accumulation step. Comment notes the cap is a belt-and-braces guard
for an unreachable-in-practice condition, not an expected branch.

### 2.10 `Manager.AddFile` runs file I/O outside the lock but holds it during goroutine spawn

**File:** `internal/seeder/seeder.go:105-144`

Sidecar load, file read, and torrent parse all happen outside `m.mu`
(good — these are slow ops). The lock is then held for the dup check,
session construction, map inserts, **goroutine spawn** (`go s.run()`),
and `notifyChanged`. The goroutine spawn is cheap (just `runtime.newproc`)
but `notifyChanged` does a lock acquisition on `subsMu`. Holding `m.mu`
while taking another lock is fine here (lock order: `m.mu` → `subsMu` is
consistent across the codebase) but worth keeping in mind for future
locking changes.

---

## 3. Test coverage

```
internal/units         100.0%
internal/bencode        93.9%
internal/seeder         85.8%
internal/tracker        85.7%
internal/torrent        84.3%
internal/client         72.8%
internal/auth           42.2%
cmd/emission             7.4%
```

### Strong

- **`internal/seeder`** — Manager API surface fully exercised (Add, Remove,
  RemoveByPath, RemoveUnder, Page filtering/paging, Visible scoping,
  SetClientOptions, Subscribe coalesce, sidecar round-trip). Uses an
  `httptest` tracker so sessions run against a real announce response.
  Tests test real code paths, not invented scenarios.
- **`internal/torrent`** — multi-file, announce-list dedup, private flag,
  `.local` filter, missing-info rejection. Bencode building via a small
  `bstr` helper keeps the test data readable.
- **`internal/bencode`** — 10 distinct error cases, plus int edge cases,
  plus empty containers. The error sub-tests genuinely exercise
  unreachable-without-careful-input branches.
- **`internal/tracker`** — round-trip via `httptest.Server` including the
  Content-Encoding: gzip branch (otherwise dead code, now covered).
  `BuildURL` substitution asserts a *server-side* observation of the URL,
  not just string contents.
- **`internal/units`** — 100%; small surface, exhaustive table.

### Adequate

- **`internal/client`** (73%) — `TestAllVersionsBuild` runs every shipped
  profile through `New` and checks the query template; regeneration
  covered for both `PeerID` and `Key`. Missing: edge cases inside
  `prefixedRandomPool`'s UTF-8 overflow path (`pickFittingRune`
  fallthrough), error-return paths in `genFromPattern`. The covered
  surface is the load-bearing one, though.

### Weak

- **`internal/auth`** (83%, was 42%) — `Service` layer now tested:
  `NewService` URL validation, `BootstrapOpen` across three states,
  `BeginRegistration` bootstrap + invited paths plus the ceremony
  store/consume lifecycle and the credential exclude list,
  `BeginLogin` empty + populated, `Finish*` unknown-ceremony errors,
  `takeCeremony` expiry + eviction, `SessionUser` / `EndSession`
  round-trip, and service-level delegation to the credential store.
  `CredentialStore` gained dup-Add, `UsernameFor`, `Remove`, `RemoveUser`,
  `List` defensive-copy, and corrupt-file cases.

  The remaining gap (`FinishRegistration` 19%, `FinishLogin` 37%) is
  the WebAuthn protocol call — testing it meaningfully requires forging
  an authenticator response, which would exercise library glue, not
  emission's own code.
- **`cmd/emission`** (7%) — Carries over from CLAUDE_REVIEW1.md #12. Helpers
  (`safeTargetPath`, `queryInt`, `parseSpeedForm`, `wsOrigins`) are
  covered; handlers (`uploadTorrent`, `updateTorrent`, `removeTorrent`,
  `listTorrents`, `handleWS`) and `requireAuth` middleware aren't.
  Integration tests would need an injectable mock for `*auth.Service`
  (currently a concrete type, not an interface).

### Meaningfulness audit

Spot-checked tests for "tests pass even when behavior is wrong":

- `TestSidecarRoundtrip` was tightened with `min=2000` specifically
  because the old "1.0 KB" format would have rounded it to 2048. Defends
  against the regression. ✓
- `TestParseSpeedFormValidation` checks substring of error message ("min-speed",
  "max-ratio", "non-negative", "exceeds"). If the error wording changes,
  the test fails. Fair trade-off vs over-matching the full text. ✓
- `TestSafeTargetPath` asserts the resolved path is under the storage
  root (`filepath.Rel` invariant). A bug that lets `..` escape would
  fail this even if the error-vs-success expectation matches. ✓
- `TestManagerSubscribeCoalesces` asserts the *absence* of a second
  signal within 50 ms. If `notifyChanged` ever forgets to coalesce, the
  test flips. ✓
- `TestPickRate` does 100 iterations and checks both bounds — a one-off
  off-by-one in `pickRate` (e.g. returning `[min, max)` instead of
  `[min, max]`) would be caught statistically. ✓

No "always-true" or "never-fails" tests spotted.

---

## 4. Recommendations

Per the criteria — only suggesting changes that (a) clearly improve
performance or (b) clearly improve human readability.

### 4.1 Use `errorResponse` in `writeError` (readability) — **DONE**

`writeError` now emits `errorResponse{Error: msg}`. The lone
`map[string]string` literal is gone; all JSON shapes flow through the
`api_types.go` types.

### 4.2 Rename shadowed `client` variable (readability) — **DONE**

`runSeed` and `runServe` now use `c` for the constructed
`*client.Client`. No more package shadowing.

### 4.3 Atomic sidecar write (reliability) — **DONE**

`SaveSidecar` writes to `<path>.tmp` then `os.Rename`s. Matches the
atomic write pattern in `internal/auth/credentials.go`.

### 4.4 `newTrackerServer` should `t.Cleanup(srv.Close)` (readability) — **DONE**

Helper registers `t.Cleanup(srv.Close)`. Removed 13 redundant
`defer srv.Close()` lines across the test file. Cleanup runs LIFO, so
`Manager.Shutdown` (registered later) fires first against a live server
— eliminates the "connection refused" log noise during the stopped
announces.

### Not recommended

- **Handler integration tests.** Real value but requires extracting
  `*auth.Service` into an interface to mock cleanly. Bigger surgery than
  the criteria support; #12 already documents this as deferred.
- **Time-injected `accumulateLoop` tests.** Would require refactoring the
  loop to take a clock. The behavior is simple enough that the cost
  outweighs the win.
- **`maxRatio` field repositioning.** Cosmetic only; comment already
  documents the difference.
- **`map[string]any` ceremony responses → named types.** Cosmetic; the
  embedded WebAuthn protocol types make the schema noisier in Swagger
  but the runtime behavior is fine.

---

## Summary

The codebase is in good shape after the CLAUDE_REVIEW1.md cleanup. All 25 fully
fixed items have held; #12 remains the documented partial. No regressions
introduced by the recent refactors.

Coverage where it matters (parsers, manager, tracker round-trip) is
strong (>84%). The two genuine gaps (`cmd/emission` handlers,
`internal/auth.Service`) are both blocked on the same refactor — making
auth.Service mockable — which is a deliberate "later" item.

Four small, well-bounded improvements suggested above (4.1–4.4). None are
load-bearing; all four together would be under 30 lines of change.
