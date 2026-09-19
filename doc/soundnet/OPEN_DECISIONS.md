# Open decisions

Things that need a human answer before the affected work can finish. Each entry
says what is blocked, what the options are, and which one is recommended and why.

Resolved decisions live in DECISIONS.md. This file is for what is still open.

---

## 1. Model hosting — RESOLVED, closed

**Hosting.** The artefacts are mirrored at
https://github.com/bert386/soundnet-models and verified fetchable. Both files
return HTTP 200 and match their pinned checksums:

| File | Bytes | SHA-256 |
|---|---|---|
| `yamnet/yamnet.tflite` | 4,126,810 | `4d8b4a53282dc83ef04e3e7dbc4fbc98082e34e44ed798e16c3a0cdd4c584faf` |
| `yamnet/yamnet_class_map.csv` | 13,574 | `b03d48f9ebe23f69ea825193de7e736934086a12410f0af47babe897b78bc0d3` |

**Retrieval.** Closed by option (1), as recommended: `CatalogEntry` gained a
`BaseURL` field. When set, files are fetched from `BaseURL + "/" + RemotePath`
and the HuggingFace repo construction, endpoint override and mirror failover are
bypassed entirely. `Install` already carried a `baseURL` parameter for test
injection, so the entry-level value folds into it rather than adding a second
download path — six added lines in `model_manager.go` and a field in
`model_catalog.go`, no deletions.

Option (2) — mirroring to HuggingFace as well — was not taken. It would have
cost nothing in code but left the artefacts duplicated across two hosts
permanently, with no mechanism to keep them in step.

The class map is now pinned too. It had no checksum, and it matters as much as
the model: it is the join between YAMNet's output indices and the event
taxonomy, so a row inserted anywhere in it relabels every class below without
anything failing.

**Not a decision, but worth recording:** registering YAMNet had been failing
five upstream tests since it landed, unnoticed because the full `internal/classifier`
package was never run. Four are exhaustive tables that oblige a new model to
declare itself — range-filter compatibility, range-filter participation, catalog
category, catalog entry count. The fifth is the drift guard between
`classifier.KnownConfigIDs()` and `conf.ValidAudioModels`, closed by extending
that map from an `init()` in a new file rather than editing
`internal/conf/validate.go`. All six pass now (a sixth, the unpinned class map,
was fixed by the pinning above).

**Licence note:** YAMNet is Apache-2.0, which permits redistribution. Pointing
at Google's MediaPipe URL directly was rejected because those paths have moved
before; a pinned mirror does not rot.

---

## 2. YAMNet inference adapter — RESOLVED, closed

**Done.** `internal/classifier/yamnet.go` implements `ModelInstance`;
`orchestrator_yamnet.go` registers the loader. Both extend package-level maps
from `init()`, so `orchestrator.go` is untouched. Verified against the real
artefact: silence classifies as `silence` at 0.80, and a gallery install laid
out on disk resolves through `resolveFamilyPaths` to the loader.

**The framing risk flagged here is resolved.** The analysis window is 3 s, not
YAMNet's 0.975 s frame, tiled with four evenly spaced frames (offsets 0, 10800,
21600, 32400 at 16 kHz) combined by per-class maximum. Maximum rather than mean
because events are sparse and local: a two-second siren inside a three-second
window is a siren, not a third of one. Sharing BirdNET's 3 s window means a
detection and its diagnostics describe the same audio, which was the original
worry.

It also side-steps a trap. `ModelSpec.ClipSizeBytes` computes
`SampleRate * int(ClipLength.Seconds())`, and `int(0.975)` is 0 — so the 0.975 s
spec registered earlier would have produced a zero-byte analysis buffer, a read
size of zero, and a model that loads, reports healthy and never infers once.
A test now guards this for every registered model.

**What was read off the artefact rather than assumed:**

| | |
|---|---|
| Input | one tensor, `waveform_binary`, float32 `[15600]` |
| Output | **one** tensor, float32 `[1 521]` |
| Activation | already per-class sigmoid; no further activation applied |

The activation was checked empirically rather than inferred: silence scores
`Silence` 0.80, a 440 Hz tone scores `Sine wave` 0.996, and the vector sums
above 1.0. BirdNET applies a sigmoid and Perch a softmax to their backends' raw
logits; either here would be wrong, and a second sigmoid in particular would
compress every score into [0.5, 0.73] and make everything look like a
half-confident detection.

---

## 3. M4 has lost its feature source — NEW, needs a decision

**Blocks:** M4 sub-classification heads. Nothing else.

The scope has M4 training small heads on YAMNet's 1024-dimensional embeddings,
which is what makes them cheap enough to run on a Pi alongside everything else.
**This build of YAMNet does not expose embeddings.** The published graph has
three outputs — scores, embeddings, log-mel patches — but the MediaPipe artefact
mirrored for this fork has exactly one: the 521 scores. Confirmed by reading the
tensor layout off the file, not inferred.

Options:

1. **Mirror a YAMNet build that exposes all three outputs** (the TF-Hub/Kaggle
   saved-model conversion rather than the MediaPipe one). Costs a second
   artefact and a re-pin; the adapter would need a second constructor, because
   upstream's TFLite wrapper only ever reads output tensor 0.
2. **Train the heads on the 521 scores instead of embeddings.** No new artefact.
   Much weaker: the scores are a 521-way bottleneck already shaped by AudioSet's
   own classes, and they arrive quantised to 1/256 steps, so fine distinctions
   within a class — which is exactly what a sub-classification head is for — are
   largely gone before the head sees them.
3. **Use the DSP feature vector from `internal/acoustics`** as the head's input.
   Cheap, already computed, and interpretable; but it was designed to measure
   properties, not to discriminate classes, so it would need evaluating rather
   than assuming.

**Recommended: (1).** It is the only option that keeps M4 doing what the scope
intends, the cost is one more mirrored file, and the checksum-pinning machinery
for that already exists. Worth deciding before M4 starts rather than during.

**Not urgent.** M4 is last in the plan and trains on M6's corpus, which is not
collected yet.

---

## 4. SoundNet configuration is not bound to `config.yaml`

**Blocks:** turning the diagnostics and enrichment layers on without a code
change. The pipeline hook is in place and inert; `ConfigureSoundNet` installs the
analyser at startup, but nothing reads settings from configuration yet.

Needs: a `soundnet:` section covering station position, per-domain diagnostics
enablement, enrichment providers and credentials, and the credit reserve.

**Recommended:** add it as its own config struct in a new file rather than
extending upstream's `conf.Settings` inline, for the usual merge reasons. The
station part already has a validated type ready to bind to
(`enrichment.StationConfig`).

---

## 5. OpenSky credentials are in a local file, not configuration

Currently read from `secrets/opensky-credentials.json` outside the repository.
That is correct for development, but the running service needs a defined
location.

**Recommended:** a config key holding a *path* or an environment variable name,
never the secret itself, so credentials stay out of any versioned file and out
of support dumps. Depends on item 4.

---

## 6. Station coordinates are recorded but not yet operator-editable

Latitude, longitude and elevation are supplied and used as the development
fixture. `StationConfig` validates operator input, including DMS and decimal
forms, and the settings page requirements are specified in DECISIONS.md — but the
web UI itself is M7 work and not built.

Until then the station is set in code or configuration rather than through the
interface.

---

## 7. Aircraft type and registration depend on a third-party service

Resolved through adsbdb.com, which is free and unauthenticated. It works, is
cached, and a failure never costs the identification.

**Worth an explicit decision eventually:** whether to depend on a community
service at all, or to ship a local aircraft database (basestation.sqb or
similar). The local option removes an outbound call and works offline; it costs
a periodic database refresh. No urgency — the current arrangement degrades
safely.

---

## 8. Not yet started

For completeness, so the status is not overstated. STATE.md holds the
authoritative milestone table; this list is what remains untouched or partial:

- **M4** sub-classification heads (aircraft type, saw type) and their training
  pipeline. Depends on M6 for training data, and now on item 3 for a feature
  source.
- **M6** ADS-B auto-labelling collector: the collector logic and its `Capturer`
  interface exist and are tested, but nothing implements the interface, so it is
  not wired to the audio buffer or to a config section.
- **M7** the web UI: detection detail, review queue and training export are
  built, along with the confusion-pair and threshold-preview endpoints. The
  threshold tuner UI and live diagnostics re-computation are not.
- **M8** enriched alerts through the existing alert engine. Untouched.
- **De-birding the UI copy** — 215 hardcoded "BirdNET-Go" strings across 130
  files. The binary still announces itself as BirdNET-Go at startup; the
  branding hook covers MQTT discovery, API links and user agents only.
