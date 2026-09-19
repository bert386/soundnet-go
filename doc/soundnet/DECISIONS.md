# Decisions log

Answers to SCOPE.md §6 "Open decisions for the human". Update as things change.

| # | Decision | Answer | Date |
|---|---|---|---|
| 4 | Project name (replaces *Acuity* placeholder) | **SoundNet** — module/binary `soundnet-go` | 2026-09-19 |
| — | Fork strategy | Forked to `github.com/bert386/soundnet-go`, cloned to `git/soundnet-go` | 2026-09-19 |
| 3 | Target deployment hardware | **Raspberry Pi 4** — sets the M3 perf budget (<100 ms/clip) and the CI benchmark ceiling | 2026-09-19 |
| 1 | ADS-B source (M5) | **OpenSky REST API** | 2026-09-19 |
| — | Module rename scope | **Full rename** to `github.com/bert386/soundnet-go` (1,190 files). Chosen over binary-only rename despite the upstream-merge cost; mitigated by `scripts/rewrite-module-path.sh` | 2026-09-19 |
| — | Build environment | **WSL2 Ubuntu** — matches the Pi 4 target and CI; Windows lacks a workable CGO/TFLite path | 2026-09-19 |
| 2 | Station geo (lat / lon / elevation) | **Resolved as a design requirement** — operator-entered via web config, not a build-time constant. See "Station geo" below | 2026-09-19 |

## Consequences

### Pi 4 target
- Diagnostics and enrichment stages must each be skippable per class; defaults off where they cost.
- YAMNet (~3.7M params) is the ceiling for added models. No Coral TPU (upstream model incompatibility).
- CI needs a benchmark gate on the M3 DSP functions.

### OpenSky
- No local receiver hardware needed, but: rate-limited, ~5–15 s state-vector latency, and coverage
  at the station depends on nearby volunteer feeders — **verify coverage before trusting M6 auto-labels**.
- Authentication: confirm current OpenSky auth model before implementing (they moved from
  anonymous/basic-auth to OAuth2 client credentials; anonymous quota is minimal or gone).
  Credentials go under `enrichment.adsb.opensky` in `config.yaml`, never hard-coded.
- Latency + rate limits make the acoustic-lag correction (§M5) more important, not less: the
  matching window must absorb both the speed-of-sound lag and OpenSky's own sampling interval.
- Provider interface still gets a `dump1090`/`readsb` implementation seam so a local receiver can
  be dropped in later without touching call sites.

### Station geo — web config requirement (M7, needed by M5/M6)
Station position is **operator input in the web config UI**, never hard-coded. Requirements:

1. **Manual entry** — latitude / longitude / elevation fields. Accept both decimal degrees
   (e.g. `-33.86785`) and degrees-minutes-seconds (e.g. `33 52 04 S`); normalise to decimal
   internally and display both.
2. **Auto-detect button** — browser Geolocation API (`navigator.geolocation`). Requires a secure
   context (HTTPS or localhost) and an explicit browser permission prompt, so it is opt-in by
   construction. Show the returned accuracy radius and let the operator accept or discard it.
   Do **not** fall back to IP geolocation: it is city-accurate at best and silently sends the
   station's whereabouts to a third party, which cuts against the privacy-by-design rule.
3. **Lookup link** — an address-to-coordinates helper opening in a new tab, for operators who
   know their address but not their coordinates. Candidate: https://www.latlong.net/
   Link only — no server-side geocoding call, nothing leaves the station.
4. **Elevation** — metres above sea level, manual entry (browser geolocation altitude is
   unreliable). Needed for the slant-range term, sqrt(horizontal^2 + altitude^2), in the
   acoustic-lag correction.
5. **Validation on load** — lat in [-90, 90], lon in [-180, 180], elevation sane; enrichment and
   the M6 collector stay inert (and say so in the UI) until all three are set.

**Precision target:** 1 arc-second is about 31 m of latitude, well inside the tolerance for ADS-B
track matching. 5 decimal places (about 1 m) is more than enough. Store as float64.

### Obtained
- OpenSky client credentials (2026-09-19). Stored in `secrets/opensky-credentials.json`,
  deliberately **outside** the git repo so they cannot be committed. Verified working:
  token exchange OK, `/states/all` returned HTTP 200, standard tier (4,000 credits/day).
  Config must reference them by *path* or env var, never inline the secret.

### Module rename — merge mitigation
Renaming the module path rewrites imports in 65% of the Go files, which would make every
`git merge upstream/main` conflict broadly. Mitigation, rather than acceptance:

- The rename is **one isolated commit**, so it can be reasoned about and reverted independently.
- `scripts/rewrite-module-path.sh` applies the identical rewrite to an upstream branch before
  merging. Upstream's import lines then already match ours and most conflicts disappear.
- The script is deliberately narrow: Go imports, `go.mod`, `.mockery.yaml` only. It must never
  touch `LICENSE`, `NOTICE` or `AUTHORS` — naming upstream there is a CC BY-NC-SA obligation.

Two side effects found and handled during the rename:
1. **Import ordering** — `bert386` sorts before `invopop`/`spf13` where `tphakala` sorted after,
   so mixed import groups went out of order and would have failed `golangci-lint`. Fixed with
   `gofmt -w`; verified 0 of 1,188 files still misordered.
2. **Line endings** — the repo has `core.autocrlf = true` and no `.gitattributes`, so the whole
   working tree was CRLF. Set to `input` locally (not a committed change) since CI and the Pi
   target are Linux. Pre-existing condition, not caused by the rename.

## Toolchain (2026-09-19)
- Go **1.27.0** — installed via winget (`GoLang.Go`).
- Task **3.53.1** — installed via winget (`Task.Task`).
- Node **24.15.0** — already present.
