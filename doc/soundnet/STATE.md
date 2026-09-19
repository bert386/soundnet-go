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
| M1 taxonomy + YAMNet | taxonomy **done**; YAMNet registered but **cannot install yet** (see below) |
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

The fork's entire contact with upstream files is **27 added lines across 5
files, zero deletions**. Keeping it this small was a deliberate goal and should
stay one:

    internal/analysis/processor/processor.go   9   pipeline hook
    internal/analysis/api_service.go           6   startup call
    internal/conf/config.go                    5   settings field
    internal/api/v2/settings.go                4   settings section
    internal/conf/defaults.go                  3   defaults call
    internal/api/v2/api.go                     3   route registration
    frontend/.../DetectionDetail.svelte       13   panel render

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

## Immediately resumable work

**In flight when this was written:** teaching the model gallery to fetch from a
non-HuggingFace mirror. `ModelManager.Install` already accepts a `baseURL` that
bypasses repo construction (`model_manager.go` ~line 1983, currently described as
test injection). The intended change is a `BaseURL` field on `CatalogEntry` used
when set, so YAMNet can fetch from
`https://raw.githubusercontent.com/bert386/soundnet-models/main` with
`RemotePath: yamnet/yamnet.tflite`. That is the only thing blocking YAMNet
installation; artefacts and checksums are already correct and verified fetchable.

**Then, in rough value order:**

1. **M8 enriched alerts** - carry diagnostics and identity through the existing
   alert engine. Self-contained, and it makes detections actionable.
2. **Wire M6** to the audio capture buffer and a config section. The collector
   logic and its `Capturer` interface exist; nothing implements the interface.
3. **Threshold tuner UI** - its API (`/threshold-preview`) is done.
4. **M4** sub-classification heads, once M6 has collected a corpus.
5. **De-bird the UI copy** - 215 hardcoded "BirdNET-Go" strings across 130 files.
   The binary still announces itself as BirdNET-Go; the branding hook only covers
   MQTT discovery, API links and user agents.

## Verification habits worth keeping

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
