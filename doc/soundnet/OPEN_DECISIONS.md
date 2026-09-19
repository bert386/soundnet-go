# Open decisions

Things that need a human answer before the affected work can finish. Each entry
says what is blocked, what the options are, and which one is recommended and why.

Resolved decisions live in DECISIONS.md. This file is for what is still open.

---

## 1. Model hosting — RESOLVED as to hosting, one step outstanding

**Done.** The artefacts are mirrored at
https://github.com/bert386/soundnet-models and verified fetchable: the file
returns HTTP 200 at 4,126,810 bytes and its SHA-256 matches the pinned value.

**Outstanding:** upstream's gallery resolves a catalog entry's repo field against
HuggingFace, and the mirror is on GitHub. The artefacts and checksums are
correct; only the retrieval route differs.

Two ways to close it, roughly equal in effort:

1. **Add a GitHub source to the fetch path.** The URL shapes are close -
   HuggingFace uses `/{repo}/resolve/main/{file}`, GitHub raw uses
   `/{owner}/{repo}/main/{file}` - so this is a small addition, but it edits an
   upstream file.
2. **Mirror the same two files to a HuggingFace repo as well.** No code change at
   all; costs a second upload and a second place to keep in step.

Recommended: (1). One upstream file gains a branch in a download helper, against
(2)'s permanent duplication of artefacts across two hosts.

**Original blocker, now historical:**

The catalog entry is written and pinned to this exact artefact:

| | |
|---|---|
| Source | `https://storage.googleapis.com/mediapipe-models/audio_classifier/yamnet/float32/1/yamnet.tflite` |
| Size | 4,126,810 bytes |
| SHA-256 | `4d8b4a53282dc83ef04e3e7dbc4fbc98082e34e44ed798e16c3a0cdd4c584faf` |
| Licence | Apache-2.0 — redistribution permitted |

**Recommended:** create a public HuggingFace repo `bert386/soundnet-models` and
upload two files:

    yamnet/yamnet.tflite            the artefact above, unmodified
    yamnet/yamnet_class_map.csv     already in internal/eventclass/testdata/

Why HuggingFace rather than anything else: upstream's gallery fetches from
HuggingFace and verifies SHA-256 and size. Using it means **no upstream download
code is modified**, which is the whole mergeability argument. Pointing at
Google's URL directly would require editing the fetch path, and MediaPipe paths
have moved before, so a pinned mirror also protects against link rot.

**Why this is not already done:** uploading needs a write token. Tokens are
credentials, so they are the operator's to handle, not the assistant's.

**Alternatives considered:** teaching the catalog to fetch arbitrary URLs
(modifies upstream, and the checksum verification would need rebuilding);
committing the 4 MB binary into the repository (bloats history, and git is a
poor artefact store).

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
mistaken for done. It cannot be meaningfully tested until item 1 is resolved,
since there is no model file to run.

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
