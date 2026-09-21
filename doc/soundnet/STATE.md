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
| M1 taxonomy + models | **done** - taxonomy, catalog, fetch route, adapters. YAMNet 66-77 ms/window live; **CED-tiny live at 89 ms** |
| M2 detection records | **done** |
| M3 DSP diagnostics | **done**, 24.8 ms/clip measured on the Pi against a 100 ms budget |
| M5 enrichment / ADS-B | **done and proven live** - registration, type, operator, route; ambiguity resolution for `Vehicle`/`Engine`/`Thunder`; corroboration-gated thresholds |
| M6 auto-label collector | **running and producing** - first two captures 2026-09-21, both RSCU208 (AW139) filed under `corpus/aircraft/A139`. Poll interval now 300 s: at 60 s it exhausted the API allowance in eight hours |
| M7 web UI | detail panel, review + training export routed, confusion + threshold APIs, **event-domain filter**, **display names**, **resolved-domain correction**, **pass grouping**, **category overview with aircraft cards** done; tuner UI and live re-compute remain |
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

**Production Go: 14 files, +141 -5.** The number that matters for merges. Only
files that already exist on `main` are counted - a new fork-owned file is not a
merge cost, an edited upstream file is.

    internal/api/v2/detections/detections.go  41 -3  category filter, routing, display name
    internal/analysis/processor/processor.go  19 -1  pipeline hook, candidate threshold,
                                                     corroboration discard, filter-only drop
    internal/classifier/model_manager.go       9 -1   fold BaseURL into the fetch
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

**Upstream tests: 7 files, +51 -5.** Exhaustive registries that oblige a fork
addition to declare itself. Adding CED tripped **five at once** - catalog entry
count, BaseURL ownership, config-alias drift, range-filter compatibility and
range-filter participation - and the drift guard caught a real mistake rather
than bookkeeping: `ced-tiny-v1` is a catalog ID, not a registry alias, and did
not belong in `conf.ValidAudioModels`. Every row is marked `SOUNDNET:`. Each of these was
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

## The station, as of 2026-09-20 evening

Three models, one source, 345 ms of inference per 3-second window - an 11%
duty cycle on a Pi 4.

    BirdNET_V2.4   TFLite   178 ms   186 MB   6522 species
    CED            ONNX      89 ms    62 MB    527 AudioSet classes
    YAMNet         TFLite     77 ms    10 MB    521 AudioSet classes

Backends: TFLite and **ONNX Runtime 1.25.1**, both present. OpenVINO compiled in,
inactive.

**The identification pipeline works end to end.** Nine aircraft identified by
registration in the first hours after the layers were fixed:

    id=1194  vehicle  -> aircraft   VH-VOL  737NG      3.32 km
    id=1193  vehicle  -> aircraft   VH-VOL  737NG      5.08 km
    id=1180  vehicle  -> aircraft   VH-XCW  PA-28-181  1.21 km
    id=1179  aircraft              VH-XCW  PA-28-181  0.99 km
    id=1177  vehicle  -> aircraft   VH-XCW  PA-28-181  1.09 km
    id=1033  aircraft              VH-NRB  PA-28-181  3.66 km
    id=990   aircraft              VH-FTU  PA-28-161  1.60 km

Five of the nine were acoustically "vehicle" and ADS-B corrected them, including
a Boeing 737 at 5 km. That is the ambiguity work and `resolvedDomain` doing
exactly what they exist for.

**Operator settings changed this session** (all reversible, backups beside each
config):

    realtime.audio.sources[].gain              9  -> 15   (config.yaml.bak.*)
    soundnet.enrichment.corroborationthreshold 0  -> 0.15 (.bak.corroboration)
    birdnet.onnxruntimepath                    -> lib/libonnxruntime.so.1.25.1 (.bak.ort)
    models.enabled + source models             += ced    (.bak.ced)
    realtime.privacyfilter.vad                 enabled by the operator at 0.35

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

## Fixed: the station had never written a SoundNet row

Found and fixed 2026-09-20. `GET /api/v2/soundnet/detections` returned
`{"count":0}` on a station where both layers had been enabled for days - not
"no aircraft today", but no diagnostics row and no enrichment row ever.

**Cause.** `SoundNetAction` reads the database-assigned detection ID from
`DetectionContext` and returns when it is still zero. `DatabaseAction` stores
it. The action was appended to the **top level** of `getDefaultActions`, and
every action returned there is enqueued as its own task on a shared worker
queue - so it ran concurrently with the save instead of after it, and lost the
race every time, because the save does disk I/O and it does not.

Upstream had already solved this for SSE and MQTT, which need the same ID, by
putting them in a `CompositeAction` after the save. SoundNet now joins that
sequence, last, and its work is bounded below `CompositeActionTimeout` so it can
never be the step that trips the sequence.

**Verified:** the first diagnostics row appeared 90 seconds after the fix was
deployed. `computeMs` 0, RMS -40.2 dBFS - which independently corroborates the
capture-gain finding in GROUND_TRUTH.md from a completely different direction.

**Why it stayed hidden.** The requirement was written on the `DetectionCtx`
field from the start ("this action must run after the save") and the code did
not do it; `actions_soundnet_test.go` asserted the action was *built* correctly,
which it always was, and nothing asserted it was *dispatched* somewhere it could
work. That is the third feature on this project to pass its unit tests while
being inert in production. The ordering is now asserted by a test that fails
against the old arrangement, a zero detection ID is logged at warn, and
`Result.Skipped` - which was always computed and never logged - is logged at
debug.

## Fixed: non-taxonomy classes were stored as detections

`chewing_and_mastication`, `burping_and_eructation` and `crying_and_sobbing`
were appearing as ordinary detections. The operator reviewed four of them as
"rustling dry grass" and one as "human cough".

They are emitted on purpose: the YAMNet and CED adapters pass through every
class `vocalization.IsHuman` or `IsDog` recognises, because the privacy and
dog-bark filters can only act on results the adapter hands them. `reportable()`
argued this "costs nothing in stored rows", since a speech hit discards the
whole window. That holds only **above** the privacy threshold. At 0.41-0.59
these were never going to trip a filter set at 0.7, so the window was kept and
the class stored with no domain, no display name and a mangled label.

Fixed in `internal/analysis/processor/filteronly_soundnet.go`: dropped after
both filters have run, before anything is stored. Dropped rather than never
emitted, because the filters still need to see them. `Dog` stays - it is both a
filter input and a default-enabled biological event, and a barking dog is worth
recording. Scoped to the models this fork adds, since upstream decides what
BirdNET's own non-species labels do.

## CED-tiny: what it took, and the two traps

Live since 2026-09-20. A 2023 tagger distilled from transformer ensembles,
running beside YAMNet rather than replacing it - they disagree usefully, and the
cross-model consensus machinery already handles that.

**Why it is worth having.** On the operator-labelled set, YAMNet called a large
truck and a propeller aircraft both `Vehicle 0.80`; CED gives the truck an
aircraft score of 0.000 and the aeroplane 0.374. It called a person whistling
`Whistling` where YAMNet said police siren at 0.74. Zero false positives across
ten non-aircraft clips. See MODEL_EVAL.md.

**Trap 1: ONNX Runtime was not on the station at all.** The Pi carried only
`libtensorflowlite_c.so`, so *no* ONNX model in the catalog could load - Perch v2
and BirdNET v3.0 as much as CED, and nobody had noticed. Two details cost time:
`RequiredORTAPIMajor` is **1.25**, so the newest release is rejected by the
version check; and `findONNXRuntimeLibrary()` searches system paths rather than
`LD_LIBRARY_PATH`, so dropping the library in the lib directory leaves the
station still reporting `onnx: available: false`. The explicit
`birdnet.onnxruntimepath` key is what makes it detectable.

**Trap 2: the generic ONNX constructor rejects it, correctly.**
`inference.NewONNXClassifier` wraps upstream's species-classifier layer, which
identifies a model by matching tensor geometry against an exhaustive table of
BirdNET and Perch shapes. CED is 48000 samples at 16 kHz with one output and
matches none, so it failed with "cannot detect model type". A static input shape
would not have helped. CED therefore has its own minimal session
(`ced_session.go`) - fixed shapes, no detection, no abstraction - because that
package is about species and CED has none.

**The front-end was never a guess.** CED's own repo states it:
`MelSpectrogram(n_fft=512, win_length=512, hop=160, center=False, n_mels=64,
f_min=0, f_max=8000)` then `AmplitudeToDB(top_db=120)`. That is **torchaudio,
not kaldi fbank** - so sherpa-onnx is an approximation of the training recipe,
and writing the front-end in Go by inferring it from sherpa would have
faithfully implemented the approximation. It is fused into the graph instead,
verified against PyTorch at 1.3e-06 across twelve windows.

**Fixed 3-second window is the contract.** CED interpolates positional
embeddings from input length and the export baked that grid in, so a differently
sized window fails loudly. Three seconds is BirdNET's window anyway.

**Still to watch:** CED has produced no detections yet. Its top scores on the
labelled set were 0.3-0.7 against a 0.7 threshold, so it may need a per-model
threshold before it contributes anything. That is the first thing to check after
a day of running.

## Why aircraft are recorded as vehicles

Asked by the operator on 2026-09-20, and the answer is in the ontology rather
than in a misfire.

**AudioSet's `Vehicle` is the parent class of `Aircraft`.** An airliner really is
a vehicle by that taxonomy, and the parent scores higher than the child because
its training positives include every aircraft, car, train and boat. Measured
live on the station, same pass: CED `Vehicle 0.54` against `Aircraft 0.17`;
YAMNet `Vehicle 0.33` against `Aircraft 0.20`. Both classes fire, and both are
saved - detections 1224 and 1225 are the same window.

Our `audioset.go` maps `Vehicle` to `DomainVehicle`, which is where the
misleading *reading* comes from: the label is correct and unspecific, and the
domain it lands in is road traffic.

Nothing upstream of the display needs to change. `ambiguousDomains` already
lists `vehicle` as a candidate for aircraft, rail and watercraft, which is what
sends it to ADS-B; the enrichment then records `soundnetResolvedDomain`. What
was missing was that nobody showed it. On the evening this was fixed, **54 of
200 rows on one day carried a correction**, including `Thunderstorm 0.89` and
`Thunder 0.85` at the same instant, both resolved to aircraft.

If the acoustic label itself should prefer the specific child when both fire,
that is a separate and larger change - a hierarchy-aware selection over the
AudioSet ontology - and it should be measured against the operator's labelled
negatives before being believed.

## What the hierarchy rule is, and what measured it

Shipped 2026-09-20. When a class that does not determine its own domain
(`Vehicle`, `Engine`, `Thunder`, `Thunderstorm`) appears alongside a class from
one of the domains it is ambiguous between, and that specific class clears its
own threshold, the general one is dropped. Only ever in that direction, and only
when the specific class will itself be recorded - so a sound the station
detected cannot become one it did not.

**First measurement, the operator's labelled clips.** 216 three-second windows,
108 from clips containing an aircraft and 108 from clips containing none, scored
with the fused CED export the station runs
(`doc/soundnet/eval/hierarchy_windows.py`). The rule fired on 35 aircraft
windows and none of the others. The separator turned out to be the child's own
threshold, not the ratio: **no negative window reached 0.15 on any aircraft
class**, including a clip of a large truck where `Vehicle` reached 0.693 and the
best aircraft class 0.055.

**Second measurement, and it moved the number.** The ratio first shipped at 0.5,
from those clips alone, where true aircraft windows ran 0.78-0.98 of their
parent. A jet recorded the same evening and confirmed by ADS-B
(`doc/soundnet/eval/thunder_ratio.py`) ran **0.35-0.82, median near 0.52** - so
half its windows were refused a correction the transponder then made anyway. The
labelled set was not wrong, it was narrow: twelve clips chosen because a person
could hear the aircraft in them, which selects for the loud ones. 0.25 now sits
between the truck at 0.08 and the faintest confirmed aircraft window at 0.35,
with a test that fails if either anchor is crossed.

The lesson is the one already in these notes, arriving from the other side: a
labelled set is evidence about the clips in it. Here the fix was not to check
the negatives - that had been done - but to notice that the positives were
selected by audibility.

## The first night, and the two things it broke

**M6 works end to end.** Two captures on 2026-09-21, both the same aircraft -
RSCU208, hex 7c617e, an AW139 rescue helicopter - at 1.6 km and 3.2 km slant,
56 minutes apart, each a WAV and a JSON sidecar filed by ICAO type under
`corpus/aircraft/A139/`. Sky to labelled corpus, unattended.

**The credit reserve worked, and the allowance is small.** At a 60-second poll
interval the collector made 480 polls and hit its 500-credit reserve by 06:27,
about eight hours in. It stopped; runtime enrichment, floor 200, was still
identifying aircraft at 07:06 on what was left. The ordering of the two floors
is the whole mechanism and it held.

What did not work was knowing any of this in numbers: the balance was never
logged, so all that could be said afterwards was that 480 polls had been too
many, never what they had been too many *of*. `credits_left` is now in the
collector's heartbeat and capture lines. First reading, 07:51: **198** - below
runtime's floor too, so ADS-B was withheld entirely until the daily reset at
UTC midnight (10:00 AEST). Interval raised to 300 s as an interim measure; set
it properly once a full day of `credits_left` exists.

**The hierarchy rule had a real bug, and the station found it within the hour.**
`getBaseConfidenceThreshold` includes the corroboration discount, so an
enrichable class is admitted at 0.15 and then discarded at flush unless an
authority vouches for it. The rule read that as "this child will be recorded",
dropped the parent, and when corroboration failed both rows disappeared - a
sound the station had detected becoming one it had not, which is exactly the
guarantee the rule was written around. The comment claiming the guarantee, and
the test pinning it, were wrong together: the test's fake did not model the
difference between *admitted* and *kept*.

It only surfaced because the credits ran out. Below the floor nothing can be
confirmed, so every correction became a deletion and the event rows went from
64 in the morning to none. `storedConfidenceThreshold` now stops before the
discount and the rule asks that instead.

Two lessons worth keeping. A guarantee asserted in a comment and a test is still
only as good as what the test's fake models. And an external dependency running
out is not an edge case for this station - it is a daily event at the current
poll rate, so every rule that leans on corroboration needs to be correct when
corroboration is unavailable.

**The rule does fire.** Confirmed by its new counter: 12 corrections in the five
minutes after the 07:46 restart, from both YAMNet and CED.

## Pass grouping

Shipped 2026-09-21, after the operator noticed one aircraft arriving as several
rows. It does: a flyby is audible for half a minute and every three-second
window in it can produce a detection under whatever class the model reached for.
Thirteen groups that morning spanned 38 to 76 seconds and held three to six rows
each, and **every one named exactly one aircraft. None named two**
(`doc/soundnet/eval/cluster_passes.py`).

The rule is time proximity within a domain, split by identity, in
`internal/eventpass`. Live on the station in the first hour: **86 event rows
became 34 list entries**, 73 of them collapsed into 21 passes.

Two things to know about it.

**The domain must be the resolved one.** Those rows belong to three different
domains by class name, so grouping on the class would scatter one aeroplane into
three passes. A detection nothing resolved keeps its own domain and stays out,
because a `Vehicle` row nobody identified may genuinely be a car.

**Which means grouping is weaker whenever ADS-B is unavailable.** With the
credits exhausted on the 21st, `Thunder` grouped with `Thunderstorm` and
`Vehicle` with `Vehicle`, but the aircraft passes could not pull their `Vehicle`
rows in - nothing had resolved them. Grouping quality is downstream of the
credit budget, which is one more reason that budget is the thing to fix next.

Nothing is discarded. Every row is a model's opinion about one window, and those
opinions are the training corpus.

## A second ADS-B source

Added 2026-09-21 after OpenSky's 4000 credits ran out and the station spent a
morning unable to identify anything - which cost more than identifications,
since the domain correction and pass grouping are both built on them.

**adsb.lol is the fallback, OpenSky stays primary.** Its limit is documented;
adsb.lol's is "dynamic based on environment load". A known limit is the better
thing to depend on. `FallbackSource` chains any number of `StateSource`s and
falls through on *any* failure, not just the credit sentinel: a broken primary
is a commoner failure than an exhausted one.

**Proven live** at 10:38 with OpenSky still below its floor: detection 2343,
`Vehicle 0.26`, identified as VH-DQV, a C208 at 4.76 km, with `source=adsb.lol`
in the stored record. adsb.lol returns registration and ICAO type inline, which
OpenSky needs a separate adsbdb lookup for.

Three things this cost, all worth knowing:

**The provider hard-coded `Source: "opensky"`.** True until a second source
existed. Caught before it wrote a row, but only by asking what an identification
would record rather than trusting that no error had been logged. Each `State`
now carries the service that reported it.

**The reuse window lived inside the OpenSky client.** So the new client
inherited none of it, took every enrichment query raw - several a second during
a pass - and was rate limited within minutes of going live, entirely fairly.
It has its own window now, at ten seconds. *"Tested live, it works"* was true and
was one curl; one request is no test of a component whose failure mode is how
often it asks.

**The limit is probabilistic, not a cooldown.** Measured: ten requests ten
seconds apart, seven answered, three refused, no Retry-After. So a refusal now
serves the most recent cached sky if it is under thirty seconds old, dead
reckoned forward with `enrichment.Project` - the mirror of the BackProject the
lag correction already uses. Replaying the stored position instead would not be
stale data, it would be wrong data, since an aircraft covers several kilometres
in thirty seconds.

**The real fix is a receiver.** An RTL-SDR on the Pi removes the whole class of
problem: unlimited local queries, no third party, no privacy question - and
feeding the data back earns 8000 OpenSky credits and an adsb.lol key. `readsb`
emits the same JSON schema `ADSBLolClient` already parses, `"ground"` string and
all, so it would drop in as a third source ahead of both networks. The operator
has a dongle somewhere but could not find it on 2026-09-21.

## Reporting by category

Added 2026-09-21, from the operator's screenshots. The inherited analytics are a
species list, and on this station they showed `Vehicle`, `Thunderstorm`,
`aircraft_and_airplane`, `car_(siren)`, `and_eructation` and `Purr` as birds -
each behind a grey bird silhouette, inside a headline count of "45 species".

Two of those are not names at all. The stored form is truncated on the way in,
so the page read `and_airscrew` where the taxonomy says `Propeller, airscrew`.
The detections list was taught the display name weeks ago; the analytics pages
never were.

`GET /api/v2/soundnet/overview?days=N` groups a period by domain, with the class
breakdown inside each, always under the taxonomy's name. It reuses
`GetSpeciesSummaryData` rather than adding a query: rows that resolve to an
event class are grouped by domain, rows that do not are birds and are counted.
Bird detections are reported beside the events rather than hidden, because the
events only mean anything in proportion to them.

First reading, seven days:

    vehicle    192   Vehicle 190, Reversing beeps 1, Car 1
    aircraft    82   Aircraft 50, Fixed-wing aircraft 25, Propeller 7
    weather     66   Thunderstorm 36, Thunder 30
    alarm        4   Police car (siren) 4
    birds     1827
    aircraft identified: 67

**Sixty-seven aircraft by registration and type** - VH-VZL a B738, VH-OFS an
A21N, VH-OHS an RV7, VH-DQV a C208 identified by *both* networks. Against nine
the day before, which is what the second ADS-B source bought.

Each aircraft card fetches its photograph from Planespotters **in the browser**,
not proxied: their terms ask for that, and for the attribution and link back
that the card carries. A missing photo is ordinary - military, private and newly
registered aircraft are often absent. Each links to its track on globe.adsb.lol,
keyed on the broadcast hex rather than a looked-up registration so the link
always resolves, and to FlightRadar24 where a registration is known.

Still species-shaped and worth doing next: the dashboard and the species
analytics pages themselves, which is where the operator actually starts.

## Names and icons, everywhere a detection is shown

The analytics pages were showing `and_airscrew`, `car_(siren)` and
`and_eructation` as species, each behind a grey bird silhouette, in a count of
"45 species". The detections list learned the display name weeks ago; nothing
else did, because every analytics endpoint returns species rows and knows
nothing of events.

**Fixed in one function.** `localizeSpeciesName` is what the species page, the
summary, the dashboard and every card already call, so it now consults the event
taxonomy when the server supplied no display name. A bird resolves to nothing
and falls through exactly as before, and so does everything else until the
taxonomy has loaded.

The taxonomy is fetched once per page load and `/api/v2/soundnet/taxonomy` now
emits **all three forms** of every name - label, raw label, storage name. That is
the point of doing it server-side: a detection is stored as two halves of a
label split at its first underscore, and rebuilding it in the browser would mean
reimplementing a splitter this project has already got wrong once, silently. The
client does a lookup.

Verified against the live taxonomy with `doc/soundnet/eval/check_name_resolution.py`:

    propeller + and_airscrew        -> Propeller, airscrew    aircraft
    police + car_(siren)            -> Police car (siren)     alarm
    fixed-wing + aircraft_and_...   -> Fixed-wing aircraft    aircraft
    reversing + beeps               -> Reversing beeps        vehicle
    purr + Purr                     -> Purr                   biological
    Turdus migratorius + Am. Robin  -> (species)              -

`Purr` and `Meow` needed mapping: BirdNET emits them from its own label set and
the taxonomy had never heard of them. Only the unambiguously non-bird animal
labels were added - upstream's animal category also holds `crow` and
`chirp_and_tweet`, and claiming those would mark real birds as non-birds, which
is the worse mistake.

**One name still does not resolve, and should not.** `burping + and_eructation`
is a human vocalisation emitted only so the privacy filter can see it. It is
dropped before storage now; the row on the operator's screen predates that fix.
Adding it to the taxonomy would make it a recordable event, which is the
opposite of why it is emitted.

The species page also stops lending a Cessna a bird silhouette: an event row
shows its domain's icon, tinted by family, with the domain where the scientific
name would be - which for an event is a truncated half-label that never told
anyone anything.

## Immediately resumable work

Everything that was on this list on 2026-09-20 morning is done and running. What
follows is what is left, ordered by value.

**1. Set the poll interval from the measured allowance.** `credits_left` is now
reported; a full day of it says what the budget actually is and therefore how
often the collector can afford to look. 300 s is a placeholder chosen because an
aircraft is inside the 4 km capture radius for one to two minutes, so a
five-minute poll certainly misses some. Worth reviewing the corroboration spend
at the same time: runtime enrichment took the balance from 500 to 198 between
06:27 and 07:51 on its own, so the collector is not the only heavy consumer.

**1b. Watch the collector's first full day.** It is running and polling; nothing
has yet been close enough and low enough to capture. Confirm the capture rate,
how many polls are refused as ambiguous, and what a day costs in API credits -
that last one has never been measured, and the collector spends a credit a
minute whether or not anything flew. Its reserve is 500 against runtime's 200,
so it stops first, but nobody has watched it reach either.

**2. Extend the hierarchy rule across models, or decide not to.** The rule that
prefers `Aircraft` over `Vehicle` compares results inside one model's chunk, and
the two readings of a sound are not always in the same model's. The `Thunder`
and `Thunderstorm` rows here are YAMNet's at 0.74-0.89, while CED hears the same
audio and puts `Thunderstorm` at 0.000 with the aircraft classes at 0.15-0.38.
Neither model holds both halves. ADS-B already resolves the domain for these, so
the row is right even when its class name is not - which may well be enough.

**3. Tune the rest of the per-domain thresholds.** The mechanism is in and
`aircraft` is set to 0.35, measured against the labelled negatives (best
aircraft-class score in any window of a clip containing no aircraft: 0.135,
including a large truck at 0.055). No other domain is set, so every other event
class still inherits BirdNET's 0.7 and is effectively silent unless
corroboration rescues it. `weather`, `alarm` and `vehicle` are the ones worth
measuring next - and `vehicle` needs care, because it has no authority to
confirm it and a low bar would fill the list.

**4. The dashboard and the summary counts.** Still species-shaped, and where
the operator starts: "Unique Species 45" counts event classes as species, the
top-10 chart mixes `Vehicle` in with `Common Myna`, and "New Species Detected"
lists `car_(siren)` as a new species. The names are right everywhere now, but
the framing is not.

**4b. Carry the correction into search results.** Done for the detection list and
the panel; `search.go` still shows the bare acoustic label. Its results are
`datastore.DetectionRecord` rather than the API response type, so this means
widening an upstream struct - weigh that footprint against how often anyone
reads a search result.

**5. Is `DomainAlarm` really not diagnosable?** A siren has Doppler and a pass-by
geometry exactly like a vehicle, but `Domain.Diagnosable()` returns false. Looks
like an oversight rather than a decision.

**6. Aircraft photos** from Planespotters, browser-fetched (their terms forbid
proxying through our own API), with attribution. Now genuinely useful: the
station produces registrations, and a photo keyed on hex code is one fetch away.

**Then, in rough value order:**

- **M8 enriched alerts** - carry diagnostics and identity through the existing
  alert engine.
- **Threshold tuner UI** - its API (`/threshold-preview`) is done.
- **An events-first view** - a new fork-owned route, zero upstream footprint,
  grouped by domain with identity and diagnostics inline. This is where UI
  polish belongs; see "do not rebuild the dashboard" below.
- **M4** sub-classification heads, once M6 has a corpus. Note CED is an embedding
  extractor by design, which is the answer to OPEN_DECISIONS item 3 - the
  YAMNet build exposing only scores no longer blocks M4 if CED is used instead.
- **De-bird the UI copy** - 215 hardcoded "BirdNET-Go" strings across 130 files.

## Things to watch on the station

- ~~**CED silence.**~~ **Answered 2026-09-20 21:15 and it was the opposite of
  the worry.** `/api/v2/system/inference` carries a per-model feed of recent
  above-threshold predictions, which is the only place the producing model is
  recoverable - `datastore.Note.Model` is `gorm:"-"` and never persisted. CED
  was firing roughly every nine seconds and scoring *higher* than YAMNet on the
  same sound (Vehicle 0.54 against 0.33), and the low-confidence Vehicle rows
  being saved are CED's, not YAMNet's. Tell them apart by arithmetic if the feed
  is unavailable: YAMNet quantises to 1/256, so its scores are exact multiples
  of 0.00390625 and CED's are not.
- **The VAD speech gate**, enabled by the operator at 0.35. Measured 2026-09-20:
  baseline privacy discards are 1-3/min, and a burst to 7-16/min for four
  minutes was real speech near the microphone, not the gate misfiring. If
  detections thin out for a *sustained* stretch rather than minutes, this is the
  first thing to check - a privacy hit discards the whole window for every model.
- **Whether OpenSky ever refills.** It did *not* reset at UTC midnight as
  expected - the balance was still 188 at 10:11 AEST, an hour later. Something
  other than a daily rollover governs it, and `credits_left` in the collector
  heartbeat will show when it moves.
- **The collector's first captures.** Running since 22:18 on 2026-09-20 and
  reporting every poll (first three at info, then hourly). An empty corpus is
  expected at night; an empty corpus after a day of daytime traffic is not.
- **ADS-B credit spend, now with a fourth consumer.** Four things now query it: aircraft detections,
  ambiguous `Vehicle`/`Engine`/`Thunder` labels, and corroboration candidates.
  The five-second sky reuse bounds it and `creditfloor` is 200, but nobody has
  watched a full day yet.

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
- **A green build proves nothing about a model.** CED compiled, passed the full
  suite and passed the linter while being unloadable on the station - the
  generic ONNX constructor rejected its tensor geometry at runtime. Install the
  artefact and read the log before believing a model works.
- **Test that a feature is reached, not only that it works.** Three times now a
  unit test has passed over something inert in production: a category filter
  whose routing never called it, a display-name lookup keyed on the wrong
  splitter, and an action dispatched where the ID it needs is always zero. Each
  had a test of the function and none of the path that reaches it. Ask what
  calls this, and assert that too.
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
