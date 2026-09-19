# Project state

Written 2026-09-19 as a handoff. Everything needed to resume without the
originating conversation. Companion files: SCOPE.md (the brief), DECISIONS.md
(resolved decisions), OPEN_DECISIONS.md (what still needs an answer),
ENVIRONMENT.md (machines, toolchain, operational gotchas).

## Where the work lives

| | |
|---|---|
| Authoritative repo | `/root/soundnet-go` inside WSL2 Ubuntu |
| Windows access | `\\wsl.localhost\Ubuntu\root\soundnet-go` |
| Push relay | `C:\Users\arnol\Projects\SoundNet\git\soundnet-go` (see ENVIRONMENT.md - pushes fail from WSL) |
| Branch | `m1-m2-taxonomy-and-records`, pushed to origin |
| `main` | M0 only |
| Model mirror | https://github.com/bert386/soundnet-models |
| Deployment | `pi@192.168.3.89` (`birdpi`), web UI on :8080 |

## Milestone status

| | State |
|---|---|
| M0 project setup | **done**, merged to main |
| M1 taxonomy + YAMNet | taxonomy **done**; YAMNet registered, pinned and installable; **no inference adapter yet**, so it cannot run |
| M2 detection records | **done** |
| M3 DSP diagnostics | **done**, 24.8 ms/clip measured on the Pi against a 100 ms budget |
| M5 enrichment / ADS-B | **done** including registration, type, operator and route |
| M6 auto-label collector | **logic done**, not wired to the audio buffer or config |
| M7 web UI | detail panel, review queue, training export, confusion + threshold APIs **done**; tuner UI and live re-compute remain |
| M4 sub-classification heads | **not started** - correctly last, it trains on M6's corpus |
| M8 enriched alerts | **not started** |

## Packages added

    internal/eventclass      event taxonomy; AudioSet + BirdNET label mapping
    internal/eventrecord     storage for diagnostics, enrichment, corrections
    internal/acoustics       the DSP properties layer (named so; see below)
    internal/enrichment      provider contract, geodesy, acoustic-lag correction
    internal/enrichment/adsb OpenSky client, aircraft identity, metadata lookup
    internal/eventpipeline   orchestration: decides which layers run
    internal/autolabel       M6 corpus collector
    internal/api/v2/soundnet_api.go  inspection and review endpoints

## Upstream footprint

Measured with `git diff --numstat main`, not from memory. The **code** footprint
is **58 added lines and 1 deleted across 9 upstream files**. Keeping it small was
a deliberate goal and should stay one:

    internal/analysis/processor/processor.go   9      pipeline hook
    internal/analysis/api_service.go           6      startup call
    internal/conf/config.go                    5      settings field
    internal/api/v2/settings.go                4      settings section
    internal/conf/defaults.go                  3      defaults call
    internal/api/v2/api.go                     3      route registration
    internal/classifier/model_catalog.go       6      CatalogEntry.BaseURL
    internal/classifier/model_manager.go       9 -1   fold BaseURL into the fetch
    frontend/.../views/DetectionDetail.svelte 13      panel render

Plus one data file, which earlier versions of this table wrongly left out:

    frontend/static/messages/en.json          95 -2   i18n strings for the panels

That is by far the largest single edit, and it is worth being honest about even
though appended JSON keys conflict less painfully than code. The 2 deletions are
reformatting, not removed strings.

Three upstream **test** files also carry a row each (9 lines), because their
tables are exhaustive over the registry and catalog and a fork-added model has
to declare itself in them:

    internal/classifier/range_filter_compat_test.go           YAMNet: compat None
    internal/classifier/model_registry_participation_test.go  YAMNet: participates false
    internal/classifier/model_catalog_test.go                 acoustic-event category, 17 entries

Every row is marked `SOUNDNET:` so a merge conflict is self-explaining.

Everything else is new files. `ModelRegistry` and `EmbeddedCatalog` are
package-level vars, so YAMNet registers from an `init()` in a new file with zero
modified lines - use that pattern for future models.

## Decisions that shaped the design

These are load-bearing. Changing one changes behaviour elsewhere.

**The scope targets a deprecated datastore.** It describes extending the `Note`
struct; that is the legacy store. The default is a normalised v2 datastore which
already generalises labels beyond species (`Label` carries a `LabelType` and a
nullable `TaxonomicClass`). M1 and M2 were therefore far smaller than written -
most of the taxonomy layer already existed.

**Identity comes from an authority or not at all.** `Domain.Enrichable()` is true
only for aircraft and weather. For gunshots, sirens and vehicles no public source
exists, so providers return nothing. A fabricated identity is indistinguishable
downstream from a real one, which makes it worse than silence.

**Diagnostics emit measurements, never labels.** Where a distinction is not
recoverable from one microphone - gunshot versus vehicle backfire - the features
are exposed with a low-confidence flag and a stated reason. An earlier version
flagged only a middle band of rise times, which implicitly asserted "gunshot" for
anything sharper; that was a bug and is fixed.

**Training labels are held to a higher standard than runtime identifications.** A
wrong runtime match is one wrong row; a wrong training label teaches the model
something false forever. M6 therefore refuses to capture when two aircraft are at
similar range, where M5 would still pick a best candidate.

**Review state is derived, not stored**, so upstream's `VerificationStatus` enum
needed no change: unreviewed = no review row, corrected = a `Correction` row
exists. Corrections keep the original label beside the corrected one, because the
confusion pair is what retraining learns from.

**`MaxCredibleLag` is 20 s (~6.9 km).** A live sample at the station showed an
airliner at 14.7 km slant range - a 42.9 s acoustic delay, over which a jet
travels ~11 km. Back-projecting that far is unsafe, so matches beyond the bound
are refused outright rather than accepted with low confidence: the failure mode
there is a confident wrong answer.

**Package named `internal/acoustics`, not `internal/diagnostics`** as the scope
says - upstream already owns that name for boot and migration journals.

## What is running on the Pi

Started by a **cron keepalive** (`~/soundnet/keepalive.sh`, every minute plus
`@reboot`). This is not decoration: `nohup`, `setsid` and `systemd-run --user`
all die when the SSH session ends because `Linger=no` for the `pi` user. Cron
runs detached from login sessions, which is why it works.

Config at `~/.config/birdnet-go/config.yaml`:

- `soundnet.enabled: true`, diagnostics and enrichment both on
- station elevation 140 m; latitude/longitude reuse `birdnet.latitude/longitude`
- `birdnet.rangefilter.passunmappedspecies: true` - **essential**. Non-bird
  classes are absent from the geomodel and were being scored 0.0 and discarded,
  so `Gun`, `Engine` and `Siren` could never be detected. Verified by
  `passed_filter` going from 0 to 1.
- audio device `usb-path:usb-0000:01:00.0-1.2` (a KT USB Audio capture device)
- OpenSky credentials at `~/soundnet/opensky.json`, referenced by path
- the running binary is `soundnet-go`, not `birdnet-go`. `pgrep birdnet-go`
  returns nothing and looks exactly like a dead service.

**Field observation, 2026-09-20 06:30 local.** 316 detections recorded, **every
one a bird species** (Willie-wagtail 103, Rainbow Lorikeet 50, Superb Fairywren
44, Eurasian Blackbird 43, Little Wattlebird 43, then a tail). The
`/api/v2/soundnet/detections` endpoint returns `count: 0`.

That is the expected result, not a fault, and it is worth stating why so it is
not re-diagnosed later. The only classifier running is BirdNET v2.4, whose label
set is species plus a handful of weakly-trained non-bird labels; at the
configured threshold of 0.7 those effectively never fire. Nothing non-bird has
been classified, so the pipeline hook has had no diagnosable domain to act on
and has correctly written nothing. **The config is not the problem** - range
filter, elevation, diagnostics and enrichment are all on and correct. The
missing piece is the YAMNet inference adapter. Until that exists this figure
will stay at zero no matter how long the Pi runs.

## Immediately resumable work

**Done since:** the gallery can now fetch from a non-HuggingFace host
(`CatalogEntry.BaseURL`), so YAMNet can be installed. Both artefacts are pinned
and verified fetchable from the GitHub mirror.

**Next, and the real blocker:**

1. **The YAMNet inference adapter.** Registration, catalog and fetch are done;
   nothing runs the model. Until this exists the fork cannot produce a single
   non-bird detection, which is the whole point of it. See OPEN_DECISIONS.md
   item 2, including the framing mismatch worth resolving first: YAMNet's
   0.975 s frame is shorter than the 3 s clip diagnostics expect.
2. **M8 enriched alerts** - carry diagnostics and identity through the existing
   alert engine. Self-contained, and it makes detections actionable.
3. **Wire M6** to the audio capture buffer and a config section. The collector
   logic and its `Capturer` interface exist; nothing implements the interface.
4. **Threshold tuner UI** - its API (`/threshold-preview`) is done.
5. **M4** sub-classification heads, once M6 has collected a corpus.
6. **De-bird the UI copy** - 215 hardcoded "BirdNET-Go" strings across 130 files.
   The binary still announces itself as BirdNET-Go; the branding hook only covers
   MQTT discovery, API links and user agents.

## Verification habits worth keeping

- **Run the whole package, not just the tests you wrote.** Registering YAMNet
  broke five upstream tests in `internal/classifier` and they sat failing across
  several commits, because each run was filtered with `-run` to the new tests.
  Upstream keeps exhaustive tables over the registry and catalog precisely so a
  new model cannot appear without declaring itself; filtering them out defeats
  the guard that was written for this exact case.
- **Lint gates the commit.** Two packages were committed with `golangci-lint`
  failing before this was enforced in the script.
- **Check the md5 after deploying**, not just that the port is open. A binary
  swap silently failed because cron restarted the old one mid-`mv` ("text file
  busy") and `set -e` aborted before printing anything.
- **Build and test through `task`**, never bare `go build`/`go test` - the
  Taskfile supplies the TensorFlow header include path, and tests need
  `-tags noembed,skipfrontend`.
- Nine of the Pi's test failures are environmental (8 from running as root, 1
  from WSL's mount table). None come from fork code.
