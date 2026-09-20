# Project state

Written 2026-09-19 as a handoff. Everything needed to resume without the
originating conversation. Companion files: SCOPE.md (the brief), DECISIONS.md
(resolved decisions), OPEN_DECISIONS.md (what still needs an answer),
ENVIRONMENT.md (machines, toolchain, operational gotchas), GROUND_TRUTH.md
(the operator-labelled aircraft set and what it proved).

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
| M1 taxonomy + YAMNet | **done** - taxonomy, catalog entry, fetch route, inference adapter. Live on the Pi at 26 ms/window |
| M2 detection records | **done** |
| M3 DSP diagnostics | **done**, 24.8 ms/clip measured on the Pi against a 100 ms budget |
| M5 enrichment / ADS-B | **done** including registration, type, operator and route, plus ambiguous-label resolution for `Vehicle`/`Engine` (untested on the Pi) |
| M6 auto-label collector | **logic done**, not wired to the audio buffer or config |
| M7 web UI | detail panel, review queue, training export, confusion + threshold APIs, **event-domain filter (API + UI)**, **display names** done; tuner UI and live re-compute remain |
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
    internal/classifier/yamnet.go               the YAMNet ModelInstance
    internal/classifier/orchestrator_yamnet.go  its loader
    internal/labels/nonbird/classes_soundnet.go AudioSet class categories
    internal/api/v2/soundnet_api.go  inspection and review endpoints

## Upstream footprint

Measured with `git diff --numstat main`, not from memory - the figure here was
wrong twice when written by hand, so recount it rather than trust it
(`cmd/clipscore` aside, the script used is in the scratchpad of the originating
conversation; `git diff --numstat main` and grouping by suffix is the whole of
it).

**Production Go: 14 files, +117 -4.** The number that matters for merges. Only
files that already exist on `main` are counted - a new fork-owned file is not a
merge cost, an edited upstream file is.

    internal/api/v2/detections/detections.go  41 -3  category filter, routing, display name
    internal/classifier/model_manager.go       9 -1   fold BaseURL into the fetch
    internal/analysis/processor/processor.go   9      pipeline hook
    internal/api/v2/apicore/sse.go             8      display name on the live feed
    internal/api/v2/detections/search.go       8      display name on search results
    internal/detection/model_info.go           7      YAMNet is not a bird
    internal/analysis/api_service.go           6      startup call
    internal/classifier/model_catalog.go       6      CatalogEntry.BaseURL
    internal/conf/config.go                    5      settings field
    internal/datastore/model.go                5      DetectionRecord.EventDisplayName
    internal/api/v2/settings.go                4      settings section
    internal/api/v2/api.go                     3      route registration
    internal/api/v2/detections/handler.go      3      categories route
    internal/conf/defaults.go                  3      defaults call

**Upstream tests: 7 files, +45 -5.** Exhaustive registries that oblige a fork
addition to declare itself. Every row is marked `SOUNDNET:`. Each of these was
found by a test failing *after* the feature shipped, because the package had
never been run in full - see the verification habits below.

    internal/api/v2/routes_enumeration_test.go                golden route set
    internal/api/v2/hotreload_coverage_test.go                settings reload category
    internal/api/v2/settings_restart_test.go                  restart exemption
    internal/classifier/model_catalog_test.go                 category, entry count
    internal/labels/nonbird/nonbird_internal_test.go          class count
    internal/classifier/model_registry_participation_test.go  YAMNet: participates false
    internal/classifier/range_filter_compat_test.go           YAMNet: compat None

**Frontend: 10 files, +254 -12**, of which `frontend/static/messages/en.json` is
107. That is still the largest single edit in the fork; appended JSON keys
conflict less painfully than code, but it should be counted honestly.

Six of the ten are the display-name change, and each is the same one-line edit:
`localizeSpeciesName` gained an optional third argument, so the ~30 call sites
that do not pass it are untouched and the detection surfaces that do each pass
`detection.eventDisplayName`. The alternative - rewriting `commonName` on the
server, which needs no frontend change at all - was rejected because
`commonName` is the key `isSpeciesExcluded` matches on.

**Generated and documentation: 4 files, +180.** `config.schema.json` and
`doc/wiki/configuration-reference.md` are regenerated by `task generate-schema`
and must never be hand-edited - a previous attempt to stamp a banner on the
generated reference is what broke `TestSchemaUpToDate`.

Everything else the fork adds is new files, which cannot conflict at all.
`ModelRegistry`, `EmbeddedCatalog`, `modelLoaders`, `conf.ValidAudioModels` and
`nonbird.classes` are all package-level vars, so the fork extends them from an
`init()` in a new file with zero modified lines - use that pattern for anything
else that needs registering.

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

**Some labels do not determine their own domain, and an authority may settle
them.** AudioSet's ontology is a hierarchy while SoundNet's domains are flat, so
`Vehicle` and `Engine` are parents of road, rail, air **and** water transport -
which is why an aircraft scores highest on `Vehicle`. Those two classes carry
candidate domains (`internal/eventclass/ambiguity.go`) and the pipeline asks each
authority in turn, its own domain first.

This is the rule above applied, not relaxed. `Domain.Enrichable()` is unchanged
and still false for vehicles: it is the *label* that is ambiguous, not the
domain, and a detection that really is a car is still unidentifiable. The table
is two entries on purpose - `Car`, `Truck` and `Car passing by` are
road-specific and fire far more often, so asking about them would spend an API
credit to learn nothing. A test pins it at exactly those two so it cannot grow
by accident.

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

**Acoustics answer "motorised", ADS-B answers "which".** Operator-labelled
clips show `Vehicle` scoring 0.59-0.74 on every aircraft while `Aircraft` swings
0.11-0.50 - the model detects something motorised dependably and gets the kind
wrong. On one clip the operator could not tell aircraft from traffic either, so
`Vehicle` 0.738 / `Aircraft` 0.109 is the honest answer rather than a mistake.
This is the scope's layer separation arrived at from data, and it makes ADS-B
load-bearing rather than decorative: 0.15 alone never clears a sensible
threshold, 0.15 beside a confirmed overflight does. See GROUND_TRUTH.md.

**A filter that matches everything is indistinguishable from no filter.** Said
twice because it has bitten twice - the category filter was a no-op in
production while every unit test passed, and an unknown category now returns
400 rather than being ignored. Anything that narrows a result set should fail
loudly when it cannot.

**Package named `internal/acoustics`, not `internal/diagnostics`** as the scope
says - upstream already owns that name for boot and migration journals.

**YAMNet runs on a 3 s window, not its own 0.975 s frame.** Four evenly spaced
frames tile the window and their scores are combined by per-class maximum.
Maximum rather than mean because events are sparse and local: a two-second siren
inside a three-second window is a siren, not a third of one. Three seconds also
matches BirdNET, so a detection and its diagnostics describe the same audio.
There is a trap underneath this: `ModelSpec.ClipSizeBytes` truncates
`ClipLength` to whole seconds, so a sub-second spec silently yields a zero-byte
buffer and a model that loads, reports healthy and never infers.

**No activation is applied to YAMNet's output.** It is already per-class
sigmoid - verified, not assumed. BirdNET applies a sigmoid and Perch a softmax
to their backends' raw logits; either here would be wrong, and a second sigmoid
would compress everything into [0.5, 0.73].

**Extension by `init()` is the fork's main technique.** `ModelRegistry`,
`EmbeddedCatalog`, `modelLoaders`, `conf.ValidAudioModels` and
`nonbird.classes` are all package-level maps, so the fork adds to them from new
files and touches no upstream line. Two cautions learned the hard way: anything
*derived* from such a map (`nonbird.firstTokenSet`) must be rebuilt afterwards
because init order between files is not guaranteed, and upstream's exhaustive
table tests will fail until the new entry is declared in them too.

## What is running on the Pi

Started by a **cron keepalive** (`~/soundnet/keepalive.sh`, every minute plus
`@reboot`). This is not decoration: `nohup`, `setsid` and `systemd-run --user`
all die when the SSH session ends because `Linger=no` for the `pi` user. Cron
runs detached from login sessions, which is why it works.

Config at `~/.config/birdnet-go/config.yaml`:

- `soundnet.enabled: true`, diagnostics and enrichment both on
- station elevation 140 m; latitude/longitude reuse `birdnet.latitude/longitude`
- `birdnet.rangefilter.passunmappedspecies: true` - **essential**. Non-bird
  classes are absent from the geomodel and were being scored 0.0 and discarded.
- `realtime.privacyfilter.confidence: 0.7` - raised from 0.05 by the operator.
  See "YAMNet and the privacy filter" below; 0.05 discarded almost everything.
- audio device `usb-path:usb-0000:01:00.0-1.2`, `gain: 9`
- OpenSky credentials at `~/soundnet/opensky.json`, referenced by path
- models dir `~/.config/birdnet-go/models`; YAMNet in `models/yamnet-v1/`
- the running binary is `soundnet-go`, not `birdnet-go`. `pgrep birdnet-go`
  returns nothing and looks exactly like a dead service.
- `ffmpeg` and `sox` are installed (2026-09-20). Without them static
  spectrograms fail with the misleading "audio file format is invalid" - it is
  ffprobe that is missing, not the file that is bad - and each attempt burns
  8.5 s timing out, repeatedly.

**Two things about this Pi that changed conclusions.** Passwordless sudo is not
available, so package installs are the operator's to run. And SoundNet settings
are read once by `initSoundNet` at startup, so every change under `soundnet.`
needs a restart - the settings UI does not say so (see OPEN_DECISIONS).

## YAMNet: live, and what it actually does

Installed through the model gallery, which exercised `CatalogEntry.BaseURL`
against the GitHub mirror end to end - both files fetched with checksums
matching the pinned values.

**Performance is not a concern.** Measured over 73 analysis windows:

| Model | Per-window inference | Frames per window |
|---|---|---|
| YAMNet | **26 ms** typical, 65 ms worst | 4 |
| BirdNET v2.4 | 191 ms typical, 258 ms worst | 1 |

Against a 100 ms budget on a Cortex-A72. Both models together use 426 MB of
7.8 GB.

**What a clean hour looks like** (2026-09-20, 11:00-12:00): 82 detections, 75
birds, **7 events** - two operator-confirmed propeller aircraft, a jet
misclassified as Thunder/Thunderstorm, a siren, and a vehicle. Before the
taxonomy filter the same period produced ~500 rows/hour of `bird`, `animals`,
`insect` and `whistling`.

### Enabling a model does not route audio to it

`models.enabled` controls which models are **loaded**. Each audio source carries
its own `models:` list naming what it actually **feeds**, and when present that
list overrides the default target set:

    realtime.audio.sources[].models: [birdnet, yamnet]

With only `models.enabled` set, YAMNet loaded, appeared in `/api/v2/models`,
registered in the database, and received not one sample, with no warning
anywhere. **How to tell:** `/api/v2/system/inference` reports `sources: []`
against the model. That is the only place it shows.

### YAMNet and the privacy filter

YAMNet recognises speech at 0.98; `privacyfilter.confidence` was **0.05**,
calibrated for BirdNET's weak `Human` label (~0.06 on ambient noise). A privacy
hit discards every detection in the window, for every model. Routing YAMNet in
at 0.05 raised the trigger rate 18x and discarded 52 rainbow lorikeet, 21 common
myna and 13 little wattlebird detections in 17 minutes.

**Resolved by raising the threshold to 0.7**, on the operator's decision. This
works *because* YAMNet is accurate: real speech (0.98) is filtered, ambient
noise (<0.1) is not. Verified after: 8 triggers in 5 minutes, max 0.969, no
birds discarded.

**The non-obvious half.** Filtering YAMNet to the event taxonomy removes speech,
and the privacy and dog-bark filters can only act on results the adapter hands
them - so the taxonomy filter would have silently undone the protection the
raised threshold depends on. The adapter therefore reports the taxonomy's
classes *plus* anything `vocalization.IsHuman` or `IsDog` recognises. It costs
nothing in stored rows: a speech hit discards the whole window anyway.

### Three faults only a live run could surface

1. **Every class above the score floor was emitted**, so six minutes produced 49
   detections dominated by `animal` 0.96, `bird` 0.92, `whistling` 0.99. Now
   only taxonomy classes are reported, matched on AudioSetIndex.
2. **YAMNet had no model type**, so `ResolveModelType` fell to `ModelTypeBird`
   and `taxonomicClassForModel` assigned Aves - every acoustic event stored as a
   bird. Now `ModelTypeMulti`.
3. **Labels resolved to `DomainOther`**, so no diagnostics ran and ADS-B was
   never called for two confirmed aircraft. See below.

### The three label forms

This is the trap that has now caused three separate bugs, so it is worth stating
plainly. Every label exists in three forms:

    "Propeller, airscrew"      display name, what internal/eventclass holds
    "propeller_and_airscrew"   raw label, what a classifier emits
    "propeller" + "and" + "airscrew"   what the datastore stores

The third is upstream's doing, and it is a **three-way split, not a truncation**
- the distinction that caused the third bug. `detection.ParseSpeciesString` does
`strings.SplitN(label, "_", 3)` and stores the parts as a scientific name, a
common name and a species code, so a multi-word class arrives as three
fragments. `ExtractScientificName` keeps only the head, which is where the
"truncated name" shorthand comes from, but a reconstruction that assumes two
parts is wrong for every label with more than two tokens.

`eventclass.Resolve` accepts all three forms and must be used for anything that
acts on a label. **Single-word classes work under any of them**, which is
exactly why `Thunderstorm` and `Vehicle` resolved correctly and hid the bug for
hours - and why the same asymmetry hid the display-name bug afterwards.

Two tests hold this down. One proves no truncated name spans two domains, which
is what makes the truncated form safe to match on for domain decisions. The
other drives every class through the **real** `ParseSpeciesString` rather than
an imitation of it, and asserts the display name comes back - a local
reimplementation of the split is exactly how the wrong assumption survived.

The species code is not usable as a third key: `datastore/v2only` recomputes
`SpeciesCode` from a map keyed on scientific name when it reads a row back, so
for an event class - which is in no such map - it comes back empty. Anything
that needs to identify a class from stored fields keys on the **first two
tokens** (`eventclass.DisplayName`).

## The station has never written a SoundNet row

Found 2026-09-20 while verifying the vehicle-enrichment change. This outranks
everything in the list below, because most of it is downstream of it.

    GET /api/v2/soundnet/detections?limit=20  ->  {"count":0,"data":[]}

`soundnet: enabled diagnostics=true enrichment=true` is logged on every start
going back hours, `soundnet.enabled`, `diagnostics.enabled`,
`enrichment.enabled` and `adsb.enabled` are all true in the running config, and
the credentials file is where the config points. Rows that should have produced
diagnostics - id 759, 760, 805, 824, 825, 828, 905, all in diagnosable domains -
produced none. So **M3 and M5 have never run in production**, and neither the
ADS-B work nor the ambiguity work on top of it can do anything until this is
found.

Nothing is logged either way, which is the same failure shape as the model that
was loaded but fed no audio. `SoundNetAction.Execute` logs at Debug on success
and Warn on error, and says **nothing at all when it skips**; the console is at
info, so a silent skip is invisible. `eventpipeline.Process` already returns
`Result.Skipped` with the reason - it just is not logged. Log it (throttled, at
info) before anything else: that alone should name the cause.

Candidates, in order of suspicion, none yet eliminated:

- `buildSoundNetAction` returns nil because `det.pcmData3s` is empty. The clip
  is read from `item.PCMdata`, and detections flush through a pending queue
  ("Flushing detection" / "approving detection" in actions.log) rather than
  going straight to the action list - so whether the PCM is still attached by
  then is worth checking first.
- `DetectionContext.NoteID` is still 0 when the action executes, in which case
  `Process` skips with "detection was not persisted".
- `soundnet_diagnostics` does not exist, so `Migrate()` never ran. This would
  error and log a Warn, and no Warn appears - so it is the least likely.

## Non-taxonomy classes are being stored as detections

Also found on 2026-09-20. `chewing_and_mastication` and `crying_and_sobbing`
are emitted by the YAMNet adapter on purpose: they are `CategoryHuman`, and
`reportable()` passes human classes through so the privacy filter can see them.
The comment there says "emitting them costs nothing in stored rows. A speech hit
makes the privacy filter discard the whole window". That is only true **above**
the privacy threshold. Below it - 0.50 and 0.59 in the observed rows - the
window is kept and the class is stored as an ordinary detection, with no domain,
no display name, and a mangled name in the UI ("and_mastication"). Four rows in
one hour.

Either drop classes that exist only as filter inputs before they reach storage,
or give them a taxonomy entry. The first is probably right: they are not events.

## Immediately resumable work

Ordered by value. Items 1 and 2 are done; both need a live run on the Pi, which
was offline when they were written.

**1. Vehicle-domain enrichment. Done; taxonomy and API verified live, pipeline
blocked.** The station reports it - id 905 (`vehicle`) now answers
`enrichable: true` with `candidateDomains: [vehicle, aircraft, rail,
watercraft]`, where before it was `false` - but no enrichment row can be
written until the section above is resolved. A class now carries
candidate domains - its own first, then any it cannot exclude - and the pipeline
asks each authority in turn (`internal/eventclass/ambiguity.go`). `Vehicle` and
`Engine` are the only two entries, because AudioSet's ontology is a hierarchy
and SoundNet's domains are flat: both are parents of road, rail, air and water
transport, which is why an aircraft scores 0.59-0.74 on `Vehicle` while
`Aircraft` swings 0.11-0.50. `Domain.Enrichable()` is unchanged and still false
for vehicles - it is the label that is ambiguous, not the domain.

Two consequences worth knowing. `Engine` is one of BirdNET v2.4's seven
non-species labels, so this works on a station with no YAMNet at all. And the
OpenSky client now reuses a fetched sky for five seconds, because /states/all
has no time parameter and costs a credit per call; without that, asking about
vehicles as well would have multiplied the daily spend.

**Still to verify:** an `adsb` enrichment on a `Vehicle` or `Engine` row
carrying `soundnetResolvedDomain: aircraft`. Absence is not proof of a bug -
most detections have no overflight, which is the honest outcome - so confirm the
layer runs at all before reading anything into a quiet result.

**2. Display names. Done and verified live.** On the station:
`propeller`/`and_airscrew` now reads "Propeller, airscrew",
`police`/`car_(siren)` reads "Police car (siren)", and no bird carries the
field. The server sends
`eventDisplayName` on detection responses, the SSE feed and search results, and
`localizeSpeciesName` prefers it.

The reconstruction this started from was wrong, and worth recording because it
was wrong in the project's usual shape. `DisplayName` rejoined the scientific
and common names with an underscore, which is correct only for a two-token
label: `ParseSpeciesString` splits into **at most three** parts, so
`propeller_and_airscrew` arrives as `("propeller", "and", "airscrew")` and the
rejoined `propeller_and` matched nothing. Every single-word class passed. The
lookup now keys on the **first two tokens**, which is as much of a label as
survives regardless of backend - the species code is not usable, because
`v2only` recomputes `SpeciesCode` from a map keyed on scientific name when it
reads a row back, and an event class is in no such map.

The field is additive rather than a rewrite of `commonName`, which the plan here
previously called for. `commonName` is the key `isSpeciesExcluded` matches on,
so rewriting it would have made "ignore this species" silently stop working for
exactly the detections the change exists to name.

**3. Per-domain thresholds.** YAMNet inherits BirdNET's 0.7 via
`modelGlobalConfidenceThreshold`, which has no YAMNet case. Those numbers do not
mean the same thing - YAMNet's are per-class sigmoid quantised to 1/256 steps,
and the two confirmed aircraft scored 0.41 and 0.50.

**4. Low-frequency second pass.** Low-passing at 1.2 kHz then normalising takes
`Aircraft` from 0.000 to 0.332 on a jet that was otherwise invisible. Order
matters: normalising first is hostage to the loudest bird transient. Worth doing
for the aircraft/vehicle/rail/watercraft domains; cheap at 26 ms a pass.

**5. Raise the capture gain.** Recordings sit at ~-40 dBFS RMS where YAMNet was
trained on normal loudness. `realtime.audio.sources[].gain` is 9. The single
largest untapped lever, and free.

**6. Is `DomainAlarm` really not diagnosable?** A siren has Doppler and a
pass-by geometry exactly like a vehicle, but `Domain.Diagnosable()` returns
false for it. Looks like an oversight in the domain table rather than a
decision.

**Then, in rough value order:**

- **M8 enriched alerts** - carry diagnostics and identity through the existing
  alert engine.
- **Wire M6** to the audio capture buffer and a config section. The collector
  logic and its `Capturer` interface exist; nothing implements the interface.
- **Threshold tuner UI** - its API (`/threshold-preview`) is done.
- **An events-first view** - a new fork-owned route, zero upstream footprint,
  grouped by domain with identity and diagnostics inline. This is where UI
  polish belongs; see "do not rebuild the dashboard" below.
- **Aircraft photos** from Planespotters, browser-fetched (their terms forbid
  proxying and re-exposing through our own API), with photographer attribution
  and a link back. Aircraft is the only domain with a real photo source.
- **M4** sub-classification heads, once M6 has a corpus and item 3 in
  OPEN_DECISIONS has an answer.
- **De-bird the UI copy** - 215 hardcoded "BirdNET-Go" strings across 130 files.

## Do not rebuild the dashboard

It looks bird-shaped but is better structured than it appears. Two name-keyed
functions carry almost all of it:

    localizeSpeciesName(scientificName, commonName)   used by 8+ components
    getThumbnailUrl(s) -> /api/v2/media/species-image?name={s}

`localizeSpeciesName` falls back to `commonName`, so fixing the DTO fixes every
surface with no frontend change. `species-image` is name-keyed, so the server
can return a bird photo for a species and a domain icon for an event class. A
rebuild would take the 45-line upstream footprint into the thousands and lose
the bird features that work.

## Verification habits worth keeping

- **Run the whole package, not just the tests you wrote.** This has now cost
  three separate rounds of cleanup. Upstream keeps exhaustive registries -
  golden routes, hot-reload categories, restart coverage, catalog counts,
  range-filter tables - precisely so a fork addition cannot appear without
  declaring itself. Filtering with `-run` defeats the guard written for this
  exact case, and the failures sit unnoticed across commits.
- **Run with `-race`.** It caught a genuine data race: lookup maps filled lazily
  from the detection pipeline. Without it the tests passed.
- **Test on the Pi, not only in tests.** The category filter passed every unit
  test and was a complete no-op in production, because the routing decision that
  reaches the filter is made somewhere else entirely. Three separate faults this
  session were invisible to the test suite and obvious within minutes of a live
  run.
- **Operator ground truth is worth more than any amount of reasoning.** The
  labelled clip set (GROUND_TRUTH.md) overturned a pessimistic conclusion,
  located the domain-resolution bug, and produced the "acoustics say motorised,
  ADS-B says which" design. None of it came from reading code.
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
