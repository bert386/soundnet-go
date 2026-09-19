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

## 2. YAMNet inference adapter — not yet written

**Blocks:** YAMNet actually producing detections. Registration and the catalog
entry are done; the code that runs the model and reshapes its output is not.

YAMNet's output differs from every model upstream ships:

- 521 scores over the AudioSet ontology, not species
- a 1024-dimensional embedding per frame, which M4's sub-classification heads
  consume rather than raw audio
- 15600-sample frames at 16 kHz, where upstream's default path assumes 3 s at
  48 kHz

**No decision needed — this is just work remaining.** Recorded here so it is not
mistaken for done. Item 1 is now closed, so the model file can be installed from
the gallery and there is something real to test the adapter against; this is the
next thing standing between the fork and its first non-bird detection.

**One risk worth flagging now:** a 0.975 s frame is shorter than the 3 s clip the
diagnostics engine expects. Doppler analysis in particular needs several seconds
to see a pass-by. The likely resolution is that YAMNet classifies on its own
short frames while diagnostics continue to run over the retained 3 s clip, but
that needs confirming against the capture buffer rather than assumed.

---

## 3. SoundNet configuration is not bound to `config.yaml`

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

## 4. OpenSky credentials are in a local file, not configuration

Currently read from `secrets/opensky-credentials.json` outside the repository.
That is correct for development, but the running service needs a defined
location.

**Recommended:** a config key holding a *path* or an environment variable name,
never the secret itself, so credentials stay out of any versioned file and out
of support dumps. Depends on item 3.

---

## 5. Station coordinates are recorded but not yet operator-editable

Latitude, longitude and elevation are supplied and used as the development
fixture. `StationConfig` validates operator input, including DMS and decimal
forms, and the settings page requirements are specified in DECISIONS.md — but the
web UI itself is M7 work and not built.

Until then the station is set in code or configuration rather than through the
interface.

---

## 6. Aircraft type and registration depend on a third-party service

Resolved through adsbdb.com, which is free and unauthenticated. It works, is
cached, and a failure never costs the identification.

**Worth an explicit decision eventually:** whether to depend on a community
service at all, or to ship a local aircraft database (basestation.sqb or
similar). The local option removes an outbound call and works offline; it costs
a periodic database refresh. No urgency — the current arrangement degrades
safely.

---

## 7. Not yet started

For completeness, so the status is not overstated:

- **M4** sub-classification heads (aircraft type, saw type) and their training
  pipeline. Depends on M6 for training data.
- **M6** ADS-B auto-labelling collector.
- **M7** the web UI: detection detail, review queue, threshold tuning, training
  export.
- **M8** enriched alerts through the existing alert engine.
- **De-birding the UI copy** — 215 hardcoded "BirdNET-Go" strings across 130
  files. The binary still announces itself as BirdNET-Go at startup; the
  branding hook covers MQTT discovery, API links and user agents only.
